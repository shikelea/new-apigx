package relayhook

import (
	"context"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/apigx/capacity"
	"github.com/QuantumNous/new-api/apigx/channelpool"
	apigxmodel "github.com/QuantumNous/new-api/apigx/model"
	"github.com/QuantumNous/new-api/common"
	basemodel "github.com/QuantumNous/new-api/model"
	"gorm.io/gorm"
)

// Key 级"剩余容量"的运行时刷新（T2.6 最后一块）。
//
// # 与"容量准入"的分工
//
// 容量准入（`AcquireForAttempt`）解决"这次请求能不能占用"；本函数解决
// **"底座下次选 Key 时要不要跳过它"**。两者都要，原因很实际：只有准入的话，
// 底座仍会把请求交给已满载的 Key，然后由准入拒绝——表现为"明明还有另一把可用的 Key，
// 请求却被拒"（用户在容量充足时看到 429）。
//
// 实现直接复用已有机制：`SaturatedKeyIndexes` 判定 + `MergeKeyStatusList` 合并
// （只回收我们自己写的格子），底座的 `GetNextEnabledKey` 会跳过被标记的索引。
//
// # 为什么要节流
//
// 判定要按 Key 读一次 Redis 计数，属每请求路径；因此按渠道做**短间隔节流**（默认 10 秒）。
// 容量状态按秒级变化，10 秒滞后可接受，而省下的是每请求 N 次计数读取。

// keySaturationRefreshInterval 是同一渠道两次刷新之间的最小间隔。
var keySaturationRefreshInterval = 10 * time.Second

var (
	keySaturationMu   sync.Mutex
	keySaturationLast = map[int]time.Time{}
)

// ResetKeySaturationThrottleForTest 清空节流记录（测试用）。
func ResetKeySaturationThrottleForTest() {
	keySaturationMu.Lock()
	keySaturationLast = map[int]time.Time{}
	keySaturationMu.Unlock()
}

// RefreshKeySaturation 依据各把 Key 的当前占用，刷新底座渠道状态表里的"满载"标记。
//
// 读不到计数时**不标记**（与熔断的取舍一致）：统计读取失败不代表 Key 满了，
// 因读取失败把可用 Key 摘掉，比"偶尔多用一把"更糟。
func RefreshKeySaturation(db *gorm.DB, baseChannelID int) {
	if db == nil || baseChannelID <= 0 {
		return
	}
	if !throttleAllows(baseChannelID, time.Now()) {
		return
	}

	shareChannelID, ok := channelpool.ShareChannelIDByBase(db, baseChannelID)
	if !ok {
		return // 平台自建渠道不参与
	}
	loads := keyLoadsForChannel(db, baseChannelID, shareChannelID)
	if len(loads) == 0 {
		return // 没有配置任何 Key 级上限：无"满载"可言
	}
	applyKeySaturationMarks(db, baseChannelID, channelpool.SaturatedKeyIndexes(loads))
}

// throttleAllows 判断该渠道是否到了可以再次刷新的时间。
func throttleAllows(baseChannelID int, now time.Time) bool {
	keySaturationMu.Lock()
	defer keySaturationMu.Unlock()
	if last, ok := keySaturationLast[baseChannelID]; ok && now.Sub(last) < keySaturationRefreshInterval {
		return false
	}
	keySaturationLast[baseChannelID] = now
	return true
}

// keyLoadsForChannel 组装该渠道各 Key 索引的占用与上限。
//
// 上限取自**每把 Key 当前生效的策略链**（与准入用的是同一套解析），
// 保证"标记"与"准入"不会出现两套口径。
func keyLoadsForChannel(db *gorm.DB, baseChannelID int, shareChannelID int64) []channelpool.KeyLoad {
	if common.RDB == nil {
		return nil
	}
	projection, err := channelpool.MapBaseKeyIndexesCached(db, shareChannelID)
	if err != nil || projection == nil || projection.KeyCount <= 0 {
		return nil
	}
	cfg := getConfig()
	if cfg.UpstreamKeyLimit <= 0 && cfg.UpstreamAccountLimit <= 0 {
		// 没有配置任何 Key/账户级上限：不必逐 Key 读计数（省掉每请求 N 次 Redis 往返）
		return nil
	}

	ctrl := capacity.New(common.RDB)
	limits := channelpool.KeyCapacityLimits{
		UpstreamKeyLimit:     cfg.UpstreamKeyLimit,
		UpstreamAccountLimit: cfg.UpstreamAccountLimit,
	}
	loads := make([]channelpool.KeyLoad, 0, projection.KeyCount)
	for index := range projection.KeyCount {
		dims := channelpool.BuildKeyCapacityDimensions(db, baseChannelID, index, limits)
		if len(dims) == 0 {
			continue
		}
		used, usageErr := ctrl.Usage(context.Background(), dims[0].Key)
		if usageErr != nil {
			continue // 读不到就不标记（见函数注释）
		}
		loads = append(loads, channelpool.KeyLoad{Index: index, InUse: used, Limit: dims[0].Limit})
	}
	return loads
}

// applyKeySaturationMarks 把饱和索引并入底座状态表（只回收我们自己的标记）。
func applyKeySaturationMarks(db *gorm.DB, baseChannelID int, saturated []int) {
	var channel basemodel.Channel
	if err := db.Where("id = ?", baseChannelID).First(&channel).Error; err != nil {
		return
	}
	if !channel.ChannelInfo.IsMultiKey || channel.ChannelInfo.MultiKeySize <= 0 {
		return
	}
	current := channel.ChannelInfo.MultiKeyStatusList
	ours := make([]int, 0, len(current))
	for index, status := range current {
		if status == apigxmodel.StatusCapacitySaturated {
			ours = append(ours, index)
		}
	}
	next := channelpool.MergeKeyStatusList(current, ours, saturated, apigxmodel.StatusCapacitySaturated)
	if equalStatusLists(current, next) {
		return
	}
	channel.ChannelInfo.MultiKeyStatusList = next
	if err := db.Model(&basemodel.Channel{}).Where("id = ?", baseChannelID).
		Update("channel_info", channel.ChannelInfo).Error; err != nil {
		common.SysError("apigx capacity: update key saturation marks failed: " + err.Error())
		return
	}
	if refreshed, err := basemodel.GetChannelById(baseChannelID, true); err == nil && refreshed != nil {
		basemodel.CacheUpdateChannel(refreshed)
	}
}

// equalStatusLists 判断两份状态表是否等价（nil 与空视为相同）。
func equalStatusLists(a, b map[int]int) bool {
	if len(a) != len(b) {
		return false
	}
	for index, status := range a {
		if other, ok := b[index]; !ok || other != status {
			return false
		}
	}
	return true
}
