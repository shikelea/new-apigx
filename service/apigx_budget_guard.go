package service

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/QuantumNous/new-api/apigx/capacity"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
)

// APIGX 每日预算维度（T1.7）。
//
// # 为什么放在计费会话这一层
//
// 预算预留的金额必须是**可执行的**：准入时我们拿得到本次要预扣的额度
// （底座在创建计费会话前已算出 `preConsumedQuota`），所以直接用它做预留，
// 不需要凭空估算 token（计划书 §9.2 对 TPM 的告诫同理）。
//
// # 与并发/速率的区别
//
// 预算计数**在窗口内不归还**（与 RPM 一致）：已花掉的钱不会因为请求失败而"没花过"。
// 窗口按**UTC 自然日**分桶，TTL 取 48 小时，避免跨时区/跨日边界时统计突然消失。
//
// 配置：环境变量 `APIGX_DAILY_BUDGET_MICRO`（微美元），0 表示未配置（不施加约束）。

// dailyBudgetWindow 返回当前 UTC 自然日的桶编号。
func dailyBudgetWindow(now time.Time) int64 {
	return now.UTC().Unix() / 86400
}

// budgetLimitFromEnv 读取部署级预算上限（微美元）。0 = 未配置。
func budgetLimitFromEnv() int64 {
	return int64(common.GetEnvOrDefault("APIGX_DAILY_BUDGET_MICRO", 0))
}

// CheckAPIGXDailyBudget 在计费会话建立前检查并累加使用者当日预算。
//
// 返回 nil 表示放行；否则返回应当直接拒绝的错误（**不建立会话**）。
// 账本不可用时**拒绝**而不是放行：无法安全判断预算时不能继续放量（与容量准入同一原则）。
func CheckAPIGXDailyBudget(c *gin.Context, relayInfo *relaycommon.RelayInfo, preConsumedQuota int) *types.NewAPIError {
	limit := budgetLimitFromEnv()
	if limit <= 0 || relayInfo == nil || relayInfo.UserId <= 0 {
		return nil
	}
	if common.RDB == nil {
		// 未启用 Redis：无法判断共享预算，按"失败关闭"处理
		return types.NewErrorWithStatusCode(errors.New("budget ledger unavailable"),
			types.ErrorCodeQueryDataError, http.StatusServiceUnavailable, types.ErrOptionWithSkipRetry())
	}

	amount := int64(preConsumedQuota)
	if amount <= 0 {
		// 预扣为 0（例如信任额度旁路）时也要**记一次最小值**，
		// 否则"零成本的请求"会完全绕过每日预算统计。
		amount = 1
	}

	ctrl := capacity.New(common.RDB)
	windowKey := capacity.UserDailyBudgetMicro(int64(relayInfo.UserId), dailyBudgetWindow(time.Now()))
	if err := ctrl.IncrementWindow(c.Request.Context(), windowKey, limit, amount, 48*time.Hour); err != nil {
		var exceeded *capacity.ErrExceeded
		if errors.As(err, &exceeded) {
			logger.LogWarn(c, fmt.Sprintf("APIGX daily budget exceeded for user %d", relayInfo.UserId))
			return types.NewErrorWithStatusCode(
				errors.New("daily budget exceeded"),
				types.ErrorCodeInsufficientUserQuota, http.StatusForbidden,
				types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog())
		}
		common.SysError("apigx budget guard: ledger error: " + err.Error())
		return types.NewErrorWithStatusCode(errors.New("budget ledger error"),
			types.ErrorCodeQueryDataError, http.StatusServiceUnavailable, types.ErrOptionWithSkipRetry())
	}
	return nil
}
