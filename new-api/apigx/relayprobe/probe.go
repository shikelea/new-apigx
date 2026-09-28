package relayprobe

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	apigxmodel "github.com/QuantumNous/new-api/apigx/model"
	"github.com/QuantumNous/new-api/apigx/secretbox"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service"
	"gorm.io/gorm"
)

// Every outbound probe gets its own deadline. The shared protected client has
// no timeout when RELAY_TIMEOUT=0, so without this a single slow or silent
// relay could pin the scheduler forever and stall every other check.
const (
	probeRequestTimeout = 20 * time.Second
	// maxRequestTimeout caps the token-budget-scaled deadline so one very slow
	// relay still cannot hold a scheduler slot indefinitely.
	maxRequestTimeout  = 4 * time.Minute
	streamProbeTimeout = 45 * time.Second
	probeInterval      = int64(30 * 60)
)

func withProbeTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithTimeout(ctx, timeout)
}

// originOf returns scheme://host for a validated base URL. New API's cookie
// refresh endpoint rejects cookie requests without a matching Origin when
// secure session cookies are enabled.
func originOf(base string) string {
	parsed, err := url.Parse(base)
	if err != nil {
		return ""
	}
	return parsed.Scheme + "://" + parsed.Host
}

type Result struct {
	Status     string
	Reason     string
	LatencyMs  int64
	ModelCount int
	Models     []string
	HTTPStatus int
}

type modelListResponse struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
}

func ValidateBaseURL(raw string) (string, error) {
	raw = normalizeBaseURL(raw)
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", fmt.Errorf("invalid relay base url")
	}
	if err := service.ValidateSSRFProtectedFetchURL(raw); err != nil {
		return "", err
	}
	return raw, nil
}

// normalizeBaseURL trims the trailing slash and the redundant "/v1".
//
// Every prober builds its path as base + "/v1/...", but the address a relay
// hands out almost always already ends in "/v1". Left alone that produces
// "/v1/v1/chat/completions" and the run fails with a bare upstream 404 that
// reads like a broken relay rather than a mistyped field, so the suffix is
// dropped here once instead of in each caller.
func normalizeBaseURL(raw string) string {
	raw = strings.TrimRight(strings.TrimSpace(raw), "/")
	return strings.TrimRight(strings.TrimSuffix(raw, "/v1"), "/")
}

// Probe performs a bounded, low-cost OpenAI-compatible model inventory check.
// It never logs or returns the credential and does not send a completion request.
func Probe(ctx context.Context, baseURL, protocol, key string) Result {
	started := time.Now()
	base, err := ValidateBaseURL(baseURL)
	if err != nil {
		return Result{Status: apigxmodel.RelayCheckFailed, Reason: "目标地址未通过安全校验"}
	}
	if !supportedProtocol(protocol) {
		return Result{Status: apigxmodel.RelayCheckFailed, Reason: "暂不支持该协议的自动检测"}
	}
	return probeInventory(ctx, service.GetSSRFProtectedHTTPClient(), base, key, started)
}

func supportedProtocol(protocol string) bool {
	return protocol == "" || protocol == "openai-compatible" || protocol == "new-api" || protocol == "sub2api"
}

// probeInventory lists /v1/models with an already validated base URL. The
// client is injected so the audit and tests share one bounded implementation.
func probeInventory(ctx context.Context, client *http.Client, base, key string, started time.Time) Result {
	ctx, cancel := withProbeTimeout(ctx, probeRequestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/v1/models", nil)
	if err != nil {
		return Result{Status: apigxmodel.RelayCheckFailed, Reason: "无法创建检测请求"}
	}
	if strings.TrimSpace(key) != "" {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(key))
	}
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return Result{Status: apigxmodel.RelayCheckFailed, Reason: "连接失败", LatencyMs: time.Since(started).Milliseconds()}
	}
	defer resp.Body.Close()
	result := Result{HTTPStatus: resp.StatusCode, LatencyMs: time.Since(started).Milliseconds()}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		result.Status = apigxmodel.RelayCheckFailed
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			result.Reason = "凭据无效或无模型列表权限"
		} else {
			result.Reason = fmt.Sprintf("上游返回 HTTP %d", resp.StatusCode)
		}
		return result
	}
	var payload modelListResponse
	if err := common.DecodeJson(io.LimitReader(resp.Body, 2<<20), &payload); err != nil {
		return Result{Status: apigxmodel.RelayCheckDegraded, Reason: "模型列表响应格式无法识别", LatencyMs: result.LatencyMs, HTTPStatus: resp.StatusCode}
	}
	result.Models = make([]string, 0, len(payload.Data))
	for _, item := range payload.Data {
		if id := strings.TrimSpace(item.ID); id != "" && len(result.Models) < 200 {
			result.Models = append(result.Models, id)
		}
	}
	result.ModelCount = len(result.Models)
	if result.ModelCount == 0 {
		result.Status = apigxmodel.RelayCheckDegraded
		result.Reason = "接口可达但没有返回模型"
	} else {
		result.Status = apigxmodel.RelayCheckHealthy
		result.Reason = "模型列表检查通过"
	}
	return result
}

func ProbeCredential(ctx context.Context, share *apigxmodel.RelayShare, credential *apigxmodel.RelayCredential) (Result, error) {
	if share == nil || credential == nil {
		return Result{}, fmt.Errorf("relay probe: missing resource")
	}
	key, err := secretbox.Decrypt(credential.SecretRef)
	if err != nil {
		return Result{}, err
	}
	return Probe(ctx, share.BaseURL, share.Protocol, key), nil
}

// RunDue executes bounded continuous checks. It stores only status/metrics and never the response body.
func RunDue(ctx context.Context, db *gorm.DB, now int64, limit int) (int, error) {
	if db == nil {
		return 0, fmt.Errorf("relay probe: nil database")
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	var credentials []apigxmodel.RelayCredential
	if err := db.Where("enabled = ? AND deleted_at IS NULL AND (next_check_at = 0 OR next_check_at <= ?)", true, now).Order("next_check_at ASC, id ASC").Limit(limit).Find(&credentials).Error; err != nil {
		return 0, err
	}
	checked := 0
	for i := range credentials {
		if ctx != nil && ctx.Err() != nil {
			return checked, ctx.Err()
		}
		var share apigxmodel.RelayShare
		err := db.Where("id = ?", credentials[i].RelayShareID).First(&share).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// The share was deleted without disabling its credential. Retire the
			// orphan so it no longer sorts first and starves live credentials.
			if updateErr := db.Model(&apigxmodel.RelayCredential{}).Where("id = ?", credentials[i].ID).
				Updates(map[string]any{"enabled": false, "updated_at": now}).Error; updateErr != nil {
				return checked, updateErr
			}
			continue
		}
		if err != nil {
			return checked, err
		}
		if share.Status != apigxmodel.RelayStatusPublished || share.AliveFlag != apigxmodel.AliveFlagAlive {
			// Suspended shares keep their credential (they may be restored) but are
			// not probed; push them back so they do not block the queue.
			if updateErr := db.Model(&apigxmodel.RelayCredential{}).Where("id = ?", credentials[i].ID).
				Updates(map[string]any{"next_check_at": now + probeInterval, "updated_at": now}).Error; updateErr != nil {
				return checked, updateErr
			}
			continue
		}
		result, probeErr := ProbeCredential(ctx, &share, &credentials[i])
		if probeErr != nil {
			result = Result{Status: apigxmodel.RelayCheckFailed, Reason: "检测凭据不可用"}
		}
		next := now + probeInterval
		updates := map[string]any{"last_checked_at": now, "next_check_at": next, "updated_at": now, "failure_count": 0}
		if result.Status == apigxmodel.RelayCheckFailed {
			updates["failure_count"] = credentials[i].FailureCount + 1
		}
		if err := db.Model(&apigxmodel.RelayCredential{}).Where("id = ?", credentials[i].ID).Updates(updates).Error; err != nil {
			return checked, err
		}
		if err := db.Model(&apigxmodel.RelayShare{}).Where("id = ?", share.ID).Updates(map[string]any{"check_status": result.Status, "check_reason": result.Reason, "last_checked_at": now, "latency_ms": result.LatencyMs, "model_count": result.ModelCount, "updated_at": now}).Error; err != nil {
			return checked, err
		}
		checked++
	}
	return checked, nil
}

// CheckinOutcome carries the owner-facing summary plus, for session_cookie
// sites, the rotated refresh cookie that must be persisted before the next run.
type CheckinOutcome struct {
	Message           string
	RotatedCredential string
}

// RunCheckin performs the two credential modes supported by the first scheduler contract.
//
// New API rotates the refresh cookie on every /api/user/auth/refresh call and
// revokes the whole session when an old cookie is replayed. The rotated cookie
// is therefore returned even when the later check-in request fails, and the
// caller must store it; otherwise the next run would log the user out remotely.
func RunCheckin(ctx context.Context, site *apigxmodel.CheckinSite) (CheckinOutcome, error) {
	var outcome CheckinOutcome
	if site == nil {
		return outcome, fmt.Errorf("checkin: nil site")
	}
	credential, err := secretbox.Decrypt(site.CredentialRef)
	if err != nil {
		return outcome, err
	}
	base, err := ValidateBaseURL(site.BaseURL)
	if err != nil {
		return outcome, err
	}
	client := service.GetSSRFProtectedHTTPClient()
	bearer := strings.TrimSpace(credential)
	if site.CredentialKind == "session_cookie" {
		accessToken, rotated, refreshErr := refreshSessionCookie(ctx, client, base, credential)
		outcome.RotatedCredential = rotated
		if refreshErr != nil {
			return outcome, refreshErr
		}
		bearer = accessToken
	}
	ctx, cancel := withProbeTimeout(ctx, probeRequestTimeout)
	defer cancel()
	checkinReq, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/api/user/checkin", nil)
	if err != nil {
		return outcome, err
	}
	checkinReq.Header.Set("Authorization", "Bearer "+bearer)
	checkinReq.Header.Set("Accept", "application/json")
	resp, err := client.Do(checkinReq)
	if err != nil {
		return outcome, fmt.Errorf("签到连接失败")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return outcome, fmt.Errorf("签到返回 HTTP %d", resp.StatusCode)
	}
	// New API answers business failures (already checked in, feature off) with
	// HTTP 200 and success=false; surface that instead of claiming success.
	var payload struct {
		Success *bool  `json:"success"`
		Message string `json:"message"`
	}
	if decodeErr := common.DecodeJson(io.LimitReader(resp.Body, 64<<10), &payload); decodeErr == nil && payload.Success != nil && !*payload.Success {
		return outcome, fmt.Errorf("站点返回：%s", truncateRunes(strings.TrimSpace(payload.Message), 80))
	}
	outcome.Message = "签到请求已提交"
	if msg := strings.TrimSpace(payload.Message); msg != "" {
		outcome.Message = "签到完成：" + truncateRunes(msg, 80)
	}
	return outcome, nil
}

func refreshSessionCookie(ctx context.Context, client *http.Client, base, credential string) (accessToken, rotated string, err error) {
	cookieHeader := strings.TrimSpace(credential)
	if !strings.Contains(cookieHeader, "=") {
		cookieHeader = service.RefreshCookieName + "=" + cookieHeader
	}
	ctx, cancel := withProbeTimeout(ctx, probeRequestTimeout)
	defer cancel()
	refreshReq, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/api/user/auth/refresh", nil)
	if err != nil {
		return "", "", err
	}
	refreshReq.Header.Set("Cookie", cookieHeader)
	refreshReq.Header.Set("Accept", "application/json")
	if origin := originOf(base); origin != "" {
		refreshReq.Header.Set("Origin", origin)
	}
	refreshResp, err := client.Do(refreshReq)
	if err != nil {
		return "", "", fmt.Errorf("登录态刷新连接失败")
	}
	defer refreshResp.Body.Close()
	for _, cookie := range refreshResp.Cookies() {
		if cookie.Name == service.RefreshCookieName && strings.TrimSpace(cookie.Value) != "" && cookie.MaxAge >= 0 {
			rotated = service.RefreshCookieName + "=" + strings.TrimSpace(cookie.Value)
		}
	}
	if refreshResp.StatusCode < 200 || refreshResp.StatusCode >= 300 {
		return "", rotated, fmt.Errorf("登录态刷新返回 HTTP %d", refreshResp.StatusCode)
	}
	var refreshPayload struct {
		Success bool `json:"success"`
		Data    struct {
			AccessToken string `json:"access_token"`
		} `json:"data"`
	}
	if decodeErr := common.DecodeJson(io.LimitReader(refreshResp.Body, 1<<20), &refreshPayload); decodeErr != nil || !refreshPayload.Success || strings.TrimSpace(refreshPayload.Data.AccessToken) == "" {
		return "", rotated, fmt.Errorf("登录态刷新响应无有效访问令牌")
	}
	return strings.TrimSpace(refreshPayload.Data.AccessToken), rotated, nil
}

// PersistRotatedCheckinCredential re-encrypts a rotated refresh cookie. It is a
// no-op when the site did not rotate anything.
func PersistRotatedCheckinCredential(db *gorm.DB, siteID int64, rotated string, now int64) error {
	if strings.TrimSpace(rotated) == "" {
		return nil
	}
	envelope, err := secretbox.Encrypt(rotated)
	if err != nil {
		return err
	}
	fingerprint, err := secretbox.Fingerprint(rotated)
	if err != nil {
		return err
	}
	return db.Model(&apigxmodel.CheckinSite{}).Where("id = ?", siteID).Updates(map[string]any{
		"credential_ref": envelope, "credential_fingerprint": fingerprint, "updated_at": now,
	}).Error
}

// RunDueCheckins runs only due, enabled site jobs. The scheduler stores a generic
// success/failure summary and never persists the credential or response body.
func RunDueCheckins(ctx context.Context, db *gorm.DB, now int64, limit int) (int, error) {
	if db == nil {
		return 0, fmt.Errorf("checkin: nil database")
	}
	sites, err := apigxmodel.ListDueCheckinSites(db, now, limit)
	if err != nil {
		return 0, err
	}
	completed := 0
	for i := range sites {
		if ctx != nil && ctx.Err() != nil {
			return completed, ctx.Err()
		}
		if err := RecordCheckinRun(ctx, db, &sites[i], now); err != nil {
			return completed, err
		}
		completed++
	}
	return completed, nil
}

// RecordCheckinRun executes one check-in and stores its outcome, failure
// counter and rotated credential. Manual and scheduled runs share it so both
// paths keep the same bookkeeping.
func RecordCheckinRun(ctx context.Context, db *gorm.DB, site *apigxmodel.CheckinSite, now int64) error {
	outcome, runErr := RunCheckin(ctx, site)
	if err := PersistRotatedCheckinCredential(db, site.ID, outcome.RotatedCredential, now); err != nil {
		return err
	}
	status, message, failures := "success", outcome.Message, 0
	if runErr != nil {
		status, failures = "failed", site.FailureCount+1
		message = "签到失败：" + truncateRunes(runErr.Error(), 120)
	}
	nextRunAt, nextErr := apigxmodel.NextCheckinRunAt(now, site.Timezone, site.ScheduleMinute)
	if nextErr != nil {
		nextRunAt = now + 86400
	}
	if err := apigxmodel.UpdateCheckinSiteRun(db, site.ID, now, nextRunAt, status, message, failures); err != nil {
		return err
	}
	site.LastStatus, site.LastMessage, site.FailureCount, site.LastRunAt, site.NextRunAt = status, message, failures, now, nextRunAt
	return nil
}

func truncateRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "…"
}
