package apigxmodel

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// T2.4 质量统计桶：样本不足与陈旧数据必须**显式**表达，不能伪装成结论。

// 计数精确累加，分类归因如实保留。
func TestRecordQualitySampleAccumulatesExactly(t *testing.T) {
	db := newRepoDB(t)
	const offeringID int64 = 8801
	at := int64(1_700_000_000)
	bucketTs := at - at%60

	for i := range 30 {
		outcome := QualityOutcomeSuccess
		switch {
		case i%10 == 7:
			outcome = QualityOutcomeTimeout
		case i%10 == 8:
			outcome = QualityOutcomeUpstream
		case i%10 == 9:
			outcome = QualityOutcomeRejected
		}
		require.NoError(t, RecordQualitySample(db, QualitySample{
			OfferingID: offeringID, Capability: 0, BucketTs: bucketTs,
			Outcome: outcome, LatencyMs: int64(100 + i),
		}))
	}

	// 同一个桶被累加（不是每次新建）
	var count int64
	require.NoError(t, db.Model(&QualityBucket{}).Where("offering_id = ?", offeringID).Count(&count).Error)
	assert.EqualValues(t, 1, count, "同一分钟应累加到同一个桶")

	view, err := ResolveQualityView(db, offeringID, QualityWindowMinute, at)
	require.NoError(t, err)
	assert.EqualValues(t, 27, view.SampleCount, "平台拒绝不进入上游质量分母")
	assert.EqualValues(t, 21, view.SuccessCount)
	assert.EqualValues(t, 3, view.TimeoutCount)
	assert.EqualValues(t, 3, view.UpstreamFailures)
	assert.EqualValues(t, 3, view.PlatformRejects, "平台拒绝要单独归因，不能混进上游失败")
	assert.False(t, view.Insufficient)
	assert.False(t, view.Stale)
	assert.Greater(t, view.LatencyMsP50, int64(0), "P50 应有估计值")
}

// 文本吞吐只在分子和分母都可观测时成立；6 小时视图应原样汇总，不能把未知写成 0 token/s。
func TestQualityViewAggregatesThroughputInputs(t *testing.T) {
	db := newRepoDB(t)
	const offeringID int64 = 8804
	at := int64(1_700_000_000)
	bucketTs := at - at%60

	require.NoError(t, RecordQualitySample(db, QualitySample{
		OfferingID: offeringID, BucketTs: bucketTs, Outcome: QualityOutcomeSuccess,
		LatencyMs: 1000, OutputTokens: 80, GenerationMs: 800,
	}))
	require.NoError(t, RecordQualitySample(db, QualitySample{
		OfferingID: offeringID, BucketTs: bucketTs, Outcome: QualityOutcomeSuccess,
		LatencyMs: 1200, OutputTokens: 20, GenerationMs: 200,
	}))

	view, err := ResolveQualityView(db, offeringID, QualityWindowSixHour, at)
	require.NoError(t, err)
	assert.EqualValues(t, 100, view.OutputTokensSum)
	assert.EqualValues(t, 1000, view.GenerationMsSum)

	unknown, err := ResolveQualityView(db, 8898, QualityWindowSixHour, at)
	require.NoError(t, err)
	assert.Zero(t, unknown.OutputTokensSum)
	assert.Zero(t, unknown.GenerationMsSum)
}

func TestQualityViewIgnoresPartialThroughputSamples(t *testing.T) {
	db := newRepoDB(t)
	const offeringID int64 = 8805
	at := int64(1_700_000_000)
	bucketTs := at - at%60

	// 两条成功样本分别只带分子和分母。它们不能被拼成一条虚假的 100 token/s 观测。
	require.NoError(t, RecordQualitySample(db, QualitySample{
		OfferingID: offeringID, BucketTs: bucketTs, Outcome: QualityOutcomeSuccess,
		LatencyMs: 1000, OutputTokens: 100,
	}))
	require.NoError(t, RecordQualitySample(db, QualitySample{
		OfferingID: offeringID, BucketTs: bucketTs, Outcome: QualityOutcomeSuccess,
		LatencyMs: 1000, GenerationMs: 1000,
	}))

	view, err := ResolveQualityView(db, offeringID, QualityWindowSixHour, at)
	require.NoError(t, err)
	assert.Zero(t, view.OutputTokensSum)
	assert.Zero(t, view.GenerationMsSum)
}

// **样本不足不给结论**：样本数不够时，不能把成功率当成质量评分。
func TestQualityViewFlagsInsufficientSamples(t *testing.T) {
	db := newRepoDB(t)
	const offeringID int64 = 8802
	at := int64(1_700_000_000)
	bucketTs := at - at%60

	// 只有 3 个样本（远低于阈值）
	for range 3 {
		require.NoError(t, RecordQualitySample(db, QualitySample{
			OfferingID: offeringID, BucketTs: bucketTs, Outcome: QualityOutcomeSuccess, LatencyMs: 120,
		}))
	}
	view, err := ResolveQualityView(db, offeringID, QualityWindowMinute, at)
	require.NoError(t, err)
	assert.EqualValues(t, 3, view.SampleCount)
	assert.True(t, view.Insufficient, "样本不足必须显式标记")

	// **即使三个样本全成功，置信下界也远低于 100%**——这正是"不能只看成功率"的原因
	assert.Less(t, view.ReliabilityLowerBoundPpm, int64(1_000_000),
		"3/3 成功不等于 100% 可靠：置信下界必须显著低于点估计")
	assert.Less(t, view.ReliabilityLowerBoundPpm, int64(500_000))

	// 窗口内**没有任何样本**时，视图是"空的"，而不是"成功率 0%"
	empty, err := ResolveQualityView(db, 8899, QualityWindowMinute, at)
	require.NoError(t, err)
	assert.Zero(t, empty.SampleCount)
	assert.True(t, empty.Insufficient)
	assert.Zero(t, empty.SuccessRatePpm)
	assert.Zero(t, empty.ReliabilityLowerBoundPpm)
}

// **过期数据退出窗口**，且数据陈旧时显式标记。
func TestQualityViewExcludesExpiredAndFlagsStale(t *testing.T) {
	db := newRepoDB(t)
	const offeringID int64 = 8803
	at := int64(1_700_000_000)

	// 一个很久以前的桶（远超 6 小时窗口）
	oldTs := at - 8*3600
	require.NoError(t, RecordQualitySample(db, QualitySample{
		OfferingID: offeringID, BucketTs: oldTs - oldTs%60, Outcome: QualityOutcomeSuccess, LatencyMs: 100,
	}))
	view, err := ResolveQualityView(db, offeringID, QualityWindowSixHour, at)
	require.NoError(t, err)
	assert.Zero(t, view.SampleCount, "窗口外的桶不得参与聚合（过期数据退出窗口）")

	// 窗口内但很陈旧（超过 QualityStaleAfter）
	recentTs := at - int64(QualityStaleAfter.Seconds()) - 120
	require.NoError(t, RecordQualitySample(db, QualitySample{
		OfferingID: offeringID, BucketTs: recentTs - recentTs%60, Outcome: QualityOutcomeSuccess, LatencyMs: 100,
	}))
	view, err = ResolveQualityView(db, offeringID, QualityWindowSixHour, at)
	require.NoError(t, err)
	assert.EqualValues(t, 1, view.SampleCount)
	assert.True(t, view.Stale, "陈旧数据必须显式标记")
	assert.Positive(t, view.LatestBucketTs)

	// 新鲜样本：不陈旧
	freshTs := at - 30
	require.NoError(t, RecordQualitySample(db, QualitySample{
		OfferingID: offeringID, BucketTs: freshTs - freshTs%60, Outcome: QualityOutcomeSuccess, LatencyMs: 100,
	}))
	view, err = ResolveQualityView(db, offeringID, QualityWindowSixHour, at)
	require.NoError(t, err)
	assert.False(t, view.Stale)
}

// 桶的身份维度必须**全部**参与：否则不同供给/能力/规格的样本会串桶（混合统计比没有统计更危险）。
func TestQualityBucketsAreIsolatedByDimensions(t *testing.T) {
	db := newRepoDB(t)
	at := int64(1_700_000_000)
	bucketTs := at - at%60

	// 三个维度各不相同的桶
	require.NoError(t, RecordQualitySample(db, QualitySample{
		OfferingID: 8811, Capability: 0, BucketTs: bucketTs, Outcome: QualityOutcomeSuccess, LatencyMs: 100}))
	require.NoError(t, RecordQualitySample(db, QualitySample{
		OfferingID: 8811, Capability: 1, BucketTs: bucketTs, Outcome: QualityOutcomeUpstream, LatencyMs: 100}))
	require.NoError(t, RecordQualitySample(db, QualitySample{
		OfferingID: 8812, Capability: 0, BucketTs: bucketTs, Outcome: QualityOutcomeSuccess, LatencyMs: 100}))
	require.NoError(t, RecordQualitySample(db, QualitySample{
		OfferingID: 8811, Capability: 0, SpecBucket: "long", BucketTs: bucketTs, Outcome: QualityOutcomeSuccess, LatencyMs: 100}))

	var count int64
	require.NoError(t, db.Model(&QualityBucket{}).Count(&count).Error)
	assert.EqualValues(t, 4, count, "不同维度组合必须是不同的桶")

	view, err := ResolveQualityView(db, 8811, QualityWindowMinute, at)
	require.NoError(t, err)
	assert.EqualValues(t, 3, view.SampleCount, "供给级汇总是跨能力/规格的（该供给共 3 个样本），但不含另一个供给")
}

// 探测请求与自然请求分开计数；归因未知的取值被拒。
func TestQualitySampleSeparatesProbesAndRejectsUnknownOutcome(t *testing.T) {
	db := newRepoDB(t)
	const offeringID int64 = 8821
	at := int64(1_700_000_000)
	bucketTs := at - at%60

	require.NoError(t, RecordQualitySample(db, QualitySample{
		OfferingID: offeringID, BucketTs: bucketTs, Outcome: QualityOutcomeSuccess, IsProbe: true, LatencyMs: 90}))
	require.NoError(t, RecordQualitySample(db, QualitySample{
		OfferingID: offeringID, BucketTs: bucketTs, Outcome: QualityOutcomeSuccess, LatencyMs: 110}))

	var bucket QualityBucket
	require.NoError(t, db.Where("offering_id = ?", offeringID).First(&bucket).Error)
	assert.EqualValues(t, 1, bucket.ProbeCount, "探测请求单独计数")
	assert.EqualValues(t, 1, bucket.NaturalRequestCount)
	assert.EqualValues(t, 1, bucket.CompleteSuccessCount, "探测成功不得混入自然流量成功数")
	assert.EqualValues(t, 1, bucket.SampleOkCount, "只有自然流量的合格上游尝试进入质量分母")

	// 失效请求数（平台拒绝）不参与成功率分母之外的"上游质量"：这里只验证计数分开
	assert.ErrorIs(t, RecordQualitySample(db, QualitySample{
		OfferingID: offeringID, BucketTs: bucketTs, Outcome: 99}), ErrInvalidArgument)
}

// Wilson 下界的性质：样本越多越接近点估计；全成功时随样本增长而上升。
func TestWilsonLowerBoundBehaviour(t *testing.T) {
	small := wilsonLowerBoundPpm(3, 3)
	medium := wilsonLowerBoundPpm(30, 30)
	large := wilsonLowerBoundPpm(300, 300)
	assert.Less(t, small, medium)
	assert.Less(t, medium, large)
	assert.Less(t, large, int64(1_000_000), "再多的全成功样本，下界也不会等于 100%")

	// 一半成功：下界低于 50%
	half := wilsonLowerBoundPpm(50, 100)
	assert.Less(t, half, int64(500_000))

	// 没有样本 → 0（而不是"默认可靠"）
	assert.Zero(t, wilsonLowerBoundPpm(0, 0))
}
