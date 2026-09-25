package relay

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

func AudioHelper(c *gin.Context, info *relaycommon.RelayInfo) (newAPIError *types.NewAPIError) {
	info.InitChannelMeta(c)

	audioReq, ok := info.Request.(*dto.AudioRequest)
	if !ok {
		return types.NewError(errors.New("invalid request type"), types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
	}

	request, err := common.DeepCopy(audioReq)
	if err != nil {
		return types.NewError(fmt.Errorf("failed to copy request to AudioRequest: %w", err), types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
	}

	err = helper.ModelMappedHelper(c, info, request)
	if err != nil {
		return types.NewError(err, types.ErrorCodeChannelModelMappedError, types.ErrOptionWithSkipRetry())
	}

	adaptor := GetAdaptor(info.ApiType)
	if adaptor == nil {
		return types.NewError(fmt.Errorf("invalid api type: %d", info.ApiType), types.ErrorCodeInvalidApiType, types.ErrOptionWithSkipRetry())
	}
	adaptor.Init(info)

	ioReader, err := adaptor.ConvertAudioRequest(c, info, *request)
	if err != nil {
		return types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
	}

	resp, err := adaptor.DoRequest(c, info, ioReader)
	if err != nil {
		return types.NewOpenAIError(err, types.ErrorCodeDoRequestFailed, http.StatusInternalServerError)
	}
	statusCodeMappingStr := c.GetString("status_code_mapping")

	var httpResp *http.Response
	if resp != nil {
		httpResp = resp.(*http.Response)
		if httpResp.StatusCode != http.StatusOK {
			newAPIError = service.RelayErrorHandler(c.Request.Context(), httpResp, false)
			// reset status code 重置状态码
			service.ResetStatusCode(newAPIError, statusCodeMappingStr)
			return newAPIError
		}
	}

	usage, newAPIError := adaptor.DoResponse(c, httpResp, info)
	if newAPIError != nil {
		// reset status code 重置状态码
		service.ResetStatusCode(newAPIError, statusCodeMappingStr)
		return newAPIError
	}
	// **成功但完全不带 usage 时，必须让这件事可见**（阶段 3 实测发现）：
	// 上游（替身，以及不少真实的转写实现）只返回 `text`/`duration`、**不带 usage**，
	// 于是 token 全为 0 ⇒ **本次计费依据不足**（用量记录仍会写，见 `PostTextConsumeQuota` 分支，
	// 但额度按 0 计）。这里把"本次响应未报告任何用量"记进日志，供运维与审计发现。
	//
	// 这里**不改变任何计费行为**（额度仍由既有规则算），只把"本次没有可用用量"写进日志，
	// 让运维与审计能发现它。**定价策略**（未知价格时拒绝 / 按兜底价并标注 / 允许但标记未计费）
	// 属产品决策，另行确认。
	parsedUsage := usage.(*dto.Usage)
	reportedTokens := parsedUsage.PromptTokens + parsedUsage.CompletionTokens +
		parsedUsage.PromptTokensDetails.AudioTokens + parsedUsage.CompletionTokenDetails.AudioTokens
	if reportedTokens == 0 {
		logger.LogWarn(c, fmt.Sprintf(
			"audio relay succeeded without usage: nothing to charge (channel_id=%d model=%s path=%s) —— 需要定价策略决策，勿静默",
			info.ChannelId, info.OriginModelName, c.Request.URL.Path))
	}
	if parsedUsage.CompletionTokenDetails.AudioTokens > 0 || parsedUsage.PromptTokensDetails.AudioTokens > 0 {
		service.PostAudioConsumeQuota(c, info, parsedUsage, "")
	} else {
		service.PostTextConsumeQuota(c, info, parsedUsage, nil)
	}

	return nil
}
