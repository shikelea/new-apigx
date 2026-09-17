// Package routing 实现 APIGX 的路由决策（T2.7）。
//
// # 五种策略（计划书 §9.3，一一对应，不自行增补）
//
//	指定来源  —— 精确选中某条供给（阶段 1 已实现，本层只保留枚举与解释）
//	延迟优先  —— 流式看首个有效输出延迟，其他请求参考同类型完成时间
//	吞吐优先  —— 有效输出 token/s（非文本按能力用相应处理速度）
//	可靠性优先 —— 最近 6 小时成功率、样本量、置信下界与近期故障
//	综合自动  —— 排除饱和候选后，综合可靠性/延迟/吞吐/剩余容量
//
// # 三条不可协商的约束（都来自计划书，且都能用测试固定）
//
//  1. **硬约束先于评分**：容量已满的候选必须**先被排除**，质量再高也不能入选
//     （§9.2「渠道 A 首字更快，但已占满 4/4 并发，自动路由必须选择有余量的 B」）。
//  2. **数据不足不强行排名**：可靠性排序要求 ≥20 个合格样本，否则进入**冷启动候选**；
//     延迟/吞吐没有可比数据时不参与该维度（而不是当成 0 或最好）。
//  3. **分享者自设优先级不能覆盖跨分享者的质量排序**：优先级只在**同等质量**时作次序参考。
//
// 本包只做**决策**，不直接改底座选渠道：决策结果由调用方投影到底座渠道的
// priority/weight 上，继续复用底座既有的选择与重试机制（二开第一原则）。
package routing

import (
	"fmt"
	"sort"

	apigxmodel "github.com/QuantumNous/new-api/apigx/model"
)

// Strategy 是五种路由方式。
type Strategy int

const (
	// StrategyPinned 指定来源：精确选中某条供给（阶段 1 已实现）。
	StrategyPinned Strategy = 0
	// StrategyLatency 延迟优先。
	StrategyLatency Strategy = 1
	// StrategyThroughput 吞吐优先。
	StrategyThroughput Strategy = 2
	// StrategyReliability 可靠性优先。
	StrategyReliability Strategy = 3
	// StrategyAuto 综合自动。
	StrategyAuto Strategy = 4
)

// StrategyName 返回策略的稳定标识（用于可解释结果与日志）。
func StrategyName(strategy Strategy) string {
	switch strategy {
	case StrategyPinned:
		return "pinned"
	case StrategyLatency:
		return "latency"
	case StrategyThroughput:
		return "throughput"
	case StrategyReliability:
		return "reliability"
	case StrategyAuto:
		return "auto"
	default:
		return "unknown"
	}
}

// 排除原因（用于"可解释的路由结果"，T2.9）。
const (
	ExcludeSaturated    = "saturated"     // 容量已满
	ExcludeNoQuality    = "no_quality"    // 没有任何质量样本
	ExcludeNoMetric     = "no_metric"     // 该策略需要的指标缺失（不强行排名）
	ExcludePriceUnknown = "price_unknown" // 价格未知：不参与需要成本的比较
	ExcludeNotAllowed   = "not_allowed"   // 调用方判定为不在授权范围内
	// ExcludeBreakerOpen 熔断打开：上游持续失败且冷却未过，**临时**摘出候选。
	ExcludeBreakerOpen = "breaker_open"
)

// Candidate 是一个参与排序的候选供给。
type Candidate struct {
	OfferingID     int64
	ShareChannelID int64
	OwnerUserID    int64

	// 价格：**未知用 PriceKnown=false 表达**，绝不能当成 0（否则免费模型会排到最前）。
	InputPriceMicro  int64
	OutputPriceMicro int64
	PriceKnown       bool

	// Quality 来自分钟桶的质量视图（含样本数、Wilson 下界、陈旧标记）。
	Quality apigxmodel.QualityView

	// 容量：CapacityLimit == 0 表示未配置上限（不施加约束）。
	CapacityLimit int64
	CapacityInUse int64

	// 吞吐观测（有效输出 token/s），ThroughputKnown=false 表示没有可比数据。
	OutputTokensPerSecond float64
	ThroughputKnown       bool

	// Priority 是分享者自设优先级：**只在同等质量时**作次序参考。
	Priority int
	// Allowed 由调用方先把授权/来源限制算完（硬限制先于评分）。
	Allowed bool
	// ExcludeReason 允许调用方给出**更具体**的排除原因（例如 BreakerOpen / 已下架）。
	// 留空时 `Decide` 用 ExcludeNotAllowed。具体原因会出现在可解释结果里——
	// 笼统的"未授权"无法回答用户"为什么我的来源没被用上"。
	ExcludeReason string
}

// RemainingCapacity 返回剩余容量（未配置上限时返回 0，由调用方按"不限"处理）。
func (c Candidate) RemainingCapacity() int64 {
	if c.CapacityLimit <= 0 {
		return 0
	}
	remaining := c.CapacityLimit - c.CapacityInUse
	if remaining < 0 {
		return 0
	}
	return remaining
}

// Saturated 报告该候选是否已满（硬约束：不得进入任何策略的候选集）。
func (c Candidate) Saturated() bool {
	return c.CapacityLimit > 0 && c.CapacityInUse >= c.CapacityLimit
}

// Options 是决策的可配置项（权重与探索比例都可调，属计划书明说的"配置项"）。
type Options struct {
	// 综合自动的权重（计划书初始值：可靠性 50% / 延迟 20% / 吞吐 20% / 容量余量 10%）。
	WeightReliability float64
	WeightLatency     float64
	WeightThroughput  float64
	WeightCapacity    float64
	// ColdStartSharePpm 是允许分配给冷启动候选的自动流量比例（百万分比，默认 5%）。
	ColdStartSharePpm int64
	// ColdStartRollPpm 是调用方传入的抽样值（0..1e6）；< ColdStartSharePpm 时允许走冷启动。
	// 由调用方提供随机数，使本层保持确定性、可测试。
	ColdStartRollPpm int64
}

// DefaultOptions 返回计划书给出的初始配置。
func DefaultOptions() Options {
	return Options{
		WeightReliability: 0.50,
		WeightLatency:     0.20,
		WeightThroughput:  0.20,
		WeightCapacity:    0.10,
		ColdStartSharePpm: 50_000, // 5%
		// **默认不探索**：抽样值取 1e6（超出 5% 窗口）意味着"不分配冷启动流量"。
		// 早期把它留成零值 0，而 0 < 50000 恒成立 → 每次都会走冷启动探索，
		// 把成熟候选的排序整个绕过（由单元测试发现）。运行时由调用方注入
		// [0,1e6) 的随机抽样值。
		ColdStartRollPpm: 1_000_000,
	}
}

// Exclusion 记录被排除的候选与原因。
type Exclusion struct {
	OfferingID int64
	Reason     string
}

// Ranked 是被排名的候选（同分共享同一 Rank，便于调用方按权重在组内分散流量）。
type Ranked struct {
	Candidate Candidate
	Rank      int
	Score     float64
	// Reasons 是该候选的得分构成说明（可解释路由结果）。
	Reasons []string
}

// Decision 是一次路由决策的结果。
type Decision struct {
	Strategy Strategy
	// Chosen 是最终选中的候选；nil 表示没有任何可用候选。
	Chosen *Candidate
	// Ranked 按 Rank 升序（0 为最优）；同分候共享 Rank。
	Ranked []Ranked
	// Excluded 记录被硬约束排除的候选。
	Excluded []Exclusion
	// ColdStart 表示本次选择来自冷启动候选（样本不足）。
	ColdStart bool
	// Reason 是一句话解释（对外可展示，不含内部标识）。
	Reason string
}

// Decide 按策略对候选排序并给出选择。
//
// 调用方必须**先把硬限制算完**（授权、允许模型、固定来源、满载等），
// 但本层仍会再排除"容量已满"与"未授权"的候选——硬约束不能只依赖调用方记得。
func Decide(strategy Strategy, candidates []Candidate, opts Options) (Decision, error) {
	decision := Decision{Strategy: strategy}
	if strategy == StrategyPinned {
		// 指定来源由阶段 1 的准入判定完成，这里只做说明
		decision.Reason = "pinned source: selection is exact, no ranking applied"
		return decision, nil
	}
	if strategy < StrategyPinned || strategy > StrategyAuto {
		return decision, fmt.Errorf("%w: unknown routing strategy %d", apigxmodel.ErrInvalidArgument, strategy)
	}
	if opts.WeightReliability == 0 && opts.WeightLatency == 0 && opts.WeightThroughput == 0 && opts.WeightCapacity == 0 {
		opts = DefaultOptions()
	}

	// ① 硬约束：满载与未授权先排除（质量再高也不能入选）
	eligible := make([]Candidate, 0, len(candidates))
	for _, candidate := range candidates {
		if !candidate.Allowed {
			reason := candidate.ExcludeReason
			if reason == "" {
				reason = ExcludeNotAllowed
			}
			decision.Excluded = append(decision.Excluded, Exclusion{candidate.OfferingID, reason})
			continue
		}
		if candidate.Saturated() {
			decision.Excluded = append(decision.Excluded, Exclusion{candidate.OfferingID, ExcludeSaturated})
			continue
		}
		eligible = append(eligible, candidate)
	}
	if len(eligible) == 0 {
		decision.Reason = "no eligible candidate after hard constraints"
		return decision, nil
	}

	// ② 可靠性维度需要足够样本：不足者进入冷启动候选
	mature := make([]Candidate, 0, len(eligible))
	cold := make([]Candidate, 0, len(eligible))
	for _, candidate := range eligible {
		if candidate.Quality.SampleCount == 0 {
			decision.Excluded = append(decision.Excluded, Exclusion{candidate.OfferingID, ExcludeNoQuality})
			cold = append(cold, candidate)
			continue
		}
		if candidate.Quality.Insufficient {
			cold = append(cold, candidate)
			continue
		}
		mature = append(mature, candidate)
	}

	if len(mature) == 0 {
		// 没有成熟候选：按剩余容量分配（不会因没有历史数据永久饿死新渠道），
		// 但仍受冷启动探索比例约束。
		return decideColdStart(strategy, cold, opts, decision), nil
	}

	ranked, err := rankCandidates(strategy, mature, opts)
	if err != nil {
		return decision, err
	}
	// 冷启动候选排在成熟候选之后，并且**只在抽样允许时**才可能被选中
	if len(cold) > 0 && opts.ColdStartSharePpm > 0 && opts.ColdStartRollPpm < opts.ColdStartSharePpm {
		coldRanked, _ := rankCandidates(StrategyAuto, cold, opts)
		if len(coldRanked) > 0 {
			decision.ColdStart = true
			decision.Chosen = &coldRanked[0].Candidate
			decision.Ranked = coldRanked
			decision.Reason = "cold-start exploration selected a candidate with insufficient samples"
			return decision, nil
		}
	}
	decision.Ranked = ranked
	if len(ranked) > 0 {
		decision.Chosen = &ranked[0].Candidate
		decision.Reason = fmt.Sprintf("strategy=%s selected highest-ranked eligible candidate", StrategyName(strategy))
	}
	return decision, nil
}

// decideColdStart 在没有成熟候选时，按剩余容量在冷启动候选里分配。
func decideColdStart(strategy Strategy, cold []Candidate, opts Options, decision Decision) Decision {
	if len(cold) == 0 {
		decision.Reason = "no candidate has quality samples"
		return decision
	}
	ranked, _ := rankCandidates(StrategyAuto, cold, opts)
	if len(ranked) == 0 {
		decision.Reason = "cold-start candidates lack comparable metrics"
		return decision
	}
	decision.ColdStart = true
	decision.Ranked = ranked
	decision.Chosen = &ranked[0].Candidate
	decision.Reason = "no mature candidate: cold-start allocation by remaining capacity (insufficient samples)"
	return decision
}

// rankCandidates 按策略打分并排名。
func rankCandidates(strategy Strategy, candidates []Candidate, opts Options) ([]Ranked, error) {
	switch strategy {
	case StrategyReliability:
		return rankByMetric(candidates, opts, func(c Candidate) (float64, bool) {
			if c.Quality.SampleCount == 0 {
				return 0, false
			}
			// 用 **Wilson 置信下界**（而非成功率）：小样本上的高成功率不可信
			return float64(c.Quality.ReliabilityLowerBoundPpm), true
		}, func(c Candidate) []string {
			return []string{fmt.Sprintf("wilson_lower_bound_ppm=%d samples=%d",
				c.Quality.ReliabilityLowerBoundPpm, c.Quality.SampleCount)}
		})
	case StrategyLatency:
		return rankByMetric(candidates, opts, func(c Candidate) (float64, bool) {
			if c.Quality.LatencyMsP50 <= 0 {
				return 0, false // 没有可比数据：不强行排名（而不是当成 0 毫秒最好）
			}
			return -float64(c.Quality.LatencyMsP50), true // 越小越好
		}, func(c Candidate) []string {
			return []string{fmt.Sprintf("latency_p50_ms=%d", c.Quality.LatencyMsP50)}
		})
	case StrategyThroughput:
		return rankByMetric(candidates, opts, func(c Candidate) (float64, bool) {
			if !c.ThroughputKnown || c.OutputTokensPerSecond <= 0 {
				return 0, false
			}
			return c.OutputTokensPerSecond, true
		}, func(c Candidate) []string {
			return []string{fmt.Sprintf("output_tokens_per_second=%.2f", c.OutputTokensPerSecond)}
		})
	case StrategyAuto:
		return rankAuto(candidates, opts), nil
	default:
		return nil, fmt.Errorf("%w: strategy %d does not rank candidates", apigxmodel.ErrInvalidArgument, strategy)
	}
}

// rankByMetric 是单指标策略的通用排名。
//
// 指标缺失的候选会被**排除出排名**（附原因），而不是拿默认值顶上——
// 否则"没有数据"会伪装成"表现中等"。
func rankByMetric(candidates []Candidate, opts Options,
	metric func(Candidate) (float64, bool), reasons func(Candidate) []string) ([]Ranked, error) {
	scored := make([]Ranked, 0, len(candidates))
	for _, candidate := range candidates {
		value, ok := metric(candidate)
		if !ok {
			continue
		}
		scored = append(scored, Ranked{Candidate: candidate, Score: value, Reasons: reasons(candidate)})
	}
	if len(scored) == 0 {
		return nil, nil
	}
	sortByScore(scored)
	return scored, nil
}

// rankAuto 是综合自动：把指标在**可比候选内部**归一化后加权。
//
// 可靠性与容量余量是**关键指标**（缺失即不可参与综合排序）；延迟与吞吐缺失时
// 按计划书"缺失的非关键指标剔除后重归一"处理。
func rankAuto(candidates []Candidate, opts Options) []Ranked {
	items := make([]autoItem, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.Quality.SampleCount == 0 {
			continue
		}
		item := autoItem{
			candidate:   candidate,
			reliability: float64(candidate.Quality.ReliabilityLowerBoundPpm) / 1_000_000,
			hasLatency:  candidate.Quality.LatencyMsP50 > 0,
			hasThrough:  candidate.ThroughputKnown && candidate.OutputTokensPerSecond > 0,
		}
		items = append(items, item)
	}
	if len(items) == 0 {
		return nil
	}

	// 延迟与吞吐在候选内部归一化（越小/越大越好）
	minLatency, maxLatency := latencyBounds(items)
	minThroughput, maxThroughput := throughputBounds(items)
	maxCapacity := int64(0)
	for i := range items {
		if remaining := items[i].candidate.RemainingCapacity(); remaining > maxCapacity {
			maxCapacity = remaining
		}
	}
	for i := range items {
		item := &items[i]
		if item.hasLatency {
			item.latency = normalizedLowerBetter(float64(item.candidate.Quality.LatencyMsP50), minLatency, maxLatency)
		}
		if item.hasThrough {
			item.throughput = normalizedHigherBetter(item.candidate.OutputTokensPerSecond, minThroughput, maxThroughput)
		}
		// 容量余量：未配置上限的候选按"最充裕"处理（计划书：未配置不等于零）
		if item.candidate.CapacityLimit <= 0 {
			item.capacity = 1
		} else if maxCapacity > 0 {
			item.capacity = float64(item.candidate.RemainingCapacity()) / float64(maxCapacity)
		}
	}

	ranked := make([]Ranked, 0, len(items))
	for i := range items {
		item := &items[i]
		// 关键指标：可靠性 + 容量；非关键：延迟 + 吞吐（缺失则剔除并重归一）
		weightSum := opts.WeightReliability + opts.WeightCapacity
		score := opts.WeightReliability*item.reliability + opts.WeightCapacity*item.capacity
		reasons := []string{fmt.Sprintf("reliability=%.4f(weight %.2f)", item.reliability, opts.WeightReliability),
			fmt.Sprintf("capacity=%.4f(weight %.2f)", item.capacity, opts.WeightCapacity)}
		if item.hasLatency {
			weightSum += opts.WeightLatency
			score += opts.WeightLatency * item.latency
			reasons = append(reasons, fmt.Sprintf("latency=%.4f(weight %.2f)", item.latency, opts.WeightLatency))
		}
		if item.hasThrough {
			weightSum += opts.WeightThroughput
			score += opts.WeightThroughput * item.throughput
			reasons = append(reasons, fmt.Sprintf("throughput=%.4f(weight %.2f)", item.throughput, opts.WeightThroughput))
		}
		if weightSum > 0 {
			score /= weightSum
		}
		ranked = append(ranked, Ranked{Candidate: item.candidate, Score: score, Reasons: reasons})
	}
	if len(ranked) == 0 {
		return nil
	}
	sortByScore(ranked)
	return ranked
}

func latencyBounds(items []autoItem) (float64, float64) {
	minValue, maxValue := 0.0, 0.0
	first := true
	for i := range items {
		if !items[i].hasLatency {
			continue
		}
		value := float64(items[i].candidate.Quality.LatencyMsP50)
		if first {
			minValue, maxValue, first = value, value, false
			continue
		}
		if value < minValue {
			minValue = value
		}
		if value > maxValue {
			maxValue = value
		}
	}
	return minValue, maxValue
}

func throughputBounds(items []autoItem) (float64, float64) {
	minValue, maxValue := 0.0, 0.0
	first := true
	for i := range items {
		if !items[i].hasThrough {
			continue
		}
		value := items[i].candidate.OutputTokensPerSecond
		if first {
			minValue, maxValue, first = value, value, false
			continue
		}
		if value < minValue {
			minValue = value
		}
		if value > maxValue {
			maxValue = value
		}
	}
	return minValue, maxValue
}

// normalizedLowerBetter 把"越小越好"的指标归一化到 [0,1]（1 最好）。
func normalizedLowerBetter(value, minValue, maxValue float64) float64 {
	if maxValue <= minValue {
		return 1 // 所有候选相同：视为同等
	}
	return (maxValue - value) / (maxValue - minValue)
}

// normalizedHigherBetter 把"越大越好"的指标归一化到 [0,1]。
func normalizedHigherBetter(value, minValue, maxValue float64) float64 {
	if maxValue <= minValue {
		return 1
	}
	return (value - minValue) / (maxValue - minValue)
}

// sortByScore 按分数降序排名；**同分共享同一 Rank**，让调用方能按权重在组内分散流量。
// 同分内的顺序按供给 ID 升序，保证结果确定（便于测试与复现）。
func sortByScore(items []Ranked) {
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Score != items[j].Score {
			return items[i].Score > items[j].Score
		}
		return items[i].Candidate.OfferingID < items[j].Candidate.OfferingID
	})
	rank := -1
	previousScore := 0.0
	for i := range items {
		if i == 0 || items[i].Score != previousScore {
			rank++
			previousScore = items[i].Score
		}
		items[i].Rank = rank
	}
}

// autoItem 是综合自动打分用的中间结构（把"是否可比"与归一化结果分开存）。
type autoItem struct {
	candidate   Candidate
	reliability float64
	latency     float64
	throughput  float64
	capacity    float64
	hasLatency  bool
	hasThrough  bool
}
