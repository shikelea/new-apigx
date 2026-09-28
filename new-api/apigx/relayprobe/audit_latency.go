package relayprobe

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"

	apigxmodel "github.com/QuantumNous/new-api/apigx/model"
)

// 延迟方差：同一个最小请求重复发多次，看耗时是否稳定。抖动大通常意味着中转超卖、
// 排队或在多个上游之间轮转；抖动异常小且内容完全一致，则更像是命中了缓存而没有真正
// 请求模型（对按量计费的用户来说同样是问题）。
//
// 单次绝对延迟受检测机房位置影响很大，所以这里判定的是离散度而不是快慢。
const (
	latencySamples = 5
	// 变异系数（标准差/均值）阈值：0.35 以内算稳定，超过 0.8 判为抖动明显。
	latencyCVWarn  = 0.35
	latencyCVHeavy = 0.80
	// 低于该耗时且多次内容完全一致，判为疑似缓存。
	latencyCacheMs = 60
)

func runLatencyVarianceCheck(ctx context.Context, target auditTarget) CheckResult {
	samples := make([]int64, 0, latencySamples)
	replies := make([]string, 0, latencySamples)
	ids := make([]string, 0, latencySamples)
	var lastErr error
	failures := 0
	for range latencySamples {
		// 每次问同一个确定性问题：内容应当一致，耗时不应当一致。
		resp, err := target.chat(ctx, map[string]any{
			"messages": userMessages("", "Reply with exactly: ok"), "max_tokens": 4, "temperature": 0,
		})
		if err != nil {
			failures++
			lastErr = err
			continue
		}
		samples = append(samples, resp.LatencyMs)
		replies = append(replies, strings.TrimSpace(resp.Content))
		ids = append(ids, resp.ID)
	}
	result := CheckResult{SampleCount: len(samples), Confidence: "medium"}
	if len(samples) < 3 {
		result.Status, result.Confidence = apigxmodel.AuditCheckInconclusive, "low"
		if lastErr != nil {
			result.Evidence = fmt.Sprintf("%d/%d 次请求失败：", failures, latencySamples) + lastErr.Error()
			return result
		}
		result.Evidence = "有效样本不足，无法评估延迟稳定性"
		return result
	}
	median, mean, cv := latencyStats(samples)
	p95 := percentile(samples, 95)
	result.LatencyMs = median
	metrics := fmt.Sprintf("%d 次采样：中位 %d ms，p95 %d ms，变异系数 %.2f", len(samples), median, p95, cv)

	var problems, notes []string
	if failures > 0 {
		problems = append(problems, fmt.Sprintf("%d/%d 次请求失败", failures, latencySamples))
	}
	// 同一 id 重复出现 = 同一条响应被重放，几乎只有缓存会这样。
	if repeated := duplicateCount(ids); repeated > 0 {
		problems = append(problems, fmt.Sprintf("%d 次响应复用了相同的 id，疑似返回缓存结果", repeated))
	} else if mean < latencyCacheMs && allSame(replies) {
		notes = append(notes, fmt.Sprintf("平均耗时仅 %.0f ms 且内容完全一致，疑似命中缓存而非实际推理", mean))
	}
	switch {
	case cv >= latencyCVHeavy:
		problems = append(problems, fmt.Sprintf("延迟抖动明显（变异系数 %.2f ≥ %.2f），可能超卖或在多个上游间轮转", cv, latencyCVHeavy))
	case cv >= latencyCVWarn:
		notes = append(notes, fmt.Sprintf("延迟存在一定抖动（变异系数 %.2f）", cv))
	default:
		notes = append(notes, "延迟稳定")
	}
	switch {
	case len(problems) > 0:
		result.Status, result.Confidence = apigxmodel.AuditCheckWarning, "high"
		result.Evidence = strings.Join(append(problems, notes...), "；") + "；" + metrics
	case hasLatencyWarning(notes):
		result.Status = apigxmodel.AuditCheckWarning
		result.Evidence = strings.Join(notes, "；") + "；" + metrics + "。网络链路本身也会造成抖动，需复核"
	default:
		result.Status = apigxmodel.AuditCheckPassed
		result.Evidence = strings.Join(notes, "；") + "；" + metrics
	}
	return result
}

func hasLatencyWarning(notes []string) bool {
	for _, note := range notes {
		if strings.Contains(note, "抖动") && !strings.Contains(note, "稳定") {
			return true
		}
		if strings.Contains(note, "缓存") {
			return true
		}
	}
	return false
}

// latencyStats 返回中位数、均值与变异系数（标准差/均值）。
func latencyStats(samples []int64) (median int64, mean, cv float64) {
	if len(samples) == 0 {
		return 0, 0, 0
	}
	sorted := append([]int64(nil), samples...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	median = sorted[len(sorted)/2]
	if len(sorted)%2 == 0 {
		median = (sorted[len(sorted)/2-1] + sorted[len(sorted)/2]) / 2
	}
	var sum float64
	for _, value := range samples {
		sum += float64(value)
	}
	mean = sum / float64(len(samples))
	if mean <= 0 {
		return median, mean, 0
	}
	var variance float64
	for _, value := range samples {
		delta := float64(value) - mean
		variance += delta * delta
	}
	return median, mean, math.Sqrt(variance/float64(len(samples))) / mean
}

func duplicateCount(values []string) int {
	seen := make(map[string]int, len(values))
	duplicates := 0
	for _, value := range values {
		if value == "" {
			continue
		}
		seen[value]++
		if seen[value] == 2 {
			duplicates++
		}
	}
	return duplicates
}

func allSame(values []string) bool {
	if len(values) < 2 {
		return false
	}
	for _, value := range values[1:] {
		if value != values[0] {
			return false
		}
	}
	return true
}
