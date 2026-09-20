// Package apigxapi 提供 APIGX 的分享者接口（受 owner 约束）。
//
// 与底座既有管理接口的关系：**不复用** `router/channel-router.go` 的管理员路由
// （那一整组挂在 `AdminAuth()` 下，且渠道是全局资源）。这里是一套独立接口，
// 每次请求都从登录态取 owner，并把它带进每一个数据库查询。
//
// 凭据处理（计划书 §10.3）：
//   - 上游 Key 以明文提交，但**只在内存中转为密文**（apigx/secretbox 的 AES-256-GCM 信封），
//     数据库里只存信封；
//   - 接口**从不回显**明文或信封，只给脱敏提示（后 4 位）；
//   - 主密钥未配置时**拒绝创建**（失败关闭），绝不退化成明文存储。
package apigxapi

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/apigx/capacity"
	"github.com/QuantumNous/new-api/apigx/channelpool"
	apigxmodel "github.com/QuantumNous/new-api/apigx/model"
	"github.com/QuantumNous/new-api/apigx/price"
	"github.com/QuantumNous/new-api/apigx/relayhook"
	"github.com/QuantumNous/new-api/apigx/routing"
	"github.com/QuantumNous/new-api/apigx/secretbox"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/middleware"
	basemodel "github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// Register 注册全部分享者接口。调用方需已挂好认证中间件（UserAuth）。
func Register(r *gin.RouterGroup, db *gorm.DB) {
	h := &handler{db: db}

	groups := r.Group("/share-groups")
	{
		groups.POST("", h.createShareGroup)
		groups.GET("", h.listShareGroups)
		// 更新分组的展示名与简介（计划书 §4"分享名称、介绍"）
		groups.PATCH("/:id", h.updateShareGroup)
		groups.GET("/:id", h.getShareGroup)
		groups.DELETE("/:id", h.deleteShareGroup)
		// 平台侧审核：发布 / 暂停（分享者不能自行把草稿对外放出）
		groups.POST("/:id/publish-status", h.setShareGroupPublishStatus)
		groups.GET("/:id/channels", h.listChannels)
	}

	channels := r.Group("/channels")
	{
		// 列出登录用户的**全部**渠道（"我的分享"页需要它；此前只有按分组的 /share-groups/:id/channels）。
		channels.GET("", h.listAllChannels)
		channels.POST("", h.createChannel)
		channels.GET("/:id", h.getChannel)
		channels.DELETE("/:id", h.deleteChannel)
		channels.POST("/:id/keys", h.createUpstreamKey)
		channels.GET("/:id/keys", h.listUpstreamKeys)
		channels.POST("/:id/apply", h.applyChannelPool)
		channels.PUT("/:id/capacity-policy", h.setChannelCapacityPolicy)
		channels.POST("/:id/offerings", h.createOffering)
		channels.GET("/:id/offerings", h.listOfferings)
		channels.POST("/:id/price-match/preview", h.previewChannelPriceMatch)
		channels.POST("/:id/price-match/confirm", h.confirmChannelPriceMatch)
		channels.POST("/:id/fetch-models", h.fetchUpstreamModels)
	}

	offerings := r.Group("/offerings")
	{
		offerings.PATCH("/:id/capabilities", h.updateOfferingCapabilities)
		offerings.POST("/:id/submit", h.submitOffering)
		offerings.POST("/:id/publish", h.publishOffering)
		offerings.POST("/:id/reject", h.rejectOffering)
		offerings.DELETE("/:id", h.deleteOffering)
	}

	// 平台审核面：跨 owner 读取待审核队列。具体发布/驳回动作仍使用 offerings 下的端点，
	// 但它们在服务端执行 reviewer-scoped 状态转换，不能复用分享者的 owner-scoped CRUD。
	reviews := r.Group("/reviews")
	{
		reviews.GET("/offerings", h.listOfferingsForReview)
	}

	plaza := r.Group("/plaza")
	{
		plaza.GET("", h.listPlaza)
		plaza.GET("/:publicId", h.getPlazaItem)
	}

	// 价格快照（T2.1/T2.2）：人工覆盖与单渠道同步。**平台侧动作**——
	// 价格决定计费，不能让分享者随意改写他人可见的价格。
	r.GET("/price-rules", h.listPriceRules)
	r.PUT("/price-rules", h.setManualPriceRule)
	r.POST("/channels/:id/price-sync", h.syncChannelPrice)

	// 任务产物的**保留期限**策略（计划书 §4：任务中心显示保留期限）。
	// 这里是"策略"而不是逐任务字段：产物本身在上游（签名直链），我们记录的是**自己的保留承诺**，
	// 因此做成可配置项，不引入任务表结构变更与迁移风险。
	r.GET("/task-retention", h.getTaskRetention)

	// 访问控制（§4「邀请兑换 / 指定用户 / 撤销与到期」）：
	//   - 邀请码是凭证：明文只在创建响应里返回一次，库里只存哈希；
	//   - 兑换**原子、单次**，失败不区分原因（防枚举）；
	//   - 发邀请/授权/撤销都要求调用者是**资源所有者**。
	access := r.Group("/access")
	{
		access.POST("/invites", h.createAccessInvite)
		access.GET("/invites", h.listAccessInvites)
		access.POST("/invites/:id/revoke", h.revokeAccessInvite)
		// 兑换是**按用户**限流的（§10 安全加固）：邀请码空间足够大，但限流是防爆破的纵深防御，
		// 复用底座既有的 UserCriticalRateLimit（随 CriticalRateLimitEnable 开关，关闭时自动 no-op）。
		access.POST("/invites/redeem", middleware.UserCriticalRateLimit("access_invite_redeem"), h.redeemAccessInvite)
		access.POST("/grants", h.createGrant)
		access.GET("/grants", h.listGrants)
		access.DELETE("/grants/:id", h.deleteGrant)
	}

	// 模型目录（§4「模型目录」+ §4「批量匹配、人工覆盖」）：候选列表与确认动作。
	// 全部是**平台侧**动作（确认映射会影响对外标准 ID 与价格匹配，不能由分享者自行决定）。
	catalog := r.Group("/model-catalog")
	{
		catalog.GET("/candidates", h.listAliasCandidates)
		catalog.POST("/candidates/:id/confirm", h.confirmAliasCandidate)
		catalog.POST("/candidates/:id/reject", h.rejectAliasCandidate)
		catalog.POST("/bulk-confirm", h.bulkConfirmAliases)
	}

	// 能力矩阵（T3.7/T3.8）：**支持范围的唯一事实来源**——界面/文档/验收脚本都从这里取，
	// 避免各自发挥而宣称了没做到的能力（计划书：尚未支持的维度不能在界面宣称已生效）。
	r.GET("/capabilities", h.listCapabilities)

	// 可解释路由结果（T2.9）：只读，不写库、不投影
	r.GET("/route-explain", h.explainRoute)
	// 应用路由排名（T2.7 生效路径）：把名次写成底座渠道权重。**平台侧动作**——
	// 跨分享者的排序不能由某个分享者决定。
	r.POST("/route-apply", h.applyRouteRanking)

	usage := r.Group("/usage")
	{
		usage.GET("", h.listUsage)
		// U-3：分享者视角的消耗汇总（按渠道 / 按 Key / 按模型）
		usage.GET("/summary", h.summarizeOwnerUsage)
	}

	platformKeys := r.Group("/platform-keys")
	{
		platformKeys.GET("/:tokenId/preferences", h.getPlatformKeyPreferences)
		platformKeys.PUT("/:tokenId/preferences", h.setPlatformKeyPreferences)
	}

	keys := r.Group("/keys")
	{
		keys.POST("/:id/rotate", h.rotateUpstreamKey)
		keys.PATCH("/:id/status", h.updateKeyStatus)
		keys.PATCH("/:id/weight", h.updateKeyWeight)
		keys.PATCH("/:id/cooldown", h.updateKeyCooldown)
		keys.DELETE("/:id", h.deleteUpstreamKey)
	}
}

type handler struct{ db *gorm.DB }

// ownerID 从登录态取所有者；底座 UserAuth 会写入 "id"。
//
// **服务端强制**：任何接口都不接受客户端提交的 owner 参数（否则等于把归属校验交给调用方）。
func (h *handler) ownerID(c *gin.Context) (int64, bool) {
	id := c.GetInt("id")
	if id <= 0 {
		fail(c, http.StatusUnauthorized, "unauthorized", "login required")
		return 0, false
	}
	return int64(id), true
}

func paramID(c *gin.Context, name string) (int64, bool) {
	raw := c.Param(name)
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		fail(c, http.StatusBadRequest, "invalid_id", "invalid "+name)
		return 0, false
	}
	return id, true
}

func ok(c *gin.Context, data any) {
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": data})
}

func fail(c *gin.Context, status int, code, message string) {
	c.JSON(status, gin.H{"success": false, "code": code, "message": message})
}

// failFromRepo 把仓储层错误映射为对外响应。
//
// 关键：`ErrNotFound`（不存在**或**不属于自己）一律 404，不区分——避免枚举他人资源。
func failFromRepo(c *gin.Context, err error) {
	switch {
	case errors.Is(err, apigxmodel.ErrNotFound):
		fail(c, http.StatusNotFound, "not_found", "resource not found")
	case errors.Is(err, apigxmodel.ErrInvalidArgument):
		fail(c, http.StatusBadRequest, "invalid_argument", err.Error())
	case isDuplicateError(err):
		// 唯一约束冲突（如「同一渠道同一模型重复发布」）应是对外可理解的 **409**，
		// 而不是 500。三种方言的错误文本不同，因此按文本匹配（各驱动未提供统一的哨兵错误）。
		fail(c, http.StatusConflict, "conflict", "resource already exists")
	default:
		common.SysError("apigx api: " + err.Error())
		fail(c, http.StatusInternalServerError, "internal_error", "internal error")
	}
}

// ---------------------------------------------------------------------------
// 分享分组
// ---------------------------------------------------------------------------

type createShareGroupRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	// DefaultVisibility 见 schema-draft §5.3：0=私有 / 1=受限 / 2=公开
	DefaultVisibility *int `json:"default_visibility"`
}

func (h *handler) createShareGroup(c *gin.Context) {
	owner, valid := h.ownerID(c)
	if !valid {
		return
	}
	var req createShareGroupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "invalid_body", "invalid request body")
		return
	}
	group := &apigxmodel.ShareGroup{
		OwnerUserID:       owner,
		Name:              strings.TrimSpace(req.Name),
		Description:       req.Description,
		DefaultVisibility: 0,
		// 发布状态（字段注释为准：0=草稿 / 1=待审核 / 2=已发布 / 3=已暂停 / 4=已退役）。
		// 规则：**公开**分组先审核（计划书 §12「新公开资源先审核」）；非公开（受限/私有）不对外暴露，
		// 无需审核即视为已发布 —— 否则分享者每次自用都要等一次人工审核，体验不合理。
		PublishStatus: initialGroupPublishStatus(groupVisibilityOrDefault(req.DefaultVisibility)),
	}
	if req.DefaultVisibility != nil {
		group.DefaultVisibility = *req.DefaultVisibility
	}
	if err := apigxmodel.CreateShareGroup(h.db, group); err != nil {
		failFromRepo(c, err)
		return
	}
	ok(c, gin.H{"id": group.ID, "public_id": group.PublicID, "name": group.Name, "publish_status": group.PublishStatus})
}

func (h *handler) listShareGroups(c *gin.Context) {
	owner, valid := h.ownerID(c)
	if !valid {
		return
	}
	groups, err := apigxmodel.ListShareGroups(h.db, owner, 100)
	if err != nil {
		failFromRepo(c, err)
		return
	}
	items := make([]gin.H, 0, len(groups))
	for _, g := range groups {
		items = append(items, gin.H{
			"id": g.ID, "public_id": g.PublicID, "name": g.Name,
			// 简介（计划书 §4"分享名称、介绍"）：不可信内容，前端渲染时按纯文本处理
			"description":        g.Description,
			"default_visibility": g.DefaultVisibility, "publish_status": g.PublishStatus,
			"updated_at": g.UpdatedAt,
		})
	}
	ok(c, gin.H{"items": items})
}

func (h *handler) getShareGroup(c *gin.Context) {
	owner, valid := h.ownerID(c)
	if !valid {
		return
	}
	id, valid := paramID(c, "id")
	if !valid {
		return
	}
	group, err := apigxmodel.GetShareGroup(h.db, owner, id)
	if err != nil {
		failFromRepo(c, err)
		return
	}
	ok(c, gin.H{
		"id": group.ID, "public_id": group.PublicID, "name": group.Name,
		"description": group.Description, "default_visibility": group.DefaultVisibility,
		"publish_status": group.PublishStatus, "created_at": group.CreatedAt, "updated_at": group.UpdatedAt,
	})
}

// initialGroupPublishStatus 返回新建分组时的初始发布状态。
//
// **公开**分组必须先审核（返回待审核），**非公开**分组不对外暴露，直接视为已发布。
// groupVisibilityOrDefault 取分组的可见性，未指定时按**公开**处理。
//
// 保守默认：未指定可见性时宁可按「公开 ⇒ 先审核」，也不要静默变成一个对人可见的分组。
func groupVisibilityOrDefault(input *int) int {
	if input == nil {
		return apigxmodel.ShareGroupVisibilityPublic
	}
	return *input
}

func initialGroupPublishStatus(visibility int) int {
	if visibility == apigxmodel.ShareGroupVisibilityPublic {
		return apigxmodel.ShareGroupPublishPending
	}
	return apigxmodel.ShareGroupPublishPublished
}

// setGroupPublishStatusRequest 平台的审核动作（发布 / 暂停）。
type setGroupPublishStatusRequest struct {
	Status int `json:"status" binding:"required"`
}

// setShareGroupPublishStatus 平台侧：把分享分组置为「已发布」或「已暂停」。
//
// 为什么由平台做：这属于「资源审核」（§4 管理后台），分享者不能自己把草稿/待审核的分组对外放出。
// 已在用（已发布）的分组被暂停后，新请求应停止 —— 生效点见广场可见性与路由候选的判据。
func (h *handler) setShareGroupPublishStatus(c *gin.Context) {
	if !requirePlatformReviewer(c) {
		return
	}
	groupID, valid := paramID(c, "id")
	if !valid {
		return
	}
	var req setGroupPublishStatusRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "invalid_body", "invalid request body")
		return
	}
	if req.Status != apigxmodel.ShareGroupPublishPublished && req.Status != apigxmodel.ShareGroupPublishSuspended {
		fail(c, http.StatusBadRequest, "invalid_status", "status must be 2 (published) or 3 (suspended)")
		return
	}
	if err := apigxmodel.SetShareGroupPublishStatus(h.db, groupID, req.Status); err != nil {
		failFromRepo(c, err)
		return
	}
	ok(c, gin.H{"id": groupID, "publish_status": req.Status})
}

func (h *handler) deleteShareGroup(c *gin.Context) {
	owner, valid := h.ownerID(c)
	if !valid {
		return
	}
	id, valid := paramID(c, "id")
	if !valid {
		return
	}
	if err := apigxmodel.DeleteShareGroup(h.db, owner, id); err != nil {
		failFromRepo(c, err)
		return
	}
	ok(c, gin.H{"deleted": id})
}

// ---------------------------------------------------------------------------
// 分享渠道
// ---------------------------------------------------------------------------

type createChannelRequest struct {
	// 以下两项用于"分享者自建渠道"（§4「渠道 CRUD」）：不给 base_channel_id 时，
	// 平台按这些信息**代建底座渠道**并绑定 —— 分享者不必知道管理员侧的渠道 ID。
	UpstreamKey   string `json:"upstream_key"`
	ShareGroupID  int64  `json:"share_group_id"`
	BaseChannelID int64  `json:"base_channel_id"`
	Name          string `json:"name"`
	Protocol      string `json:"protocol"`
	BaseURL       string `json:"base_url"`
	KeyPoolMode   *int   `json:"key_pool_mode"`
}

func (h *handler) createChannel(c *gin.Context) {
	owner, valid := h.ownerID(c)
	if !valid {
		return
	}
	var req createChannelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "invalid_body", "invalid request body")
		return
	}
	// 自建渠道时随附的上游 Key 与两类渠道记录在同一事务内登记。
	var selfServeKey string

	channel := &apigxmodel.Channel{
		OwnerUserID:  owner,
		ShareGroupID: req.ShareGroupID,
		Name:         strings.TrimSpace(req.Name),
		Protocol:     req.Protocol,
		// 归一化上游 Base URL（真实 API 回扫发现）：用户自然会填 `https://host/v1`
		// 这种"API 基址"，而底座会再拼 `/v1/...` → 变成 `/v1/v1/...`，上游返回 404。
		BaseURL:       channelpool.NormalizeUpstreamBaseURL(req.BaseURL),
		BaseChannelID: req.BaseChannelID,
		KeyPoolMode:   0, // 默认轮询（计划书 §3.5）
		HealthState:   0,
	}
	if req.KeyPoolMode != nil {
		channel.KeyPoolMode = *req.KeyPoolMode
	}
	// 绑定既有底座渠道是平台级动作。普通分享者只能提交自己的配方，由平台代建专属渠道；
	// 否则只要猜到全局 channel ID，就能把别人的渠道挂到自己的分享资源下。
	if channel.BaseChannelID > 0 {
		if c.GetInt("role") < common.RoleAdminUser {
			fail(c, http.StatusForbidden, "platform_binding_required",
				"binding an existing base channel requires platform privileges")
			return
		}
		var base basemodel.Channel
		if err := h.db.Select("id").Where("id = ?", channel.BaseChannelID).First(&base).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				fail(c, http.StatusNotFound, "base_channel_not_found", "base channel not found")
			} else {
				failFromRepo(c, err)
			}
			return
		}
		if err := apigxmodel.CreateChannel(h.db, channel); err != nil {
			failFromRepo(c, err)
			return
		}
	} else {
		selfServeKey = strings.TrimSpace(req.UpstreamKey)
		if recipeErr := validateSelfServeChannelRecipe(channel, selfServeKey); recipeErr != nil {
			failFromRepo(c, recipeErr)
			return
		}
		keyRecord, keyErr := prepareSelfServeUpstreamKey(selfServeKey)
		if keyErr != nil {
			common.SysError("apigx api: refuse to store upstream key: " + keyErr.Error())
			fail(c, http.StatusServiceUnavailable, "secret_store_unavailable",
				"credential store is not configured; refusing to store the key")
			return
		}
		// 底座渠道、APIGX 渠道和初始加密 Key 共用同一个主库事务。任何一步失败都会
		// 回滚底座行，避免留下仍为 enabled 的孤儿渠道。
		if err := h.db.Transaction(func(tx *gorm.DB) error {
			if err := provisionBaseChannelForShareChannel(tx, channel, selfServeKey); err != nil {
				return err
			}
			if err := apigxmodel.CreateChannel(tx, channel); err != nil {
				return err
			}
			keyRecord.ChannelID = channel.ID
			return apigxmodel.CreateUpstreamKey(tx, owner, keyRecord)
		}); err != nil {
			failFromRepo(c, err)
			return
		}
		if basemodel.DB != nil {
			basemodel.InitChannelCache()
		}
	}
	ok(c, gin.H{"id": channel.ID, "public_id": channel.PublicID, "name": channel.Name, "key_pool_mode": channel.KeyPoolMode})
}

func (h *handler) listChannels(c *gin.Context) {
	owner, valid := h.ownerID(c)
	if !valid {
		return
	}
	groupID, valid := paramID(c, "id")
	if !valid {
		return
	}
	channels, err := apigxmodel.ListChannelsByGroup(h.db, owner, groupID, 100)
	if err != nil {
		failFromRepo(c, err)
		return
	}
	items := make([]gin.H, 0, len(channels))
	for _, ch := range channels {
		items = append(items, gin.H{
			"id": ch.ID, "public_id": ch.PublicID, "name": ch.Name, "protocol": ch.Protocol,
			"share_group_id": ch.ShareGroupID, "key_pool_mode": ch.KeyPoolMode,
			"health_state": ch.HealthState, "base_url": ch.BaseURL,
		})
	}
	ok(c, gin.H{"items": items})
}

func (h *handler) getChannel(c *gin.Context) {
	owner, valid := h.ownerID(c)
	if !valid {
		return
	}
	id, valid := paramID(c, "id")
	if !valid {
		return
	}
	ch, err := apigxmodel.GetChannel(h.db, owner, id)
	if err != nil {
		failFromRepo(c, err)
		return
	}
	// 注意：不返回 BaseURL 之外的任何上游细节，也不返回凭据
	ok(c, gin.H{
		"id": ch.ID, "public_id": ch.PublicID, "name": ch.Name, "protocol": ch.Protocol,
		"share_group_id": ch.ShareGroupID, "base_url": ch.BaseURL, "key_pool_mode": ch.KeyPoolMode,
		"health_state": ch.HealthState, "health_reason": ch.HealthReason,
	})
}

func (h *handler) deleteChannel(c *gin.Context) {
	owner, valid := h.ownerID(c)
	if !valid {
		return
	}
	id, valid := paramID(c, "id")
	if !valid {
		return
	}
	shareChannel, err := apigxmodel.GetChannelIncludingDeleted(h.db, owner, id)
	if err != nil {
		failFromRepo(c, err)
		return
	}
	// 第一步单独提交并立即刷新缓存：从这一刻起，即使后续清理失败，底座渠道也不会
	// 再接收新请求。重试 DELETE 会继续完成剩余步骤。
	baseExists, err := channelpool.QuiesceBaseChannel(h.db, shareChannel.BaseChannelID)
	if err != nil {
		failFromRepo(c, err)
		return
	}
	if baseExists {
		basemodel.CacheUpdateChannelStatus(int(shareChannel.BaseChannelID), common.ChannelStatusManuallyDisabled)
	}

	// 第二步撤销明文 Key、多 Key 状态、模型和能力投影。底座行保留为停用墓碑，
	// 使历史任务仍能识别原渠道，同时没有任何可再次出站的凭据或能力。
	if err := channelpool.ClearBaseChannelProjection(h.db, shareChannel.BaseChannelID); err != nil {
		failFromRepo(c, err)
		return
	}
	if shareChannel.AliveFlag == apigxmodel.AliveFlagAlive {
		if err := apigxmodel.DeleteChannel(h.db, owner, id); err != nil {
			failFromRepo(c, err)
			return
		}
	}
	if basemodel.DB != nil {
		basemodel.InitChannelCache()
	}
	ok(c, gin.H{"deleted": id})
}

// ---------------------------------------------------------------------------
// 上游 Key 池
// ---------------------------------------------------------------------------

type createUpstreamKeyRequest struct {
	StableKeyID string `json:"stable_key_id"`
	Name        string `json:"name"`
	// Key 是**明文**，仅在此处出现；服务端立即加密，落库只存信封，响应不回显。
	Key               string `json:"key"`
	Weight            *int64 `json:"weight"`
	ExpiresAt         *int64 `json:"expires_at"`
	UpstreamAccountID *int64 `json:"upstream_account_id"`
}

func (h *handler) createUpstreamKey(c *gin.Context) {
	owner, valid := h.ownerID(c)
	if !valid {
		return
	}
	channelID, valid := paramID(c, "id")
	if !valid {
		return
	}
	var req createUpstreamKeyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "invalid_body", "invalid request body")
		return
	}
	if strings.TrimSpace(req.Key) == "" {
		fail(c, http.StatusBadRequest, "missing_key", "upstream key is required")
		return
	}
	if req.Weight != nil && (*req.Weight < 1 || *req.Weight > 1000) {
		fail(c, http.StatusBadRequest, "invalid_weight", "weight must be between 1 and 1000")
		return
	}

	// 立即加密：明文不落库、不进日志、不回显。
	plain := strings.TrimSpace(req.Key)
	envelope, err := secretbox.Encrypt(plain)
	if err != nil {
		// 主密钥未配置时失败关闭——绝不退化成明文存储
		common.SysError("apigx api: refuse to store upstream key: " + err.Error())
		fail(c, http.StatusServiceUnavailable, "secret_store_unavailable",
			"credential store is not configured; refusing to store the key")
		return
	}
	// 指纹用于查重：密文带随机 nonce 无法比对，重复导入只能靠它发现。
	fingerprint, err := secretbox.Fingerprint(plain)
	if err != nil {
		common.SysError("apigx api: fingerprint failed: " + err.Error())
		fail(c, http.StatusServiceUnavailable, "secret_store_unavailable", "credential store is not configured")
		return
	}

	key := &apigxmodel.UpstreamKey{
		ChannelID:         channelID,
		StableKeyID:       req.StableKeyID,
		Name:              strings.TrimSpace(req.Name),
		SecretRef:         envelope,
		SecretFingerprint: fingerprint,
		Status:            apigxmodel.UpstreamKeyStatusEnabled,
	}
	if strings.TrimSpace(key.StableKeyID) == "" {
		key.StableKeyID = "key-" + common.GetRandomString(12)
	}
	if req.Weight != nil {
		key.Weight = *req.Weight
	}
	if req.ExpiresAt != nil {
		key.ExpiresAt = *req.ExpiresAt
	}
	if req.UpstreamAccountID != nil {
		key.UpstreamAccountID = *req.UpstreamAccountID
	}
	if err := apigxmodel.CreateUpstreamKey(h.db, owner, key); err != nil {
		failFromRepo(c, err)
		return
	}

	// 响应只给脱敏提示：后 4 位，其余打码。
	ok(c, gin.H{
		"id": key.ID, "stable_key_id": key.StableKeyID, "name": key.Name,
		"status": key.Status, "weight": key.Weight, "masked": maskTail(req.Key),
	})
}

// maskTail 只保留末尾 4 位。用于给分享者确认「加的是哪一把」，不构成泄露。
func maskTail(secret string) string {
	s := strings.TrimSpace(secret)
	if len(s) <= 4 {
		return "****"
	}
	return "****" + s[len(s)-4:]
}

func (h *handler) listUpstreamKeys(c *gin.Context) {
	owner, valid := h.ownerID(c)
	if !valid {
		return
	}
	channelID, valid := paramID(c, "id")
	if !valid {
		return
	}
	keys, err := apigxmodel.ListUpstreamKeysByChannel(h.db, owner, channelID)
	if err != nil {
		failFromRepo(c, err)
		return
	}
	now := common.GetTimestamp()
	items := make([]gin.H, 0, len(keys))
	for _, k := range keys {
		// 只输出脱敏与状态类信息：**绝不输出 SecretRef**（那是密文信封，也属于凭据材料）
		items = append(items, gin.H{
			"id": k.ID, "stable_key_id": k.StableKeyID, "name": k.Name,
			"status": k.Status, "weight": k.Weight, "cooldown_until": k.CooldownUntil,
			"expires_at": k.ExpiresAt, "credential_version": k.CredentialVersion,
			"usable": apigxmodel.IsUpstreamKeyUsable(&k, now),
			"masked": "****",
		})
	}
	ok(c, gin.H{"items": items})
}

type rotateUpstreamKeyRequest struct {
	Key string `json:"key"`
}

func (h *handler) rotateUpstreamKey(c *gin.Context) {
	owner, valid := h.ownerID(c)
	if !valid {
		return
	}
	id, valid := paramID(c, "id")
	if !valid {
		return
	}
	var req rotateUpstreamKeyRequest
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Key) == "" {
		fail(c, http.StatusBadRequest, "missing_key", "upstream key is required")
		return
	}
	plain := strings.TrimSpace(req.Key)
	envelope, err := secretbox.Encrypt(plain)
	if err != nil {
		fail(c, http.StatusServiceUnavailable, "secret_store_unavailable", "credential store is not configured")
		return
	}
	fingerprint, err := secretbox.Fingerprint(plain)
	if err != nil {
		fail(c, http.StatusServiceUnavailable, "secret_store_unavailable", "credential store is not configured")
		return
	}
	key, err := apigxmodel.RotateUpstreamKeyCredential(h.db, owner, id, envelope, fingerprint)
	if err != nil {
		failFromRepo(c, err)
		return
	}
	if _, err := channelpool.ProjectKeyPool(h.db, key.ChannelID); err != nil {
		failFromRepo(c, err)
		return
	}
	ok(c, gin.H{"id": key.ID, "credential_version": key.CredentialVersion, "masked": maskTail(plain)})
}

type updateStatusRequest struct {
	Status *int `json:"status"`
}

func (h *handler) updateKeyStatus(c *gin.Context) {
	owner, valid := h.ownerID(c)
	if !valid {
		return
	}
	id, valid := paramID(c, "id")
	if !valid {
		return
	}
	var req updateStatusRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.Status == nil {
		fail(c, http.StatusBadRequest, "invalid_body", "status is required")
		return
	}
	switch *req.Status {
	case apigxmodel.UpstreamKeyStatusDisabled, apigxmodel.UpstreamKeyStatusEnabled, apigxmodel.UpstreamKeyStatusInvalid:
	default:
		fail(c, http.StatusBadRequest, "invalid_status", "unsupported status")
		return
	}
	if err := apigxmodel.UpdateUpstreamKeyStatus(h.db, owner, id, *req.Status); err != nil {
		failFromRepo(c, err)
		return
	}
	ok(c, gin.H{"id": id, "status": *req.Status})
}

type updateWeightRequest struct {
	Weight *int64 `json:"weight"`
}

func (h *handler) updateKeyWeight(c *gin.Context) {
	owner, valid := h.ownerID(c)
	if !valid {
		return
	}
	id, valid := paramID(c, "id")
	if !valid {
		return
	}
	var req updateWeightRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.Weight == nil {
		fail(c, http.StatusBadRequest, "invalid_body", "weight is required")
		return
	}
	if err := apigxmodel.UpdateUpstreamKeyWeight(h.db, owner, id, *req.Weight); err != nil {
		failFromRepo(c, err)
		return
	}
	ok(c, gin.H{"id": id, "weight": *req.Weight})
}

type updateCooldownRequest struct {
	CooldownUntil *int64 `json:"cooldown_until"`
}

func (h *handler) updateKeyCooldown(c *gin.Context) {
	owner, valid := h.ownerID(c)
	if !valid {
		return
	}
	id, valid := paramID(c, "id")
	if !valid {
		return
	}
	var req updateCooldownRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.CooldownUntil == nil {
		fail(c, http.StatusBadRequest, "invalid_body", "cooldown_until is required")
		return
	}
	if err := apigxmodel.SetUpstreamKeyCooldown(h.db, owner, id, *req.CooldownUntil); err != nil {
		failFromRepo(c, err)
		return
	}
	ok(c, gin.H{"id": id, "cooldown_until": *req.CooldownUntil})
}

func (h *handler) deleteUpstreamKey(c *gin.Context) {
	owner, valid := h.ownerID(c)
	if !valid {
		return
	}
	id, valid := paramID(c, "id")
	if !valid {
		return
	}
	if err := apigxmodel.DeleteUpstreamKey(h.db, owner, id); err != nil {
		failFromRepo(c, err)
		return
	}
	ok(c, gin.H{"deleted": id})
}

// ---------------------------------------------------------------------------
// 应用 Key 池（投影到底座渠道）
// ---------------------------------------------------------------------------

// applyChannelPool 把当前 Key 池投影到底座渠道，使分享者的改动生效。
//
// 为什么需要这个显式动作：投影会**重写底座渠道的 Key 列表并清空它按索引存放的状态**
// （索引会变，旧状态套到别的凭据上就是隐性故障）。因此它不应该在每次加/删 Key 时自动发生，
// 而应由分享者确认后触发——也便于把「改了但没生效」这件事说清楚。
//
// 返回 409 表示池内没有可用凭据：这不是参数错误，而是**渠道将不会接收新请求**
// （计划书 §9.2），必须让分享者看到。
func (h *handler) applyChannelPool(c *gin.Context) {
	owner, valid := h.ownerID(c)
	if !valid {
		return
	}
	id, valid := paramID(c, "id")
	if !valid {
		return
	}
	// 归属校验：不属于自己的渠道一律 404（与不存在等价）
	shareChannel, err := apigxmodel.GetChannel(h.db, owner, id)
	if err != nil {
		failFromRepo(c, err)
		return
	}

	projection, err := channelpool.ProjectKeyPool(h.db, shareChannel.ID)
	if err != nil {
		if errors.Is(err, channelpool.ErrNoUsableKey) {
			fail(c, http.StatusConflict, "no_usable_key",
				"no usable upstream key in this channel; the channel will not accept new requests")
			return
		}
		failFromRepo(c, err)
		return
	}

	// 只回数量，不回任何凭据信息
	ok(c, gin.H{"channel_id": shareChannel.ID, "applied_keys": projection.KeyCount})
}

// ---------------------------------------------------------------------------
// 渠道容量策略
// ---------------------------------------------------------------------------

type capacityPolicyRequest struct {
	// 各维度都遵循四态：0 未配置 / 1 禁用 / 2 继承 / 3 明确不限 / 4 明确值。
	ConcurrencyMode  *int   `json:"concurrency_mode"`
	ConcurrencyValue *int64 `json:"concurrency_value"`
	RPMMode          *int   `json:"rpm_mode"`
	RPMLimit         *int64 `json:"rpm_limit"`
	TPMMode          *int   `json:"tpm_mode"`
	TPMLimit         *int64 `json:"tpm_limit"`
}

// setChannelCapacityPolicy 为分享渠道设置容量策略（当前支持"并发"维度）。
//
// 写入的是**四态原始值**，不做解释：解释统一由 `apigx/capacity` 的四态解析器负责，
// 避免接口层与解析层各有一套规则。策略变更后上限最长一个缓存 TTL 生效（准入宽严的变化，
// 不影响任何记账正确性）。
func (h *handler) setChannelCapacityPolicy(c *gin.Context) {
	owner, valid := h.ownerID(c)
	if !valid {
		return
	}
	id, valid := paramID(c, "id")
	if !valid {
		return
	}
	shareChannel, err := apigxmodel.GetChannel(h.db, owner, id)
	if err != nil {
		failFromRepo(c, err)
		return
	}
	var req capacityPolicyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "invalid_body", "invalid request body")
		return
	}
	// 至少要提供一个维度：否则这个请求什么也没配（但**不要求**必须是并发维度——
	// 早期强制要求 concurrency_mode，会让"只配 RPM/TPM"变成不可能，属设计缺陷）。
	if req.ConcurrencyMode == nil && req.RPMMode == nil && req.TPMMode == nil {
		fail(c, http.StatusBadRequest, "missing_mode", "at least one dimension mode is required")
		return
	}
	// 未提供的维度按「未配置」处理，并逐个用四态解析器校验「模式 + 值」的组合
	value := int64(0)
	if req.ConcurrencyValue != nil {
		value = *req.ConcurrencyValue
	}
	concurrencyMode := capacity.LimitModeUnset
	if req.ConcurrencyMode != nil {
		concurrencyMode = *req.ConcurrencyMode
	}
	policy := capacity.Policy{Mode: concurrencyMode, Value: value}
	if err := policy.Validate(); err != nil {
		fail(c, http.StatusBadRequest, "invalid_policy", err.Error())
		return
	}
	rpmValue := int64(0)
	if req.RPMLimit != nil {
		rpmValue = *req.RPMLimit
	}
	rpmMode := capacity.LimitModeUnset
	if req.RPMMode != nil {
		rpmMode = *req.RPMMode
	}
	if err := (capacity.Policy{Mode: rpmMode, Value: rpmValue}).Validate(); err != nil {
		fail(c, http.StatusBadRequest, "invalid_policy", err.Error())
		return
	}
	tpmValue := int64(0)
	if req.TPMLimit != nil {
		tpmValue = *req.TPMLimit
	}
	tpmMode := capacity.LimitModeUnset
	if req.TPMMode != nil {
		tpmMode = *req.TPMMode
	}
	if err := (capacity.Policy{Mode: tpmMode, Value: tpmValue}).Validate(); err != nil {
		fail(c, http.StatusBadRequest, "invalid_policy", err.Error())
		return
	}

	// 无论新建还是更新，都必须把策略**挂到渠道上**（`apigx_channels.capacity_policy_id`）。
	// 只写策略行不建立这层引用的话，读取侧按渠道的 FK 找不到策略——上限静默不生效。
	// 这是本接口首版实现的真实缺陷，由端到端验证发现（策略=明确值 1 时并发 3 仍全部放行）。
	linkPolicy := func(policyID int64) error {
		return h.db.Model(&apigxmodel.Channel{}).
			Where("id = ? AND owner_user_id = ? AND alive_flag = ?", shareChannel.ID, owner, apigxmodel.AliveFlagAlive).
			Updates(map[string]any{"capacity_policy_id": policyID, "updated_at": common.GetTimestamp()}).Error
	}

	// 同一渠道只保留一条策略（唯一索引是 scope 维度），因此存在则更新
	existing, err := apigxmodel.FindCapacityPolicyByScope(h.db, owner, apigxmodel.CapacityScopeChannel, shareChannel.ID)
	switch {
	case err == nil:
		updateErr := h.db.Model(&apigxmodel.CapacityPolicy{}).Where("id = ?", existing.ID).
			Updates(map[string]any{
				"max_concurrency_mode": concurrencyMode,
				"max_concurrency":      policy.Value,
				// **每个维度都必须出现在更新映射里**：早期只写了并发与版本号，
				// 导致"改 RPM/TPM"静默不生效（内存对象改了、库里没改）。
				// 表现极具迷惑性：新建策略（走创建分支）时一切正常，
				// 只有更新已有策略时才失效——由 TPM 端到端验证发现。
				"rpm_mode":       rpmMode,
				"rpm_limit":      rpmValue,
				"tpm_mode":       tpmMode,
				"tpm_limit":      tpmValue,
				"policy_version": existing.PolicyVersion + 1,
				"updated_at":     common.GetTimestamp(),
			}).Error
		if updateErr != nil {
			failFromRepo(c, updateErr)
			return
		}
		existing.MaxConcurrencyMode = concurrencyMode
		existing.MaxConcurrency = policy.Value
		existing.RpmMode = rpmMode
		existing.RpmLimit = rpmValue
		existing.TpmMode = tpmMode
		existing.TpmLimit = tpmValue
		if linkErr := linkPolicy(existing.ID); linkErr != nil {
			failFromRepo(c, linkErr)
			return
		}
		// 准入热路径缓存了渠道策略链。写入成功后必须立即失效，否则质量路由会按新上限
		// 判断容量，而最终原子预留仍按旧策略执行，产生“看起来已满但仍继续选中”的窗口。
		channelpool.InvalidatePolicyCache(shareChannel.ID)
		ok(c, gin.H{"id": existing.ID, "concurrency_mode": policy.Mode, "concurrency_value": policy.Value})
	case errors.Is(err, apigxmodel.ErrNotFound):
		created := &apigxmodel.CapacityPolicy{
			OwnerUserID: owner, ScopeType: apigxmodel.CapacityScopeChannel, ScopeID: shareChannel.ID,
			PolicyVersion:      1,
			MaxConcurrencyMode: concurrencyMode, MaxConcurrency: policy.Value,
			RpmMode: rpmMode, RpmLimit: rpmValue,
			TpmMode: tpmMode, TpmLimit: tpmValue,
		}
		now := common.GetTimestamp()
		created.CreatedAt, created.UpdatedAt = now, now
		if createErr := h.db.Create(created).Error; createErr != nil {
			failFromRepo(c, createErr)
			return
		}
		if linkErr := linkPolicy(created.ID); linkErr != nil {
			failFromRepo(c, linkErr)
			return
		}
		channelpool.InvalidatePolicyCache(shareChannel.ID)
		ok(c, gin.H{"id": created.ID, "concurrency_mode": policy.Mode, "concurrency_value": policy.Value})
	default:
		failFromRepo(c, err)
	}
}

// ---------------------------------------------------------------------------
// 平台 Key 的 APIGX 偏好（T1.10）
// ---------------------------------------------------------------------------

type platformKeyPreferencesRequest struct {
	ScopeMode              *int     `json:"scope_mode"`
	PinnedOfferingID       *int64   `json:"pinned_offering_id"`
	PinnedOfferingPublicID *string  `json:"pinned_offering_public_id"`
	AllowedModelIDs        []string `json:"allowed_model_ids"`
	AllowedOwnerUserIDs    []int64  `json:"allowed_owner_user_ids"`
	AllowFallback          *bool    `json:"allow_fallback"`
	// DefaultRouteMode 是「默认路由策略」（计划书 §9.1：保存到平台 Key 的偏好）。
	// 空串与 "auto" 都表示综合自动；latency / throughput / reliability 会把它
	// 变成**请求级候选限定**（见 relayhook.EligibleBaseChannelsForStrategy）。
	DefaultRouteMode *string `json:"default_route_mode"`
}

func (h *handler) getPlatformKeyPreferences(c *gin.Context) {
	owner, valid := h.ownerID(c)
	if !valid {
		return
	}
	tokenID, valid := paramID(c, "tokenId")
	if !valid {
		return
	}
	key, err := apigxmodel.GetAPIKeyByToken(h.db, owner, tokenID)
	if errors.Is(err, apigxmodel.ErrNotFound) {
		// 未配置偏好 = 按默认自动路由（不是错误）
		ok(c, gin.H{
			"token_id": tokenID, "scope_mode": apigxmodel.ScopeModeDefault, "configured": false,
			"pinned_offering_id": int64(0), "pinned_offering_public_id": "",
			"allowed_model_ids": []string{}, "allowed_owner_user_ids": []int64{},
			"allow_fallback": true, "default_route_mode": "auto",
		})
		return
	}
	if err != nil {
		failFromRepo(c, err)
		return
	}
	pinnedPublicID := h.visibleOfferingPublicID(owner, key.PinnedOfferingID)
	ok(c, gin.H{
		"token_id":                  key.TokenID,
		"configured":                true,
		"scope_mode":                key.ScopeMode,
		"pinned_offering_id":        key.PinnedOfferingID,
		"pinned_offering_public_id": pinnedPublicID,
		"allowed_model_ids":         key.AllowedModels(),
		"allowed_owner_user_ids":    key.AllowedOwnerIDs(),
		"allow_fallback":            key.AllowFallback,
		"default_route_mode":        key.DefaultRouteMode,
	})
}

// setPlatformKeyPreferences 设置某平台 Key 的 APIGX 偏好。
//
// 凭据本身仍由底座的令牌体系持有（TD-07），这里只写偏好；校验在仓储层完成
// （例如「允许来源」模式必须给出非空列表），避免写入看起来生效实际不生效的组合。
func (h *handler) setPlatformKeyPreferences(c *gin.Context) {
	owner, valid := h.ownerID(c)
	if !valid {
		return
	}
	tokenID, valid := paramID(c, "tokenId")
	if !valid {
		return
	}
	var req platformKeyPreferencesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "invalid_body", "invalid request body")
		return
	}
	if req.PinnedOfferingPublicID != nil {
		publicID := strings.TrimSpace(*req.PinnedOfferingPublicID)
		if publicID == "" {
			zero := int64(0)
			req.PinnedOfferingID = &zero
		} else {
			item, resolveErr := apigxmodel.GetPlazaItem(h.db, owner, publicID)
			if resolveErr != nil {
				failFromRepo(c, resolveErr)
				return
			}
			req.PinnedOfferingID = &item.OfferingID
		}
	}

	key, err := apigxmodel.UpsertAPIKeyPreferences(h.db, owner, tokenID, func(k *apigxmodel.APIKey) error {
		if req.ScopeMode != nil {
			k.ScopeMode = *req.ScopeMode
		}
		if req.PinnedOfferingID != nil {
			k.PinnedOfferingID = *req.PinnedOfferingID
		}
		if req.DefaultRouteMode != nil {
			// 只接受已知取值或空串（空串=自动）：**不接受拼错的策略名**——
			// 否则用户以为设了策略、实际永远不会生效（静默失效）。
			// 这里返回错误（由上层映射为 400），而不是直接写响应：本函数是回调，只负责改字段。
			mode := strings.ToLower(strings.TrimSpace(*req.DefaultRouteMode))
			switch mode {
			case "", "auto", "latency", "throughput", "reliability":
				k.DefaultRouteMode = mode
			default:
				return fmt.Errorf("%w: unknown default_route_mode %q", apigxmodel.ErrInvalidArgument, mode)
			}
		}
		if req.AllowFallback != nil {
			k.AllowFallback = *req.AllowFallback
		}
		if req.AllowedModelIDs != nil {
			encoded, encErr := common.Marshal(req.AllowedModelIDs)
			if encErr != nil {
				return encErr
			}
			k.AllowedModelIDs = string(encoded)
		}
		if req.AllowedOwnerUserIDs != nil {
			return k.SetAllowedOwnerIDs(req.AllowedOwnerUserIDs)
		}
		return nil
	})
	if err != nil {
		failFromRepo(c, err)
		return
	}
	// 偏好已变更：让准入侧立刻看到新限制（否则最长一个缓存 TTL 才生效，
	// 「刚收紧来源却仍被放行」是不能接受的）
	relayhook.InvalidatePreferenceCache(tokenID)
	pinnedPublicID := h.visibleOfferingPublicID(owner, key.PinnedOfferingID)
	ok(c, gin.H{
		"token_id":                  key.TokenID,
		"scope_mode":                key.ScopeMode,
		"pinned_offering_id":        key.PinnedOfferingID,
		"pinned_offering_public_id": pinnedPublicID,
		"allowed_model_ids":         key.AllowedModels(),
		"allowed_owner_user_ids":    key.AllowedOwnerIDs(),
		"allow_fallback":            key.AllowFallback,
		"default_route_mode":        key.DefaultRouteMode,
	})
}

func (h *handler) visibleOfferingPublicID(owner, offeringID int64) string {
	if offeringID <= 0 {
		return ""
	}
	var offering apigxmodel.ModelOffering
	if err := h.db.Select("public_id").Where("id = ?", offeringID).First(&offering).Error; err != nil {
		return ""
	}
	if _, err := apigxmodel.GetPlazaItem(h.db, owner, offering.PublicID); err != nil {
		return ""
	}
	return offering.PublicID
}

// ---------------------------------------------------------------------------
// 用量记录（T1.11，使用者视角）
// ---------------------------------------------------------------------------

// listUsage 返回**当前使用者自己**的用量记录。
//
// 复用底座的 `model.GetUserLogs`：它已经处理了分页、过滤、以及**独立日志库**
// （含 ClickHouse）的分支，自己写查询会把这些全部重做一遍（二开第一原则，CODE_STYLE 第 25 条）。
//
// 在底座日志的基础上补两项 APIGX 关心的事：**实际提供服务的分享来源**
// （底座只给 channel_id，我们翻译成对外的 public_id 与所有者）以及金额换算。
// 归属由服务端强制：只按登录用户过滤，不接受任何客户端传入的 user 参数。
func (h *handler) listUsage(c *gin.Context) {
	owner, valid := h.ownerID(c)
	if !valid {
		return
	}
	page, pageSize := 1, 20
	if raw := c.Query("page"); raw != "" {
		if parsed, convErr := strconv.Atoi(raw); convErr == nil && parsed > 0 {
			page = parsed
		}
	}
	if raw := c.Query("page_size"); raw != "" {
		if parsed, convErr := strconv.Atoi(raw); convErr == nil && parsed > 0 && parsed <= 100 {
			pageSize = parsed
		}
	}
	start := int64(0)
	if raw := c.Query("start"); raw != "" {
		if parsed, convErr := strconv.ParseInt(raw, 10, 64); convErr == nil {
			start = parsed
		}
	}
	end := common.GetTimestamp()
	if raw := c.Query("end"); raw != "" {
		if parsed, convErr := strconv.ParseInt(raw, 10, 64); convErr == nil {
			end = parsed
		}
	}

	logs, total, err := basemodel.GetUserLogs(int(owner), 0, start, end, c.Query("model"), "", (page-1)*pageSize, pageSize, "", "", "")
	if err != nil {
		common.SysError("apigx api: list usage failed: " + err.Error())
		fail(c, http.StatusInternalServerError, "internal_error", "failed to load usage records")
		return
	}

	items := make([]gin.H, 0, len(logs))
	for _, entry := range logs {
		item := gin.H{
			"created_at":        entry.CreatedAt,
			"model":             entry.ModelName,
			"prompt_tokens":     entry.PromptTokens,
			"completion_tokens": entry.CompletionTokens,
			"total_tokens":      entry.PromptTokens + entry.CompletionTokens,
			// 配额/费用：底座 logs 里有（阶段 3 实测图片请求 quota=38/张），
			// 我们的视图此前没暴露，导致"我的用量"看不到花了多少。
			"quota": entry.Quota,
			// 底座以「quota」为记账单位；这里换算成微美元（1e-6 USD）便于使用者理解。
			// 换算率用底座的 QuotaPerUnit（与计费同一来源，不自己写死）。
			"cost_micro_usd": quotaToMicroUSD(entry.Quota),
			// 底座的日志只记录**整秒**（UseTime 为秒）。这里如实暴露秒级精度，
			// 不做「秒 × 1000」这种假毫秒——那会让亚秒请求显示成 0 ms，属于误导。
			"elapsed_seconds": entry.UseTime,
			"is_stream":       entry.IsStream,
			"request_id":      entry.RequestId,
		}
		// 实际来源：底座的 channel_id 翻译成对外可见的分享来源信息
		if publicID, ownerUserID, found := channelpool.ShareChannelPublicInfoByBase(h.db, entry.ChannelId); found {
			item["source"] = gin.H{"channel_public_id": publicID, "owner_user_id": ownerUserID, "kind": "share"}
		} else if entry.ChannelId > 0 {
			item["source"] = gin.H{"kind": "platform"}
		}
		items = append(items, item)
	}

	ok(c, gin.H{"items": items, "total": total, "page": page, "page_size": pageSize})
}

// quotaToMicroUSD 把底座的 quota 记账单位换算为微美元。
//
// 换算率取底座的 `common.QuotaPerUnit`（与计费同一来源）；该值为 0 时不做换算，
// 避免除零把金额显示成无意义的巨大数字。
func quotaToMicroUSD(quota int) int64 {
	if common.QuotaPerUnit <= 0 {
		return 0
	}
	return int64(float64(quota) / common.QuotaPerUnit * 1_000_000)
}

// ---------------------------------------------------------------------------
// 模型供给与发布流程（T1.8）
// ---------------------------------------------------------------------------

type createOfferingRequest struct {
	UpstreamModelID  string                 `json:"upstream_model_id"`
	CanonicalModelID *int64                 `json:"canonical_model_id"`
	Capabilities     *apigxmodel.Capability `json:"capabilities"`
}

func (h *handler) createOffering(c *gin.Context) {
	owner, valid := h.ownerID(c)
	if !valid {
		return
	}
	channelID, valid := paramID(c, "id")
	if !valid {
		return
	}
	var req createOfferingRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "invalid_body", "invalid request body")
		return
	}
	offering := &apigxmodel.ModelOffering{
		ChannelID:       channelID,
		UpstreamModelID: strings.TrimSpace(req.UpstreamModelID),
	}
	if req.CanonicalModelID != nil {
		offering.CanonicalModelID = *req.CanonicalModelID
	}
	if req.Capabilities != nil {
		encoded, encErr := common.Marshal(*req.Capabilities)
		if encErr != nil {
			fail(c, http.StatusBadRequest, "invalid_body", "invalid capabilities")
			return
		}
		offering.Capabilities = string(encoded)
	}
	if err := apigxmodel.CreateModelOffering(h.db, owner, offering); err != nil {
		failFromRepo(c, err)
		return
	}
	ok(c, gin.H{
		"id": offering.ID, "public_id": offering.PublicID,
		"upstream_model_id": offering.UpstreamModelID, "status": offering.Status,
	})
}

func (h *handler) listOfferings(c *gin.Context) {
	owner, valid := h.ownerID(c)
	if !valid {
		return
	}
	channelID, valid := paramID(c, "id")
	if !valid {
		return
	}
	list, err := apigxmodel.ListModelOfferingsByChannel(h.db, owner, channelID)
	if err != nil {
		failFromRepo(c, err)
		return
	}
	items := make([]gin.H, 0, len(list))
	for i := range list {
		item := &list[i]
		items = append(items, gin.H{
			"id": item.ID, "public_id": item.PublicID,
			"upstream_model_id": item.UpstreamModelID,
			"status":            item.Status, "revision": item.Revision,
			"capabilities": item.CapabilityOf(), "price_rule_id": item.PriceRuleID,
		})
	}
	ok(c, gin.H{"items": items})
}

type updateCapabilitiesRequest struct {
	Capabilities *apigxmodel.Capability `json:"capabilities"`
}

func (h *handler) updateOfferingCapabilities(c *gin.Context) {
	owner, valid := h.ownerID(c)
	if !valid {
		return
	}
	id, valid := paramID(c, "id")
	if !valid {
		return
	}
	var req updateCapabilitiesRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.Capabilities == nil {
		fail(c, http.StatusBadRequest, "invalid_body", "capabilities is required")
		return
	}
	if err := apigxmodel.UpdateModelOfferingCapabilities(h.db, owner, id, *req.Capabilities); err != nil {
		failFromRepo(c, err)
		return
	}
	ok(c, gin.H{"id": id})
}

// submitOffering 分享者提交审核（草稿或已驳回 → 待审核）。
func (h *handler) submitOffering(c *gin.Context) {
	owner, valid := h.ownerID(c)
	if !valid {
		return
	}
	id, valid := paramID(c, "id")
	if !valid {
		return
	}
	if err := apigxmodel.SubmitModelOffering(h.db, owner, id); err != nil {
		failFromRepo(c, err)
		return
	}
	ok(c, gin.H{"id": id, "status": apigxmodel.OfferingStatusPending})
}

// requirePlatformReviewer 判定当前身份是否有权做审核动作（发布 / 驳回）。
//
// 审核**必须**由平台侧完成：如果分享者能自行发布，"待审核"就形同虚设
// （计划书 §12 要求新公开资源先审核）。
func requirePlatformReviewer(c *gin.Context) bool {
	if c.GetInt("role") < common.RoleAdminUser {
		fail(c, http.StatusForbidden, "review_required",
			"this action requires platform review privileges")
		return false
	}
	return true
}

type reviewOfferingRequest struct {
	// ReasonCode 是脱敏的运营理由码，只允许 a-z / 0-9 / 点 / 下划线 / 连字符。
	// 不接受自由文本，避免把凭据或上游原始错误写入不可变审计表。
	ReasonCode string `json:"reason_code"`
}

func readReviewReasonCode(c *gin.Context, fallback string) (string, bool) {
	var req reviewOfferingRequest
	if err := c.ShouldBindJSON(&req); err != nil && !errors.Is(err, io.EOF) {
		fail(c, http.StatusBadRequest, "invalid_body", "invalid request body")
		return "", false
	}
	reason := strings.ToLower(strings.TrimSpace(req.ReasonCode))
	if reason == "" {
		reason = fallback
	}
	if len(reason) > 64 {
		fail(c, http.StatusBadRequest, "invalid_reason_code", "reason_code must be at most 64 characters")
		return "", false
	}
	for i := 0; i < len(reason); i++ {
		ch := reason[i]
		if (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '.' || ch == '_' || ch == '-' {
			continue
		}
		fail(c, http.StatusBadRequest, "invalid_reason_code", "reason_code contains unsupported characters")
		return "", false
	}
	return reason, true
}

func offeringReviewContext(c *gin.Context, reviewerID int64, reasonCode string) apigxmodel.OfferingReviewContext {
	ipHash := ""
	if clientIP := strings.TrimSpace(c.ClientIP()); clientIP != "" {
		sum := sha256.Sum256([]byte(clientIP))
		ipHash = hex.EncodeToString(sum[:])
	}
	userAgent := []rune(strings.TrimSpace(c.Request.UserAgent()))
	if len(userAgent) > 255 {
		userAgent = userAgent[:255]
	}
	return apigxmodel.OfferingReviewContext{
		ReviewerUserID: reviewerID,
		ReviewerRole:   c.GetInt("role"),
		ReviewerKeyID:  int64(c.GetInt("token_id")),
		ReasonCode:     reasonCode,
		RequestID:      c.GetString(common.RequestIdKey),
		IPHash:         ipHash,
		UserAgent:      string(userAgent),
	}
}

func (h *handler) listOfferingsForReview(c *gin.Context) {
	if _, valid := h.ownerID(c); !valid || !requirePlatformReviewer(c) {
		return
	}
	page, pageSize := 1, 20
	if parsed, err := strconv.Atoi(c.Query("page")); err == nil && parsed > 0 {
		page = parsed
	}
	if parsed, err := strconv.Atoi(c.Query("page_size")); err == nil && parsed > 0 && parsed <= 100 {
		pageSize = parsed
	}
	status := apigxmodel.OfferingStatusPending
	if c.Query("status") == "all" {
		status = -1
	}
	items, total, err := apigxmodel.ListOfferingsForReview(h.db, status, (page-1)*pageSize, pageSize)
	if err != nil {
		failFromRepo(c, err)
		return
	}
	ownerIDs := make([]int64, 0, len(items))
	for _, item := range items {
		ownerIDs = append(ownerIDs, item.OwnerUserID)
	}
	ownerNames := h.ownerDisplayNames(ownerIDs)
	result := make([]gin.H, 0, len(items))
	for _, item := range items {
		var caps apigxmodel.Capability
		_ = common.UnmarshalJsonStr(item.Capabilities, &caps)
		result = append(result, gin.H{
			"id": item.OfferingID, "public_id": item.OfferingPublicID,
			"owner_user_id": item.OwnerUserID, "owner_display_name": ownerNames[item.OwnerUserID],
			"share_group_id": item.ShareGroupID, "share_group_public_id": item.ShareGroupPublicID,
			"share_group_name": item.ShareGroupName, "group_visibility": item.GroupVisibility,
			"channel_id": item.ChannelID, "channel_public_id": item.ChannelPublicID, "channel_name": item.ChannelName,
			"upstream_model_id": item.UpstreamModelID, "capabilities": caps,
			"status": item.Status, "revision": item.Revision,
			"price_configured": h.offeringHasAcceptedPrice(item.OfferingID),
			"created_at":       item.CreatedAt, "updated_at": item.UpdatedAt,
		})
	}
	ok(c, gin.H{"items": result, "total": total, "page": page, "page_size": pageSize})
}

func (h *handler) publishOffering(c *gin.Context) {
	reviewer, valid := h.ownerID(c)
	if !valid || !requirePlatformReviewer(c) {
		return
	}
	id, valid := paramID(c, "id")
	if !valid {
		return
	}
	reasonCode, valid := readReviewReasonCode(c, "approved")
	if !valid {
		return
	}
	// **强制要求设置价格**（与底座一致：底座在价格未配置时直接判定"模型暂时无法使用"）。
	// 分享者在发布供给前必须已有**被接受的价格**（供给作用域或渠道作用域皆可），
	// 否则这份供给不该出现在广场里、也不该被计费型调用选中。
	if !h.offeringHasAcceptedPrice(id) {
		fail(c, http.StatusBadRequest, "price_not_configured",
			"该模型的价格未配置，无法发布；请先配置价格（系统设置 → 分组与模型定价设置）或提交价格同步")
		return
	}
	var reviewed *apigxmodel.OfferingReviewResult
	err := h.db.Transaction(func(tx *gorm.DB) error {
		var reviewErr error
		reviewed, reviewErr = apigxmodel.ReviewModelOffering(tx, id, apigxmodel.OfferingStatusPublished,
			offeringReviewContext(c, reviewer, reasonCode))
		if reviewErr != nil {
			return reviewErr
		}
		// 审核状态、不可变审计、底座模型列表与能力必须一起提交。否则能力写入失败时，
		// 广场会出现一条已经发布但底座永远无法选择的供给。
		return syncBaseChannelServesModel(tx, id)
	})
	if err != nil {
		failFromRepo(c, err)
		return
	}
	if basemodel.DB != nil {
		basemodel.InitChannelCache()
	}
	ok(c, gin.H{
		"id": reviewed.OfferingID, "public_id": reviewed.OfferingPublicID,
		"owner_user_id": reviewed.OwnerUserID,
		"from_status":   reviewed.FromStatus, "status": reviewed.ToStatus,
	})
}

// offeringHasAcceptedPrice 判断该供给是否已有**被接受**的价格（供给作用域或它所属渠道作用域）。
//
// 与底座"价格未配置即不可用"的语义对齐；这里只做**存在性**判断，不参与具体计价。
func (h *handler) offeringHasAcceptedPrice(offeringID int64) bool {
	var offering apigxmodel.ModelOffering
	if err := h.db.Where("id = ?", offeringID).First(&offering).Error; err != nil {
		return false
	}
	scopes := []struct {
		scopeType int
		scopeID   int64
	}{
		{apigxmodel.PriceScopeOffering, offeringID},
		{apigxmodel.PriceScopeChannel, offering.ChannelID},
	}
	for _, scope := range scopes {
		if scope.scopeID <= 0 {
			continue
		}
		if _, _, err := apigxmodel.GetEffectivePriceRule(h.db, scope.scopeType, scope.scopeID, 0); err == nil {
			return true
		}
	}
	return false
}

func (h *handler) rejectOffering(c *gin.Context) {
	reviewer, valid := h.ownerID(c)
	if !valid || !requirePlatformReviewer(c) {
		return
	}
	id, valid := paramID(c, "id")
	if !valid {
		return
	}
	reasonCode, valid := readReviewReasonCode(c, "policy_rejected")
	if !valid {
		return
	}
	reviewed, err := apigxmodel.ReviewModelOffering(h.db, id, apigxmodel.OfferingStatusRejected,
		offeringReviewContext(c, reviewer, reasonCode))
	if err != nil {
		failFromRepo(c, err)
		return
	}
	ok(c, gin.H{
		"id": reviewed.OfferingID, "public_id": reviewed.OfferingPublicID,
		"owner_user_id": reviewed.OwnerUserID,
		"from_status":   reviewed.FromStatus, "status": reviewed.ToStatus,
	})
}

func (h *handler) deleteOffering(c *gin.Context) {
	owner, valid := h.ownerID(c)
	if !valid {
		return
	}
	id, valid := paramID(c, "id")
	if !valid {
		return
	}
	if err := apigxmodel.DeleteModelOffering(h.db, owner, id); err != nil {
		failFromRepo(c, err)
		return
	}
	ok(c, gin.H{"deleted": id})
}

// isDuplicateError 判定是否为唯一约束冲突（SQLite / MySQL / PostgreSQL 的文本各不相同）。
func isDuplicateError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unique constraint failed") ||
		strings.Contains(msg, "duplicate entry") ||
		strings.Contains(msg, "duplicate key value")
}

type fetchModelsRequest struct {
	// Import 为 true 时，把拉取到的模型**批量导入为草稿供给**（一键获取的落库动作）。
	Import bool `json:"import"`
}

// fetchUpstreamModels 一键获取该渠道的上游模型列表（可选直接导入为草稿供给）。
//
// **复用底座的取模型能力**：按渠道类型分派到对应 provider 适配器（40+ 家）的实现已经在底座里，
// 这里通过 `controller.FetchChannelUpstreamModelIDsForAPIGX`（3 行导出包装，台账 D-017）调用它，
// 而不是自己重写一套。归属仍由服务端强制：先按 owner 取出分享渠道，再用它绑定的底座渠道去拉取。
func (h *handler) fetchUpstreamModels(c *gin.Context) {
	owner, valid := h.ownerID(c)
	if !valid {
		return
	}
	id, valid := paramID(c, "id")
	if !valid {
		return
	}
	var req fetchModelsRequest
	_ = c.ShouldBindJSON(&req) // 允许空 body（仅预览）

	shareChannel, err := apigxmodel.GetChannel(h.db, owner, id)
	if err != nil {
		failFromRepo(c, err)
		return
	}
	if shareChannel.BaseChannelID <= 0 {
		fail(c, http.StatusBadRequest, "invalid_channel", "share channel has no base channel")
		return
	}
	var baseChannel basemodel.Channel
	if err := h.db.Where("id = ?", shareChannel.BaseChannelID).First(&baseChannel).Error; err != nil {
		failFromRepo(c, err)
		return
	}

	modelIDs, err := controller.FetchChannelUpstreamModelIDsForAPIGX(&baseChannel)
	if err != nil {
		// 上游取列表失败通常是凭据/网络问题：对外给明确原因，但不泄露上游原始报文
		common.SysError("apigx api: fetch upstream models failed: " + err.Error())
		fail(c, http.StatusBadGateway, "upstream_fetch_failed", "failed to fetch models from upstream")
		return
	}

	result := gin.H{"models": modelIDs}
	if req.Import {
		created, skipped, importErr := apigxmodel.ImportModelOfferings(h.db, owner, shareChannel.ID, modelIDs)
		if importErr != nil {
			failFromRepo(c, importErr)
			return
		}
		result["created"] = created
		result["skipped"] = skipped
	}
	ok(c, result)
}

// ---------------------------------------------------------------------------
// 广场与详情（T1.9）
// ---------------------------------------------------------------------------

// listPlaza 列出广场条目：**只含已发布且分组公开的供给**。
//
// 能力对外**只给平台当前真正具备的部分**（见 `PlatformSupportedCapabilities`），
// 分享者声明但尚未具备的单独列在 `unverified_capabilities` 里如实展示——
// 既不冒充可用，也不隐瞒（计划书「只启用已完成能力」）。
func (h *handler) listPlaza(c *gin.Context) {
	// 保留浏览者身份：广场的可见性判定需要它（公开 / 本人所有 / 有未过期授权）
	viewer, valid := h.ownerID(c)
	if !valid {
		return
	}
	page, pageSize := 1, 20
	if raw := c.Query("page"); raw != "" {
		if parsed, convErr := strconv.Atoi(raw); convErr == nil && parsed > 0 {
			page = parsed
		}
	}
	if raw := c.Query("page_size"); raw != "" {
		if parsed, convErr := strconv.Atoi(raw); convErr == nil && parsed > 0 && parsed <= 100 {
			pageSize = parsed
		}
	}
	items, total, err := apigxmodel.ListPlazaItems(h.db, viewer, pageSize, (page-1)*pageSize)
	if err != nil {
		failFromRepo(c, err)
		return
	}
	out := make([]gin.H, 0, len(items))
	ownerIDs := make([]int64, 0, len(items))
	for _, item := range items {
		ownerIDs = append(ownerIDs, item.OwnerUserID)
	}
	ownerNames := h.ownerDisplayNames(ownerIDs)
	for _, item := range items {
		item.OwnerDisplayName = ownerNames[item.OwnerUserID]
		out = append(out, plazaItemToJSON(item))
	}
	ok(c, gin.H{"items": out, "total": total, "page": page, "page_size": pageSize})
}

func (h *handler) getPlazaItem(c *gin.Context) {
	viewer, valid := h.ownerID(c)
	if !valid {
		return
	}
	publicID := c.Param("publicId")
	item, err := apigxmodel.GetPlazaItem(h.db, viewer, publicID)
	if err != nil {
		failFromRepo(c, err)
		return
	}
	item.OwnerDisplayName = h.ownerDisplayNames([]int64{item.OwnerUserID})[item.OwnerUserID]
	ok(c, plazaItemToJSON(*item))
}

func (h *handler) ownerDisplayNames(ownerIDs []int64) map[int64]string {
	if !h.db.Migrator().HasTable(&basemodel.User{}) {
		return map[int64]string{}
	}
	ids := make([]int64, 0, len(ownerIDs))
	seen := make(map[int64]struct{}, len(ownerIDs))
	for _, ownerID := range ownerIDs {
		if _, exists := seen[ownerID]; exists {
			continue
		}
		seen[ownerID] = struct{}{}
		ids = append(ids, ownerID)
	}
	type ownerRow struct {
		ID          int64
		Username    string
		DisplayName string
	}
	rows := make([]ownerRow, 0, len(ids))
	if len(ids) > 0 {
		_ = h.db.Model(&basemodel.User{}).Select("id", "username", "display_name").Where("id IN ?", ids).Scan(&rows).Error
	}
	out := make(map[int64]string, len(rows))
	for _, row := range rows {
		name := strings.TrimSpace(row.DisplayName)
		if name == "" {
			name = row.Username
		}
		out[row.ID] = name
	}
	return out
}

// plazaItemToJSON 把广场条目转成对外 JSON。
//
// 只输出对外可见字段：**不含**渠道内部 ID、上游地址、凭据、成本价或任何内部标识。
func plazaItemToJSON(item apigxmodel.PlazaItem) gin.H {
	out := gin.H{
		"offering_public_id": item.OfferingPublicID,
		// 对外标准模型 ID 与介绍（仅当映射**已确认**时才有值；未命中为空，界面显示上游名）
		"canonical_model_id":          item.CanonicalAPIModelID,
		"model_description":           item.ModelDescription,
		"sample_count":                item.SampleCount,
		"reliability_lower_bound_ppm": item.ReliabilityLowerBoundPpm,
		"latency_ms_p50":              item.LatencyMsP50,
		"insufficient":                item.Insufficient,
		"stale":                       item.Stale,
		"group_description":           item.GroupDescription,
		"group_public_id":             item.GroupPublicID,
		"group_name":                  item.GroupName,
		"model":                       item.UpstreamModelID,
		"channel_public_id":           item.ChannelPublicID,
		"owner_user_id":               item.OwnerUserID,
		"owner_display_name":          item.OwnerDisplayName,
		"capabilities":                item.Capabilities,
		"updated_at":                  item.UpdatedAt,
	}
	if len(item.UnverifiedCapabilities) > 0 {
		out["unverified_capabilities"] = item.UnverifiedCapabilities
	}
	return out
}

// ---------------------------------------------------------------------------
// 可解释路由结果（T2.9）
// ---------------------------------------------------------------------------

// explainRoute 回答"这个模型为什么选了/没选某条来源"。**只读**，不影响任何路由状态。
//
// 输出经 `routing.Explain` 逐字段挑选，只含对外标识与指标数值——
// 不含底座渠道 ID、上游 Key、内部作用域 ID（有泄露扫描用例守着）。
func (h *handler) explainRoute(c *gin.Context) {
	if _, valid := h.ownerID(c); !valid {
		return
	}
	modelName := strings.TrimSpace(c.Query("model"))
	if modelName == "" {
		fail(c, http.StatusBadRequest, "invalid_query", "model is required")
		return
	}
	strategy, valid := parseStrategyQuery(c.Query("strategy"))
	if !valid {
		fail(c, http.StatusBadRequest, "invalid_strategy", "unknown routing strategy")
		return
	}

	candidates, public, err := routing.AssembleCandidates(h.db, capacity.New(common.RDB), modelName,
		routing.AssembleOptions{})
	if err != nil {
		failFromRepo(c, err)
		return
	}
	// **解释必须反映真实状态**：正在熔断的供给要显示为被摘出（原因 breaker_open），
	// 否则用户看到"它在候选里"而实际请求根本不会用它——那是最难排查的一类不一致。
	applyBreakerToCandidates(candidates)
	explanation, err := routing.ExplainForStrategy(strategy, candidates, public, routing.DefaultOptions())
	if err != nil {
		fail(c, http.StatusBadRequest, "invalid_strategy", err.Error())
		return
	}
	ok(c, explanation)
}

// parseStrategyQuery 解析策略查询参数；空值取系统默认 auto（计划书：最后使用系统默认 auto）。
func parseStrategyQuery(raw string) (routing.Strategy, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "auto":
		return routing.StrategyAuto, true
	case "latency":
		return routing.StrategyLatency, true
	case "throughput":
		return routing.StrategyThroughput, true
	case "reliability":
		return routing.StrategyReliability, true
	case "pinned":
		return routing.StrategyPinned, true
	default:
		return 0, false
	}
}

// applyRouteRanking 把路由策略算出的名次**投影到底座渠道权重**（T2.7 生效路径）。
//
// # 为什么是平台侧动作
//
// 排序是**跨分享者**的（同一模型的供给来自不同分享者）。让某个分享者能改这份权重，
// 等于让他能抢别人的流量。因此要求平台审核权限（与发布审核同一门槛）。
//
// # 为什么默认先 dry_run
//
// 投影会**改动真实选路行为**。默认只预览（返回解释但不写库），
// 需要显式 `dry_run=false` 才落库——这种"会改变生产行为"的操作不该因为少写一个参数就发生。
func (h *handler) applyRouteRanking(c *gin.Context) {
	if _, valid := h.ownerID(c); !valid || !requirePlatformReviewer(c) {
		return
	}
	var req struct {
		Model    string `json:"model"`
		Strategy string `json:"strategy"`
		Group    string `json:"group"`
		// DryRun 为 true（默认）时只返回将要发生的事，不写库。
		DryRun *bool `json:"dry_run"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "invalid_body", "invalid request body")
		return
	}
	modelName := strings.TrimSpace(req.Model)
	if modelName == "" {
		fail(c, http.StatusBadRequest, "invalid_body", "model is required")
		return
	}
	strategy, strategyOK := parseStrategyQuery(req.Strategy)
	if !strategyOK {
		fail(c, http.StatusBadRequest, "invalid_strategy", "unknown routing strategy")
		return
	}
	dryRun := true
	if req.DryRun != nil {
		dryRun = *req.DryRun
	}
	group := strings.TrimSpace(req.Group)
	if group == "" {
		group = "default"
	}

	candidates, public, err := routing.AssembleCandidates(h.db, capacity.New(common.RDB), modelName,
		routing.AssembleOptions{})
	if err != nil {
		failFromRepo(c, err)
		return
	}
	// 按熔断摘除不可用候选，再决策（与解释接口共用同一函数，避免两处口径不一致）
	applyBreakerToCandidates(candidates)
	decision, err := routing.Decide(strategy, candidates, routing.DefaultOptions())
	if err != nil {
		fail(c, http.StatusBadRequest, "invalid_strategy", err.Error())
		return
	}
	explanation := routing.Explain(routing.ExplainInput{Decision: decision, Candidates: candidates, Public: public})

	result := gin.H{"dry_run": true, "explanation": explanation, "updated": 0}
	if dryRun {
		ok(c, result)
		return
	}
	applyResult, applyErr := routing.ApplyRankingToBaseChannels(h.db, decision, group, routing.DefaultApplyOptions())
	if applyErr != nil {
		failFromRepo(c, applyErr)
		return
	}
	skipped := make([]gin.H, 0, len(applyResult.Skipped))
	for _, item := range applyResult.Skipped {
		skipped = append(skipped, gin.H{"offering_id_public": item.OfferingID, "reason": item.Reason})
	}
	ok(c, gin.H{
		"dry_run":     false,
		"explanation": explanation,
		"updated":     applyResult.Updated,
		"skipped":     skipped,
	})
}

// ---------------------------------------------------------------------------
// 价格快照：人工覆盖、单渠道同步、读取（T2.1 / T2.2）
// ---------------------------------------------------------------------------

// setManualPriceRule 建立**人工覆盖**的价格快照（可选锁定）。
//
// 人工覆盖的语义（计划书）：**优先且锁定**——同步不得改写它。锁定后由人工再改才能变更。
func (h *handler) setManualPriceRule(c *gin.Context) {
	if _, valid := h.ownerID(c); !valid || !requirePlatformReviewer(c) {
		return
	}
	var req struct {
		ScopeType int    `json:"scope_type"`
		ScopeID   int64  `json:"scope_id"`
		Currency  string `json:"currency"`
		// 单位统一用 **USD per 1M tokens**（与底座内置计价口径一致），0 表示该维度不单独计价。
		InputUSDPerMillion  float64 `json:"input_usd_per_million"`
		OutputUSDPerMillion float64 `json:"output_usd_per_million"`
		Lock                bool    `json:"lock"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "invalid_body", "invalid request body")
		return
	}
	if req.ScopeID <= 0 {
		fail(c, http.StatusBadRequest, "invalid_body", "scope_id is required")
		return
	}
	if req.ScopeType != apigxmodel.PriceScopeOffering && req.ScopeType != apigxmodel.PriceScopeChannel &&
		req.ScopeType != apigxmodel.PriceScopeUpstreamKey {
		fail(c, http.StatusBadRequest, "invalid_scope", "unsupported price scope type")
		return
	}
	items, err := price.ItemsFromUSDPerMillion(req.InputUSDPerMillion, req.OutputUSDPerMillion, 0, 0)
	if err != nil {
		fail(c, http.StatusBadRequest, "invalid_price", err.Error())
		return
	}
	if len(items) == 0 {
		fail(c, http.StatusBadRequest, "invalid_price", "at least one dimension price is required")
		return
	}
	currency := strings.ToUpper(strings.TrimSpace(req.Currency))
	if currency == "" {
		currency = "USD"
	}
	rule := &apigxmodel.PriceRule{
		ScopeType: req.ScopeType, ScopeID: req.ScopeID,
		Source: apigxmodel.PriceRuleSourceManual, Currency: currency,
		PricingMode:     apigxmodel.PriceModePerDimension,
		ValidationState: apigxmodel.PriceValidationAccepted,
	}
	if err := apigxmodel.CreatePriceRuleSnapshot(h.db, rule, items); err != nil {
		failFromRepo(c, err)
		return
	}
	if req.Lock {
		if err := apigxmodel.SetPriceRuleLock(h.db, req.ScopeType, req.ScopeID, true); err != nil {
			failFromRepo(c, err)
			return
		}
	}
	ok(c, gin.H{"id": rule.ID, "version": rule.Version, "locked": req.Lock, "source": "manual"})
}

// listPriceRules 读取某作用域的**生效快照**与全部版本（含待确认）。
//
// 刻意同时给出两者：只给"生效价"会让被挡下的可疑版本变成看不见的垃圾数据
// （计划书要求"待确认"可见）。
func (h *handler) listPriceRules(c *gin.Context) {
	if _, valid := h.ownerID(c); !valid {
		return
	}
	scopeType, err := strconv.Atoi(c.DefaultQuery("scope_type", "0"))
	if err != nil {
		fail(c, http.StatusBadRequest, "invalid_query", "invalid scope_type")
		return
	}
	scopeID, err := strconv.ParseInt(c.DefaultQuery("scope_id", "0"), 10, 64)
	if err != nil || scopeID <= 0 {
		fail(c, http.StatusBadRequest, "invalid_query", "scope_id is required")
		return
	}

	effective, items, effErr := apigxmodel.GetEffectivePriceRule(h.db, scopeType, scopeID, 0)
	response := gin.H{"price_known": false}
	if effErr == nil {
		// **价格未知 ≠ 零**：这里逐维度给出"可用价格"，不可用的维度根本不出现
		dimensions := make([]gin.H, 0, len(items))
		for i := range items {
			item := &items[i]
			usdMicro, ok := apigxmodel.EffectiveUSDUnitPriceMicro(effective, item)
			if !ok {
				dimensions = append(dimensions, gin.H{"dimension": item.Dimension, "price_known": false})
				continue
			}
			dimensions = append(dimensions, gin.H{
				"dimension": item.Dimension, "unit_scale": item.UnitScale,
				"usd_unit_price_micro": usdMicro, "price_known": true,
			})
		}
		response["price_known"] = len(dimensions) > 0
		response["effective"] = gin.H{
			"version": effective.Version, "source": effective.Source, "currency": effective.Currency,
			"manual_override": effective.ManualOverride, "locked": effective.Locked,
			"dimensions": dimensions,
		}
	} else if !errors.Is(effErr, apigxmodel.ErrNoPriceRule) {
		failFromRepo(c, effErr)
		return
	} else {
		response["reason"] = "no_accepted_snapshot"
	}

	versions, err := apigxmodel.ListPriceRuleSnapshots(h.db, scopeType, scopeID, 20)
	if err != nil {
		failFromRepo(c, err)
		return
	}
	history := make([]gin.H, 0, len(versions))
	for i := range versions {
		item := &versions[i]
		history = append(history, gin.H{
			"version": item.Version, "source": item.Source,
			"validation_state": item.ValidationState, "validation_note": item.ValidationNote,
			"manual_override": item.ManualOverride, "locked": item.Locked, "created_at": item.CreatedAt,
		})
	}
	response["versions"] = history
	ok(c, response)
}

// syncChannelPrice 按渠道摄入一批来源价格（单渠道同步）。
//
// **作用域隔离**由 `price.SyncChannelPrices` 保证：有供给落到供给作用域、否则落到渠道作用域，
// 任何情况下都**不写全局**——这是"单渠道同步不污染全局或其他分享者"的实现层体现。
func (h *handler) syncChannelPrice(c *gin.Context) {
	owner, valid := h.ownerID(c)
	if !valid || !requirePlatformReviewer(c) {
		return
	}
	channelID, valid := paramID(c, "id")
	if !valid {
		return
	}
	var req struct {
		Source   int `json:"source"`
		Readings []struct {
			UpstreamModelID     string  `json:"upstream_model_id"`
			InputUSDPerMillion  float64 `json:"input_usd_per_million"`
			OutputUSDPerMillion float64 `json:"output_usd_per_million"`
			SourceRef           string  `json:"source_ref"`
			RawRecord           string  `json:"raw_record"`
		} `json:"readings"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "invalid_body", "invalid request body")
		return
	}
	if len(req.Readings) == 0 {
		fail(c, http.StatusBadRequest, "invalid_body", "readings is required")
		return
	}
	readings := make([]price.Reading, 0, len(req.Readings))
	for _, item := range req.Readings {
		readings = append(readings, price.Reading{
			UpstreamModelID:     item.UpstreamModelID,
			InputUSDPerMillion:  item.InputUSDPerMillion,
			OutputUSDPerMillion: item.OutputUSDPerMillion,
			Source:              req.Source,
			SourceRef:           item.SourceRef,
			RawRecord:           item.RawRecord,
		})
	}
	report, err := price.SyncChannelPrices(h.db, owner, channelID, readings)
	if err != nil {
		failFromRepo(c, err)
		return
	}
	ok(c, gin.H{
		"applied": report.Applied, "pending": report.Pending,
		"skipped": report.Skipped, "notes": report.Notes,
	})
}

// previewChannelPriceMatch reads New API's active local pricing for every model
// in one owned channel. It is read-only and never accepts client-supplied prices.
func (h *handler) previewChannelPriceMatch(c *gin.Context) {
	owner, valid := h.ownerID(c)
	if !valid {
		return
	}
	channelID, valid := paramID(c, "id")
	if !valid {
		return
	}
	candidates, err := price.PreviewChannelMatches(h.db, owner, channelID)
	if err != nil {
		failFromRepo(c, err)
		return
	}
	matched := 0
	requiresManual := 0
	for i := range candidates {
		switch candidates[i].Status {
		case price.MatchStatusMatched, price.MatchStatusUnchanged:
			matched++
		case price.MatchStatusExpressionRequiresManual:
			requiresManual++
		}
	}
	ok(c, gin.H{
		"channel_id": channelID, "candidates": candidates,
		"matched": matched, "requires_manual": requiresManual,
	})
}

// confirmChannelPriceMatch confirms opaque candidate tokens from a prior
// preview. The server re-reads local pricing and rejects stale candidates.
func (h *handler) confirmChannelPriceMatch(c *gin.Context) {
	owner, valid := h.ownerID(c)
	if !valid {
		return
	}
	channelID, valid := paramID(c, "id")
	if !valid {
		return
	}
	var req struct {
		Selections []struct {
			OfferingID     int64  `json:"offering_id"`
			CandidateToken string `json:"candidate_token"`
		} `json:"selections"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || len(req.Selections) == 0 {
		fail(c, http.StatusBadRequest, "invalid_body", "selections is required")
		return
	}
	selections := make([]price.MatchSelection, 0, len(req.Selections))
	for _, item := range req.Selections {
		selections = append(selections, price.MatchSelection{
			OfferingID: item.OfferingID, CandidateToken: item.CandidateToken,
		})
	}
	report, err := price.ConfirmChannelMatches(h.db, owner, channelID, selections)
	if err != nil {
		if errors.Is(err, price.ErrPriceCandidateChanged) {
			fail(c, http.StatusConflict, "price_candidate_changed", "price candidate changed; preview again")
			return
		}
		failFromRepo(c, err)
		return
	}
	ok(c, gin.H{
		"channel_id": channelID, "applied": report.Applied,
		"pending": report.Pending, "skipped": report.Skipped, "notes": report.Notes,
	})
}

// applyBreakerToCandidates 按熔断结果把"打开"的候选置为不可用（含具体原因）。
//
// 抽成共用函数的原因：`route-explain` 与 `route-apply` 必须用**同一套口径**，
// 否则会出现"解释里它在候选里、实际请求却不会用它"的最难排查类不一致。
func applyBreakerToCandidates(candidates []routing.Candidate) {
	now := common.GetTimestamp()
	for i := range candidates {
		if routing.EvaluateBreaker(candidates[i].Quality, now, routing.DefaultBreakerConfig()).State == routing.BreakerOpen {
			candidates[i].Allowed = false
			candidates[i].ExcludeReason = routing.ExcludeBreakerOpen
		}
	}
}

// taskRetentionDefaultDays 是未配置时的默认保留天数。
const taskRetentionDefaultDays = 7

// getTaskRetention 返回任务产物的保留策略（天数）。
//
// **为什么是策略而不是逐任务字段**：产物在上游（签名直链），我们并不持有副本；
// "保留期限"表达的是**本平台承诺的可查期限**。做成可配置项可以满足计划书 §4 的展示要求，
// 又不必改动任务表结构（避免迁移与三库兼容风险）。上游直链自身可能更早失效，
// 这属上游行为，界面只按本策略展示。
func (h *handler) getTaskRetention(c *gin.Context) {
	if _, valid := h.ownerID(c); !valid {
		return
	}
	days := taskRetentionDefaultDays
	source := "default"
	if raw := strings.TrimSpace(os.Getenv("APIGX_TASK_RETENTION_DAYS")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 {
			fail(c, http.StatusBadRequest, "invalid_config",
				"APIGX_TASK_RETENTION_DAYS must be a non-negative integer")
			return
		}
		days = parsed
		source = "env"
	}
	ok(c, gin.H{"days": days, "source": source})
}

// listAllChannels 列出当前用户的**全部**分享渠道（不限分组）。
//
// 与 listChannels 的差别只有"是否按分组过滤"；字段完全一致，
// 避免同一个列表在两处各有一套字段定义（前端也只认一套）。
func (h *handler) listAllChannels(c *gin.Context) {
	owner, valid := h.ownerID(c)
	if !valid {
		return
	}
	channels, err := apigxmodel.ListChannelsByOwner(h.db, owner, 200)
	if err != nil {
		failFromRepo(c, err)
		return
	}
	items := make([]gin.H, 0, len(channels))
	for _, ch := range channels {
		items = append(items, gin.H{
			"id": ch.ID, "public_id": ch.PublicID, "name": ch.Name, "protocol": ch.Protocol,
			"share_group_id": ch.ShareGroupID, "key_pool_mode": ch.KeyPoolMode,
			"health_state": ch.HealthState, "base_url": ch.BaseURL,
		})
	}
	ok(c, gin.H{"items": items})
}

// updateShareGroup 更新分组的展示名与简介。
//
// 只改展示字段（更名与改简介不影响授权，见 model.ShareGroup 的注释）；
// 名称长度与模型列宽一致（64），简介为不可信文本 —— 前端按纯文本渲染，不做 HTML 解析。
func (h *handler) updateShareGroup(c *gin.Context) {
	owner, valid := h.ownerID(c)
	if !valid {
		return
	}
	id, valid := paramID(c, "id")
	if !valid {
		return
	}
	var req struct {
		Name        *string `json:"name"`
		Description *string `json:"description"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "invalid_body", "invalid request body")
		return
	}
	if req.Name == nil && req.Description == nil {
		fail(c, http.StatusBadRequest, "invalid_body", "name or description is required")
		return
	}
	existing, err := apigxmodel.GetShareGroup(h.db, owner, id)
	if err != nil {
		failFromRepo(c, err)
		return
	}
	name := existing.Name
	if req.Name != nil {
		name = strings.TrimSpace(*req.Name)
	}
	if name == "" || len([]rune(name)) > 64 {
		fail(c, http.StatusBadRequest, "invalid_name", "name must be 1..64 characters")
		return
	}
	description := existing.Description
	if req.Description != nil {
		description = *req.Description
	}
	if err := apigxmodel.UpdateShareGroup(h.db, owner, id, name, description); err != nil {
		failFromRepo(c, err)
		return
	}
	ok(c, gin.H{"id": id, "name": name, "description": description})
}

// listAliasCandidates 列出别名候选（默认只看待确认的）。
func (h *handler) listAliasCandidates(c *gin.Context) {
	if _, valid := h.ownerID(c); !valid || !requirePlatformReviewer(c) {
		return
	}
	state := 0
	if raw := c.Query("state"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 || parsed > 2 {
			fail(c, http.StatusBadRequest, "invalid_query", "state must be 0..2")
			return
		}
		state = parsed
	}
	page, pageSize := 1, 50
	if raw := c.Query("page"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			page = parsed
		}
	}
	if raw := c.Query("page_size"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 && parsed <= 200 {
			pageSize = parsed
		}
	}
	rows, total, err := apigxmodel.ListAliasCandidates(h.db, state, pageSize, (page-1)*pageSize)
	if err != nil {
		failFromRepo(c, err)
		return
	}
	items := make([]gin.H, 0, len(rows))
	for _, row := range rows {
		items = append(items, gin.H{
			"id": row.ID, "external_model_id": row.ExternalModelID, "normalized_model_id": row.NormalizedModelID,
			"scope_type": row.ScopeType, "scope_id": row.ScopeID, "source": row.Source, "source_ref": row.SourceRef,
			"confirmation_state": row.ConfirmationState, "target_canonical_model_id": row.TargetCanonicalID,
		})
	}
	ok(c, gin.H{"items": items, "total": total, "page": page, "page_size": pageSize})
}

// resolveConfirmTarget 从请求里取"确认到哪个标准模型"：既支持给现成的 ID，也支持给对外标准 ID
// （后者不存在时**顺手创建** —— 否则管理员要先绕去别处建模型，这一步就会被跳过，候选永远堆着）。
func (h *handler) resolveConfirmTarget(c *gin.Context, apiModelID string, canonicalID int64, description string, modalityType int) (int64, bool) {
	if canonicalID > 0 {
		return canonicalID, true
	}
	if strings.TrimSpace(apiModelID) == "" {
		fail(c, http.StatusBadRequest, "invalid_body", "api_model_id or target_canonical_model_id is required")
		return 0, false
	}
	model, err := apigxmodel.EnsureCanonicalModel(h.db, apiModelID, description, modalityType)
	if err != nil {
		failFromRepo(c, err)
		return 0, false
	}
	return model.ID, true
}

// confirmAliasCandidate 确认一个候选（平台侧）。
func (h *handler) confirmAliasCandidate(c *gin.Context) {
	owner, valid := h.ownerID(c)
	if !valid || !requirePlatformReviewer(c) {
		return
	}
	id, valid := paramID(c, "id")
	if !valid {
		return
	}
	var req struct {
		APIModelID             string `json:"api_model_id"`
		TargetCanonicalModelID int64  `json:"target_canonical_model_id"`
		Description            string `json:"description"`
		ModalityType           int    `json:"modality_type"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "invalid_body", "invalid request body")
		return
	}
	target, okTarget := h.resolveConfirmTarget(c, req.APIModelID, req.TargetCanonicalModelID, req.Description, req.ModalityType)
	if !okTarget {
		return
	}
	if err := apigxmodel.ConfirmAliasCandidate(h.db, id, target, owner); err != nil {
		failFromRepo(c, err)
		return
	}
	ok(c, gin.H{"id": id, "confirmation_state": apigxmodel.AliasConfirmApproved, "target_canonical_model_id": target})
}

// rejectAliasCandidate 拒绝一个候选（平台侧）。
func (h *handler) rejectAliasCandidate(c *gin.Context) {
	owner, valid := h.ownerID(c)
	if !valid || !requirePlatformReviewer(c) {
		return
	}
	id, valid := paramID(c, "id")
	if !valid {
		return
	}
	if err := apigxmodel.RejectAliasCandidate(h.db, id, owner); err != nil {
		failFromRepo(c, err)
		return
	}
	ok(c, gin.H{"id": id, "confirmation_state": apigxmodel.AliasConfirmRejected})
}

// bulkConfirmAliases 批量确认：把所有**归一化后同名**的待确认候选一次性指到同一个标准模型（§4）。
func (h *handler) bulkConfirmAliases(c *gin.Context) {
	owner, valid := h.ownerID(c)
	if !valid || !requirePlatformReviewer(c) {
		return
	}
	var req struct {
		NormalizedModelID      string `json:"normalized_model_id"`
		APIModelID             string `json:"api_model_id"`
		TargetCanonicalModelID int64  `json:"target_canonical_model_id"`
		Description            string `json:"description"`
		ModalityType           int    `json:"modality_type"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "invalid_body", "invalid request body")
		return
	}
	if strings.TrimSpace(req.NormalizedModelID) == "" {
		fail(c, http.StatusBadRequest, "invalid_body", "normalized_model_id is required")
		return
	}
	target, okTarget := h.resolveConfirmTarget(c, req.APIModelID, req.TargetCanonicalModelID, req.Description, req.ModalityType)
	if !okTarget {
		return
	}
	affected, err := apigxmodel.BulkConfirmAliases(h.db, req.NormalizedModelID, target, owner)
	if err != nil {
		failFromRepo(c, err)
		return
	}
	ok(c, gin.H{"confirmed": affected, "target_canonical_model_id": target})
}

// provisionBaseChannelForShareChannel 为"分享者自建渠道"代建一条**底座渠道**并绑定。
//
// 设计取舍（§4「渠道 CRUD」+ §7 权限边界）：
//   - 分享者只填 名称/协议/上游地址/自己的 Key ⇒ **不需要知道管理员侧的渠道 ID**；
//   - 上游 Key 用**分享者自己的**，平台不代垫 ⇒ 消耗归分享者；
//   - 底座渠道的模型列表留空，由「同步模型」按上游实际能力填充（不做猜测）。
func provisionBaseChannelForShareChannel(db *gorm.DB, channel *apigxmodel.Channel, upstreamKey string) error {
	if db == nil {
		return errors.New("apigx api: nil db")
	}
	if err := validateSelfServeChannelRecipe(channel, upstreamKey); err != nil {
		return err
	}
	channelType, _ := baseChannelTypeForProtocol(channel.Protocol)

	base := basemodel.Channel{
		Name:        "APIGX-" + channel.Name,
		Type:        channelType,
		Key:         upstreamKey,
		BaseURL:     &channel.BaseURL,
		Group:       "default",
		Models:      "",
		Status:      common.ChannelStatusEnabled,
		CreatedTime: common.GetTimestamp(),
	}
	if err := db.Create(&base).Error; err != nil {
		return err
	}
	channel.BaseChannelID = int64(base.Id)

	return nil
}

func validateSelfServeChannelRecipe(channel *apigxmodel.Channel, upstreamKey string) error {
	if channel == nil {
		return fmt.Errorf("%w: channel is required", apigxmodel.ErrInvalidArgument)
	}
	if channel.Name == "" {
		return fmt.Errorf("%w: name is required", apigxmodel.ErrInvalidArgument)
	}
	if channel.BaseURL == "" {
		return fmt.Errorf("%w: base_url is required", apigxmodel.ErrInvalidArgument)
	}
	if upstreamKey == "" {
		return fmt.Errorf("%w: upstream_key is required", apigxmodel.ErrInvalidArgument)
	}
	_, ok := baseChannelTypeForProtocol(channel.Protocol)
	if !ok {
		return fmt.Errorf("%w: unsupported protocol for self-serve channel", apigxmodel.ErrInvalidArgument)
	}
	return nil
}

// prepareSelfServeUpstreamKey 把"自建渠道"随附的上游 Key 变成待落库记录。
//
// 加密和指纹在任何数据库写入之前完成；ChannelID 在事务内创建 APIGX 渠道后补上。
func prepareSelfServeUpstreamKey(upstreamKey string) (*apigxmodel.UpstreamKey, error) {
	envelope, encErr := secretbox.Encrypt(upstreamKey)
	if encErr != nil {
		return nil, encErr
	}
	fingerprint, fpErr := secretbox.Fingerprint(upstreamKey)
	if fpErr != nil {
		return nil, fpErr
	}
	return &apigxmodel.UpstreamKey{
		StableKeyID:       upstreamKeyStableID(upstreamKey),
		Name:              "default",
		SecretRef:         envelope,
		SecretFingerprint: fingerprint,
		Weight:            1,
		Status:            apigxmodel.UpstreamKeyStatusEnabled,
	}, nil
}

// baseChannelTypeForProtocol 把我们的协议标识映射到底座渠道类型（用底座常量，不写死数字）。
func baseChannelTypeForProtocol(protocol string) (int, bool) {
	switch protocol {
	case "openai":
		return constant.ChannelTypeOpenAI, true
	case "anthropic":
		return constant.ChannelTypeAnthropic, true
	case "gemini":
		return constant.ChannelTypeGemini, true
	default:
		return 0, false
	}
}

// upstreamKeyStableID 生成稳定的 Key 标识（同一把 Key 重复登记时保持幂等）。
func upstreamKeyStableID(key string) string {
	sum := sha256.Sum256([]byte(key))
	return "apigx-" + hex.EncodeToString(sum[:8])
}

const (
	ownerUsageDefaultWindowSeconds = int64(30 * 24 * 60 * 60)
	ownerUsageMaximumWindowSeconds = int64(90 * 24 * 60 * 60)
)

// summarizeOwnerUsage 返回**当前分享者自己**的消耗汇总（U-3）。
//
// Attempt 只提供请求、重试与延迟；实际数量和成本来自不可变 UsageEvent 快照。
// 默认查询最近 30 天，单次最多 90 天且结果分页，避免统计页触发无界聚合。
func (h *handler) summarizeOwnerUsage(c *gin.Context) {
	owner, valid := h.ownerID(c)
	if !valid {
		return
	}

	groupBy := apigxmodel.OwnerUsageGroupBy(c.DefaultQuery("group_by", "channel"))
	end, queryOK := parseOwnerUsageInt64(c, "end", common.GetTimestamp())
	if !queryOK {
		return
	}
	start, queryOK := parseOwnerUsageInt64(c, "start", end-ownerUsageDefaultWindowSeconds)
	if !queryOK {
		return
	}
	page, queryOK := parseOwnerUsageInt(c, "page", 1)
	if !queryOK {
		return
	}
	pageSize, queryOK := parseOwnerUsageInt(c, "page_size", 20)
	if !queryOK {
		return
	}
	if start <= 0 || end <= 0 || start > end || end-start > ownerUsageMaximumWindowSeconds {
		fail(c, http.StatusBadRequest, "invalid_argument", "usage time range must be positive, ordered, and no longer than 90 days")
		return
	}
	if page <= 0 || pageSize <= 0 || pageSize > 100 {
		fail(c, http.StatusBadRequest, "invalid_argument", "page must be positive and page_size must be between 1 and 100")
		return
	}

	items, hasMore, err := apigxmodel.SummarizeOwnerUsage(h.db, owner, groupBy, start, end, page, pageSize)
	if err != nil {
		failFromRepo(c, err)
		return
	}
	ok(c, gin.H{
		"group_by":  string(groupBy),
		"start":     start,
		"end":       end,
		"page":      page,
		"page_size": pageSize,
		"has_more":  hasMore,
		"items":     items,
	})
}

func parseOwnerUsageInt64(c *gin.Context, name string, fallback int64) (int64, bool) {
	raw := c.Query(name)
	if raw == "" {
		return fallback, true
	}
	parsed, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		fail(c, http.StatusBadRequest, "invalid_argument", "invalid "+name)
		return 0, false
	}
	return parsed, true
}

func parseOwnerUsageInt(c *gin.Context, name string, fallback int) (int, bool) {
	raw := c.Query(name)
	if raw == "" {
		return fallback, true
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil {
		fail(c, http.StatusBadRequest, "invalid_argument", "invalid "+name)
		return 0, false
	}
	return parsed, true
}

// syncBaseChannelServesModel 让"绑定到该供给所属分享渠道"的底座渠道真正能服务该模型。
//
// 背景（实测发现的产品缺陷）：U-1 的「分享者自建渠道」由平台**直接插入**底座 channels 行，
// 因此**绕过了底座的建能力步骤**（底座在建/改渠道后会调用 Channel.AddAbilities）。
// 结果：这类渠道 models 为空、abilities 为 0，底座分配器对该模型永远返回 model_not_found。
//
// 修法（与底座自己的姿势一致）：把该模型并入底座渠道的 models，然后重建能力。
// 只动 models 与能力，不触碰上游地址与凭据。
func syncBaseChannelServesModel(db *gorm.DB, offeringID int64) error {
	if db == nil {
		return errors.New("apigx api: nil db")
	}
	var offering apigxmodel.ModelOffering
	if err := db.Where("id = ?", offeringID).First(&offering).Error; err != nil {
		return err
	}
	var shareChannel apigxmodel.Channel
	if err := db.Where("id = ?", offering.ChannelID).First(&shareChannel).Error; err != nil {
		return err
	}
	if shareChannel.BaseChannelID <= 0 || offering.UpstreamModelID == "" {
		return nil
	}

	var base basemodel.Channel
	if err := db.Where("id = ?", shareChannel.BaseChannelID).First(&base).Error; err != nil {
		return err
	}

	models := strings.Split(base.Models, ",")
	exists := false
	for _, m := range models {
		if strings.TrimSpace(m) == offering.UpstreamModelID {
			exists = true
			break
		}
	}
	if !exists {
		merged := append(models, offering.UpstreamModelID)
		joined := strings.Join(merged, ",")
		if err := db.Model(&basemodel.Channel{}).Where("id = ?", base.Id).
			Update("models", joined).Error; err != nil {
			return err
		}
		base.Models = joined
	}

	// 重建能力（底座建渠道后的同一姿势）
	if err := base.AddAbilities(db); err != nil {
		return err
	}
	return nil
}
