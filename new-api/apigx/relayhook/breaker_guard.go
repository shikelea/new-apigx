package relayhook

import (
	"errors"
	"maps"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/apigx/channelpool"
	apigxmodel "github.com/QuantumNous/new-api/apigx/model"
	"github.com/QuantumNous/new-api/apigx/routing"
	"github.com/QuantumNous/new-api/common"
	basemodel "github.com/QuantumNous/new-api/model"
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

func BreakerEnabled() bool { return breakerEnabled() }

const breakerCacheTTL = 30 * time.Second

// breakerClosedCacheTTL 是"未打开"结论的缓存时长，故意很短：
// 该结论随时可能被下一个请求推翻，缓存久了会让熔断在失败爆发期失明（实测过）。
const breakerClosedCacheTTL = 3 * time.Second

const (
	// breakerRecentFailureSeconds 是"算作刚刚失败过"的时间窗。
	breakerRecentFailureSeconds = 60
	// breakerRecentFailureCacheTTL 是"刚失败过"的供给的判定缓存时长——刻意极短。
	breakerRecentFailureCacheTTL = time.Second
)

// breakerCooldown 是"打开后至少保持不可用"的时长。
//
// # 为什么必须有冷却期（端到端回扫发现的抖动）
//
// 一旦熔断打开、我们把流量挪到别的渠道，这条坏渠道就**不再产生新失败**了——
// 于是分钟窗口里的失败**过期**，下一次判定变成"未打开"，我们就把它**恢复**；
// 流量再次打到它上面、再次失败、再次打开……表现为**请求间歇性失败**且坏渠道的样本数还在增长。
//
// 因此：打开后**必须**先冷却，冷却期内忽略"看起来已经好了"的判定；
// 冷却结束后的第一次经过才允许恢复（这也就是计划书里的"冷却后再半开探测"）。
var breakerCooldown = 60 * time.Second

// ResetBreakerCooldownForTest is retained for tests that predate the durable
// probe state. Cooldown ownership now lives in apigx_breaker_probe_states.
func ResetBreakerCooldownForTest() {}

type cachedBreaker struct {
	open      bool
	expiresAt time.Time
}

var (
	breakerCacheMu sync.RWMutex
	// breakerCache 以**供给 ID** 为键：熔断是针对"某条供给（某渠道的某模型）"的。
	breakerCache = map[int64]cachedBreaker{}
)

const breakerProbeContextKey = "apigx_breaker_probe"

// MarkBreakerProbe marks an internally-created channel test as the active
// recovery probe. The probe still passes ACL, Key selection, capacity and the
// real relay path; it bypasses only the breaker that deliberately blocks user
// traffic from reaching this source.
func MarkBreakerProbe(c *gin.Context) {
	if c != nil {
		c.Set(breakerProbeContextKey, true)
	}
}

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
	if c.GetBool(breakerProbeContextKey) {
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

	open := breakerOpenCached(db, offeringID)
	// 无论开还是关，都同步一次底座渠道的"可跳过标记"：
	// 打开 → 标记；已恢复 → 回收我们自己的标记。
	syncBreakerMarks(db, channelID, offeringID, open)
	if !open {
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
	blocked, blockErr := apigxmodel.BreakerProbeBlocksTraffic(db, offeringID)
	if blockErr != nil {
		common.SysError("apigx breaker: resolve probe state failed: " + blockErr.Error())
		return false
	}
	if blocked {
		breakerCacheMu.Lock()
		breakerCache[offeringID] = cachedBreaker{open: true, expiresAt: now.Add(breakerCacheTTL)}
		breakerCacheMu.Unlock()
		return true
	}
	baseline, baselineErr := apigxmodel.BreakerRecoveryBaseline(db, offeringID)
	if baselineErr != nil {
		common.SysError("apigx breaker: resolve recovery baseline failed: " + baselineErr.Error())
		return false
	}
	view, err := apigxmodel.ResolveQualityViewSince(db, offeringID, apigxmodel.QualityWindowSixHour, nowTs, baseline)
	if err != nil {
		common.SysError("apigx breaker: resolve quality view failed: " + err.Error())
		return false
	}
	verdict := routing.EvaluateBreaker(view, nowTs, routing.DefaultBreakerConfig())
	open := verdict.State == routing.BreakerOpen
	// **缓存分级（这是端到端回扫抓到的真问题）**：
	//
	// 早先"打开/未打开"都缓存 30 秒。但"未打开"是**随时可能被下一个请求推翻**的结论——
	// 实测：第一次判定时样本还没攒够（`insufficient_attempts(0<5)`）→ 记为未打开并缓存 30 秒，
	// 而失败爆发期与"改道后"阶段**恰好都落在这 30 秒里** ⇒ 熔断在它本该抓住的那一刻**完全失明**
	// （表现为请求间歇性 500、坏上游样本持续增长）。
	//
	// 因此：**"打开"可以缓存较久**（它背后有冷却期兜底，不怕多拦一会儿），
	// **"未打开"只缓存极短时间**，让失败一旦攒够就能立刻生效。
	ttl := breakerCacheTTL
	if !open {
		ttl = breakerClosedCacheTTL
		// **刚发生过上游失败的供给几乎立刻重新判定**：它可能正在跨越阈值。
		// 否则"失败攒够的那一刻"与"缓存过期的那一刻"之间，熔断对整段爆发**失明**
		// （端到端实测：去掉这条时，熔断打开后仍有 2/6 请求打到坏上游）。
		// 健康供给不受影响，照旧走较长的缓存，避免每请求都查一次统计。
		if view.LatestUpstreamFailureTs > 0 && nowTs-view.LatestUpstreamFailureTs < breakerRecentFailureSeconds {
			ttl = breakerRecentFailureCacheTTL
		}
	}
	breakerCacheMu.Lock()
	breakerCache[offeringID] = cachedBreaker{open: open, expiresAt: now.Add(ttl)}
	breakerCacheMu.Unlock()
	return open
}

// syncBreakerMarks 把"熔断打开"同步为底座渠道上的**不可用标记**。
//
// # 为什么不能只返回错误让底座重试
//
// 端到端验收证明：底座的重试是**随机重选**，而 `use_channel` 只用于日志、
// **并不排除已经试过的渠道**（`controller/relay.go`）。因此"拒绝了再重试"有约一半概率
// 又回到同一条坏上游，重试耗尽即 500 —— 也就是说，**拒绝不足以实现改道**。
//
// 要做到真改道，必须让底座**根本不选中它**：复用底座既有的"跳过"机制——
// 把该渠道的多 Key 条目全部标为不可用，`GetNextEnabledKey` 会跳过它们；
// 全部不可用时底座会明确返回"no enabled keys"，于是它去选别的渠道。
//
// # 恢复
//
// 熔断不再打开时调用本函数即可**回收熔断来源**，不碰底座自己的禁用原因，
// 也不碰容量饱和来源；兼容单值表由 ChannelInfo 重新合并。
func syncBreakerMarks(db *gorm.DB, channelID int, offeringID int64, open bool) {
	// Recovery is deliberately background-only. Once a breaker has removed a
	// channel, user traffic cannot safely act as the half-open probe and must
	// not restore the channel merely because old failures aged out.
	if !open {
		return
	}
	probeSupported, probeErr := apigxmodel.CanActivelyProbeOffering(db, offeringID)
	if probeErr != nil {
		common.SysError("apigx breaker: resolve active probe protocol failed: " + probeErr.Error())
		return
	}
	if !probeSupported {
		// The current request is still rejected with a retryable error, so the
		// native cross-channel path can move it elsewhere. Do not persistently
		// remove a source that the background worker cannot safely verify and
		// restore (audio uploads/binary TTS and asynchronous video jobs).
		return
	}
	var channel basemodel.Channel
	if err := db.Where("id = ?", channelID).First(&channel).Error; err != nil {
		return
	}
	if err := apigxmodel.OpenBreakerProbe(db, offeringID, int64(channelID), common.GetTimestamp(), int64(breakerCooldown.Seconds())); err != nil {
		// Do not permanently remove a channel unless recovery ownership is
		// durable. The current request still fails and the native retry path can
		// select another channel.
		common.SysError("apigx breaker: persist recovery state failed: " + err.Error())
		return
	}

	// 复用 ChannelInfo 的来源合并：熔断、容量和底座健康分别持有标记，
	// 恢复某个来源后重新生成兼容单值表。
	keyCount := channel.ChannelInfo.MultiKeySize
	if channel.ChannelInfo.IsMultiKey && keyCount > 0 {
		all := make([]int, 0, keyCount)
		for index := range keyCount {
			all = append(all, index)
		}
		channel.ChannelInfo.ReplaceMultiKeyStatusMarks(apigxmodel.StatusBreakerOpen, all)
		if err := db.Model(&basemodel.Channel{}).Where("id = ?", channelID).
			Update("channel_info", channel.ChannelInfo).Error; err != nil {
			common.SysError("apigx breaker: update channel marks failed: " + err.Error())
			return
		}
	}

	// **真正让底座不选中它**（修法①）：标记只让"选中后立刻失败"，不足以改道；
	// 权重又因为底座的 +10 平滑而无法表达"接近零"（两者都被实测否掉）。
	// 因此这里调用底座**自己的**渠道状态入口——它会同步更新能力索引，
	// 于是该渠道在同模型下**根本不出现在候选里**，重试自然落到别的渠道。
	// ⚠️ 不能直接用 `basemodel.UpdateChannelStatus`：我们的分享渠道是**多 Key 渠道**，
	// 而它对多 Key 渠道会走 `handlerMultiKeyUpdate`——传入空 key 时找不到对应索引，
	// **不会禁用整条渠道**（实测：调用后排除了个寂寞，坏上游照样被选中）。
	// 因此直接做两件等价的事：置渠道状态 + 关掉它的**能力索引**（后者才是选择逻辑真正看的）。
	if err := db.Model(&basemodel.Channel{}).Where("id = ?", channelID).
		Update("status", common.ChannelStatusAutoDisabled).Error; err != nil {
		common.SysError("apigx breaker: disable channel status failed: " + err.Error())
	}
	if err := basemodel.UpdateAbilityStatus(channelID, false); err != nil {
		common.SysError("apigx breaker: disable ability failed: " + err.Error())
	}

	// 必须刷新底座缓存：只改库不会被选择逻辑看到（阶段 1 已经踩过同一个坑）
	if refreshed, err := basemodel.GetChannelById(channelID, true); err == nil && refreshed != nil {
		basemodel.CacheUpdateChannel(refreshed)
	}
	// 权重写在渠道行上，但选择逻辑读的是能力索引里的权重 → 必须重建（与投影同一处理）
	if basemodel.DB != nil {
		basemodel.InitChannelCache()
	}
}

// PrepareBreakerProbeChannel returns an in-memory channel copy whose breaker
// marks are removed. Other key status sources remain intact, so a probe never
// revives credentials disabled by authentication or capacity handling.
func PrepareBreakerProbeChannel(channel *basemodel.Channel) *basemodel.Channel {
	if channel == nil {
		return nil
	}
	probe := *channel
	probe.ChannelInfo = cloneBreakerProbeChannelInfo(channel.ChannelInfo)
	probe.ChannelInfo.ReplaceMultiKeyStatusMarks(apigxmodel.StatusBreakerOpen, nil)
	return &probe
}

func cloneBreakerProbeChannelInfo(source basemodel.ChannelInfo) basemodel.ChannelInfo {
	cloned := source
	cloned.MultiKeyStableIDs = slices.Clone(source.MultiKeyStableIDs)
	cloned.MultiKeyWeights = slices.Clone(source.MultiKeyWeights)
	cloned.MultiKeyStatusList = maps.Clone(source.MultiKeyStatusList)
	cloned.MultiKeyDisabledReason = maps.Clone(source.MultiKeyDisabledReason)
	cloned.MultiKeyDisabledTime = maps.Clone(source.MultiKeyDisabledTime)
	cloned.MultiKeyStatusByStableID = maps.Clone(source.MultiKeyStatusByStableID)
	cloned.MultiKeyDisabledReasonByStableID = maps.Clone(source.MultiKeyDisabledReasonByStableID)
	cloned.MultiKeyDisabledTimeByStableID = maps.Clone(source.MultiKeyDisabledTimeByStableID)
	cloned.MultiKeyStatusSources = cloneBreakerProbeStatusSources(source.MultiKeyStatusSources)
	cloned.MultiKeyStatusSourcesByStableID = cloneBreakerProbeStableStatusSources(source.MultiKeyStatusSourcesByStableID)
	return cloned
}

func cloneBreakerProbeStatusSources(source map[int]map[string]int) map[int]map[string]int {
	cloned := maps.Clone(source)
	for index, statuses := range cloned {
		cloned[index] = maps.Clone(statuses)
	}
	return cloned
}

func cloneBreakerProbeStableStatusSources(source map[string]map[string]int) map[string]map[string]int {
	cloned := maps.Clone(source)
	for stableID, statuses := range cloned {
		cloned[stableID] = maps.Clone(statuses)
	}
	return cloned
}

// RestoreBreakerChannel removes only APIGX's breaker mark and returns the
// channel to the native selector after all offering-level breaker states on the
// channel are ready. Authentication and capacity marks are preserved.
func RestoreBreakerChannel(db *gorm.DB, state apigxmodel.BreakerProbeState) error {
	mayRestore, err := apigxmodel.BreakerChannelMayRestore(db, state.BaseChannelID)
	if err != nil || !mayRestore {
		return err
	}
	var channel basemodel.Channel
	if err := db.Where("id = ?", state.BaseChannelID).First(&channel).Error; err != nil {
		return err
	}
	if channel.ChannelInfo.IsMultiKey {
		channel.ChannelInfo.ReplaceMultiKeyStatusMarks(apigxmodel.StatusBreakerOpen, nil)
		if err := db.Model(&basemodel.Channel{}).Where("id = ?", state.BaseChannelID).
			Update("channel_info", channel.ChannelInfo).Error; err != nil {
			return err
		}
	}

	canEnable := state.OwnsChannelDisable && channel.Status == common.ChannelStatusAutoDisabled
	if canEnable && channel.ChannelInfo.IsMultiKey {
		canEnable = false
		for index := 0; index < channel.ChannelInfo.MultiKeySize; index++ {
			if channel.ChannelInfo.MultiKeyStatusAt(index) == common.ChannelStatusEnabled {
				canEnable = true
				break
			}
		}
	}
	if canEnable {
		if err := db.Model(&basemodel.Channel{}).Where("id = ? AND status = ?", state.BaseChannelID, common.ChannelStatusAutoDisabled).
			Update("status", common.ChannelStatusEnabled).Error; err != nil {
			return err
		}
		if err := basemodel.UpdateAbilityStatus(int(state.BaseChannelID), true); err != nil {
			return err
		}
	}
	if refreshed, err := basemodel.GetChannelById(int(state.BaseChannelID), true); err == nil && refreshed != nil {
		basemodel.CacheUpdateChannel(refreshed)
	}
	if basemodel.DB != nil {
		basemodel.InitChannelCache()
	}
	InvalidateBreakerCache()
	return nil
}
