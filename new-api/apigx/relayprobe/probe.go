package relayprobe

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	apigxmodel "github.com/QuantumNous/new-api/apigx/model"
	"github.com/QuantumNous/new-api/apigx/secretbox"
	"github.com/QuantumNous/new-api/service"
	"gorm.io/gorm"
)

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
	raw = strings.TrimRight(strings.TrimSpace(raw), "/")
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", fmt.Errorf("invalid relay base url")
	}
	if err := service.ValidateSSRFProtectedFetchURL(raw); err != nil {
		return "", err
	}
	return raw, nil
}

// Probe performs a bounded, low-cost OpenAI-compatible model inventory check.
// It never logs or returns the credential and does not send a completion request.
func Probe(ctx context.Context, baseURL, protocol, key string) Result {
	started := time.Now()
	base, err := ValidateBaseURL(baseURL)
	if err != nil {
		return Result{Status: apigxmodel.RelayCheckFailed, Reason: "目标地址未通过安全校验"}
	}
	if protocol == "" {
		protocol = "openai-compatible"
	}
	if protocol != "openai-compatible" && protocol != "new-api" && protocol != "sub2api" {
		return Result{Status: apigxmodel.RelayCheckFailed, Reason: "暂不支持该协议的自动检测"}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/v1/models", nil)
	if err != nil {
		return Result{Status: apigxmodel.RelayCheckFailed, Reason: "无法创建检测请求"}
	}
	if strings.TrimSpace(key) != "" {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(key))
	}
	req.Header.Set("Accept", "application/json")
	client := service.GetSSRFProtectedHTTPClient()
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
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&payload); err != nil {
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
		if err := db.Where("id = ? AND deleted_at IS NULL", credentials[i].RelayShareID).First(&share).Error; err != nil {
			continue
		}
		result, probeErr := ProbeCredential(ctx, &share, &credentials[i])
		if probeErr != nil {
			result = Result{Status: apigxmodel.RelayCheckFailed, Reason: "检测凭据不可用"}
		}
		next := now + 30*60
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

// RunCheckin performs the two credential modes supported by the first scheduler contract.
func RunCheckin(ctx context.Context, site *apigxmodel.CheckinSite) (string, error) {
	if site == nil {
		return "", fmt.Errorf("checkin: nil site")
	}
	credential, err := secretbox.Decrypt(site.CredentialRef)
	if err != nil {
		return "", err
	}
	base, err := ValidateBaseURL(site.BaseURL)
	if err != nil {
		return "", err
	}
	client := service.GetSSRFProtectedHTTPClient()
	accessToken := ""
	cookieHeader := ""
	if site.CredentialKind == "session_cookie" {
		cookieHeader = strings.TrimSpace(credential)
		if !strings.Contains(cookieHeader, "=") {
			cookieHeader = service.RefreshCookieName + "=" + cookieHeader
		}
		refreshReq, requestErr := http.NewRequestWithContext(ctx, http.MethodPost, base+"/api/user/auth/refresh", nil)
		if requestErr != nil {
			return "", requestErr
		}
		refreshReq.Header.Set("Cookie", cookieHeader)
		refreshReq.Header.Set("Accept", "application/json")
		refreshResp, requestErr := client.Do(refreshReq)
		if requestErr != nil {
			return "", fmt.Errorf("登录态刷新连接失败")
		}
		defer refreshResp.Body.Close()
		if refreshResp.StatusCode < 200 || refreshResp.StatusCode >= 300 {
			return "", fmt.Errorf("登录态刷新返回 HTTP %d", refreshResp.StatusCode)
		}
		var refreshPayload struct {
			Success bool `json:"success"`
			Data    struct {
				AccessToken string `json:"access_token"`
			} `json:"data"`
		}
		if decodeErr := json.NewDecoder(io.LimitReader(refreshResp.Body, 1<<20)).Decode(&refreshPayload); decodeErr != nil || !refreshPayload.Success || strings.TrimSpace(refreshPayload.Data.AccessToken) == "" {
			return "", fmt.Errorf("登录态刷新响应无有效访问令牌")
		}
		accessToken = strings.TrimSpace(refreshPayload.Data.AccessToken)
	}
	checkinReq, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/api/user/checkin", nil)
	if err != nil {
		return "", err
	}
	if accessToken != "" {
		checkinReq.Header.Set("Authorization", "Bearer "+accessToken)
	} else {
		checkinReq.Header.Set("Authorization", "Bearer "+credential)
	}
	checkinReq.Header.Set("Accept", "application/json")
	resp, err := client.Do(checkinReq)
	if err != nil {
		return "", fmt.Errorf("签到连接失败")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("签到返回 HTTP %d", resp.StatusCode)
	}
	return "签到请求已提交", nil
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
		message, runErr := RunCheckin(ctx, &sites[i])
		status := "success"
		failures := sites[i].FailureCount
		if runErr != nil {
			status = "failed"
			failures++
			message = "签到失败"
		} else {
			failures = 0
		}
		nextRunAt, nextErr := apigxmodel.NextCheckinRunAt(now, sites[i].Timezone, sites[i].ScheduleMinute)
		if nextErr != nil {
			nextRunAt = now + 86400
		}
		if err := apigxmodel.UpdateCheckinSiteRun(db, sites[i].ID, now, nextRunAt, status, message, failures); err != nil {
			return completed, err
		}
		completed++
	}
	return completed, nil
}
