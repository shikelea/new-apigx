package apigxapi

import (
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

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
	r.POST("/:publicID/check", middleware.UserAuth(), middleware.UserCriticalRateLimit("relay_share_check"), h.checkRelayShare)
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

const (
	relayTitleMaxRunes   = 120
	relayContentMaxRunes = 20000
	relayNameMaxRunes    = 100
	relayBaseURLMaxLen   = 512
)

// relayOwnerNames resolves display names for a page of shares in one query
// instead of one query per card.
func relayOwnerNames(db *gorm.DB, shares []apigxmodel.RelayShare) map[int64]string {
	ids := make([]int64, 0, len(shares))
	for _, share := range shares {
		ids = append(ids, share.OwnerUserID)
	}
	names := make(map[int64]string, len(ids))
	if len(ids) == 0 {
		return names
	}
	var users []basemodel.User
	if err := db.Select("id", "username", "display_name").Where("id IN ?", ids).Find(&users).Error; err != nil {
		return names
	}
	for _, user := range users {
		switch {
		case strings.TrimSpace(user.DisplayName) != "":
			names[int64(user.Id)] = user.DisplayName
		case strings.TrimSpace(user.Username) != "":
			names[int64(user.Id)] = user.Username
		}
	}
	return names
}

func relayShareJSON(share apigxmodel.RelayShare, names map[int64]string) gin.H {
	owner := names[share.OwnerUserID]
	if owner == "" {
		owner = "平台用户"
	}
	return gin.H{
		"public_id": share.PublicID, "title": share.Title, "base_url": share.BaseURL,
		"kind": share.Kind, "protocol": share.Protocol, "content": share.Content,
		"status": share.Status, "check_status": share.CheckStatus, "check_reason": share.CheckReason,
		"last_checked_at": share.LastCheckedAt, "latency_ms": share.LatencyMs,
		"first_token_ms": share.FirstTokenMs, "model_count": share.ModelCount,
		"vote_usable": share.VoteUsable, "vote_unavailable": share.VoteUnavailable,
		"owner_name": owner,
		"created_at": share.CreatedAt, "updated_at": share.UpdatedAt,
	}
}

func (h *handler) singleRelayShareJSON(share apigxmodel.RelayShare) gin.H {
	return relayShareJSON(share, relayOwnerNames(h.db, []apigxmodel.RelayShare{share}))
}

func (h *handler) listPublicRelayShares(c *gin.Context) {
	shares, err := apigxmodel.ListRelayShares(h.db, 0, false, 100)
	if err != nil {
		failFromRepo(c, err)
		return
	}
	names := relayOwnerNames(h.db, shares)
	myVotes := map[int64]bool{}
	if viewer := c.GetInt("id"); viewer > 0 && len(shares) > 0 {
		ids := make([]int64, 0, len(shares))
		for _, share := range shares {
			ids = append(ids, share.ID)
		}
		var votes []apigxmodel.RelayVote
		if err := h.db.Where("user_id = ? AND relay_share_id IN ?", viewer, ids).Find(&votes).Error; err != nil {
			failFromRepo(c, err)
			return
		}
		for _, vote := range votes {
			myVotes[vote.RelayShareID] = vote.Usable
		}
	}
	items := make([]gin.H, 0, len(shares))
	for _, share := range shares {
		item := relayShareJSON(share, names)
		if usable, voted := myVotes[share.ID]; voted {
			item["my_vote"] = usable
		}
		items = append(items, item)
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
	ok(c, h.singleRelayShareJSON(share))
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
	names := relayOwnerNames(h.db, shares)
	items := make([]gin.H, 0, len(shares))
	for _, share := range shares {
		item := relayShareJSON(share, names)
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
	if utf8.RuneCountInString(strings.TrimSpace(req.Title)) > relayTitleMaxRunes ||
		utf8.RuneCountInString(req.Content) > relayContentMaxRunes || len(strings.TrimSpace(req.BaseURL)) > relayBaseURLMaxLen {
		fail(c, http.StatusBadRequest, "field_too_long", "名称最多 120 字、介绍最多 20000 字、地址最多 512 字符")
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
	suffix := keySuffix(strings.TrimSpace(req.Key))
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
	ok(c, gin.H{"relay": h.singleRelayShareJSON(*share), "credential_configured": true, "key_suffix": suffix})
}

// keySuffix shows the last four characters only when the key is long enough
// that they do not reveal a meaningful share of the secret.
func keySuffix(key string) string {
	if len(key) < 12 {
		return "****"
	}
	return key[len(key)-4:]
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
	if err := h.db.Model(&apigxmodel.RelayCredential{}).Where("id = ?", credential.ID).Updates(map[string]any{"last_checked_at": now, "next_check_at": now + 1800, "updated_at": now}).Error; err != nil {
		failFromRepo(c, err)
		return
	}
	if probeErr != nil {
		fail(c, http.StatusBadGateway, "probe_failed", result.Reason)
		return
	}
	ok(c, gin.H{"status": result.Status, "reason": result.Reason, "latency_ms": result.LatencyMs, "model_count": result.ModelCount, "models": result.Models})
}

func (h *handler) voteRelayShare(c *gin.Context) {
	voter, valid := h.ownerID(c)
	if !valid {
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
	if err := apigxmodel.AddRelayVote(h.db, share.ID, voter, req.Usable); err != nil {
		failFromRepo(c, err)
		return
	}
	ok(c, gin.H{"voted": true, "usable": req.Usable})
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
	// Share and credential are retired together; a half-applied delete used to
	// leave an enabled credential behind that clogged the probe queue.
	err = h.db.Transaction(func(tx *gorm.DB) error {
		now := time.Now().UTC()
		result := tx.Model(&apigxmodel.RelayShare{}).Where("id = ? AND owner_user_id = ? AND alive_flag = ?", id, owner, apigxmodel.AliveFlagAlive).Updates(map[string]any{"deleted_at": now, "alive_flag": apigxmodel.AliveFlagAlive + id, "status": apigxmodel.RelayStatusSuspended, "updated_at": common.GetTimestamp()})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return apigxmodel.ErrNotFound
		}
		return tx.Model(&apigxmodel.RelayCredential{}).Where("relay_share_id = ? AND owner_user_id = ? AND deleted_at IS NULL", id, owner).Updates(map[string]any{"enabled": false, "deleted_at": now, "updated_at": common.GetTimestamp()}).Error
	})
	if err != nil {
		failFromRepo(c, err)
		return
	}
	ok(c, gin.H{"deleted": id})
}

type auditRunRequest struct {
	RelayShareID       int64  `json:"relay_share_id"`
	RelaySharePublicID string `json:"relay_share_public_id"`
	Model              string `json:"model"`
}

// auditCooldownRuns bounds how many paid audits one account can start per
// minute: every run sends ~12 real (small) completions through the relay.
const (
	auditCooldownRuns    = 3
	auditCooldownSeconds = 60
)

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
	model := strings.TrimSpace(req.Model)
	if model == "" || (req.RelayShareID <= 0 && strings.TrimSpace(req.RelaySharePublicID) == "") {
		fail(c, http.StatusBadRequest, "invalid_argument", "relay share and model are required")
		return
	}
	if utf8.RuneCountInString(model) > 191 {
		fail(c, http.StatusBadRequest, "invalid_argument", "model is too long")
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
	key, err := secretbox.Decrypt(credential.SecretRef)
	if err != nil {
		fail(c, http.StatusServiceUnavailable, "credential_unavailable", "检测凭据不可用")
		return
	}
	var recent int64
	if err := h.db.Model(&apigxmodel.AuditRun{}).Where("owner_user_id = ? AND created_at >= ?", owner, common.GetTimestamp()-auditCooldownSeconds).Count(&recent).Error; err != nil {
		failFromRepo(c, err)
		return
	}
	if recent >= auditCooldownRuns {
		fail(c, http.StatusTooManyRequests, "audit_cooldown", "审计请求过于频繁，请一分钟后再试")
		return
	}
	run := &apigxmodel.AuditRun{OwnerUserID: owner, RelayShareID: share.ID, Model: model, Status: apigxmodel.AuditStatusRunning, StartedAt: common.GetTimestamp()}
	if err := apigxmodel.CreateAuditRun(h.db, run); err != nil {
		failFromRepo(c, err)
		return
	}
	report, auditErr := relayprobe.RunAudit(c.Request.Context(), share.BaseURL, share.Protocol, key, model)
	finished := common.GetTimestamp()
	if auditErr != nil {
		_ = h.db.Model(run).Updates(map[string]any{"status": apigxmodel.AuditStatusFailed, "error_message": auditErr.Error(), "finished_at": finished, "updated_at": finished}).Error
		fail(c, http.StatusBadRequest, "audit_failed", auditErr.Error())
		return
	}
	samples := 0
	err = h.db.Transaction(func(tx *gorm.DB) error {
		for _, check := range report.Checks {
			samples += check.SampleCount
			if createErr := apigxmodel.CreateAuditCheck(tx, &apigxmodel.AuditCheck{
				AuditRunID: run.ID, CheckID: check.ID, Name: check.Name, Category: check.Category,
				Status: check.Status, SampleCount: check.SampleCount, LatencyMs: check.LatencyMs,
				EvidenceSummary: check.Evidence, Confidence: check.Confidence, CreatedAt: finished,
			}); createErr != nil {
				return createErr
			}
		}
		return tx.Model(run).Updates(map[string]any{
			"status": apigxmodel.AuditStatusCompleted, "risk_level": report.RiskLevel, "sample_count": samples,
			"finished_at": finished, "evidence_summary": relayprobe.AuditDisclaimer, "updated_at": finished,
		}).Error
	})
	if err != nil {
		_ = h.db.Model(run).Updates(map[string]any{"status": apigxmodel.AuditStatusFailed, "error_message": "保存审计结果失败", "finished_at": finished, "updated_at": finished}).Error
		failFromRepo(c, err)
		return
	}
	ok(c, gin.H{
		"public_id": run.PublicID, "relay_share_id": share.ID, "relay_title": share.Title, "model": model,
		"status": apigxmodel.AuditStatusCompleted, "risk_level": report.RiskLevel, "sample_count": samples,
		"started_at": run.StartedAt, "finished_at": finished, "evidence_summary": relayprobe.AuditDisclaimer,
		"checks": report.Checks,
	})
}

func auditCheckJSON(check apigxmodel.AuditCheck) gin.H {
	return gin.H{
		"id": check.CheckID, "name": check.Name, "category": check.Category, "status": check.Status,
		"evidence": check.EvidenceSummary, "confidence": check.Confidence,
		"sample_count": check.SampleCount, "latency_ms": check.LatencyMs,
	}
}

func (h *handler) listAuditRuns(c *gin.Context) {
	owner, valid := h.ownerID(c)
	if !valid {
		return
	}
	var runs []apigxmodel.AuditRun
	if err := h.db.Where("owner_user_id = ?", owner).Order("id DESC").Limit(50).Find(&runs).Error; err != nil {
		failFromRepo(c, err)
		return
	}
	runIDs := make([]int64, 0, len(runs))
	shareIDs := make([]int64, 0, len(runs))
	for _, run := range runs {
		runIDs = append(runIDs, run.ID)
		shareIDs = append(shareIDs, run.RelayShareID)
	}
	checksByRun := map[int64][]gin.H{}
	titles := map[int64]string{}
	if len(runIDs) > 0 {
		var checks []apigxmodel.AuditCheck
		if err := h.db.Where("audit_run_id IN ?", runIDs).Order("id ASC").Find(&checks).Error; err != nil {
			failFromRepo(c, err)
			return
		}
		for _, check := range checks {
			checksByRun[check.AuditRunID] = append(checksByRun[check.AuditRunID], auditCheckJSON(check))
		}
		var shares []apigxmodel.RelayShare
		if err := h.db.Unscoped().Select("id", "title").Where("id IN ? AND owner_user_id = ?", shareIDs, owner).Find(&shares).Error; err != nil {
			failFromRepo(c, err)
			return
		}
		for _, share := range shares {
			titles[share.ID] = share.Title
		}
	}
	items := make([]gin.H, 0, len(runs))
	for _, run := range runs {
		checks := checksByRun[run.ID]
		if checks == nil {
			checks = []gin.H{}
		}
		items = append(items, gin.H{
			"public_id": run.PublicID, "relay_share_id": run.RelayShareID, "relay_title": titles[run.RelayShareID],
			"model": run.Model, "status": run.Status, "risk_level": run.RiskLevel, "sample_count": run.SampleCount,
			"started_at": run.StartedAt, "finished_at": run.FinishedAt, "evidence_summary": run.EvidenceSummary,
			"error_message": run.ErrorMessage, "checks": checks,
		})
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
	if name := strings.TrimSpace(req.Name); name == "" || utf8.RuneCountInString(name) > relayNameMaxRunes || len(strings.TrimSpace(req.BaseURL)) > relayBaseURLMaxLen {
		fail(c, http.StatusBadRequest, "invalid_argument", "名称必填且最多 100 字，地址最多 512 字符")
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
	if len(timezone) > 64 {
		fail(c, http.StatusBadRequest, "invalid_timezone", "时区无效")
		return
	}
	nextRunAt, err := apigxmodel.NextCheckinRunAt(common.GetTimestamp(), timezone, minute)
	if err != nil {
		failFromRepo(c, err)
		return
	}
	site := &apigxmodel.CheckinSite{OwnerUserID: owner, Name: strings.TrimSpace(req.Name), BaseURL: base, CredentialRef: env, CredentialFingerprint: fp, CredentialKind: req.CredentialKind, Timezone: timezone, ScheduleMinute: minute, NextRunAt: nextRunAt}
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
	if err := relayprobe.RecordCheckinRun(c.Request.Context(), h.db, site, common.GetTimestamp()); err != nil {
		failFromRepo(c, err)
		return
	}
	if site.LastStatus != "success" {
		fail(c, http.StatusBadGateway, "checkin_failed", site.LastMessage)
		return
	}
	ok(c, gin.H{"status": site.LastStatus, "message": site.LastMessage})
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
