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

const breakerCacheTTL = 30 * time.Second

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

// breakerOpenedAt 记录各渠道**本次**被我们打开的时刻（进程内即可：
// 重启后 Map 为空，等价于"冷却已过"，于是会按判定恢复，不会把渠道永久锁死）。
var breakerOpenedAt sync.Map // baseChannelID -> time.Time

// ResetBreakerCooldownForTest 清空冷却记录（测试用）。
func ResetBreakerCooldownForTest() {
	breakerOpenedAt.Range(func(key, _ any) bool { breakerOpenedAt.Delete(key); return true })
}

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

	open := breakerOpenCached(db, offeringID)
	// 无论开还是关，都同步一次底座渠道的"可跳过标记"：
	// 打开 → 标记；已恢复 → 回收我们自己的标记。
	syncBreakerMarks(db, channelID, open)
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
// 熔断不再打开时调用本函数即可**回收我们自己的标记**（只删值等于 StatusBreakerOpen 的格子，
// 不碰底座自己的禁用原因，也不碰容量饱和标记）。
func syncBreakerMarks(db *gorm.DB, channelID int, open bool) {
	var channel basemodel.Channel
	if err := db.Where("id = ?", channelID).First(&channel).Error; err != nil {
		return
	}
	indices := channel.ChannelInfo.MultiKeyStatusList
	keyCount := channel.ChannelInfo.MultiKeySize
	if !channel.ChannelInfo.IsMultiKey || keyCount <= 0 {
		return // 非多 Key 渠道：没有可标记的条目（这类渠道的改道由底座自身的失败处理负责）
	}

	// 复用 `channelpool.MergeKeyStatusList`：那份"只回收自己写过的格子、保留别人标记"
	// 的逻辑已经有纯函数级用例（T2.6），在这里再写一遍等于把同一件事实现两次，
	// 而且第二份不会有测试覆盖。
	var next map[int]int
	if open {
		all := make([]int, 0, keyCount)
		for index := range keyCount {
			all = append(all, index)
		}
		next = channelpool.MergeKeyStatusList(indices, nil, all, apigxmodel.StatusBreakerOpen)
	} else {
		// 恢复：把"上一轮我们标记过的索引"交给它回收（只删值等于我们标记的格子）
		ours := make([]int, 0, len(indices))
		for index, status := range indices {
			if status == apigxmodel.StatusBreakerOpen {
				ours = append(ours, index)
			}
		}
		if len(ours) == 0 {
			return
		}
		next = channelpool.MergeKeyStatusList(indices, ours, nil, apigxmodel.StatusBreakerOpen)
	}
	channel.ChannelInfo.MultiKeyStatusList = next

	// **刻意不动权重**：曾经的方案是"把熔断渠道的权重压到接近零"，但实测与底座语义相冲突——
	// 底座对权重有 **+10 平滑**，权重 1 与 1 之间的分配是 11:11（≈50%），
	// 也就是说**权重根本无法表达"接近零"**（要让它接近零，同层其他渠道的权重都得远大于 10，
	// 而我们无权假设/改动别人的权重）。更危险的是"恢复"时无从判断原值是不是平台设置的。
	// 因此这条被证据否掉，改走"让渠道在底座眼里不可选"（见下面的状态处理）。
	if err := db.Model(&basemodel.Channel{}).Where("id = ?", channelID).
		Update("channel_info", channel.ChannelInfo).Error; err != nil {
		common.SysError("apigx breaker: update channel marks failed: " + err.Error())
		return
	}
	if open {
		if _, loaded := breakerOpenedAt.Load(channelID); !loaded {
			breakerOpenedAt.Store(channelID, time.Now())
		}
	} else if openedAt, loaded := breakerOpenedAt.Load(channelID); loaded {
		// 冷却未过：**保持不可用**，不因为"窗口里没有新失败"就恢复（那会造成抖动）
		if since, ok := openedAt.(time.Time); ok && time.Since(since) < breakerCooldown {
			return
		}
		breakerOpenedAt.Delete(channelID)
	}

	// **真正让底座不选中它**（修法①）：标记只让"选中后立刻失败"，不足以改道；
	// 权重又因为底座的 +10 平滑而无法表达"接近零"（两者都被实测否掉）。
	// 因此这里调用底座**自己的**渠道状态入口——它会同步更新能力索引，
	// 于是该渠道在同模型下**根本不出现在候选里**，重试自然落到别的渠道。
	// ⚠️ 不能直接用 `basemodel.UpdateChannelStatus`：我们的分享渠道是**多 Key 渠道**，
	// 而它对多 Key 渠道会走 `handlerMultiKeyUpdate`——传入空 key 时找不到对应索引，
	// **不会禁用整条渠道**（实测：调用后排除了个寂寞，坏上游照样被选中）。
	// 因此直接做两件等价的事：置渠道状态 + 关掉它的**能力索引**（后者才是选择逻辑真正看的）。
	if open {
		if err := db.Model(&basemodel.Channel{}).Where("id = ?", channelID).
			Update("status", common.ChannelStatusAutoDisabled).Error; err != nil {
			common.SysError("apigx breaker: disable channel status failed: " + err.Error())
		}
		if err := basemodel.UpdateAbilityStatus(channelID, false); err != nil {
			common.SysError("apigx breaker: disable ability failed: " + err.Error())
		}
	} else {
		// 恢复：**只还原我们自己禁用的**（判据 = 我们的多 Key 标记仍在 + 渠道处于自动禁用），
		// 避免把底座因鉴权失败等原因禁用的渠道顺手恢复。
		var now basemodel.Channel
		if err := db.Where("id = ?", channelID).First(&now).Error; err == nil {
			if now.Status == common.ChannelStatusAutoDisabled && hasOurBreakerMark(now.ChannelInfo.MultiKeyStatusList) {
				if err := db.Model(&basemodel.Channel{}).Where("id = ?", channelID).
					Update("status", common.ChannelStatusEnabled).Error; err != nil {
					common.SysError("apigx breaker: restore channel status failed: " + err.Error())
				}
				if err := basemodel.UpdateAbilityStatus(channelID, true); err != nil {
					common.SysError("apigx breaker: restore ability failed: " + err.Error())
				}
			}
		}
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

// hasOurBreakerMark 判断状态表里是否还有我们写的熔断标记。
func hasOurBreakerMark(statusList map[int]int) bool {
	for _, status := range statusList {
		if status == apigxmodel.StatusBreakerOpen {
			return true
		}
	}
	return false
}
