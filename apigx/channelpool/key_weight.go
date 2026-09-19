package channelpool

import (
	"sort"

	apigxmodel "github.com/QuantumNous/new-api/apigx/model"
)

// Key 加权与剩余容量策略。
//
// 阶段 C 起，每把真实 Key 只投影一个槽位；稳定 ID 和权重随 ChannelInfo 一同写入，
// `model.Channel.GetNextEnabledKey` 直接执行加权轮询/随机。这里仅负责限制输入权重并提供容量纯函数。

// maxKeyWeight 是单个 Key 的最大选择权重，避免异常配置垄断选择窗口。
const maxKeyWeight = 16

// effectiveWeight 把权重归一为至少一个份额，并限制单 Key 最大份额。
func effectiveWeight(weight int64) int64 {
	if weight <= 0 {
		return 1
	}
	if weight > maxKeyWeight {
		return maxKeyWeight
	}
	return weight
}

// KeyLoad 是一把 Key 的容量占用情况。
type KeyLoad struct {
	Index int
	// InUse / Limit：Limit <= 0 表示未配置上限（不施加约束）。
	InUse int64
	Limit int64
}

// SaturatedKeyIndexes 返回已满（或超限）的 Key 索引。
//
// 这些索引应当被标记为"非启用"，让底座的 `GetNextEnabledKey` 跳过它们——
// 当合格 Key 全部被跳过时，底座会明确返回"no enabled keys"，
// 调用方据此把渠道视为不可选（计划书验收：「无合格 Key 时渠道不可选」）。
func SaturatedKeyIndexes(keys []KeyLoad) []int {
	saturated := make([]int, 0, len(keys))
	for _, key := range keys {
		if key.Limit <= 0 {
			continue // 未配置上限 ≠ 已满
		}
		if key.InUse >= key.Limit {
			saturated = append(saturated, key.Index)
		}
	}
	sort.Ints(saturated)
	return saturated
}

// MergeKeyStatusList 把"容量饱和"的索引并入底座的多 Key 状态表。
//
// # 为什么必须传入上一轮的标记
//
// 底座的状态表同时承载**它自己的**禁用标记（例如鉴权失败自动禁用）。
// 若我们直接整表覆盖或整表清空，就会把底座自己的标记一并抹掉
// （那等于把一把鉴权失败的 Key 重新放回池子）。
//
// 因此这里采用"只回收自己写过的格子"的做法：调用方把上一轮我们标记过的索引传进来，
// 本次不再饱和的格子会被**删除**（恢复"缺失即启用"），底座自己的标记原样保留。
func MergeKeyStatusList(current map[int]int, previousOurMarks []int, saturated []int, markValue int) map[int]int {
	merged := make(map[int]int, len(current)+len(saturated))
	for index, status := range current {
		merged[index] = status
	}
	// 回收上一轮我们自己的标记（只在值仍等于我们的标记值时删除）
	for _, index := range previousOurMarks {
		if status, ok := merged[index]; ok && status == markValue {
			delete(merged, index)
		}
	}
	for _, index := range saturated {
		merged[index] = markValue
	}
	if len(merged) == 0 {
		return nil
	}
	return merged
}

// KeySaturationStatusValue 是我们为"容量饱和"写入底座状态表的值。
//
// 只要**不等于** `common.ChannelStatusEnabled`，底座就会跳过该 Key；
// 用一个与底座自身禁用原因不同的值，是为了让 `MergeKeyStatusList` 能安全地只回收自己的标记。
const KeySaturationStatusValue = apigxmodel.StatusCapacitySaturated
