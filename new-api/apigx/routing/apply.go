package routing

import (
	"fmt"

	apigxmodel "github.com/QuantumNous/new-api/apigx/model"
	"github.com/QuantumNous/new-api/common"
	basemodel "github.com/QuantumNous/new-api/model"
	"gorm.io/gorm"
)

// 把路由决策**投影到底座渠道**（T2.7 收尾）。
//
// # 为什么投影 weight 而不是 priority
//
// 底座的选渠道语义（`model/ability.go` + `model/channel_cache.go`）是：
//   - 先按 **priority 降序**取最高的一层；
//   - **同一 priority 内按 weight 加权随机**（且带 +10 平滑，weight 为 0 也有机会）。
//
// 计划书明确要求：「分享者自设的渠道优先级**不能覆盖**跨分享者的质量排序；
// 默认所有渠道同级，平台允许的优先级只在同等质量时作为次序参考。
// **渠道权重仅用于平台允许范围内相近评分的流量分散**」。
//
// 所以默认只投影 **weight**：既让底座按我们的评分分散流量，
// 又不会把别的渠道（或平台管理员）配好的 priority 压掉。
// 需要严格分层（第一名独占）时才用 `ApplyOptions.AllowPriorityProjection` 显式开启，
// 且即便如此也只动**我们自己的**渠道。

// ApplyOptions 控制投影行为。
type ApplyOptions struct {
	// AllowPriorityProjection 允许把排名投影为 priority 分层（默认关闭）。
	//
	// 关闭是默认值，因为 priority 是全局语义：擅自抬高会让我们的渠道压过
	// 同模型下平台或他人配置的优先级。
	AllowPriorityProjection bool
	// PriorityBase 是开启分层时的基准优先级（排名 0 得到 PriorityBase，依次递减）。
	PriorityBase int64
	// MaxWeight 是最高分候选得到的权重；其余按分数比例，下限为 1。
	MaxWeight int
}

// DefaultApplyOptions 返回默认投影配置：只动 weight，不动 priority。
func DefaultApplyOptions() ApplyOptions {
	return ApplyOptions{MaxWeight: 100}
}

// ApplyResult 报告投影结果。
type ApplyResult struct {
	// Updated 是被更新的底座渠道数量。
	Updated int
	// Skipped 记录未投影的候选及原因（例如渠道不属于我们）。
	Skipped []Exclusion
}

// ApplyRankingToBaseChannels 把决策的排名投影到底座渠道的权重（可选优先级）。
//
// 只影响**该决策里出现的、且属于我们分享渠道的**底座渠道；其它渠道一律不动。
// 投影后刷新底座缓存（渠道缓存 + 能力索引），否则底座仍按旧权重选渠道。
func ApplyRankingToBaseChannels(db *gorm.DB, decision Decision, group string, opts ApplyOptions) (ApplyResult, error) {
	result := ApplyResult{}
	if db == nil {
		return result, fmt.Errorf("%w: db is required", apigxmodel.ErrInvalidArgument)
	}
	if len(decision.Ranked) == 0 {
		return result, nil
	}
	if opts.MaxWeight <= 0 {
		opts.MaxWeight = DefaultApplyOptions().MaxWeight
	}
	if opts.PriorityBase == 0 {
		opts.PriorityBase = 1000
	}

	// 权重**按名次**分配，而不是按分数：分数在各策略下量纲完全不同
	// （可靠性是百万分比、延迟是毫秒、吞吐是 token/s），直接当权重会得出
	// 数量级离谱的值（早期实现就因此算出过 99000000 这种权重）。
	// 按名次衰减既与量纲无关，又保持单调；**同分共享同一 Rank ⇒ 同权重**，
	// 让底座的加权随机在"相近评分"的组内分散流量（计划书对权重的定位）。
	weightByRank := map[int]int{}
	for _, ranked := range decision.Ranked {
		if _, seen := weightByRank[ranked.Rank]; seen {
			continue
		}
		weight := opts.MaxWeight
		for i := 0; i < ranked.Rank; i++ {
			weight /= 2
		}
		if weight < 1 {
			weight = 1
		}
		weightByRank[ranked.Rank] = weight
	}

	touchedChannels := make([]int, 0, len(decision.Ranked))
	for _, ranked := range decision.Ranked {
		shareChannelID := ranked.Candidate.ShareChannelID
		if shareChannelID <= 0 {
			result.Skipped = append(result.Skipped, Exclusion{ranked.Candidate.OfferingID, "no_share_channel"})
			continue
		}
		var shareChannel apigxmodel.Channel
		if err := db.Where("id = ?", shareChannelID).First(&shareChannel).Error; err != nil {
			result.Skipped = append(result.Skipped, Exclusion{ranked.Candidate.OfferingID, "share_channel_missing"})
			continue
		}
		if shareChannel.BaseChannelID <= 0 {
			result.Skipped = append(result.Skipped, Exclusion{ranked.Candidate.OfferingID, "no_base_channel"})
			continue
		}

		updates := map[string]any{"weight": uint(weightByRank[ranked.Rank])}
		if opts.AllowPriorityProjection {
			// 排名越靠前优先级越高（底座取 priority 最大的一层）
			priority := opts.PriorityBase - int64(ranked.Rank)
			if priority < 1 {
				priority = 1
			}
			updates["priority"] = priority
		}
		if err := db.Model(&basemodel.Channel{}).Where("id = ?", shareChannel.BaseChannelID).
			Updates(updates).Error; err != nil {
			return result, err
		}
		result.Updated++
		touchedChannels = append(touchedChannels, int(shareChannel.BaseChannelID))
	}

	// 刷新底座缓存：只改数据库不会被选择逻辑看到（阶段 1 踩过同一个坑）。
	//
	// 守卫：底座缓存函数依赖**底座的全局 DB**（生产环境与 `apigxmodel.DefaultDB()` 是同一句柄，
	// 但测试或 CLI 场景可能尚未初始化）。此时跳过刷新而不是崩掉——
	// 权重已经写进库里，缓存会在下次初始化时读到。
	if basemodel.DB == nil {
		common.SysLog("channelpool: skip base cache refresh because base DB is not initialized")
		return result, nil
	}
	for _, channelID := range touchedChannels {
		if channel, err := basemodel.GetChannelById(channelID, true); err == nil && channel != nil {
			basemodel.CacheUpdateChannel(channel)
		}
	}
	if len(touchedChannels) > 0 {
		basemodel.InitChannelCache()
	}
	return result, nil
}

// WeightedRankingForGroup 是给调用方的便捷入口：
// 按策略算出排名并投影到底座渠道（group 为该请求使用的用户分组）。
func WeightedRankingForGroup(db *gorm.DB, strategy Strategy, candidates []Candidate, group string,
	decideOpts Options, applyOpts ApplyOptions) (Decision, ApplyResult, error) {
	decision, err := Decide(strategy, candidates, decideOpts)
	if err != nil {
		return decision, ApplyResult{}, err
	}
	result, err := ApplyRankingToBaseChannels(db, decision, group, applyOpts)
	return decision, result, err
}
