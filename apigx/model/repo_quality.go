package apigxmodel

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// 质量统计桶（`apigx_quality_buckets`，T2.4）。
//
// # 两条硬约束（与"价格未知 ≠ 零"同一性质）
//
//  1. **样本不足不给结论**：样本数低于 `MinSamplesForQuality` 时，视图把
//     `Insufficient` 置位，调用方**不得**把成功率当成"质量评分"使用
//     （计划书验收：显示样本不足）。
//  2. **陈旧数据退出窗口**：只聚合窗口内的桶；窗口内最后一个桶距今超过
//     `QualityStaleAfter` 时把 `Stale` 置位，调用方不得把陈旧数据当作当前状态
//     （计划书验收：过期数据退出窗口、显示陈旧数据）。
//
// 这两条都通过**显式字段**表达，而不是让调用方各自判断或默认"看起来还行"。

// 窗口种类（`WindowKind`）。
const (
	// QualityWindowMinute 分钟桶（原始写入粒度）。
	QualityWindowMinute int = 0
	// QualityWindowSixHour 6 小时滚动窗口（用于对外质量结论）。
	QualityWindowSixHour int = 1
)

// 归因结果（`Outcome`）——与底座的错误分类保持正交：这里回答"这次算不算成功"。
const (
	QualityOutcomeSuccess  int = 0 // 完整成功
	QualityOutcomeTimeout  int = 1 // 超时
	QualityOutcomeUpstream int = 2 // 上游失败
	QualityOutcomeCancel   int = 3 // 用户取消
	QualityOutcomeRejected int = 4 // 平台拒绝（准入/配额），**不算上游质量问题**
)

const (
	// MinSamplesForQuality 是给出质量结论所需的最小样本数。
	//
	// 20 是一个保守起点：更小的样本上，成功率下界会低到没有区分度。
	MinSamplesForQuality int64 = 20
	// QualityStaleAfter 超过这么久没有新样本，就把数据视为陈旧。
	QualityStaleAfter = 15 * time.Minute
	// wilsonZ 是 Wilson 区间使用的 z 值（95% 置信）。
	wilsonZ = 1.96
)

// QualityWindowLength 返回窗口种类对应的时长。
func QualityWindowLength(windowKind int) time.Duration {
	if windowKind == QualityWindowSixHour {
		return 6 * time.Hour
	}
	return time.Minute
}

// QualityBucketKey 生成桶的身份哈希。
//
// 桶的维度必须**全部**进哈希，否则不同供给/能力/规格的样本会串到一个桶里，
// 质量结论就变成"混合统计"——那比没有统计更危险。
func QualityBucketKey(offeringID int64, capability int, deployPoint, specBucket string, windowKind int, bucketTs int64) string {
	raw := strconv.FormatInt(offeringID, 10) + "|" + strconv.Itoa(capability) + "|" +
		deployPoint + "|" + specBucket + "|" + strconv.Itoa(windowKind) + "|" + strconv.FormatInt(bucketTs, 10)
	sum := sha256.Sum256([]byte("apigx:qbucket:" + raw))
	return hex.EncodeToString(sum[:])
}

// QualitySample 是一条待记录的质量样本。
type QualitySample struct {
	OfferingID  int64
	Capability  int
	DeployPoint string
	SpecBucket  string
	// BucketTs 为 0 时按当前时间的分钟对齐。
	BucketTs int64
	Outcome  int
	// LatencyMs / TtftMs 为 0 表示没有观测到（不参与统计，而不是记为 0 毫秒）。
	LatencyMs    int64
	TtftMs       int64
	OutputTokens int64
	GenerationMs int64
	// IsProbe 表示这是平台的探测请求（与自然请求分开计数）。
	IsProbe bool
}

// RecordQualitySample 把一个样本累加进分钟桶。
//
// 计数永远是**精确累加**（用 SQL 表达式自增，不做读改写），
// 只有 P50 是近似估计——见 `updateMedianEstimate` 的说明。
func RecordQualitySample(db *gorm.DB, sample QualitySample) error {
	if db == nil || sample.OfferingID <= 0 {
		return fmt.Errorf("%w: offering_id is required", ErrInvalidArgument)
	}
	if !validQualityOutcome(sample.Outcome) {
		return fmt.Errorf("%w: unknown outcome %d", ErrInvalidArgument, sample.Outcome)
	}
	if sample.BucketTs == 0 {
		sample.BucketTs = alignToMinute(common.GetTimestamp())
	}
	keyHash := QualityBucketKey(sample.OfferingID, sample.Capability, sample.DeployPoint,
		sample.SpecBucket, QualityWindowMinute, sample.BucketTs)
	now := common.GetTimestamp()

	return db.Transaction(func(tx *gorm.DB) error {
		var existing QualityBucket
		err := tx.Where("bucket_key_hash = ?", keyHash).First(&existing).Error
		switch {
		case errors.Is(err, gorm.ErrRecordNotFound):
			bucket := &QualityBucket{
				BucketKeyHash: keyHash, OfferingID: sample.OfferingID, Capability: sample.Capability,
				DeployPoint: sample.DeployPoint, SpecBucket: sample.SpecBucket,
				WindowKind: QualityWindowMinute, BucketTs: sample.BucketTs,
				UpdatedAt: now,
			}
			applySampleToBucket(bucket, sample)
			return tx.Create(bucket).Error
		case err != nil:
			return err
		}

		// 已存在：先算近似中位数（需要旧估计），再用表达式精确累加计数与求和
		updates := map[string]any{
			"updated_at": now,
		}
		if sample.IsProbe {
			updates["probe_count"] = gorm.Expr("probe_count + 1")
		} else {
			updates["natural_request_count"] = gorm.Expr("natural_request_count + 1")
		}
		// 主动探测只记 ProbeCount；它不能改变自然流量的可靠性、延迟或吞吐评分。
		if !sample.IsProbe {
			applyOutcomeUpdates(updates, sample.Outcome)
		}

		qualified := !sample.IsProbe && qualityOutcomeQualified(sample.Outcome)
		totalAfter := bucketQualifiedCount(existing) + 1
		if qualified {
			updates["sample_ok_count"] = gorm.Expr("sample_ok_count + 1")
		}
		if qualified && sample.LatencyMs > 0 {
			updates["latency_ms_sum"] = gorm.Expr("latency_ms_sum + ?", sample.LatencyMs)
			updates["latency_ms_p50"] = updateMedianEstimate(existing.LatencyMsP50, sample.LatencyMs, totalAfter)
		}
		if qualified && sample.TtftMs > 0 {
			updates["ttft_ms_sum"] = gorm.Expr("ttft_ms_sum + ?", sample.TtftMs)
			updates["ttft_ms_p50"] = updateMedianEstimate(existing.TtftMsP50, sample.TtftMs, totalAfter)
		}
		// token 与生成时长必须成对累计；只记录其中一项会让窗口汇总把不同请求的
		// 分子、分母拼在一起，得到无法对应任何真实样本的吞吐率。
		throughputKnown := !sample.IsProbe && sample.Outcome == QualityOutcomeSuccess &&
			sample.OutputTokens > 0 && sample.GenerationMs > 0
		if throughputKnown {
			updates["output_tokens_sum"] = gorm.Expr("output_tokens_sum + ?", sample.OutputTokens)
			updates["generation_ms_sum"] = gorm.Expr("generation_ms_sum + ?", sample.GenerationMs)
		}
		return tx.Model(&QualityBucket{}).Where("bucket_key_hash = ?", keyHash).Updates(updates).Error
	})
}

// applySampleToBucket 用于首次创建桶时把样本写进去。
func applySampleToBucket(bucket *QualityBucket, sample QualitySample) {
	if sample.IsProbe {
		bucket.ProbeCount = 1
	} else {
		bucket.NaturalRequestCount = 1
		applyOutcomeToBucket(bucket, sample.Outcome)
	}
	qualified := !sample.IsProbe && qualityOutcomeQualified(sample.Outcome)
	if qualified {
		bucket.SampleOkCount = 1
	}
	if qualified && sample.LatencyMs > 0 {
		bucket.LatencyMsSum = sample.LatencyMs
		bucket.LatencyMsP50 = sample.LatencyMs // 首个样本即当前中位数的起点
	}
	if qualified && sample.TtftMs > 0 {
		bucket.TtftMsSum = sample.TtftMs
		bucket.TtftMsP50 = sample.TtftMs
	}
	if !sample.IsProbe && sample.Outcome == QualityOutcomeSuccess &&
		sample.OutputTokens > 0 && sample.GenerationMs > 0 {
		bucket.OutputTokensSum = sample.OutputTokens
		bucket.GenerationMsSum = sample.GenerationMs
	}
}

func validQualityOutcome(outcome int) bool {
	return outcome >= QualityOutcomeSuccess && outcome <= QualityOutcomeRejected
}

func qualityOutcomeQualified(outcome int) bool {
	return outcome == QualityOutcomeSuccess || outcome == QualityOutcomeTimeout || outcome == QualityOutcomeUpstream
}

func applyOutcomeUpdates(updates map[string]any, outcome int) {
	switch outcome {
	case QualityOutcomeSuccess:
		updates["complete_success_count"] = gorm.Expr("complete_success_count + 1")
	case QualityOutcomeTimeout:
		updates["timeout_count"] = gorm.Expr("timeout_count + 1")
	case QualityOutcomeUpstream:
		updates["upstream_failure_count"] = gorm.Expr("upstream_failure_count + 1")
	case QualityOutcomeCancel:
		updates["cancelled_count"] = gorm.Expr("cancelled_count + 1")
	case QualityOutcomeRejected:
		updates["platform_reject_count"] = gorm.Expr("platform_reject_count + 1")
	}
}

func applyOutcomeToBucket(bucket *QualityBucket, outcome int) {
	switch outcome {
	case QualityOutcomeSuccess:
		bucket.CompleteSuccessCount = 1
	case QualityOutcomeTimeout:
		bucket.TimeoutCount = 1
	case QualityOutcomeUpstream:
		bucket.UpstreamFailureCount = 1
	case QualityOutcomeCancel:
		bucket.CancelledCount = 1
	case QualityOutcomeRejected:
		bucket.PlatformRejectCount = 1
	}
}

func bucketQualifiedCount(bucket QualityBucket) int64 {
	if bucket.SampleOkCount > 0 {
		return bucket.SampleOkCount
	}
	// 兼容 B4 前已写入但尚未维护 SampleOkCount 的桶。
	return bucket.CompleteSuccessCount + bucket.TimeoutCount + bucket.UpstreamFailureCount
}

// updateMedianEstimate 用**流式中位数估计**更新 P50。
//
// ⚠️ 这是**近似值**，不是精确分位数：桶里只存了"和"与一个估计值，
// 没有完整分布，精确分位无法事后还原。估计采用步长随样本数收敛的经典做法
// （每次朝样本方向移动一小步），样本越多越准。
//
// 之所以敢这么用，是因为本层的对外契约是「**样本不足就不给结论**」+
// 「显示样本量」，而不是宣称分位数精确。调用方看到的 `SampleCount` 就是它可信度的边界。
func updateMedianEstimate(currentEstimate, newSample, totalAfter int64) int64 {
	if totalAfter <= 1 || currentEstimate <= 0 {
		return newSample
	}
	step := currentEstimate / (2 * totalAfter)
	if step < 1 {
		step = 1
	}
	switch {
	case newSample > currentEstimate:
		return currentEstimate + step
	case newSample < currentEstimate:
		next := currentEstimate - step
		if next < 1 {
			return 1
		}
		return next
	default:
		return currentEstimate
	}
}

func alignToMinute(ts int64) int64 {
	return ts - ts%60
}

// QualityView 是对外可用的质量视图。
type QualityView struct {
	OfferingID   int64
	SampleCount  int64
	SuccessCount int64
	// SuccessRatePpm 是成功率（百万分比）。**仅在 `Insufficient` 为 false 时有结论意义**。
	SuccessRatePpm int64
	// ReliabilityLowerBoundPpm 是成功率的 **Wilson 95% 置信下界**（百万分比）。
	// 路由用质量排序时应当用它，而不是直接比成功率——小样本上的高成功率不可信。
	ReliabilityLowerBoundPpm int64
	// LatencyMsP50 是**近似**中位数（见 updateMedianEstimate）。
	LatencyMsP50 int64
	// OutputTokensSum / GenerationMsSum 只累计带可靠 usage 的文本成功样本。
	// 两者任一为 0 都表示吞吐未知，调用方不得把它当作 0 token/s。
	OutputTokensSum int64
	GenerationMsSum int64
	// LatestBucketTs 是窗口内最新的桶时间（0 表示窗口内没有任何样本）。
	LatestBucketTs int64
	// LatestUpstreamFailureTs 是窗口内**最后一次上游失败/超时**所在桶的时间（0 表示没有）。
	//
	// 熔断（T2.8）需要它来算"冷却是否已过"：只关心**上游侧**的失败，
	// 平台自身的拒绝（准入/配额）与用户取消不参与——它们不是上游的问题，
	// 拿它们熔断分享者的供给会误伤。
	LatestUpstreamFailureTs int64
	// Insufficient 样本数不足以给出质量结论。
	Insufficient bool
	// Stale 窗口内数据已陈旧。
	Stale bool
	// TimeoutCount 等分类计数如实暴露，便于"归因正确"（计划书验收）。
	TimeoutCount     int64
	UpstreamFailures int64
	CancelledCount   int64
	PlatformRejects  int64
}

// OutputTokensPerSecond returns the observed text-generation throughput when
// both sides of the measurement are present. A missing numerator or
// denominator stays unknown instead of becoming a misleading zero.
func (view QualityView) OutputTokensPerSecond() (float64, bool) {
	if view.OutputTokensSum <= 0 || view.GenerationMsSum <= 0 {
		return 0, false
	}
	return float64(view.OutputTokensSum) * 1000 / float64(view.GenerationMsSum), true
}

// ResolveQualityView 汇总某供给在窗口内（截至 `at`）的质量视图。
//
// 窗口外的桶**不参与聚合**（过期数据退出窗口）；窗口内没有任何样本时，
// 视图是"空"的：`SampleCount == 0`、`Insufficient == true`，而不是一份"成功率 0%"的假结论。
func ResolveQualityView(db *gorm.DB, offeringID int64, windowKind int, at int64) (QualityView, error) {
	return ResolveQualityViewSince(db, offeringID, windowKind, at, 0)
}

// ResolveQualityViewSince is ResolveQualityView with an additional inclusive
// lower bound. Breaker recovery uses it to start a clean reliability baseline
// after successful probes without deleting historical quality buckets.
func ResolveQualityViewSince(db *gorm.DB, offeringID int64, windowKind int, at, notBefore int64) (QualityView, error) {
	view := QualityView{OfferingID: offeringID, Insufficient: true}
	if db == nil || offeringID <= 0 {
		return view, fmt.Errorf("%w: offering_id is required", ErrInvalidArgument)
	}
	if at == 0 {
		at = common.GetTimestamp()
	}
	from := at - int64(QualityWindowLength(windowKind).Seconds())
	if notBefore > from {
		from = notBefore
	}

	// 只聚合**分钟桶**，按请求的时长跨度取范围：
	// 写入端永远产生分钟桶（`QualityWindowMinute`），6 小时那样的长窗口是**聚合视图**
	// 而不是另一种写入粒度。早期实现按 `window_kind` 精确匹配，导致长窗口永远查不到
	// 任何数据——那会让"质量路由"在长窗口下静默地永远看不到样本。
	// （`QualityWindowSixHour` 保留给"物化聚合桶"，那是后续优化；届时这里要优先读物化结果。）
	var buckets []QualityBucket
	if err := db.Where("offering_id = ? AND window_kind = ? AND bucket_ts >= ? AND bucket_ts <= ?",
		offeringID, QualityWindowMinute, from, at).Find(&buckets).Error; err != nil {
		return view, err
	}
	if len(buckets) == 0 {
		return view, nil // 窗口内无样本：保持 Insufficient，不给结论
	}

	var latencyWeightedSum, latencyWeight int64
	for i := range buckets {
		bucket := &buckets[i]
		qualifiedCount := bucketQualifiedCount(*bucket)
		view.SampleCount += qualifiedCount
		view.SuccessCount += bucket.CompleteSuccessCount
		view.TimeoutCount += bucket.TimeoutCount
		view.UpstreamFailures += bucket.UpstreamFailureCount
		view.CancelledCount += bucket.CancelledCount
		view.PlatformRejects += bucket.PlatformRejectCount
		if qualifiedCount > 0 && bucket.BucketTs > view.LatestBucketTs {
			view.LatestBucketTs = bucket.BucketTs
		}
		if bucket.UpstreamFailureCount+bucket.TimeoutCount > 0 && bucket.BucketTs > view.LatestUpstreamFailureTs {
			view.LatestUpstreamFailureTs = bucket.BucketTs
		}
		if bucket.LatencyMsP50 > 0 {
			weight := qualifiedCount
			if weight <= 0 {
				weight = 1
			}
			latencyWeightedSum += bucket.LatencyMsP50 * weight
			latencyWeight += weight
		}
		view.OutputTokensSum += bucket.OutputTokensSum
		view.GenerationMsSum += bucket.GenerationMsSum
	}
	if latencyWeight > 0 {
		view.LatencyMsP50 = latencyWeightedSum / latencyWeight
	}

	view.Insufficient = view.SampleCount < MinSamplesForQuality
	view.Stale = view.LatestBucketTs > 0 && at-view.LatestBucketTs > int64(QualityStaleAfter.Seconds())
	if view.SampleCount > 0 {
		reliabilitySuccesses := min(view.SuccessCount, view.SampleCount)
		view.SuccessRatePpm = reliabilitySuccesses * 1_000_000 / view.SampleCount
		view.ReliabilityLowerBoundPpm = wilsonLowerBoundPpm(reliabilitySuccesses, view.SampleCount)
	}
	return view, nil
}

// wilsonLowerBoundPpm 计算成功率的 Wilson 95% 置信下界（百万分比）。
//
// 用下界而不是成功率本身，是为了让"3 次里成功 3 次"不会被当成 100% 可靠：
// 小样本的置信下界会显著低于点估计，这正是路由排序需要的保守量。
func wilsonLowerBoundPpm(successes, total int64) int64 {
	if total <= 0 {
		return 0
	}
	n := float64(total)
	p := float64(successes) / n
	z := wilsonZ
	denominator := 1 + z*z/n
	center := (p + z*z/(2*n)) / denominator
	margin := (z / denominator) * math.Sqrt(p*(1-p)/n+z*z/(4*n*n))
	lower := center - margin
	if lower < 0 {
		lower = 0
	}
	if lower > 1 {
		lower = 1
	}
	return int64(math.Round(lower * 1_000_000))
}
