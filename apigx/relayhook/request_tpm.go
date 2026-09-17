package relayhook

import (
	"errors"
	"net/http"
	"time"

	"github.com/QuantumNous/new-api/apigx/capacity"
	"github.com/QuantumNous/new-api/apigx/channelpool"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
)

// TPM（每分钟 token 数）维度 —— 挂在**请求解析之后**。
//
// # 为什么不能和并发/RPM 挂在同一个位置
//
// 计划书 §9.2 要求 TPM 预留必须使用**可执行的输出上限**，不得凭空估算 token 来预留。
// 而"输出上限"（`max_tokens` 等）只在**请求体解析之后**才存在：
// 我们的准入钩子位于 `SetupContextForSelectedChannel`，那里请求体还没解析。
//
// 因此本维度挂在 `relay.PrepareRequestBilling`：底座在那里已经算出
// `TokenCountMeta.MaxTokens`（缺省 8192，见 `relay/helper/price.go`）与 prompt token 数，
// **并且尚未发送请求**。预留量 = prompt tokens + 输出上限，即该次请求 token 用量的**上界**。
//
// 该位置还有两个好处：
//  1. 它在 `FreeModel` 短路**之前**执行，因此免费模型同样受 TPM 约束（计划书明确要求不得靠短路绕过计量）；
//  2. 它是协议无关的统一入口（OpenAI / Claude / Responses / 图像都经过它）。
//
// 与 RPM 一样，TPM 计数在窗口内**不归还**（已经发出去的 token 不会因为请求失败而"没发过"）。

// CheckRequestTPM 依据「prompt tokens + 输出上限」做 TPM 窗口预留。
//
// 返回 nil 表示放行；否则返回错误，调用方应当**直接中止**（不得发送请求）。
func CheckRequestTPM(c *gin.Context, channelID int, promptTokens int, outputCap int) *types.NewAPIError {
	if c == nil || channelID <= 0 {
		return nil
	}
	// 输出上限缺省值与底座估算保持一致（`relay/helper/price.go` 的 defaultTieredPreConsumeMaxTokens）
	if outputCap <= 0 {
		outputCap = 8192
	}
	amount := int64(promptTokens + outputCap)
	if amount <= 0 {
		return nil
	}

	limit, ok := resolveChannelTPMLimit(c, channelID)
	if !ok {
		// 未配置 / 明确不限：不施加约束
		return nil
	}
	if common.RDB == nil {
		// 未启用 Redis 时无法判断共享限额 → 失败关闭
		return types.NewErrorWithStatusCode(errors.New("tpm ledger unavailable"),
			types.ErrorCodeQueryDataError, http.StatusServiceUnavailable, types.ErrOptionWithSkipRetry())
	}

	ctrl := capacity.New(common.RDB)
	windowKey := capacity.ChannelTPM(int64(channelID), capacity.MinuteWindow(time.Now()))
	if err := ctrl.IncrementWindow(c.Request.Context(), windowKey, limit, amount, capacity.WindowTTL(time.Minute)); err != nil {
		var exceeded *capacity.ErrExceeded
		if errors.As(err, &exceeded) {
			common.SysLog("apigx capacity: channel tpm exceeded")
			c.Header("Retry-After", "1")
			abortJSON(c, http.StatusTooManyRequests, "capacity_exceeded",
				"capacity is temporarily exceeded, please retry later")
			return types.NewErrorWithStatusCode(errors.New("tpm exceeded"),
				types.ErrorCodeChannelNoAvailableKey, http.StatusTooManyRequests,
				types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog())
		}
		common.SysError("apigx capacity: tpm ledger error: " + err.Error())
		return types.NewErrorWithStatusCode(errors.New("tpm ledger error"),
			types.ErrorCodeQueryDataError, http.StatusServiceUnavailable, types.ErrOptionWithSkipRetry())
	}
	return nil
}

// resolveChannelTPMLimit 解析渠道的 TPM 上限（策略链：渠道策略 → 环境变量兜底）。
//
// 返回 false 表示「未配置 / 明确不限」——两种情形都不施加约束。
func resolveChannelTPMLimit(c *gin.Context, channelID int) (int64, bool) {
	envLimit := getConfig().ChannelTPMLimit
	db := channelpool.CurrentDB()
	shareChannelID, ok := channelpool.ShareChannelIDByBase(db, channelID)
	if !ok {
		return envLimit, envLimit > 0
	}
	chain := channelpool.ChannelTPMChain(db, shareChannelID, envLimit)
	res, err := capacity.Resolve(chain...)
	if err != nil {
		common.SysError("apigx capacity: resolve tpm limit failed: " + err.Error())
		return 0, false
	}
	if res.Decision == capacity.DecisionLimited {
		return res.Value, true
	}
	return 0, false
}
