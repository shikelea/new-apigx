package channelpool

import (
	"fmt"
	"net/http"
	"sync/atomic"

	apigxmodel "github.com/QuantumNous/new-api/apigx/model"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// 逐 Key 统计在请求路径上的接入开关。
//
// **默认关闭**：它挂在每一次失败处理上（`service.ProcessChannelError`），属于热路径；
// 在真实流量下验证过之前，不应改变既有行为。
var keyStatsEnabled atomic.Bool

// retryableKeyCooldownSeconds is deliberately short: it prevents an unhealthy
// credential from being selected by immediately following requests while
// still allowing automatic recovery without a probe or manual pool apply.
const retryableKeyCooldownSeconds int64 = 60

func init() {
	keyStatsEnabled.Store(common.GetEnvOrDefaultBool("APIGX_KEY_STATS_ENABLED", false))
}

// SetKeyStatsEnabled 运行期切换（供测试与未来的后台配置使用）。
func SetKeyStatsEnabled(on bool) { keyStatsEnabled.Store(on) }

// IsKeyStatsEnabled 报告当前是否开启逐 Key 统计。
func IsKeyStatsEnabled() bool { return keyStatsEnabled.Load() }

// RecordFailureFromContext 依据请求上下文，把一次渠道失败记到对应的上游凭据上。
//
// 由底座的失败处理路径调用（见 docs/stage0/upstream-diff.md 的 D-014）。设计约束：
//
//   - **默认关闭**，关闭时立即返回，不产生任何查询；
//   - 只对**多 Key 渠道**生效：上下文里没有 Key 索引（`ContextKeyChannelMultiKeyIndex`）
//     就说明本次请求用的不是多 Key 渠道，直接跳过；
//   - 只记录**脱敏后的状态码与错误码**，不记录凭据、不记录上游原始报文（计划书 §10.3）；
//   - 任何错误都不向上传播：统计失败绝不能影响请求本身。
func RecordFailureFromContext(c *gin.Context, channelID int, statusCode int, errorCode string) {
	if !keyStatsEnabled.Load() || c == nil || channelID <= 0 {
		return
	}
	// 非多 Key 渠道：上下文没有索引，没有可归属的凭据
	if !common.GetContextKeyBool(c, constant.ContextKeyChannelIsMultiKey) {
		return
	}
	keyIndex := common.GetContextKeyInt(c, constant.ContextKeyChannelMultiKeyIndex)

	db := defaultDB()
	if db == nil {
		return
	}
	outcome := KeyOutcome{
		Success:   false,
		ErrorCode: formatOutcomeCode(statusCode, errorCode),
		// 401/403 表示上游明确拒绝了这份凭据 → Key 级故障隔离（计划书 §9.2）
		AuthFailed: statusCode == 401 || statusCode == 403,
	}
	if shouldCooldownRetryableKey(statusCode) {
		outcome.CooldownSeconds = retryableKeyCooldownSeconds
	}
	if err := RecordKeyOutcome(db, channelID, keyIndex, outcome); err != nil {
		// 统计失败只记日志，绝不影响请求结果
		common.SysError("channelpool: record key failure failed: " + err.Error())
	}
}

// shouldCooldownRetryableKey mirrors the transport-level retry boundary that
// can be determined from an HTTP status alone. 429 is intentionally excluded:
// it changes keys only inside the current request and remains eligible for the
// next request. Client/business errors also do not punish a healthy key.
func shouldCooldownRetryableKey(statusCode int) bool {
	if statusCode == http.StatusTooManyRequests || statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden {
		return false
	}
	if statusCode < 100 || statusCode > 599 {
		return true
	}
	return statusCode == http.StatusRequestTimeout || statusCode >= http.StatusInternalServerError
}

// formatOutcomeCode 生成脱敏的失败标签，例如 `429_upstream_rate_limited`。
func formatOutcomeCode(statusCode int, errorCode string) string {
	if errorCode == "" {
		return fmt.Sprintf("http_%d", statusCode)
	}
	return fmt.Sprintf("%d_%s", statusCode, errorCode)
}

// dbProvider 提供数据库句柄：默认为 apigx 层在迁移时捕获的**主库**（见 apigxmodel.Migrate）。
//
// 必须默认接上生产句柄——否则钩子会在生产静默失效（这正是本函数初版的问题，
// 靠「默认返回 nil」在测试里很方便，但上线后统计一条都不会写）。
var dbProvider = func() *gorm.DB { return apigxmodel.DefaultDB() }

// SetDBProvider 设置数据库来源（仅测试使用；生产由 apigxmodel.Migrate 捕获的主库提供）。
func SetDBProvider(fn func() *gorm.DB) {
	if fn == nil {
		dbProvider = func() *gorm.DB { return nil }
		return
	}
	dbProvider = fn
}

func defaultDB() *gorm.DB { return dbProvider() }

// RecordSuccessFromContext 依据请求上下文，把一次**成功**调用记到对应的上游凭据上。
//
// 由底座的 `middleware.Distribute` 在 `c.Next()` 之后、响应状态 < 400 时调用
// （见 docs/stage0/upstream-diff.md 的 D-015）——底座自己也在那一处记录渠道亲和性，
// 因此上下文里的 Key 索引仍然有效，且天然只覆盖成功响应。
//
// 与失败路径同样的四条约束：默认关闭、只对多 Key 渠道生效、不记录任何凭据信息、失败不影响请求。
func RecordSuccessFromContext(c *gin.Context, channelID int) {
	if !keyStatsEnabled.Load() || c == nil || channelID <= 0 {
		return
	}
	if !common.GetContextKeyBool(c, constant.ContextKeyChannelIsMultiKey) {
		return
	}
	keyIndex := common.GetContextKeyInt(c, constant.ContextKeyChannelMultiKeyIndex)

	db := defaultDB()
	if db == nil {
		return
	}
	if err := RecordKeyOutcome(db, channelID, keyIndex, KeyOutcome{Success: true}); err != nil {
		common.SysError("channelpool: record key success failed: " + err.Error())
	}
}
