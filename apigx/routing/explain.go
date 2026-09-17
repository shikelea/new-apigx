package routing

import (
	"sort"
)

// 可解释的路由结果（T2.9）。
//
// # 对外只给"为什么"，不给"内部是谁"
//
// 说明里出现的必须是**对外标识**（供给 public_id / 渠道 public_id）与**指标数值**
// （样本数、下界、延迟、价格），**不得包含**底座渠道 ID、上游 Key、凭据、内部作用域 ID。
// 因此本层不直接输出 `Candidate`，而是显式挑字段——漏字段是显式的编译错误，
// 而不是"某个内部字段被顺手序列化出去"。
//
// # 为什么要把"没选它"也说出来
//
// 只给"选中了谁"无法排障，用户会怀疑是黑箱。把**被硬约束排除的候选及原因**
// （满载 / 未授权 / 没有样本 / 该策略需要的指标缺失）一并给出，
// 才能回答"为什么我这条来源没被用上"——这正是计划书要求的可解释性。

// CandidatePublicInfo 是候选的**对外标识**（由调用方从我们的库里查好，不在这里查）。
type CandidatePublicInfo struct {
	OfferingPublicID string `json:"offering_public_id"`
	ChannelPublicID  string `json:"channel_public_id"`
	OwnerUserID      int64  `json:"owner_user_id"`
	// ModelName 对外展示的模型名（上游模型名即对外名）。
	ModelName string `json:"model_name"`
}

// ExplainInput 是解释所需的全部输入。
type ExplainInput struct {
	Decision   Decision
	Candidates []Candidate
	// Public 按供给 ID 提供对外标识；缺失的候选会被标记为 missing_public_info 而不是泄露内部 ID。
	Public map[int64]CandidatePublicInfo
}

// CandidateExplanation 是**单个候选**的对外说明。
//
// 字段带 snake_case 的 JSON 标签：本结构是**直接对外序列化**的，
// 缺标签会输出 Go 的字段名（PascalCase），与 API 其余部分的命名约定不一致
// （这个缺陷由接口用例发现）。
type CandidateExplanation struct {
	OfferingPublicID string `json:"offering_public_id"`
	ChannelPublicID  string `json:"channel_public_id"`
	OwnerUserID      int64  `json:"owner_user_id"`
	ModelName        string `json:"model_name"`

	Rank     int  `json:"rank"`
	Selected bool `json:"selected"`
	// Score 是策略内部的评分（量纲随策略而变，仅供同一策略内比较，不对用户承诺绝对含义）。
	Score float64 `json:"score"`
	// Reasons 是得分构成（指标原值），例如 wilson_lower_bound_ppm=... samples=...
	Reasons []string `json:"reasons"`

	// 质量摘要：**样本不足/陈旧必须如实标出**，而不是给一个看似确定的值。
	SampleCount              int64 `json:"sample_count"`
	SuccessRatePpm           int64 `json:"success_rate_ppm"`
	ReliabilityLowerBoundPpm int64 `json:"reliability_lower_bound_ppm"`
	LatencyMsP50             int64 `json:"latency_ms_p50"`
	Insufficient             bool  `json:"insufficient"`
	Stale                    bool  `json:"stale"`

	// 价格：PriceKnown=false 表示**价格未知**（不是零）。
	PriceKnown       bool  `json:"price_known"`
	InputPriceMicro  int64 `json:"input_price_micro"`
	OutputPriceMicro int64 `json:"output_price_micro"`

	// ExcludedReason 非空表示该候选被排除（满载/未授权/无数据…）。
	ExcludedReason string `json:"excluded_reason,omitempty"`
}

// Explanation 是一次路由决策的对外说明。
type Explanation struct {
	Strategy string `json:"strategy"`
	// Selected 是最终选中的候选（对外标识）；nil 表示没有可用候选。
	Selected *CandidateExplanation `json:"selected,omitempty"`
	// Candidates 按名次排序（被排除者排在最后，带 ExcludedReason）。
	Candidates []CandidateExplanation `json:"candidates"`
	// ColdStart 表示本次选择来自冷启动候选（样本不足）。
	ColdStart bool `json:"cold_start"`
	// Reason 是一句话说明。
	Reason string `json:"reason"`
}

// Explain 把决策结果转成对外可展示的说明。
func Explain(input ExplainInput) Explanation {
	explanation := Explanation{
		Strategy:  StrategyName(input.Decision.Strategy),
		ColdStart: input.Decision.ColdStart,
		Reason:    input.Decision.Reason,
	}

	// 被排除的候选（原因取自决策）
	excludedByOffering := make(map[int64]string, len(input.Decision.Excluded))
	for _, exclusion := range input.Decision.Excluded {
		excludedByOffering[exclusion.OfferingID] = exclusion.Reason
	}

	// 已排名的候选：按 Rank 升序（决策已排好，这里保持稳定）
	for i := range input.Decision.Ranked {
		ranked := input.Decision.Ranked[i]
		item := explainOne(ranked.Candidate, input.Public)
		item.Rank = ranked.Rank
		item.Score = ranked.Score
		item.Reasons = append(item.Reasons, ranked.Reasons...)
		if input.Decision.Chosen != nil && ranked.Candidate.OfferingID == input.Decision.Chosen.OfferingID {
			item.Selected = true
			selected := item
			explanation.Selected = &selected
		}
		explanation.Candidates = append(explanation.Candidates, item)
	}

	// 被排除的候选附在最后：用户最常问的就是"为什么没用我这条来源"
	excludedIDs := make([]int64, 0, len(excludedByOffering))
	for offeringID := range excludedByOffering {
		excludedIDs = append(excludedIDs, offeringID)
	}
	sort.Slice(excludedIDs, func(i, j int) bool { return excludedIDs[i] < excludedIDs[j] })
	for _, offeringID := range excludedIDs {
		var candidate Candidate
		for i := range input.Candidates {
			if input.Candidates[i].OfferingID == offeringID {
				candidate = input.Candidates[i]
				break
			}
		}
		candidate.OfferingID = offeringID
		item := explainOne(candidate, input.Public)
		item.ExcludedReason = excludedByOffering[offeringID]
		item.Rank = -1
		explanation.Candidates = append(explanation.Candidates, item)
	}
	return explanation
}

// explainOne 把单个候选转成对外说明（**逐字段挑选**，避免内部字段被顺手带出去）。
func explainOne(candidate Candidate, public map[int64]CandidatePublicInfo) CandidateExplanation {
	item := CandidateExplanation{
		SampleCount:              candidate.Quality.SampleCount,
		ReliabilityLowerBoundPpm: candidate.Quality.ReliabilityLowerBoundPpm,
		LatencyMsP50:             candidate.Quality.LatencyMsP50,
		Insufficient:             candidate.Quality.Insufficient,
		Stale:                    candidate.Quality.Stale,
		PriceKnown:               candidate.PriceKnown,
		InputPriceMicro:          candidate.InputPriceMicro,
		OutputPriceMicro:         candidate.OutputPriceMicro,
	}
	// **样本不足时不给成功率**：给了就会被当成结论（与质量视图同一原则）
	if !candidate.Quality.Insufficient {
		item.SuccessRatePpm = candidate.Quality.SuccessRatePpm
	}
	if info, ok := public[candidate.OfferingID]; ok {
		item.OfferingPublicID = info.OfferingPublicID
		item.ChannelPublicID = info.ChannelPublicID
		item.OwnerUserID = info.OwnerUserID
		item.ModelName = info.ModelName
	} else {
		// 查不到对外标识时**不退化输出内部 ID**，只标出来
		item.ExcludedReason = "missing_public_info"
	}
	return item
}

// ExplainForStrategy 是便捷入口：按策略决策并解释。
func ExplainForStrategy(strategy Strategy, candidates []Candidate, public map[int64]CandidatePublicInfo,
	opts Options) (Explanation, error) {
	decision, err := Decide(strategy, candidates, opts)
	if err != nil {
		return Explanation{}, err
	}
	return Explain(ExplainInput{Decision: decision, Candidates: candidates, Public: public}), nil
}

// ExplainForStrategyWithBreaker 在决策前先按熔断结果摘除"打开"的候选，再给出解释。
//
// 摘除**不改写调用方的候选集**：这里按值建副本（`Candidate` 是值类型），
// 因此调用方持有的切片不受影响——避免"看一眼解释就把候选状态改了"的隐蔽副作用。
func ExplainForStrategyWithBreaker(strategy Strategy, candidates []Candidate,
	public map[int64]CandidatePublicInfo, opts Options, breaker BreakerConfig, now int64) (Explanation, error) {
	effective := make([]Candidate, len(candidates))
	copy(effective, candidates)
	for i := range effective {
		verdict := EvaluateBreaker(effective[i].Quality, now, breaker)
		if verdict.State == BreakerOpen {
			effective[i].Allowed = false
			effective[i].ExcludeReason = ExcludeBreakerOpen
		}
	}
	return ExplainForStrategy(strategy, effective, public, opts)
}
