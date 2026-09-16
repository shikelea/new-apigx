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

	apigxmodel "github.com/QuantumNous/new-api/apigx/model"
	"github.com/QuantumNous/new-api/apigx/secretbox"
	"github.com/QuantumNous/new-api/common"
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
	envelope, err := secretbox.Encrypt(strings.TrimSpace(req.Key))
	if err != nil {
		// 主密钥未配置时失败关闭——绝不退化成明文存储
		common.SysError("apigx api: refuse to store upstream key: " + err.Error())
		fail(c, http.StatusServiceUnavailable, "secret_store_unavailable",
			"credential store is not configured; refusing to store the key")
		return
	}

	key := &apigxmodel.UpstreamKey{
		ChannelID:   channelID,
		StableKeyID: req.StableKeyID,
		Name:        strings.TrimSpace(req.Name),
		SecretRef:   envelope,
		Status:      apigxmodel.UpstreamKeyStatusEnabled,
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
