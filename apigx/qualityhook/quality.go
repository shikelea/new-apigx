package qualityhook

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/QuantumNous/new-api/apigx/channelpool"
	apigxmodel "github.com/QuantumNous/new-api/apigx/model"
	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
)

// 质量样本采集（T2.4 接线）：把真实请求的**结果与耗时**写进分钟统计桶。
//
// # 记录什么、不记录什么
//
//   - 时间用 `relayInfo.StartTime` / `FirstResponseTime`（都是 `time.Time`），因此
//     延迟与首字延迟有**真实毫秒精度**——不需要像底座日志那样退化成整秒。
//   - 归属到**供给**：桶按供给 ID 归档，而供给就是"某个渠道的某个上游模型"。
//     非分享渠道（没有供给）直接跳过，不制造无主统计。
//   - 记录失败**不影响请求**：统计是旁路，任何错误只写系统日志。
//
// 默认关闭（`APIGX_QUALITY_STATS_ENABLED`），与其它 APIGX 开关保持一致。

// ClassifyOutcome 判断一次失败属于哪一类。
//
// 关键区分：**平台自身的拒绝不是上游质量问题**。准入（来源不允许）、容量（429）、
// 配额不足这些拒绝都发生在**我们这里**，把它们算成"上游失败"会误伤健康渠道——
// 那正是"错误归因"要避免的典型错误。
func ClassifyOutcome(err *types.NewAPIError, statusCode int) int {
	if err == nil {
		return apigxmodel.QualityOutcomeSuccess
	}
	switch err.GetErrorCode() {
	case types.ErrorCodeChannelNoAvailableKey, types.ErrorCodeInsufficientUserQuota,
		types.ErrorCodePreConsumeTokenQuotaFailed, types.ErrorCodeQueryDataError:
		return apigxmodel.QualityOutcomeRejected
	}
	if statusCode == http.StatusTooManyRequests || statusCode == http.StatusForbidden ||
		statusCode == http.StatusPaymentRequired {
		return apigxmodel.QualityOutcomeRejected
	}
	// 超时：底座有专门的错误码 `channel:response_time_exceeded`；此外 context 的 deadline
	// 与上游 504 也按超时归因（三者指向同一件事，不必拆成三类）。
	if errors.Is(err, context.DeadlineExceeded) || err.GetErrorCode() == types.ErrorCodeChannelResponseTimeExceeded ||
		statusCode == http.StatusGatewayTimeout {
		return apigxmodel.QualityOutcomeTimeout
	}
	// 客户端主动断开：既不是上游问题，也不是质量问题，单独归因
	if errors.Is(err, context.Canceled) {
		return apigxmodel.QualityOutcomeCancel
	}
	return apigxmodel.QualityOutcomeUpstream
}

// Enabled 报告质量采集是否开启。
func Enabled() bool {
	return common.GetEnvOrDefaultBool("APIGX_QUALITY_STATS_ENABLED", false)
}

// RecordFromRelay 按一次请求的结果写入质量样本。
//
// `apiErr` 为 nil 表示成功。函数**不返回错误**：统计失败不应影响请求处理，
// 但会写系统日志以便发现"统计悄悄停了"。
func RecordFromRelay(relayInfo *relaycommon.RelayInfo, apiErr *types.NewAPIError) {
	if !Enabled() || relayInfo == nil {
		return
	}
	db := channelpool.CurrentDB()
	if db == nil || relayInfo.ChannelId <= 0 {
		return
	}
	// 只有分享渠道才有供给；平台自建渠道不参与质量统计
	shareChannelID, ok := channelpool.ShareChannelIDByBase(db, relayInfo.ChannelId)
	if !ok {
		return
	}
	modelName := relayInfo.GetOriginModelName()
	offeringID, found, err := apigxmodel.FindOfferingByUpstreamModel(db, shareChannelID, modelName)
	if err != nil {
		common.SysError("apigx quality: lookup offering failed: " + err.Error())
		return
	}
	if !found {
		return
	}

	statusCode := 0
	if apiErr != nil {
		statusCode = apiErr.StatusCode
	}
	sample := apigxmodel.QualitySample{
		OfferingID: offeringID,
		Outcome:    ClassifyOutcome(apiErr, statusCode),
	}
	// 真实毫秒精度（`StartTime` / `FirstResponseTime` 都是 time.Time）
	if !relayInfo.StartTime.IsZero() {
		now := time.Now()
		if elapsed := now.Sub(relayInfo.StartTime).Milliseconds(); elapsed > 0 {
			sample.LatencyMs = elapsed
			if !relayInfo.FirstResponseTime.IsZero() && relayInfo.FirstResponseTime.After(relayInfo.StartTime) {
				ttft := relayInfo.FirstResponseTime.Sub(relayInfo.StartTime).Milliseconds()
				if ttft > 0 {
					sample.TtftMs = ttft
					// 生成阶段 = 总时长 - 首字延迟（近似：中间的网络往返不计入）
					if generation := elapsed - ttft; generation > 0 {
						sample.GenerationMs = generation
					}
				}
			}
		}
	}

	if recordErr := apigxmodel.RecordQualitySample(db, sample); recordErr != nil {
		common.SysError("apigx quality: record sample failed: " + recordErr.Error())
	}
}
