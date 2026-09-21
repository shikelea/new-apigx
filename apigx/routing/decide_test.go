package routing

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sync/atomic"
	"testing"

	apigxmodel "github.com/QuantumNous/new-api/apigx/model"
	"github.com/QuantumNous/new-api/common"
	basemodel "github.com/QuantumNous/new-api/model"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// 造一个"成熟"候选（样本充足，避免被判为冷启动）。
func matureCandidate(offeringID int64, wilsonPpm int64, latencyMs int64, samples int64) Candidate {
	return Candidate{
		OfferingID: offeringID, ShareChannelID: offeringID * 10, OwnerUserID: 1, Allowed: true,
		Quality: apigxmodel.QualityView{
			OfferingID: offeringID, SampleCount: samples, SuccessCount: samples,
			ReliabilityLowerBoundPpm: wilsonPpm, LatencyMsP50: latencyMs,
			Insufficient: samples < apigxmodel.MinSamplesForQuality,
		},
	}
}

// 硬约束先于评分：**容量已满的候选必须被排除**，质量再高也不能入选
// （计划书 §9.2 的例子：A 首字更快但已占满 4/4，必须选有余量的 B）。
func TestSaturatedCandidateIsExcludedBeforeScoring(t *testing.T) {
	fast := matureCandidate(1, 990_000, 100, 100) // 质量明显更好
	fast.CapacityLimit, fast.CapacityInUse = 4, 4 // 但已满

	slow := matureCandidate(2, 500_000, 900, 100) // 质量较差
	slow.CapacityLimit, slow.CapacityInUse = 4, 1 // 但有余量

	for _, strategy := range []Strategy{StrategyReliability, StrategyLatency, StrategyAuto} {
		decision, err := Decide(strategy, []Candidate{fast, slow}, DefaultOptions())
		require.NoError(t, err)
		require.NotNil(t, decision.Chosen)
		assert.EqualValues(t, 2, decision.Chosen.OfferingID,
			"策略 %s：满载候选必须被排除，不能因为质量高就选中", StrategyName(strategy))
		require.Len(t, decision.Excluded, 1)
		assert.Equal(t, ExcludeSaturated, decision.Excluded[0].Reason)
	}
}

// 全部满载时没有任何候选可选中（而不是硬选一个）。
func TestAllSaturatedYieldsNoChoice(t *testing.T) {
	first := matureCandidate(1, 990_000, 100, 100)
	first.CapacityLimit, first.CapacityInUse = 2, 2
	second := matureCandidate(2, 980_000, 120, 100)
	second.CapacityLimit, second.CapacityInUse = 3, 3

	decision, err := Decide(StrategyAuto, []Candidate{first, second}, DefaultOptions())
	require.NoError(t, err)
	assert.Nil(t, decision.Chosen)
	assert.Len(t, decision.Excluded, 2)
	assert.Contains(t, decision.Reason, "hard constraints")
}

// 可靠性优先用 **Wilson 置信下界**排序，而不是成功率——
// 3/3 全成功的下界远低于 100%，所以样本充足的低成功率候选可能反而更靠前。
func TestReliabilityUsesWilsonLowerBound(t *testing.T) {
	// A：样本很多、成功率 90%（下界较高）
	rich := Candidate{OfferingID: 1, Allowed: true, Quality: apigxmodel.QualityView{
		SampleCount: 1000, SuccessCount: 950, ReliabilityLowerBoundPpm: 935_000,
		LatencyMsP50: 200, Insufficient: false,
	}}
	// B：样本很少、成功率 100%（下界因样本少而低）
	small := Candidate{OfferingID: 2, Allowed: true, Quality: apigxmodel.QualityView{
		SampleCount: 1, SuccessCount: 1, ReliabilityLowerBoundPpm: 200_000,
		LatencyMsP50: 100, Insufficient: true,
	}}

	decision, err := Decide(StrategyReliability, []Candidate{rich, small}, DefaultOptions())
	require.NoError(t, err)
	require.NotNil(t, decision.Chosen)
	assert.EqualValues(t, 1, decision.Chosen.OfferingID, "样本少的 100% 成功不应压过样本充足的高成功率")
	require.NotEmpty(t, decision.Ranked)
	assert.Contains(t, decision.Ranked[0].Reasons[0], "wilson_lower_bound_ppm")
}

// 延迟优先：没有可比延迟数据的候选**不参与排名**（不是当成 0 毫秒最优）。
func TestLatencyStrategySkipsCandidatesWithoutData(t *testing.T) {
	known := matureCandidate(1, 900_000, 300, 100)
	unknown := matureCandidate(2, 900_000, 0, 100) // P50 = 0 表示没有观测

	decision, err := Decide(StrategyLatency, []Candidate{known, unknown}, DefaultOptions())
	require.NoError(t, err)
	require.NotNil(t, decision.Chosen)
	assert.EqualValues(t, 1, decision.Chosen.OfferingID, "无延迟数据的候选不得因'0 毫秒'被选中")
	assert.Len(t, decision.Ranked, 1, "无数据者不在排名里")
}

// 吞吐优先：按输出 token/s 排序；无吞吐数据者不参与。
func TestThroughputStrategy(t *testing.T) {
	fast := matureCandidate(1, 900_000, 300, 100)
	fast.ThroughputKnown, fast.OutputTokensPerSecond = true, 120
	slow := matureCandidate(2, 950_000, 200, 100) // 可靠性更高，但吞吐低
	slow.ThroughputKnown, slow.OutputTokensPerSecond = true, 20

	decision, err := Decide(StrategyThroughput, []Candidate{slow, fast}, DefaultOptions())
	require.NoError(t, err)
	require.NotNil(t, decision.Chosen)
	assert.EqualValues(t, 1, decision.Chosen.OfferingID, "吞吐优先应按 token/s 选")

	// 无吞吐数据者不参与
	noData := matureCandidate(3, 900_000, 100, 100)
	decision, err = Decide(StrategyThroughput, []Candidate{noData}, DefaultOptions())
	require.NoError(t, err)
	assert.Nil(t, decision.Chosen, "没有任何可比吞吐数据时不应强行排名")
}

// 综合自动：可靠性 50% / 延迟 20% / 吞吐 20% / 容量余量 10%，
// 且**关键指标（可靠性、容量）缺失不参与**，非关键指标缺失则剔除后重归一。
func TestAutoStrategyWeightsAndRenormalization(t *testing.T) {
	// 只有可靠性与容量（延迟/吞吐缺失）：重归一后仍应选出可靠性更高者
	strong := matureCandidate(1, 990_000, 0, 100)
	weak := matureCandidate(2, 300_000, 0, 100)

	decision, err := Decide(StrategyAuto, []Candidate{weak, strong}, DefaultOptions())
	require.NoError(t, err)
	require.NotNil(t, decision.Chosen)
	assert.EqualValues(t, 1, decision.Chosen.OfferingID, "缺失非关键指标时应重归一，而不是把它们当 0 分")

	// 可靠性接近时，容量余量（10%）成为区分项
	tight := matureCandidate(1, 900_000, 0, 100)
	tight.CapacityLimit, tight.CapacityInUse = 10, 9 // 余量 1
	roomy := matureCandidate(2, 900_000, 0, 100)
	roomy.CapacityLimit, roomy.CapacityInUse = 10, 1 // 余量 9

	decision, err = Decide(StrategyAuto, []Candidate{tight, roomy}, DefaultOptions())
	require.NoError(t, err)
	require.NotNil(t, decision.Chosen)
	assert.EqualValues(t, 2, decision.Chosen.OfferingID, "同可靠性下容量余量更大者应靠前")

	// 未配置上限按"最充裕"处理（未配置 ≠ 零）
	unlimited := matureCandidate(1, 900_000, 0, 100)
	limited := matureCandidate(2, 900_000, 0, 100)
	limited.CapacityLimit, limited.CapacityInUse = 4, 3 // 余量 1

	decision, err = Decide(StrategyAuto, []Candidate{limited, unlimited}, DefaultOptions())
	require.NoError(t, err)
	require.NotNil(t, decision.Chosen)
	assert.EqualValues(t, 1, decision.Chosen.OfferingID, "未配置容量上限不得被当成余量为零")
}

// 冷启动：没有成熟候选时按剩余容量分配，并**显式标记样本不足**；
// 有成熟候选时只有在抽样允许的比例内才探索冷启动（默认 5%）。
func TestColdStartBehaviour(t *testing.T) {
	// ① 没有成熟候选：冷启动分配，标记 ColdStart
	coldA := matureCandidate(1, 100_000, 200, 3)
	coldB := matureCandidate(2, 100_000, 200, 5)
	decision, err := Decide(StrategyAuto, []Candidate{coldA, coldB}, DefaultOptions())
	require.NoError(t, err)
	require.NotNil(t, decision.Chosen)
	assert.True(t, decision.ColdStart, "样本不足必须显式标记，而不是伪装成正常选择")
	assert.Contains(t, decision.Reason, "insufficient samples")

	// ② 有成熟候选 + 抽样落在 5% 之外 → 走成熟候选
	mature := matureCandidate(9, 900_000, 300, 200)
	opts := DefaultOptions()
	opts.ColdStartRollPpm = 999_999 // 未命中 5%
	decision, err = Decide(StrategyAuto, []Candidate{mature, coldA}, opts)
	require.NoError(t, err)
	require.NotNil(t, decision.Chosen)
	assert.False(t, decision.ColdStart)
	assert.EqualValues(t, 9, decision.Chosen.OfferingID)

	// ③ 抽样落在 5% 之内 → 允许探索冷启动（不会因没有历史数据永久饿死新渠道）
	opts.ColdStartRollPpm = 1
	decision, err = Decide(StrategyAuto, []Candidate{mature, coldA}, opts)
	require.NoError(t, err)
	require.NotNil(t, decision.Chosen)
	assert.True(t, decision.ColdStart)
	assert.EqualValues(t, 1, decision.Chosen.OfferingID, "探索窗口内应给冷启动候选机会")
}

// 没有任何质量样本的候选既不参与正常排序，也**不会**被当成"100% 可靠"。
func TestCandidateWithoutSamplesNeverRanksAsReliable(t *testing.T) {
	unknown := Candidate{OfferingID: 1, Allowed: true}
	known := matureCandidate(2, 500_000, 400, 50)

	decision, err := Decide(StrategyReliability, []Candidate{unknown, known}, DefaultOptions())
	require.NoError(t, err)
	require.NotNil(t, decision.Chosen)
	assert.EqualValues(t, 2, decision.Chosen.OfferingID)
	assert.False(t, decision.ColdStart, "有成熟候选时不应走冷启动")
}

// 未授权的候选由硬约束排除（不能只依赖调用方记得过滤）。
func TestUnauthorizedCandidateExcluded(t *testing.T) {
	allowed := matureCandidate(1, 500_000, 400, 50)
	denied := matureCandidate(2, 999_000, 100, 100)
	denied.Allowed = false

	decision, err := Decide(StrategyAuto, []Candidate{denied, allowed}, DefaultOptions())
	require.NoError(t, err)
	require.NotNil(t, decision.Chosen)
	assert.EqualValues(t, 1, decision.Chosen.OfferingID, "未授权候选不得因质量高而入选")
	assert.Equal(t, ExcludeNotAllowed, decision.Excluded[0].Reason)
}

// 未知策略与"指定来源"的语义。
func TestStrategyValidationAndPinned(t *testing.T) {
	_, err := Decide(Strategy(99), []Candidate{matureCandidate(1, 1, 1, 50)}, DefaultOptions())
	assert.ErrorIs(t, err, apigxmodel.ErrInvalidArgument)

	decision, err := Decide(StrategyPinned, []Candidate{matureCandidate(1, 1, 1, 50)}, DefaultOptions())
	require.NoError(t, err)
	assert.Nil(t, decision.Chosen, "指定来源由准入判定完成，本层不做排序")
	assert.Contains(t, decision.Reason, "pinned")
}

// 同分候选共享同一 Rank（便于调用方按权重在组内分散流量），且顺序确定。
func TestTiedCandidatesShareRankDeterministically(t *testing.T) {
	first := matureCandidate(1, 900_000, 300, 100)
	second := matureCandidate(2, 900_000, 300, 100)

	decision, err := Decide(StrategyReliability, []Candidate{second, first}, DefaultOptions())
	require.NoError(t, err)
	require.Len(t, decision.Ranked, 2)
	assert.Equal(t, decision.Ranked[0].Rank, decision.Ranked[1].Rank, "同分应共享 Rank")
	assert.EqualValues(t, 1, decision.Ranked[0].Candidate.OfferingID, "同分时按供给 ID 升序，保证确定性")
}

// ---- 投影到底座渠道（T2.7 收尾） ----

// 默认只投影 weight，**不动 priority**：priority 是全局语义，擅自抬高会压过
// 同模型下平台或他人配置的优先级（计划书：分享者优先级不能抢占跨分享者质量排序）。
func TestApplyRankingProjectsWeightNotPriority(t *testing.T) {
	env := newRoutingTestEnv(t)
	first := env.seedChannelWithOffering(t, 1, "gpt-x")
	second := env.seedChannelWithOffering(t, 1, "gpt-x")

	decision, err := Decide(StrategyReliability, []Candidate{
		matureCandidate(first.OfferingID, 990_000, 200, 100),
		matureCandidate(second.OfferingID, 300_000, 200, 100),
	}, DefaultOptions())
	require.NoError(t, err)
	// 让候选能映射到我们的分享渠道与底座渠道
	decision.Ranked[0].Candidate.ShareChannelID = first.ShareChannelID
	decision.Ranked[1].Candidate.ShareChannelID = second.ShareChannelID

	// 底座渠道上先放一个"平台配置的优先级"，投影必须原样保留
	require.NoError(t, env.db.Model(&basemodel.Channel{}).Where("id = ?", first.BaseChannelID).
		Update("priority", int64(5000)).Error)

	result, err := ApplyRankingToBaseChannels(env.db, decision, "default", DefaultApplyOptions())
	require.NoError(t, err)
	assert.Equal(t, 2, result.Updated)

	var winnerChannel, loserChannel basemodel.Channel
	require.NoError(t, env.db.Where("id = ?", first.BaseChannelID).First(&winnerChannel).Error)
	require.NoError(t, env.db.Where("id = ?", second.BaseChannelID).First(&loserChannel).Error)

	assert.Greater(t, winnerChannel.GetWeight(), loserChannel.GetWeight(),
		"评分高的候选应得到更大权重（底座在同优先级内按权重分散流量）")
	assert.Less(t, winnerChannel.GetWeight(), 101)
	assert.GreaterOrEqual(t, loserChannel.GetWeight(), 1, "权重下限为 1，不能让候选完全没有机会")
	assert.EqualValues(t, 5000, *winnerChannel.Priority, "默认投影不得改动 priority")
}

// 显式开启分层时才改 priority，且排名越靠前优先级越高（底座取 priority 最大的一层）。
func TestApplyRankingProjectsPriorityWhenExplicitlyAllowed(t *testing.T) {
	env := newRoutingTestEnv(t)
	first := env.seedChannelWithOffering(t, 1, "gpt-y")
	second := env.seedChannelWithOffering(t, 1, "gpt-y")

	decision, err := Decide(StrategyReliability, []Candidate{
		matureCandidate(first.OfferingID, 990_000, 200, 100),
		matureCandidate(second.OfferingID, 300_000, 200, 100),
	}, DefaultOptions())
	require.NoError(t, err)
	decision.Ranked[0].Candidate.ShareChannelID = first.ShareChannelID
	decision.Ranked[1].Candidate.ShareChannelID = second.ShareChannelID

	opts := DefaultApplyOptions()
	opts.AllowPriorityProjection = true
	opts.PriorityBase = 1000
	_, err = ApplyRankingToBaseChannels(env.db, decision, "default", opts)
	require.NoError(t, err)

	var winnerChannel, loserChannel basemodel.Channel
	require.NoError(t, env.db.Where("id = ?", first.BaseChannelID).First(&winnerChannel).Error)
	require.NoError(t, env.db.Where("id = ?", second.BaseChannelID).First(&loserChannel).Error)
	assert.Greater(t, *winnerChannel.Priority, *loserChannel.Priority,
		"排名靠前者应得到更高优先级（底座按 priority 降序取）")
}

// 投影只动决策里出现的渠道：别的渠道（例如他人的或平台自建的）不得被修改。
func TestApplyRankingTouchesOnlyDecidedChannels(t *testing.T) {
	env := newRoutingTestEnv(t)
	mine := env.seedChannelWithOffering(t, 1, "gpt-z")
	others := env.seedChannelWithOffering(t, 2, "gpt-z") // 另一位分享者

	require.NoError(t, env.db.Model(&basemodel.Channel{}).
		Where("id = ?", others.BaseChannelID).Updates(map[string]any{"weight": uint(77), "priority": int64(42)}).Error)

	decision, err := Decide(StrategyReliability, []Candidate{
		matureCandidate(mine.OfferingID, 900_000, 200, 100),
	}, DefaultOptions())
	require.NoError(t, err)
	decision.Ranked[0].Candidate.ShareChannelID = mine.ShareChannelID

	_, err = ApplyRankingToBaseChannels(env.db, decision, "default", DefaultApplyOptions())
	require.NoError(t, err)

	var otherChannel basemodel.Channel
	require.NoError(t, env.db.Where("id = ?", others.BaseChannelID).First(&otherChannel).Error)
	assert.EqualValues(t, 77, otherChannel.GetWeight(), "他人的渠道权重不得被改写")
	assert.EqualValues(t, 42, *otherChannel.Priority)
}

// 同分候选得到**相同权重**（让底座在组内按权重分散，而不是硬选一个）。
func TestApplyRankingGivesTiedCandidatesEqualWeight(t *testing.T) {
	env := newRoutingTestEnv(t)
	first := env.seedChannelWithOffering(t, 1, "gpt-tie")
	second := env.seedChannelWithOffering(t, 1, "gpt-tie")

	decision, err := Decide(StrategyReliability, []Candidate{
		matureCandidate(first.OfferingID, 900_000, 200, 100),
		matureCandidate(second.OfferingID, 900_000, 200, 100),
	}, DefaultOptions())
	require.NoError(t, err)
	for i := range decision.Ranked {
		if decision.Ranked[i].Candidate.OfferingID == first.OfferingID {
			decision.Ranked[i].Candidate.ShareChannelID = first.ShareChannelID
		} else {
			decision.Ranked[i].Candidate.ShareChannelID = second.ShareChannelID
		}
	}

	_, err = ApplyRankingToBaseChannels(env.db, decision, "default", DefaultApplyOptions())
	require.NoError(t, err)

	var channelA, channelB basemodel.Channel
	require.NoError(t, env.db.Where("id = ?", first.BaseChannelID).First(&channelA).Error)
	require.NoError(t, env.db.Where("id = ?", second.BaseChannelID).First(&channelB).Error)
	assert.Equal(t, channelA.GetWeight(), channelB.GetWeight(), "同分应同权重")
}

func TestAssembleCandidatesAppliesACLBeforeQualityAndCapacity(t *testing.T) {
	env := newRoutingTestEnv(t)
	allowed := env.seedChannelWithOffering(t, 1, "gpt-acl")
	denied := env.seedChannelWithOffering(t, 2, "gpt-acl")
	for _, seeded := range []seededChannel{allowed, denied} {
		require.NoError(t, env.db.Model(&apigxmodel.ModelOffering{}).Where("id = ?", seeded.OfferingID).
			Update("status", apigxmodel.OfferingStatusPublished).Error)
		var channel apigxmodel.Channel
		require.NoError(t, env.db.Where("id = ?", seeded.ShareChannelID).First(&channel).Error)
		require.NoError(t, env.db.Model(&apigxmodel.ShareGroup{}).Where("id = ?", channel.ShareGroupID).
			Update("publish_status", apigxmodel.ShareGroupPublishPublished).Error)
	}

	candidates, public, err := AssembleCandidates(env.db, nil, "gpt-acl", AssembleOptions{
		AllowedOfferingIDs: map[int64]struct{}{allowed.OfferingID: {}},
	})
	require.NoError(t, err)
	require.Len(t, candidates, 1)
	assert.Equal(t, allowed.OfferingID, candidates[0].OfferingID)
	assert.Contains(t, public, allowed.OfferingID)
	assert.NotContains(t, public, denied.OfferingID, "无权供给不得进入质量/容量装配或解释输出")

	candidates, public, err = AssembleCandidates(env.db, nil, "gpt-acl", AssembleOptions{
		AllowedOfferingIDs: map[int64]struct{}{},
	})
	require.NoError(t, err)
	assert.Empty(t, candidates)
	assert.Empty(t, public)
}

func TestAssembleCandidatesCarriesSixHourTextThroughput(t *testing.T) {
	env := newRoutingTestEnv(t)
	seeded := env.seedChannelWithOffering(t, 1, "gpt-throughput-view")
	require.NoError(t, env.db.Model(&apigxmodel.ModelOffering{}).Where("id = ?", seeded.OfferingID).
		Update("status", apigxmodel.OfferingStatusPublished).Error)
	var channel apigxmodel.Channel
	require.NoError(t, env.db.Where("id = ?", seeded.ShareChannelID).First(&channel).Error)
	require.NoError(t, env.db.Model(&apigxmodel.ShareGroup{}).Where("id = ?", channel.ShareGroupID).
		Update("publish_status", apigxmodel.ShareGroupPublishPublished).Error)

	now := common.GetTimestamp()
	bucketTs := now - now%60
	for range apigxmodel.MinSamplesForQuality {
		require.NoError(t, apigxmodel.RecordQualitySample(env.db, apigxmodel.QualitySample{
			OfferingID: seeded.OfferingID, BucketTs: bucketTs,
			Outcome: apigxmodel.QualityOutcomeSuccess, LatencyMs: 1000,
			OutputTokens: 120, GenerationMs: 1000,
		}))
	}

	candidates, _, err := AssembleCandidates(env.db, nil, "gpt-throughput-view", AssembleOptions{Now: now})
	require.NoError(t, err)
	require.Len(t, candidates, 1)
	assert.True(t, candidates[0].ThroughputKnown)
	assert.InDelta(t, 120, candidates[0].OutputTokensPerSecond, 0.001)
}

func TestAssembleCandidatesKeepsProbeOpenAndIgnoresPreRecoveryFailuresAfterClose(t *testing.T) {
	env := newRoutingTestEnv(t)
	seeded := env.seedChannelWithOffering(t, 1, "gpt-breaker-recovery")
	require.NoError(t, env.db.Model(&apigxmodel.ModelOffering{}).Where("id = ?", seeded.OfferingID).
		Update("status", apigxmodel.OfferingStatusPublished).Error)
	var channel apigxmodel.Channel
	require.NoError(t, env.db.Where("id = ?", seeded.ShareChannelID).First(&channel).Error)
	require.NoError(t, env.db.Model(&apigxmodel.ShareGroup{}).Where("id = ?", channel.ShareGroupID).
		Update("publish_status", apigxmodel.ShareGroupPublishPublished).Error)

	const openedAt int64 = 1_700_000_000
	bucketTs := openedAt - openedAt%60
	for range 20 {
		require.NoError(t, apigxmodel.RecordQualitySample(env.db, apigxmodel.QualitySample{
			OfferingID: seeded.OfferingID, BucketTs: bucketTs,
			Outcome: apigxmodel.QualityOutcomeUpstream, LatencyMs: 100,
		}))
	}
	require.NoError(t, apigxmodel.OpenBreakerProbe(
		env.db, seeded.OfferingID, seeded.BaseChannelID, openedAt, 60,
	))

	candidates, _, err := AssembleCandidates(
		env.db, nil, "gpt-breaker-recovery", AssembleOptions{Now: openedAt + 60},
	)
	require.NoError(t, err)
	require.Len(t, candidates, 1)
	assert.False(t, candidates[0].Allowed, "an active durable probe must keep user traffic blocked")
	assert.Equal(t, ExcludeBreakerOpen, candidates[0].ExcludeReason)

	var state apigxmodel.BreakerProbeState
	require.NoError(t, env.db.Where("offering_id = ?", seeded.OfferingID).First(&state).Error)
	ready, err := apigxmodel.RecordBreakerProbeResult(env.db, state.ID, openedAt+60, true, false, "")
	require.NoError(t, err)
	assert.False(t, ready)
	ready, err = apigxmodel.RecordBreakerProbeResult(env.db, state.ID, openedAt+75, true, false, "")
	require.NoError(t, err)
	require.True(t, ready)
	require.NoError(t, env.db.Where("id = ?", state.ID).First(&state).Error)
	require.NoError(t, apigxmodel.FinalizeBreakerRecovery(env.db, state.ID, openedAt+76))

	candidates, _, err = AssembleCandidates(
		env.db, nil, "gpt-breaker-recovery", AssembleOptions{Now: state.RecoveryBaselineTs + 1},
	)
	require.NoError(t, err)
	require.Len(t, candidates, 1)
	assert.True(t, candidates[0].Allowed, "pre-recovery failures must not reopen a closed breaker")
	assert.Zero(t, candidates[0].Quality.UpstreamFailures)
}

// ---- fixture（投影测试需要真实库 + 我们的分享渠道） ----

type routingTestEnv struct {
	db  *gorm.DB
	seq atomic.Int64
}

func newRoutingTestEnv(t *testing.T) *routingTestEnv {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "apigx_routing_test.db")
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, apigxmodel.Migrate(db))
	// 底座自己的表（channels / abilities）不由 APIGX 迁移创建，
	// 但投影要写 base channel 的 weight/priority，因此这里按需建出来。
	require.NoError(t, db.AutoMigrate(&basemodel.Channel{}, &basemodel.Ability{}))
	// 底座的缓存刷新函数走的是**底座的全局 DB**：这里接上，让刷新路径也被覆盖
	previousBaseDB := basemodel.DB
	basemodel.DB = db
	t.Cleanup(func() { basemodel.DB = previousBaseDB })
	return &routingTestEnv{db: db}
}

type seededChannel struct {
	ShareChannelID int64
	BaseChannelID  int64
	OfferingID     int64
}

// seedChannelWithOffering 建一个"底座渠道 + 我们的分享渠道 + 一条供给"，
// 让投影能沿着 sharing → base 的映射落到底座渠道上。
func (e *routingTestEnv) seedChannelWithOffering(t *testing.T, ownerUserID int64, modelName string) seededChannel {
	t.Helper()
	seq := e.seq.Add(1)
	baseChannelID := 900000 + seq
	priority := int64(0)
	weight := uint(0)
	require.NoError(t, e.db.Create(&basemodel.Channel{
		Id: int(baseChannelID), Name: fmt.Sprintf("base-%d", seq), Key: "sk-x",
		Status: common.ChannelStatusEnabled, Priority: &priority, Weight: &weight,
	}).Error)

	group := &apigxmodel.ShareGroup{OwnerUserID: ownerUserID, Name: fmt.Sprintf("g-%d", seq), PublicID: fmt.Sprintf("g%d", seq)}
	require.NoError(t, apigxmodel.CreateShareGroup(e.db, group))
	channel := &apigxmodel.Channel{
		PublicID: fmt.Sprintf("c%d", seq), OwnerUserID: ownerUserID, ShareGroupID: group.ID,
		Name: fmt.Sprintf("share-%d", seq), BaseChannelID: baseChannelID,
	}
	require.NoError(t, apigxmodel.CreateChannel(e.db, channel))
	offering := &apigxmodel.ModelOffering{ChannelID: channel.ID, UpstreamModelID: modelName}
	require.NoError(t, apigxmodel.CreateModelOffering(e.db, ownerUserID, offering))

	return seededChannel{ShareChannelID: channel.ID, BaseChannelID: baseChannelID, OfferingID: offering.ID}
}

// ---- 可解释的路由结果（T2.9） ----

// 对外说明必须**只有对外标识**：不得泄露底座渠道 ID、上游 Key、内部作用域 ID。
func TestExplainExposesOnlyPublicIdentifiers(t *testing.T) {
	winner := matureCandidate(11, 990_000, 200, 100)
	winner.ShareChannelID = 555001 // 内部 ID，绝不应出现在输出里
	loser := matureCandidate(12, 300_000, 900, 100)
	loser.ShareChannelID = 555002

	public := map[int64]CandidatePublicInfo{
		11: {OfferingPublicID: "off-pub-A", ChannelPublicID: "ch-pub-A", OwnerUserID: 7, ModelName: "gpt-x"},
		12: {OfferingPublicID: "off-pub-B", ChannelPublicID: "ch-pub-B", OwnerUserID: 8, ModelName: "gpt-x"},
	}
	explanation, err := ExplainForStrategy(StrategyReliability, []Candidate{loser, winner}, public, DefaultOptions())
	require.NoError(t, err)

	require.NotNil(t, explanation.Selected)
	assert.Equal(t, "off-pub-A", explanation.Selected.OfferingPublicID)
	assert.Equal(t, "ch-pub-A", explanation.Selected.ChannelPublicID, "只给对外渠道标识")
	assert.True(t, explanation.Selected.Selected)
	assert.Contains(t, explanation.Selected.Reasons[0], "wilson_lower_bound_ppm")

	// 序列化后不得出现任何内部 ID 或凭据形态
	raw, err := json.Marshal(explanation)
	require.NoError(t, err)
	text := string(raw)
	for _, forbidden := range []string{"555001", "555002", "sk-", "channel_id", "upstream_key"} {
		assert.NotContains(t, text, forbidden, "对外说明不得包含内部标识：%s", forbidden)
	}
}

// **样本不足时不给成功率**：给了就会被当成结论。
func TestExplainHidesSuccessRateWhenSamplesInsufficient(t *testing.T) {
	cold := matureCandidate(21, 200_000, 150, 3) // 样本 3 < 阈值
	cold.Quality.SuccessRatePpm = 1_000_000      // 即便 100% 成功也不该输出
	public := map[int64]CandidatePublicInfo{21: {OfferingPublicID: "off-cold", ChannelPublicID: "ch-cold"}}

	explanation, err := ExplainForStrategy(StrategyAuto, []Candidate{cold}, public, DefaultOptions())
	require.NoError(t, err)
	require.NotNil(t, explanation.Selected)
	assert.True(t, explanation.Selected.Insufficient, "样本不足必须标出")
	assert.Zero(t, explanation.Selected.SuccessRatePpm, "样本不足不得给出成功率")
	assert.True(t, explanation.ColdStart)
	assert.Contains(t, explanation.Reason, "insufficient samples")
}

// "为什么没选它"也要说清楚：被硬约束排除的候选带原因附在最后。
func TestExplainReportsWhyCandidatesWereExcluded(t *testing.T) {
	chosen := matureCandidate(31, 900_000, 200, 100)
	full := matureCandidate(32, 999_000, 100, 100) // 质量最好但满载
	full.CapacityLimit, full.CapacityInUse = 4, 4
	denied := matureCandidate(33, 999_000, 100, 100)
	denied.Allowed = false

	public := map[int64]CandidatePublicInfo{
		31: {OfferingPublicID: "off-31", ChannelPublicID: "ch-31"},
		32: {OfferingPublicID: "off-32", ChannelPublicID: "ch-32"},
		33: {OfferingPublicID: "off-33", ChannelPublicID: "ch-33"},
	}
	explanation, err := ExplainForStrategy(StrategyReliability, []Candidate{full, denied, chosen}, public, DefaultOptions())
	require.NoError(t, err)
	require.NotNil(t, explanation.Selected)
	assert.EqualValues(t, "off-31", explanation.Selected.OfferingPublicID)

	reasons := map[string]string{}
	for _, item := range explanation.Candidates {
		if item.ExcludedReason != "" {
			reasons[item.OfferingPublicID] = item.ExcludedReason
		}
	}
	assert.Equal(t, ExcludeSaturated, reasons["off-32"], "满载应给出明确原因，用户才能理解\n为什么没用上质量最好的来源")
	assert.Equal(t, ExcludeNotAllowed, reasons["off-33"])
}

// 价格未知与陈旧数据都要如实标出（未知 ≠ 零；陈旧 ≠ 当前）。
func TestExplainSurfacesUnknownPriceAndStaleness(t *testing.T) {
	candidate := matureCandidate(41, 900_000, 200, 100)
	candidate.PriceKnown = false
	candidate.Quality.Stale = true

	public := map[int64]CandidatePublicInfo{41: {OfferingPublicID: "off-41", ChannelPublicID: "ch-41"}}
	explanation, err := ExplainForStrategy(StrategyAuto, []Candidate{candidate}, public, DefaultOptions())
	require.NoError(t, err)
	require.Len(t, explanation.Candidates, 1)
	item := explanation.Candidates[0]
	assert.False(t, item.PriceKnown, "价格未知必须如实标记")
	assert.Zero(t, item.InputPriceMicro, "未知价格不得给出 0 以外的任何暗示值（这里是未提供）")
	assert.True(t, item.Stale, "陈旧数据必须标记")
}

// 查不到对外标识时**不退化输出内部 ID**。
func TestExplainNeverFallsBackToInternalIDs(t *testing.T) {
	candidate := matureCandidate(51, 900_000, 200, 100)
	explanation, err := ExplainForStrategy(StrategyReliability, []Candidate{candidate}, map[int64]CandidatePublicInfo{}, DefaultOptions())
	require.NoError(t, err)
	require.Len(t, explanation.Candidates, 1)
	assert.Empty(t, explanation.Candidates[0].OfferingPublicID, "没有对外标识就留空")
	assert.Equal(t, "missing_public_info", explanation.Candidates[0].ExcludedReason)

	raw, err := json.Marshal(explanation)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "51", "内部供给 ID 不得出现")
}

// ---- 熔断与错误归因（T2.8） ----

// 归因红线：**平台自身的拒绝不得参与熔断**。
// 准入拒绝（容量/来源）与用户取消都发生在我们这边或用户侧，与上游质量无关；
// 拿它们熔断分享者的供给会误伤健康渠道。
func TestBreakerIgnoresPlatformRejectionsAndCancellations(t *testing.T) {
	cfg := DefaultBreakerConfig()
	now := int64(1_700_000_000)

	// 10 次尝试里：2 次成功、8 次**平台拒绝**（不是上游失败），外加大量用户取消
	view := apigxmodel.QualityView{
		SampleCount: 20, SuccessCount: 2,
		PlatformRejects: 8, CancelledCount: 10,
		UpstreamFailures: 0, TimeoutCount: 0,
		ReliabilityLowerBoundPpm: 100_000, LatestUpstreamFailureTs: 0,
	}
	verdict := EvaluateBreaker(view, now, cfg)
	assert.Equal(t, BreakerClosed, verdict.State, "平台拒绝与用户取消不得触发熔断")
	assert.EqualValues(t, 2, verdict.Attempts, "合格尝试只算上游成功 + 上游失败 + 超时")
	assert.Zero(t, verdict.FailureRatePpm)
}

// 上游持续失败 → 打开；冷却结束后 → 半开（放行试探，不永久封禁）。
func TestBreakerOpensOnUpstreamFailuresAndRecoversAfterCooldown(t *testing.T) {
	cfg := BreakerConfig{MinAttempts: 5, FailureRatePpm: 500_000, CooldownSeconds: 60}
	now := int64(1_700_000_000)

	view := apigxmodel.QualityView{
		SampleCount: 10, SuccessCount: 2, UpstreamFailures: 7, TimeoutCount: 1,
		LatestUpstreamFailureTs: now - 10, // 刚刚还在失败
	}
	verdict := EvaluateBreaker(view, now, cfg)
	assert.Equal(t, BreakerOpen, verdict.State)
	assert.EqualValues(t, 800_000, verdict.FailureRatePpm, "失败率 = 8/10")
	assert.Contains(t, verdict.Reason, "failure_rate_high")

	// 冷却已过 → 半开（给上游恢复的机会）
	view.LatestUpstreamFailureTs = now - 120
	verdict = EvaluateBreaker(view, now, cfg)
	assert.Equal(t, BreakerHalfOpen, verdict.State, "冷却结束后必须放行试探，而不是永久封禁")
	assert.Contains(t, verdict.Reason, "cooldown_elapsed")
}

// 样本不足不熔断：一两次失败不足以说明上游坏了（与"样本不足不给结论"同一原则）。
func TestBreakerRequiresMinimumAttempts(t *testing.T) {
	cfg := DefaultBreakerConfig()
	view := apigxmodel.QualityView{
		SampleCount: 2, SuccessCount: 0, UpstreamFailures: 2,
		LatestUpstreamFailureTs: 1_700_000_000,
	}
	verdict := EvaluateBreaker(view, 1_700_000_000, cfg)
	assert.Equal(t, BreakerClosed, verdict.State)
	assert.Contains(t, verdict.Reason, "insufficient_attempts")
}

// 健康供给保持关闭（失败率低于阈值）。
func TestBreakerStaysClosedForHealthySource(t *testing.T) {
	cfg := DefaultBreakerConfig()
	view := apigxmodel.QualityView{
		SampleCount: 100, SuccessCount: 98, UpstreamFailures: 2,
		LatestUpstreamFailureTs: 1_700_000_000,
	}
	verdict := EvaluateBreaker(view, 1_700_000_000, cfg)
	assert.Equal(t, BreakerClosed, verdict.State)
	assert.EqualValues(t, 20_000, verdict.FailureRatePpm)
}

// 熔断打开时该候选被摘出，且解释里给出**具体原因**（而不是笼统的"未授权"）。
func TestBreakerOpenRemovesCandidateWithSpecificReason(t *testing.T) {
	unhealthy := matureCandidate(61, 100_000, 200, 20)
	unhealthy.Quality.SuccessCount = 2
	unhealthy.Quality.UpstreamFailures = 18
	unhealthy.Quality.LatestUpstreamFailureTs = 1_700_000_000
	healthy := matureCandidate(62, 900_000, 300, 100)

	public := map[int64]CandidatePublicInfo{
		61: {OfferingPublicID: "off-61", ChannelPublicID: "ch-61"},
		62: {OfferingPublicID: "off-62", ChannelPublicID: "ch-62"},
	}
	opts := DefaultOptions()
	explanation, err := ExplainForStrategyWithBreaker(StrategyReliability, []Candidate{unhealthy, healthy}, public,
		opts, DefaultBreakerConfig(), 1_700_000_000)
	require.NoError(t, err)
	require.NotNil(t, explanation.Selected)
	assert.Equal(t, "off-62", explanation.Selected.OfferingPublicID, "熔断的供给不得被选中")

	reasons := map[string]string{}
	for _, item := range explanation.Candidates {
		if item.ExcludedReason != "" {
			reasons[item.OfferingPublicID] = item.ExcludedReason
		}
	}
	assert.Equal(t, ExcludeBreakerOpen, reasons["off-61"], "解释必须说明是熔断，而不是笼统的未授权")
}

// TestBreakerAlertIsRateLimitedPerOffering 覆盖阶段 4 的「告警」要求：
// 同一供给的熔断告警必须**限频**（避免刷屏），不同供给互不影响，间隔过后可再次告警。
//
// 注意：这里断言的是"是否该告警"这一**不变量**，不去断言日志文本
// （new-api/AGENTS.md 明确反对 log-only 断言）。
func TestBreakerAlertIsRateLimitedPerOffering(t *testing.T) {
	resetBreakerAlertState()

	const offeringA, offeringB int64 = 101, 202
	now := int64(1700000000)

	assert.True(t, claimBreakerAlertSlot(offeringA, now), "首次熔断应当告警")
	assert.False(t, claimBreakerAlertSlot(offeringA, now+1), "间隔内不得重复告警")
	assert.False(t, claimBreakerAlertSlot(offeringA, now+breakerAlertInterval-1), "差 1 秒仍不得告警")
	assert.True(t, claimBreakerAlertSlot(offeringA, now+breakerAlertInterval), "间隔过后应当可以再次告警")
	assert.True(t, claimBreakerAlertSlot(offeringB, now+1), "不同供给的限频互不影响")
	assert.False(t, claimBreakerAlertSlot(0, now), "无有效供给 id 时不告警")
}
