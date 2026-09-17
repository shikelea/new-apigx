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
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/apigx/capacity"
	"github.com/QuantumNous/new-api/apigx/channelpool"
	apigxmodel "github.com/QuantumNous/new-api/apigx/model"
	"github.com/QuantumNous/new-api/apigx/relayhook"
	"github.com/QuantumNous/new-api/apigx/secretbox"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/controller"
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
		groups.GET("/:id", h.getShareGroup)
		groups.DELETE("/:id", h.deleteShareGroup)
		groups.GET("/:id/channels", h.listChannels)
	}

	channels := r.Group("/channels")
	{
		channels.POST("", h.createChannel)
		channels.GET("/:id", h.getChannel)
		channels.DELETE("/:id", h.deleteChannel)
		channels.POST("/:id/keys", h.createUpstreamKey)
		channels.GET("/:id/keys", h.listUpstreamKeys)
		channels.POST("/:id/apply", h.applyChannelPool)
		channels.PUT("/:id/capacity-policy", h.setChannelCapacityPolicy)
		channels.POST("/:id/offerings", h.createOffering)
		channels.GET("/:id/offerings", h.listOfferings)
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

	plaza := r.Group("/plaza")
	{
		plaza.GET("", h.listPlaza)
		plaza.GET("/:publicId", h.getPlazaItem)
	}

	usage := r.Group("/usage")
	{
		usage.GET("", h.listUsage)
	}

	platformKeys := r.Group("/platform-keys")
	{
		platformKeys.GET("/:tokenId/preferences", h.getPlatformKeyPreferences)
		platformKeys.PUT("/:tokenId/preferences", h.setPlatformKeyPreferences)
	}

	keys := r.Group("/keys")
	{
		keys.PATCH("/:id/status", h.updateKeyStatus)
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
		PublishStatus:     0, // 待审核（计划书 §12：新公开资源先审核）
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
	channel := &apigxmodel.Channel{
		OwnerUserID:   owner,
		ShareGroupID:  req.ShareGroupID,
		Name:          strings.TrimSpace(req.Name),
		Protocol:      req.Protocol,
		BaseURL:       req.BaseURL,
		BaseChannelID: req.BaseChannelID,
		KeyPoolMode:   0, // 默认轮询（计划书 §3.5）
		HealthState:   0,
	}
	if req.KeyPoolMode != nil {
		channel.KeyPoolMode = *req.KeyPoolMode
	}
	if err := apigxmodel.CreateChannel(h.db, channel); err != nil {
		failFromRepo(c, err)
		return
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
			"key_pool_mode": ch.KeyPoolMode, "health_state": ch.HealthState, "base_url": ch.BaseURL,
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
		"base_url": ch.BaseURL, "key_pool_mode": ch.KeyPoolMode,
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
	if err := apigxmodel.DeleteChannel(h.db, owner, id); err != nil {
		failFromRepo(c, err)
		return
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
				"max_concurrency_mode": policy.Mode,
				"max_concurrency":      policy.Value,
				"policy_version":       existing.PolicyVersion + 1,
				"updated_at":           common.GetTimestamp(),
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
		ok(c, gin.H{"id": created.ID, "concurrency_mode": policy.Mode, "concurrency_value": policy.Value})
	default:
		failFromRepo(c, err)
	}
}

// ---------------------------------------------------------------------------
// 平台 Key 的 APIGX 偏好（T1.10）
// ---------------------------------------------------------------------------

type platformKeyPreferencesRequest struct {
	ScopeMode           *int     `json:"scope_mode"`
	PinnedOfferingID    *int64   `json:"pinned_offering_id"`
	AllowedModelIDs     []string `json:"allowed_model_ids"`
	AllowedOwnerUserIDs []int64  `json:"allowed_owner_user_ids"`
	AllowFallback       *bool    `json:"allow_fallback"`
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
		ok(c, gin.H{"token_id": tokenID, "scope_mode": apigxmodel.ScopeModeDefault, "configured": false})
		return
	}
	if err != nil {
		failFromRepo(c, err)
		return
	}
	ok(c, gin.H{
		"token_id":               key.TokenID,
		"configured":             true,
		"scope_mode":             key.ScopeMode,
		"pinned_offering_id":     key.PinnedOfferingID,
		"allowed_model_ids":      key.AllowedModels(),
		"allowed_owner_user_ids": key.AllowedOwnerIDs(),
		"allow_fallback":         key.AllowFallback,
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

	key, err := apigxmodel.UpsertAPIKeyPreferences(h.db, owner, tokenID, func(k *apigxmodel.APIKey) error {
		if req.ScopeMode != nil {
			k.ScopeMode = *req.ScopeMode
		}
		if req.PinnedOfferingID != nil {
			k.PinnedOfferingID = *req.PinnedOfferingID
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
	ok(c, gin.H{
		"token_id":               key.TokenID,
		"scope_mode":             key.ScopeMode,
		"pinned_offering_id":     key.PinnedOfferingID,
		"allowed_model_ids":      key.AllowedModels(),
		"allowed_owner_user_ids": key.AllowedOwnerIDs(),
		"allow_fallback":         key.AllowFallback,
	})
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

func (h *handler) publishOffering(c *gin.Context) {
	owner, valid := h.ownerID(c)
	if !valid || !requirePlatformReviewer(c) {
		return
	}
	id, valid := paramID(c, "id")
	if !valid {
		return
	}
	if err := apigxmodel.PublishModelOffering(h.db, owner, id); err != nil {
		failFromRepo(c, err)
		return
	}
	ok(c, gin.H{"id": id, "status": apigxmodel.OfferingStatusPublished})
}

func (h *handler) rejectOffering(c *gin.Context) {
	owner, valid := h.ownerID(c)
	if !valid || !requirePlatformReviewer(c) {
		return
	}
	id, valid := paramID(c, "id")
	if !valid {
		return
	}
	if err := apigxmodel.RejectModelOffering(h.db, owner, id); err != nil {
		failFromRepo(c, err)
		return
	}
	ok(c, gin.H{"id": id, "status": apigxmodel.OfferingStatusRejected})
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
	if _, valid := h.ownerID(c); !valid {
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
	items, total, err := apigxmodel.ListPlazaItems(h.db, pageSize, (page-1)*pageSize)
	if err != nil {
		failFromRepo(c, err)
		return
	}
	out := make([]gin.H, 0, len(items))
	for _, item := range items {
		out = append(out, plazaItemToJSON(item))
	}
	ok(c, gin.H{"items": out, "total": total, "page": page, "page_size": pageSize})
}

func (h *handler) getPlazaItem(c *gin.Context) {
	if _, valid := h.ownerID(c); !valid {
		return
	}
	publicID := c.Param("publicId")
	item, err := apigxmodel.GetPlazaItem(h.db, publicID)
	if err != nil {
		failFromRepo(c, err)
		return
	}
	ok(c, plazaItemToJSON(*item))
}

// plazaItemToJSON 把广场条目转成对外 JSON。
//
// 只输出对外可见字段：**不含**渠道内部 ID、上游地址、凭据、成本价或任何内部标识。
func plazaItemToJSON(item apigxmodel.PlazaItem) gin.H {
	out := gin.H{
		"offering_public_id": item.OfferingPublicID,
		"model":              item.UpstreamModelID,
		"channel_public_id":  item.ChannelPublicID,
		"owner_user_id":      item.OwnerUserID,
		"capabilities":       item.Capabilities,
		"updated_at":         item.UpdatedAt,
	}
	if len(item.UnverifiedCapabilities) > 0 {
		out["unverified_capabilities"] = item.UnverifiedCapabilities
	}
	return out
}
