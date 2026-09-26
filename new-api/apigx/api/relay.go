package apigxapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	apigxmodel "github.com/QuantumNous/new-api/apigx/model"
	"github.com/QuantumNous/new-api/apigx/relayprobe"
	"github.com/QuantumNous/new-api/apigx/secretbox"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	basemodel "github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// RegisterRelayPublic registers public relay discovery plus authenticated owner actions.
// The caller supplies TryUserAuth so public reads work for anonymous visitors while
// owner routes still enforce authentication in the handler.
func RegisterRelayPublic(r *gin.RouterGroup, db *gorm.DB) {
	h := &handler{db: db}
	r.GET("", h.listPublicRelayShares)
	// Static paths must be registered before /:publicID, otherwise Gin treats
	// /mine as a public share identifier.
	r.GET("/mine", middleware.UserAuth(), h.listMyRelayShares)
	r.POST("", middleware.UserAuth(), h.createRelayShare)
	r.GET("/:publicID", h.getPublicRelayShare)
	r.POST("/:publicID/check", middleware.UserAuth(), h.checkRelayShare)
	r.POST("/:publicID/vote", middleware.UserAuth(), h.voteRelayShare)
	r.DELETE("/:publicID", middleware.UserAuth(), h.deleteRelayShare)
}

func relayPublicID(c *gin.Context) string { return strings.TrimSpace(c.Param("publicID")) }

func (h *handler) getOwnedRelayByPublicID(owner int64, publicID string) (*apigxmodel.RelayShare, error) {
	var share apigxmodel.RelayShare
	err := h.db.Where("public_id = ? AND owner_user_id = ? AND alive_flag = ?", strings.TrimSpace(publicID), owner, apigxmodel.AliveFlagAlive).First(&share).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, apigxmodel.ErrNotFound
	}
	return &share, err
}

func relayOwnerName(db *gorm.DB, ownerID int64) string {
	var user basemodel.User
	if err := db.Select("username", "display_name").First(&user, "id = ?", ownerID).Error; err != nil {
		return "平台用户"
	}
	if strings.TrimSpace(user.DisplayName) != "" {
		return user.DisplayName
	}
	if strings.TrimSpace(user.Username) != "" {
		return user.Username
	}
	return "平台用户"
}

func relayShareJSON(db *gorm.DB, share apigxmodel.RelayShare) gin.H {
	return gin.H{
		"public_id": share.PublicID, "title": share.Title, "base_url": share.BaseURL,
		"kind": share.Kind, "protocol": share.Protocol, "content": share.Content,
		"status": share.Status, "check_status": share.CheckStatus, "check_reason": share.CheckReason,
		"last_checked_at": share.LastCheckedAt, "latency_ms": share.LatencyMs,
		"first_token_ms": share.FirstTokenMs, "model_count": share.ModelCount,
		"vote_usable": share.VoteUsable, "vote_unavailable": share.VoteUnavailable,
		"owner_name": relayOwnerName(db, share.OwnerUserID),
		"created_at": share.CreatedAt, "updated_at": share.UpdatedAt,
	}
}

func (h *handler) listPublicRelayShares(c *gin.Context) {
	var viewer int64
	if id := c.GetInt("id"); id > 0 {
		viewer = int64(id)
	}
	shares, err := apigxmodel.ListRelayShares(h.db, viewer, false, 100)
	if err != nil {
		failFromRepo(c, err)
		return
	}
	items := make([]gin.H, 0, len(shares))
	for _, share := range shares {
		items = append(items, relayShareJSON(h.db, share))
	}
	ok(c, gin.H{"items": items})
}

func (h *handler) getPublicRelayShare(c *gin.Context) {
	publicID := relayPublicID(c)
	if publicID == "" {
		fail(c, http.StatusBadRequest, "invalid_id", "invalid public id")
		return
	}
	var share apigxmodel.RelayShare
	err := h.db.Where("public_id = ? AND status = ? AND alive_flag = ?", publicID, apigxmodel.RelayStatusPublished, apigxmodel.AliveFlagAlive).First(&share).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		fail(c, http.StatusNotFound, "not_found", "relay share not found")
		return
	}
	if err != nil {
		failFromRepo(c, err)
		return
	}
	ok(c, relayShareJSON(h.db, share))
}

func (h *handler) listMyRelayShares(c *gin.Context) {
	owner, valid := h.ownerID(c)
	if !valid {
		return
	}
	shares, err := apigxmodel.ListRelayShares(h.db, owner, true, 100)
	if err != nil {
		failFromRepo(c, err)
		return
	}
	items := make([]gin.H, 0, len(shares))
	for _, share := range shares {
		item := relayShareJSON(h.db, share)
		if credential, credErr := apigxmodel.GetRelayCredential(h.db, owner, share.ID); credErr == nil {
			item["credential_configured"] = true
			item["key_suffix"] = credential.KeySuffix
			item["credential_last_checked_at"] = credential.LastCheckedAt
		} else {
			item["credential_configured"] = false
		}
		items = append(items, item)
	}
	ok(c, gin.H{"items": items})
}

type createRelayShareRequest struct {
	Title    string `json:"title"`
	BaseURL  string `json:"base_url"`
	Kind     string `json:"kind"`
	Protocol string `json:"protocol"`
	Content  string `json:"content"`
	Key      string `json:"key"`
}

func (h *handler) createRelayShare(c *gin.Context) {
	owner, valid := h.ownerID(c)
	if !valid {
		return
	}
	var req createRelayShareRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "invalid_body", "invalid request body")
		return
	}
	if strings.TrimSpace(req.Key) == "" {
		fail(c, http.StatusBadRequest, "key_required", "持续检测需要提供一个检测 Key")
		return
	}
	base, err := relayprobe.ValidateBaseURL(req.BaseURL)
	if err != nil {
		fail(c, http.StatusBadRequest, "invalid_base_url", "站点地址未通过安全校验")
		return
	}
	if req.Protocol == "" {
		req.Protocol = "new-api"
	}
	if req.Protocol != "new-api" && req.Protocol != "sub2api" {
		fail(c, http.StatusBadRequest, "unsupported_protocol", "当前仅支持 New API 或 Sub2API")
		return
	}
	encrypted, err := secretbox.Encrypt(strings.TrimSpace(req.Key))
	if err != nil {
		fail(c, http.StatusServiceUnavailable, "credential_unavailable", "平台未配置安全凭据存储")
		return
	}
	fingerprint, err := secretbox.Fingerprint(strings.TrimSpace(req.Key))
	if err != nil {
		fail(c, http.StatusServiceUnavailable, "credential_unavailable", "平台未配置安全凭据存储")
		return
	}
	key := strings.TrimSpace(req.Key)
	suffix := key
	if len(suffix) > 4 {
		suffix = suffix[len(suffix)-4:]
	}
	share := &apigxmodel.RelayShare{OwnerUserID: owner, Title: strings.TrimSpace(req.Title), BaseURL: base, Kind: strings.TrimSpace(req.Kind), Protocol: req.Protocol, Content: req.Content, Status: apigxmodel.RelayStatusPublished}
	credential := &apigxmodel.RelayCredential{OwnerUserID: owner, SecretRef: encrypted, Fingerprint: fingerprint, KeySuffix: suffix, NextCheckAt: common.GetTimestamp()}
	err = h.db.Transaction(func(tx *gorm.DB) error {
		if createErr := apigxmodel.CreateRelayShare(tx, share); createErr != nil {
			return createErr
		}
		credential.RelayShareID = share.ID
		return apigxmodel.CreateRelayCredential(tx, credential)
	})
	if err != nil {
		failFromRepo(c, err)
		return
	}
	ok(c, gin.H{"relay": relayShareJSON(h.db, *share), "credential_configured": true, "key_suffix": suffix})
}

func (h *handler) checkRelayShare(c *gin.Context) {
	owner, valid := h.ownerID(c)
	if !valid {
		return
	}
	share, err := h.getOwnedRelayByPublicID(owner, c.Param("publicID"))
	if err != nil {
		failFromRepo(c, err)
		return
	}
	credential, err := apigxmodel.GetRelayCredential(h.db, owner, share.ID)
	if err != nil {
		failFromRepo(c, err)
		return
	}
	result, probeErr := relayprobe.ProbeCredential(c.Request.Context(), share, credential)
	now := common.GetTimestamp()
	if probeErr != nil {
		result = relayprobe.Result{Status: apigxmodel.RelayCheckFailed, Reason: "检测凭据不可用"}
	}
	if err := h.db.Model(&apigxmodel.RelayShare{}).Where("id = ?", share.ID).Updates(map[string]any{"check_status": result.Status, "check_reason": result.Reason, "last_checked_at": now, "latency_ms": result.LatencyMs, "model_count": result.ModelCount, "updated_at": now}).Error; err != nil {
		failFromRepo(c, err)
		return
	}
	_ = h.db.Model(&apigxmodel.RelayCredential{}).Where("id = ?", credential.ID).Updates(map[string]any{"last_checked_at": now, "next_check_at": now + 1800, "updated_at": now})
	if probeErr != nil {
		fail(c, http.StatusBadGateway, "probe_failed", result.Reason)
		return
	}
	ok(c, gin.H{"status": result.Status, "reason": result.Reason, "latency_ms": result.LatencyMs, "model_count": result.ModelCount, "models": result.Models})
}

func (h *handler) voteRelayShare(c *gin.Context) {
	if _, valid := h.ownerID(c); !valid {
		return
	}
	var share apigxmodel.RelayShare
	if err := h.db.Where("public_id = ? AND status = ? AND alive_flag = ?", c.Param("publicID"), apigxmodel.RelayStatusPublished, apigxmodel.AliveFlagAlive).First(&share).Error; err != nil {
		fail(c, http.StatusNotFound, "not_found", "relay share not found")
		return
	}
	var req struct {
		Usable bool `json:"usable"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "invalid_body", "invalid request body")
		return
	}
	if err := apigxmodel.AddRelayVote(h.db, share.ID, req.Usable); err != nil {
		failFromRepo(c, err)
		return
	}
	ok(c, gin.H{"voted": true})
}

func (h *handler) deleteRelayShare(c *gin.Context) {
	owner, valid := h.ownerID(c)
	if !valid {
		return
	}
	share, err := h.getOwnedRelayByPublicID(owner, c.Param("publicID"))
	if err != nil {
		failFromRepo(c, err)
		return
	}
	id := share.ID
	result := h.db.Model(&apigxmodel.RelayShare{}).Where("id = ? AND owner_user_id = ? AND alive_flag = ?", id, owner, apigxmodel.AliveFlagAlive).Updates(map[string]any{"deleted_at": time.Now().UTC(), "alive_flag": apigxmodel.AliveFlagAlive + id, "status": apigxmodel.RelayStatusSuspended, "updated_at": common.GetTimestamp()})
	if result.Error != nil {
		failFromRepo(c, result.Error)
		return
	}
	if result.RowsAffected == 0 {
		fail(c, http.StatusNotFound, "not_found", "relay share not found")
		return
	}
	_ = h.db.Model(&apigxmodel.RelayCredential{}).Where("relay_share_id = ? AND owner_user_id = ? AND deleted_at IS NULL", id, owner).Updates(map[string]any{"enabled": false, "deleted_at": time.Now().UTC(), "updated_at": common.GetTimestamp()})
	ok(c, gin.H{"deleted": id})
}

type auditRunRequest struct {
	RelayShareID       int64  `json:"relay_share_id"`
	RelaySharePublicID string `json:"relay_share_public_id"`
	Model              string `json:"model"`
}

func (h *handler) createAuditRun(c *gin.Context) {
	owner, valid := h.ownerID(c)
	if !valid {
		return
	}
	var req auditRunRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "invalid_body", "invalid request body")
		return
	}
	if strings.TrimSpace(req.Model) == "" || (req.RelayShareID <= 0 && strings.TrimSpace(req.RelaySharePublicID) == "") {
		fail(c, http.StatusBadRequest, "invalid_argument", "relay share and model are required")
		return
	}
	var share *apigxmodel.RelayShare
	var err error
	if strings.TrimSpace(req.RelaySharePublicID) != "" {
		share, err = h.getOwnedRelayByPublicID(owner, req.RelaySharePublicID)
	} else {
		share, err = apigxmodel.GetRelayShareByOwner(h.db, owner, req.RelayShareID)
	}
	if err != nil {
		failFromRepo(c, err)
		return
	}
	credential, err := apigxmodel.GetRelayCredential(h.db, owner, share.ID)
	if err != nil {
		failFromRepo(c, err)
		return
	}
	run := &apigxmodel.AuditRun{OwnerUserID: owner, RelayShareID: share.ID, Model: strings.TrimSpace(req.Model), Status: apigxmodel.AuditStatusRunning, StartedAt: common.GetTimestamp()}
	if err := apigxmodel.CreateAuditRun(h.db, run); err != nil {
		failFromRepo(c, err)
		return
	}
	result, probeErr := relayprobe.ProbeCredential(c.Request.Context(), share, credential)
	status := apigxmodel.AuditCheckPassed
	reason := "模型列表接口可达，未发现协议级异常"
	if probeErr != nil || result.Status == apigxmodel.RelayCheckFailed {
		status = apigxmodel.AuditCheckFailed
		reason = result.Reason
		if probeErr != nil {
			reason = "检测凭据不可用"
		}
	}
	checks := []struct {
		id, name, category, status, reason string
		samples                            int
	}{
		{"protocol", "协议与模型列表", "基础设施", status, reason, 1},
		{"token_usage", "Token 用量对比", "计量", apigxmodel.AuditCheckSkipped, "需要指定模型请求样本", 0},
		{"prompt_leakage", "Prompt 提取探测", "安全", apigxmodel.AuditCheckSkipped, "首阶段不自动发送攻击性探针", 0},
		{"identity", "模型身份一致性", "身份", apigxmodel.AuditCheckSkipped, "需要模型请求样本", 0},
		{"sse_integrity", "流式完整性", "传输", apigxmodel.AuditCheckSkipped, "需要启用流式审计", 0},
		{"error_leakage", "错误响应泄漏", "安全", apigxmodel.AuditCheckSkipped, "需要受控错误样本", 0},
	}
	for _, check := range checks {
		_ = apigxmodel.CreateAuditCheck(h.db, &apigxmodel.AuditCheck{AuditRunID: run.ID, CheckID: check.id, Name: check.name, Category: check.category, Status: check.status, SampleCount: check.samples, LatencyMs: result.LatencyMs, EvidenceSummary: check.reason, Confidence: "low"})
	}
	run.Status = apigxmodel.AuditStatusCompleted
	run.RiskLevel = "需复核"
	run.SampleCount = result.ModelCount
	run.FinishedAt = common.GetTimestamp()
	run.EvidenceSummary = "仅保存脱敏摘要；未保存原始 Key、Prompt 或完整响应。"
	_ = h.db.Model(run).Updates(map[string]any{"status": run.Status, "risk_level": run.RiskLevel, "sample_count": run.SampleCount, "finished_at": run.FinishedAt, "evidence_summary": run.EvidenceSummary, "updated_at": run.FinishedAt})
	ok(c, gin.H{"id": run.ID, "public_id": run.PublicID, "status": run.Status, "risk_level": run.RiskLevel, "sample_count": run.SampleCount, "checks": checks})
}

func (h *handler) listAuditRuns(c *gin.Context) {
	owner, valid := h.ownerID(c)
	if !valid {
		return
	}
	var runs []apigxmodel.AuditRun
	if err := h.db.Where("owner_user_id = ? AND deleted_at IS NULL", owner).Order("id DESC").Limit(50).Find(&runs).Error; err != nil {
		failFromRepo(c, err)
		return
	}
	items := make([]gin.H, 0, len(runs))
	for _, run := range runs {
		items = append(items, gin.H{"public_id": run.PublicID, "relay_share_id": run.RelayShareID, "model": run.Model, "status": run.Status, "risk_level": run.RiskLevel, "sample_count": run.SampleCount, "started_at": run.StartedAt, "finished_at": run.FinishedAt, "evidence_summary": run.EvidenceSummary})
	}
	ok(c, gin.H{"items": items})
}

type checkinSiteRequest struct {
	Name           string `json:"name"`
	BaseURL        string `json:"base_url"`
	Key            string `json:"key"`
	CredentialKind string `json:"credential_kind"`
	Timezone       string `json:"timezone"`
	ScheduleMinute *int   `json:"schedule_minute"`
}

func (h *handler) createCheckinSite(c *gin.Context) {
	owner, valid := h.ownerID(c)
	if !valid {
		return
	}
	var req checkinSiteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "invalid_body", "invalid request body")
		return
	}
	base, err := relayprobe.ValidateBaseURL(req.BaseURL)
	if err != nil {
		fail(c, http.StatusBadRequest, "invalid_base_url", "站点地址未通过安全校验")
		return
	}
	plain := strings.TrimSpace(req.Key)
	if plain == "" {
		fail(c, http.StatusBadRequest, "key_required", "需要提供签到凭据")
		return
	}
	if req.CredentialKind == "" {
		req.CredentialKind = "bearer"
	}
	if req.CredentialKind != "bearer" && req.CredentialKind != "session_cookie" {
		fail(c, http.StatusBadRequest, "invalid_credential_kind", "仅支持 bearer 或 session_cookie")
		return
	}
	env, err := secretbox.Encrypt(plain)
	if err != nil {
		fail(c, http.StatusServiceUnavailable, "credential_unavailable", "平台未配置安全凭据存储")
		return
	}
	fp, err := secretbox.Fingerprint(plain)
	if err != nil {
		fail(c, http.StatusServiceUnavailable, "credential_unavailable", "平台未配置安全凭据存储")
		return
	}
	minute := 540
	if req.ScheduleMinute != nil {
		minute = *req.ScheduleMinute
	}
	if minute < 0 || minute > 1439 {
		fail(c, http.StatusBadRequest, "invalid_schedule", "签到时间必须在 00:00 到 23:59")
		return
	}
	timezone := strings.TrimSpace(req.Timezone)
	if timezone == "" {
		timezone = "Asia/Shanghai"
	}
	nextRunAt, err := apigxmodel.NextCheckinRunAt(common.GetTimestamp(), timezone, minute)
	if err != nil {
		failFromRepo(c, err)
		return
	}
	site := &apigxmodel.CheckinSite{OwnerUserID: owner, Name: req.Name, BaseURL: base, CredentialRef: env, CredentialFingerprint: fp, CredentialKind: req.CredentialKind, Timezone: timezone, ScheduleMinute: minute, NextRunAt: nextRunAt}
	if err := apigxmodel.CreateCheckinSite(h.db, site); err != nil {
		failFromRepo(c, err)
		return
	}
	ok(c, gin.H{"id": site.ID, "name": site.Name, "base_url": site.BaseURL, "credential_kind": site.CredentialKind, "enabled": site.Enabled, "schedule_minute": site.ScheduleMinute})
}
func (h *handler) listCheckinSites(c *gin.Context) {
	owner, valid := h.ownerID(c)
	if !valid {
		return
	}
	sites, err := apigxmodel.ListCheckinSites(h.db, owner, 100)
	if err != nil {
		failFromRepo(c, err)
		return
	}
	items := make([]gin.H, 0, len(sites))
	for _, s := range sites {
		items = append(items, gin.H{"id": s.ID, "name": s.Name, "base_url": s.BaseURL, "credential_kind": s.CredentialKind, "enabled": s.Enabled, "timezone": s.Timezone, "schedule_minute": s.ScheduleMinute, "last_run_at": s.LastRunAt, "next_run_at": s.NextRunAt, "last_status": s.LastStatus, "last_message": s.LastMessage, "failure_count": s.FailureCount})
	}
	ok(c, gin.H{"items": items})
}
func (h *handler) runCheckinSite(c *gin.Context) {
	owner, valid := h.ownerID(c)
	if !valid {
		return
	}
	id, valid := paramID(c, "id")
	if !valid {
		return
	}
	site, err := apigxmodel.GetCheckinSite(h.db, owner, id)
	if err != nil {
		failFromRepo(c, err)
		return
	}
	message, runErr := relayprobe.RunCheckin(c.Request.Context(), site)
	now := common.GetTimestamp()
	status := "success"
	if runErr != nil {
		status = "failed"
		message = "签到失败"
	}
	nextRunAt, nextErr := apigxmodel.NextCheckinRunAt(now, site.Timezone, site.ScheduleMinute)
	if nextErr != nil {
		nextRunAt = now + 86400
	}
	_ = apigxmodel.UpdateCheckinSiteRun(h.db, site.ID, now, nextRunAt, status, message, site.FailureCount)
	if runErr != nil {
		fail(c, http.StatusBadGateway, "checkin_failed", message)
		return
	}
	ok(c, gin.H{"status": status, "message": message})
}
func (h *handler) updateCheckinSiteStatus(c *gin.Context) {
	owner, valid := h.ownerID(c)
	if !valid {
		return
	}
	id, valid := paramID(c, "id")
	if !valid {
		return
	}
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "invalid_body", "invalid request body")
		return
	}
	site, err := apigxmodel.GetCheckinSite(h.db, owner, id)
	if err != nil {
		failFromRepo(c, err)
		return
	}
	if err := h.db.Model(site).Updates(map[string]any{"enabled": req.Enabled, "updated_at": common.GetTimestamp()}).Error; err != nil {
		failFromRepo(c, err)
		return
	}
	ok(c, gin.H{"id": id, "enabled": req.Enabled})
}
