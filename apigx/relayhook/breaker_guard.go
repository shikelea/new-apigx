package relayhook

import (
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/apigx/channelpool"
	apigxmodel "github.com/QuantumNous/new-api/apigx/model"
	"github.com/QuantumNous/new-api/apigx/routing"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// 熔断的**实时生效**（T2.8 生效路径）。
//
// # 复用哪一层
//
// 判定逻辑在 `apigx/routing`（推导式，见那里的说明）。这里只做两件事：
//  1. 在每次尝试的准入点，判断"当前选中的这条供给是否处于熔断打开"；
//  2. 是则返回**可重试**错误，让底座用它自己的重选机制换一条渠道——
//     **不自己写候选循环**（二开第一原则），也不永久禁用渠道。
//
// 为什么返回可重试而不是直接拒绝：熔断是"这条上游暂时不行"，不是"这个请求不该被服务"。
// 底座有别的合格渠道时就该换一条；都没有时底座的既有逻辑会给出无可用渠道的结果。
//
// # 性能
//
// 判定要读质量桶，属于每请求都会走的路径，因此**加短 TTL 缓存**（30s）：
// 熔断状态本身按分钟窗口变化，30 秒的滞后完全可接受；而省下的是每次尝试一次聚合查询。

// breakerEnabled 每次读取环境变量（默认关闭，与其它 APIGX 开关一致）。
//
// 刻意不做成初始化时读取的包级变量：那样开关在进程启动后就固定了，
// 既无法在测试里切换，也不便在运维侧临时关掉。
func breakerEnabled() bool {
	return common.GetEnvOrDefaultBool("APIGX_BREAKER_ENABLED", false)
}

const breakerCacheTTL = 30 * time.Second

type cachedBreaker struct {
	open      bool
	expiresAt time.Time
}

var (
	breakerCacheMu sync.RWMutex
	// breakerCache 以**供给 ID** 为键：熔断是针对"某条供给（某渠道的某模型）"的。
	breakerCache = map[int64]cachedBreaker{}
)

// InvalidateBreakerCache 清空熔断判定缓存（运维/测试用）。
func InvalidateBreakerCache() {
	breakerCacheMu.Lock()
	breakerCache = map[int64]cachedBreaker{}
	breakerCacheMu.Unlock()
}

// CheckBreakerAllows 判断当前选中的渠道上、该模型对应的供给是否被熔断。
//
// 返回 nil 表示放行。
func CheckBreakerAllows(c *gin.Context, channelID int, modelName string) *types.NewAPIError {
	if !breakerEnabled() || c == nil || channelID <= 0 || modelName == "" {
		return nil
	}
	db := channelpool.CurrentDB()
	if db == nil {
		return nil
	}
	shareChannelID, ok := channelpool.ShareChannelIDByBase(db, channelID)
	if !ok {
		return nil // 平台自建渠道不参与熔断
	}
	offeringID, found, err := apigxmodel.FindOfferingByUpstreamModel(db, shareChannelID, modelName)
	if err != nil || !found {
		return nil // 没有供给：交给其它准入判定处理
	}

	if !breakerOpenCached(db, offeringID) {
		return nil
	}
	common.SysLog("apigx breaker: offering is open, retry with another channel")
	return types.NewErrorWithStatusCode(
		errors.New("upstream source is temporarily unhealthy, retry with another channel"),
		types.ErrorCodeChannelNoAvailableKey, http.StatusServiceUnavailable,
		types.ErrOptionWithNoRecordErrorLog())
}

// breakerOpenCached 带短 TTL 缓存地判断某供给是否熔断打开。
//
// **读不到统计时不熔断**：统计读失败（数据库抖动）不代表上游坏了，
// 因统计故障把一个健康渠道摘掉，比"短暂用到可能不健康的渠道"更糟。
// 这条与"准入失败要失败关闭"不矛盾——那里守卫的是**配额与安全**，这里守卫的是**可用性判断**。
func breakerOpenCached(db *gorm.DB, offeringID int64) bool {
	now := time.Now()
	breakerCacheMu.RLock()
	if cached, ok := breakerCache[offeringID]; ok && now.Before(cached.expiresAt) {
		breakerCacheMu.RUnlock()
		return cached.open
	}
	breakerCacheMu.RUnlock()

	nowTs := common.GetTimestamp()
	view, err := apigxmodel.ResolveQualityView(db, offeringID, apigxmodel.QualityWindowSixHour, nowTs)
	if err != nil {
		common.SysError("apigx breaker: resolve quality view failed: " + err.Error())
		return false
	}
	verdict := routing.EvaluateBreaker(view, nowTs, routing.DefaultBreakerConfig())
	open := verdict.State == routing.BreakerOpen

	breakerCacheMu.Lock()
	breakerCache[offeringID] = cachedBreaker{open: open, expiresAt: now.Add(breakerCacheTTL)}
	breakerCacheMu.Unlock()
	return open
}
