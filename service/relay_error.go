package service

import (
	"fmt"
	"time"

	"github.com/QuantumNous/new-api/apigx/channelpool"
	"github.com/QuantumNous/new-api/apigx/qualityhook"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/bytedance/gopkg/util/gopool"
	"github.com/gin-gonic/gin"
)

func ShouldRetryRelayError(c *gin.Context, openaiErr *types.NewAPIError, retryTimes int) bool {
	if openaiErr == nil {
		return false
	}
	// Once any part of an HTTP response has reached the client, retrying would
	// append another upstream response to the same body. This is especially
	// dangerous for SSE, where an adaptor can fail after earlier events were
	// flushed successfully.
	if relayResponseStarted(c) {
		return false
	}
	if ShouldSkipRetryAfterChannelAffinityFailure(c) {
		return false
	}
	if GetChannelConstraints(c).SuppressesRetry() {
		return false
	}
	if types.IsChannelError(openaiErr) {
		return true
	}
	if types.IsSkipRetryError(openaiErr) {
		return false
	}
	if retryTimes <= 0 {
		return false
	}
	code := openaiErr.StatusCode
	if code >= 200 && code < 300 {
		return false
	}
	if code < 100 || code > 599 {
		return true
	}
	if operation_setting.IsAlwaysSkipRetryCode(openaiErr.GetErrorCode()) {
		return false
	}
	return operation_setting.ShouldRetryByStatusCode(code)
}

// ShouldFailoverMultiKeyRelayError applies the normal retry policy without the
// global cross-channel retry budget. A request can therefore exhaust the
// current channel's key pool even when RetryTimes is zero. Explicit no-retry
// errors, request cancellation, affinity rules, and single-attempt pins still
// stop immediately.
func ShouldFailoverMultiKeyRelayError(c *gin.Context, openaiErr *types.NewAPIError) bool {
	if openaiErr == nil {
		return false
	}
	if relayResponseStarted(c) {
		return false
	}
	if c != nil && c.Request != nil && c.Request.Context().Err() != nil {
		return false
	}
	if ShouldSkipRetryAfterChannelAffinityFailure(c) {
		return false
	}
	if GetChannelConstraints(c).SuppressesRetry() {
		return false
	}
	if types.IsChannelError(openaiErr) {
		return true
	}
	if types.IsSkipRetryError(openaiErr) {
		return false
	}
	code := openaiErr.StatusCode
	if code >= 200 && code < 300 {
		return false
	}
	if code < 100 || code > 599 {
		return true
	}
	if operation_setting.IsAlwaysSkipRetryCode(openaiErr.GetErrorCode()) {
		return false
	}
	return operation_setting.ShouldRetryByStatusCode(code)
}

func relayResponseStarted(c *gin.Context) bool {
	return c != nil && c.Writer != nil && c.Writer.Written()
}

func ProcessChannelError(c *gin.Context, channelError types.ChannelError, err *types.NewAPIError, relayInfo *relaycommon.RelayInfo) {
	if err == nil {
		return
	}
	logger.LogError(c, fmt.Sprintf("channel error (channel #%d, status code: %d): %s", channelError.ChannelId, err.StatusCode, common.LocalLogPreview(err.MaskSensitiveErrorWithStatusCode())))
	if ShouldDisableChannel(err) && channelError.AutoBan {
		reason := err.MaskSensitiveErrorWithStatusCode()
		gopool.Go(func() {
			DisableChannel(channelError, reason)
		})
	}

	// APIGX 逐 Key 统计与故障隔离（T1.5）：把失败记到对应的上游凭据上。
	// 默认关闭（APIGX_KEY_STATS_ENABLED）；只对多 Key 渠道生效，且统计失败不影响请求。
	channelpool.RecordFailureFromContext(c, channelError.ChannelId, err.StatusCode, string(err.GetErrorCode()))

	// APIGX 质量样本（T2.4）：失败也计入分钟桶，并按"平台拒绝 / 超时 / 取消 / 上游失败"归因。
	// 平台自身的准入与配额拒绝不算上游质量问题（见 qualityhook.ClassifyOutcome）。
	qualityhook.RecordFromRelay(relayInfo, err)

	if constant.ErrorLogEnabled && types.IsRecordErrorLog(err) {
		userId := c.GetInt("id")
		tokenName := c.GetString("token_name")
		modelName := c.GetString("original_model")
		tokenId := c.GetInt("token_id")
		userGroup := c.GetString("group")
		other := model.NewLogOther()
		if c.Request != nil && c.Request.URL != nil {
			other.SetPublic("request_path", c.Request.URL.Path)
		}
		other.SetPublic("error_type", err.GetErrorType())
		other.SetPublic("error_code", err.GetErrorCode())
		other.SetPublic("status_code", err.StatusCode)
		AppendRelayLogAdminInfo(c, relayInfo, other)
		AppendTaskPluginContextAuditInfo(c, other)
		startTime := common.GetContextKeyTime(c, constant.ContextKeyRequestStartTime)
		if startTime.IsZero() {
			startTime = time.Now()
		}
		useTimeSeconds := int(time.Since(startTime).Seconds())
		model.RecordErrorLog(c, userId, channelError.ChannelId, modelName, tokenName, err.MaskSensitiveErrorWithStatusCode(), tokenId, useTimeSeconds, common.GetContextKeyBool(c, constant.ContextKeyIsStream), userGroup, other)
	}
}
