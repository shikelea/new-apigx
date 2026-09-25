package relayhook

import (
	"time"

	"github.com/QuantumNous/new-api/apigx/capacity"
	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
)

// TPM 预留的结算期调整（T2.5 最后一环）。
//
// # 问题
//
// TPM 预留必须用**可执行的输出上限**（计划书 §9.2），而上限通常远高于实际输出
// （缺省 8192）。预留后若不调整，渠道的每分钟 token 额度会被"最坏情况"占满，
// 结果是**明明没用到那么多也被限流**。
//
// # 做法
//
// 预留时把 {窗口 key, 预留量, TTL} 记在请求上下文里；拿到真实 usage 之后调用
// `AdjustReservedTPM`，把 `实际 - 预留` 的差额退回去（`capacity.AdjustWindow` 带下限，
// 重复调用也不会把计数减成负数）。
//
// # 为什么挂在消费日志点
//
// 底座把最终 usage 暴露到**消费日志参数**里（`summary.PromptTokens/CompletionTokens`），
// 而那里同时有 gin 上下文——是唯一"两样都齐"的位置。上下文的 token 键只有 prompt 部分，
// 结算入口只有 quota，都拿不到完整 usage。

// tpmReservationKey 是存放本次请求 TPM 预留信息的上下文键。
const tpmReservationKey = "apigx_tpm_reservation"

type tpmReservation struct {
	windowKey string
	reserved  int64
	ttl       time.Duration
}

// storeTPMReservation 记录一次 TPM 预留（供结算期按实际用量调整）。
func storeTPMReservation(c *gin.Context, reservation tpmReservation) {
	if c == nil || reservation.windowKey == "" || reservation.reserved <= 0 {
		return
	}
	c.Set(tpmReservationKey, reservation)
}

// tpmAdjustEnabled 是"结算期按实际用量调整预留"的开关（默认关闭，与其它 APIGX 开关一致）。
func tpmAdjustEnabled() bool {
	return common.GetEnvOrDefaultBool("APIGX_TPM_ADJUST_ENABLED", false)
}

// AdjustReservedTPM 按实际用量把 TPM 预留的差额退回窗口计数。
//
// 任何失败只记日志，**不影响请求**——这是旁路性质的记账修正。
func AdjustReservedTPM(c *gin.Context, actualTokens int64) {
	if !tpmAdjustEnabled() || c == nil || actualTokens <= 0 {
		return
	}
	value, exists := c.Get(tpmReservationKey)
	if !exists {
		return
	}
	reservation, ok := value.(tpmReservation)
	if !ok || reservation.windowKey == "" {
		return
	}
	delta := actualTokens - reservation.reserved
	if delta == 0 {
		return
	}
	if common.RDB == nil {
		return
	}
	ctrl := capacity.New(common.RDB)
	if _, err := ctrl.AdjustWindow(c.Request.Context(), reservation.windowKey, delta, reservation.ttl); err != nil {
		common.SysError("apigx capacity: adjust reserved tpm failed: " + err.Error())
		return
	}
	// 一次性记账：调整过就清掉，避免同一请求被重复调整
	c.Set(tpmReservationKey, tpmReservation{})
	common.SysLog("apigx capacity: adjusted reserved tpm by actual usage")
}
