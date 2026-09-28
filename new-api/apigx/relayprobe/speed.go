package relayprobe

import (
	"context"
	"fmt"
	"math/rand/v2"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/service"
)

// 速度测试：只看两个指标。
//
//   - TTFT（time to first token）：从发出请求到收到第一个含内容的分片。反映排队、
//     路由跳数与上游负载，是交互体验里用户最先感知到的延迟。
//   - 输出 TPS（tokens per second）：首 token 之后的纯生成速率。用
//     completion_tokens/(总耗时-TTFT) 计算，把排队时间从吞吐里剔除出去。
//
// 分两档各采样 3 次，共 6 次，每档单独取中位数与极差。分档的理由是短输出和长输出测
// 到的根本不是一回事：短输出主要反映排队与首包延迟，长输出才会暴露上游在生成中途降速、
// 被限流、或者把长请求丢到更差的后端。只报一个平均值会把这两种情况混成一个没用的数。
//
// 题面每次从池子里随机组合，不用固定句子。固定题面会被上游或中间层缓存，缓存命中时
// TTFT 可以低到几毫秒，测出来的是缓存速度而不是生成速度。
const (
	// 必须是偶数：采样按「短、长」交替，奇数会让两档样本数不等。
	speedSamples      = 6
	speedShortTokens  = 200
	speedLongTokens   = 900
	speedMaxTokens    = speedLongTokens
	speedMinChunks    = 3
	speedMinTokensFor = 16
)

// 题面池：主题 × 切入角度组合，再叠一个随机序号，让每次请求的 prompt 都不同。
// 刻意全部选成「解释某个工程概念」这类任务——答案长度可控、不依赖时效知识、
// 不同模型都能正常作答，这样测到的差异来自链路而不是题目难度。
var (
	speedTopics = []string{
		"a load balancer", "a write-ahead log", "connection pooling", "a bloom filter",
		"consistent hashing", "a circuit breaker", "copy-on-write snapshots", "backpressure in queues",
		"idempotency keys", "read replicas", "a reverse proxy cache", "optimistic locking",
	}
	speedShortAngles = []string{
		"Explain %s in one paragraph of about 120 words. Plain prose, no lists.",
		"Describe what %s does and why it matters, in about 120 words of plain prose.",
		"In roughly 120 words of continuous prose, explain the purpose of %s.",
	}
	speedLongAngles = []string{
		"Write about 700 words of continuous prose explaining %s: what problem it solves, how it works, where it breaks down, and what to watch in production. No headings, no bullet lists.",
		"Explain %s in roughly 700 words of plain prose. Cover the motivation, the mechanism, the common failure modes, and the operational trade-offs. Do not use headings or lists.",
		"In about 700 words of flowing prose, walk through %s from first principles to production concerns. No headings, no bullets.",
	}
)

// speedPromptFor 组一道题。seq 只是用来打散内容，保证同一轮里两次请求也不完全相同。
func speedPromptFor(long bool, seq int) string {
	angles := speedShortAngles
	if long {
		angles = speedLongAngles
	}
	topic := speedTopics[rand.IntN(len(speedTopics))]
	prompt := fmt.Sprintf(angles[rand.IntN(len(angles))], topic)
	// 末尾挂一个不影响任务的编号，避免逐字命中缓存。
	return fmt.Sprintf("%s (variation %d-%d)", prompt, seq, rand.IntN(100000))
}

type SpeedSample struct {
	// Tier 是 "short" 或 "long"，让前端能分档展示。
	Tier            string  `json:"tier"`
	TTFTMs          int64   `json:"ttft_ms"`
	TotalMs         int64   `json:"total_ms"`
	OutputTokens    int64   `json:"output_tokens"`
	OutputTPS       float64 `json:"output_tps"`
	ContentChunks   int     `json:"content_chunks"`
	ReasoningChunks int     `json:"reasoning_chunks"`
	TokensEstimated bool    `json:"tokens_estimated"`
}

// 采样档位。
const (
	SpeedTierShort = "short"
	SpeedTierLong  = "long"
)

// SpeedTierStats 是单档的汇总。
type SpeedTierStats struct {
	Samples      int     `json:"samples"`
	Failed       int     `json:"failed"`
	TTFTMs       int64   `json:"ttft_ms"`
	TTFTSpreadMs int64   `json:"ttft_spread_ms"`
	OutputTPS    float64 `json:"output_tps"`
	TPSSpread    float64 `json:"tps_spread"`
	OutputTokens int64   `json:"output_tokens"`
}

type SpeedReport struct {
	Model   string        `json:"model"`
	Samples []SpeedSample `json:"samples"`
	// 顶层指标取短文本档，与改版前口径一致，便于和历史记录比较。
	TTFTMs       int64   `json:"ttft_ms"`
	TTFTSpreadMs int64   `json:"ttft_spread_ms"`
	OutputTPS    float64 `json:"output_tps"`
	TPSSpread    float64 `json:"tps_spread"`
	// 分档明细。长文本档单独看才能发现生成中途降速。
	Short         SpeedTierStats `json:"short"`
	Long          SpeedTierStats `json:"long"`
	Pseudostream  bool           `json:"pseudostream"`
	TokensExact   bool           `json:"tokens_exact"`
	Note          string         `json:"note"`
	FailedSamples int            `json:"failed_samples"`
	// Disclaimer 随结果一起返回，避免前端各自复制一份口径不一的说明。
	Disclaimer string `json:"disclaimer"`
}

const SpeedDisclaimer = "速度受检测节点位置、上游负载与采样时刻影响；中位数与极差一起看才有意义。"

// RunSpeedTest measures TTFT and output TPS through the SSRF-protected client.
func RunSpeedTest(ctx context.Context, baseURL, protocol, key, model string) (SpeedReport, error) {
	base, err := ValidateBaseURL(baseURL)
	if err != nil {
		return SpeedReport{}, fmt.Errorf("目标地址未通过安全校验")
	}
	if !supportedProtocol(protocol) {
		return SpeedReport{}, fmt.Errorf("暂不支持该协议的自动检测")
	}
	target := auditTarget{client: service.GetSSRFProtectedHTTPClient(), base: base, key: strings.TrimSpace(key), model: strings.TrimSpace(model)}
	return runSpeedTest(ctx, target)
}

func runSpeedTest(ctx context.Context, target auditTarget) (SpeedReport, error) {
	// 长文本档 token 数是短档的 4 倍多，超时预算按档位分别给，否则长档会被短档的
	// 预算卡死在中途。
	// 短档每次给一份超时预算，长档给两份（token 数是短档的 4 倍多）。
	budget := speedSamples / 2 * 3 * streamProbeTimeout
	ctx, cancel := withProbeTimeout(ctx, budget)
	defer cancel()
	report := SpeedReport{Model: target.model, TokensExact: true, Disclaimer: SpeedDisclaimer}
	var lastErr error
	// 两档交替跑（短、长、短、长……），而不是先跑完短档再跑长档。上游负载会随时间
	// 漂移，交替采样让两档落在同一个负载窗口里，两档的数才可比。
	shortFailed, longFailed := 0, 0
	for round := range speedSamples {
		long := round%2 == 1
		sample, err := target.speedSample(ctx, long, round)
		if err != nil {
			report.FailedSamples++
			if long {
				longFailed++
			} else {
				shortFailed++
			}
			lastErr = err
			continue
		}
		if sample.TokensEstimated {
			report.TokensExact = false
		}
		report.Samples = append(report.Samples, sample)
	}
	if len(report.Samples) == 0 {
		if lastErr != nil {
			return report, lastErr
		}
		return report, fmt.Errorf("未取得有效样本")
	}
	report.Short = summarizeTier(report.Samples, SpeedTierShort, shortFailed)
	report.Long = summarizeTier(report.Samples, SpeedTierLong, longFailed)
	// 顶层指标优先用短文本档；短档全挂时退回长档，总比没有数值好。
	headline := report.Short
	if headline.Samples == 0 {
		headline = report.Long
	}
	report.TTFTMs, report.TTFTSpreadMs = headline.TTFTMs, headline.TTFTSpreadMs
	report.OutputTPS, report.TPSSpread = headline.OutputTPS, headline.TPSSpread

	chunks, reasoningOnly := 0, 0
	for _, sample := range report.Samples {
		// 伪流式看的是「输出有没有被分片」，推理分片也算，否则推理模型会被误判。
		chunks += sample.ContentChunks + sample.ReasoningChunks
		if sample.ContentChunks == 0 && sample.ReasoningChunks > 0 {
			reasoningOnly++
		}
	}
	report.Pseudostream = chunks/len(report.Samples) < speedMinChunks

	notes := make([]string, 0, 4)
	if reasoningOnly == len(report.Samples) {
		notes = append(notes, fmt.Sprintf("上游是推理模型：max_tokens(%d) 全部用于思维链，未产出正文；TTFT 与 TPS 按推理 token 计算", speedMaxTokens))
	}
	if report.Pseudostream {
		notes = append(notes, "内容平均只分 1–2 个分片返回，疑似伪流式；此时 TTFT 接近总耗时、TPS 会虚高")
	}
	if !report.TokensExact {
		notes = append(notes, "上游未返回 completion_tokens，输出量按 4 字符≈1 token 估算")
	}
	if report.FailedSamples > 0 {
		notes = append(notes, fmt.Sprintf("%d/%d 次采样失败", report.FailedSamples, speedSamples))
	}
	if report.TTFTMs > 0 && report.TTFTSpreadMs > 2*report.TTFTMs {
		notes = append(notes, "TTFT 波动超过中位数的两倍，上游可能在排队或多上游轮转")
	}
	// 长文本掉速是限流或长请求被降级路由的典型表现，单看短文本档发现不了。
	if report.Short.OutputTPS > 0 && report.Long.OutputTPS > 0 && report.Long.OutputTPS*2 < report.Short.OutputTPS {
		notes = append(notes, fmt.Sprintf("长文本吞吐（%.1f tok/s）不到短文本（%.1f tok/s）的一半，上游可能对长请求限流或降级路由",
			report.Long.OutputTPS, report.Short.OutputTPS))
	}
	if report.Long.Samples > 0 && report.Long.OutputTokens > 0 && report.Long.OutputTokens < speedLongTokens/3 {
		notes = append(notes, fmt.Sprintf("长文本档平均只产出 %d token，远低于请求的 %d，上游可能在截断输出",
			report.Long.OutputTokens, speedLongTokens))
	}
	report.Note = strings.Join(notes, "；")
	return report, nil
}

// summarizeTier 汇总单档样本。failed 由调用方传入，因为失败样本不在 samples 里。
func summarizeTier(samples []SpeedSample, tier string, failed int) SpeedTierStats {
	stats := SpeedTierStats{Failed: failed}
	ttfts := make([]int64, 0, len(samples))
	rates := make([]float64, 0, len(samples))
	var tokens int64
	for _, sample := range samples {
		if sample.Tier != tier {
			continue
		}
		stats.Samples++
		ttfts = append(ttfts, sample.TTFTMs)
		rates = append(rates, sample.OutputTPS)
		tokens += sample.OutputTokens
	}
	if stats.Samples == 0 {
		return stats
	}
	stats.TTFTMs, _, _ = latencyStats(ttfts)
	stats.TTFTSpreadMs = spreadInt(ttfts)
	stats.OutputTPS = medianFloat(rates)
	stats.TPSSpread = spreadFloat(rates)
	stats.OutputTokens = tokens / int64(stats.Samples)
	return stats
}

// speedSample 跑一次流式请求，分别记录首个含内容分片的时刻与全流结束的时刻。
func (t auditTarget) speedSample(ctx context.Context, long bool, seq int) (SpeedSample, error) {
	tier, maxTokens := SpeedTierShort, speedShortTokens
	if long {
		tier, maxTokens = SpeedTierLong, speedLongTokens
	}
	stats, status, err := t.streamPrompt(ctx, speedPromptFor(long, seq), maxTokens)
	if err != nil {
		return SpeedSample{}, err
	}
	if status < 200 || status >= 300 {
		return SpeedSample{}, fmt.Errorf("上游返回 HTTP %d", status)
	}
	// 推理模型会先吐一长串 reasoning_content 再给正文，正文分片为 0 并不等于没在
	// 输出；只有两者都为空才算真的没收到东西。
	if stats.contentChunks == 0 && stats.reasoningChunks == 0 {
		return SpeedSample{}, fmt.Errorf("未收到任何输出分片")
	}
	sample := SpeedSample{
		Tier:            tier,
		TTFTMs:          stats.firstTokenMs,
		TotalMs:         stats.totalMs,
		ContentChunks:   stats.contentChunks,
		ReasoningChunks: stats.reasoningChunks,
	}
	if tokens, _, ok := usageInt(stats.usage, "completion_tokens"); ok && tokens > 0 {
		// completion_tokens 已包含推理 token，所以直接用。
		sample.OutputTokens = tokens
	} else {
		// 没有 usage 时按 4 字符≈1 token 估算，只用于给出量级。
		sample.OutputTokens = int64((len(stats.text.String()) + stats.reasoningLen) / 4)
		sample.TokensEstimated = true
	}
	// 生成窗口 = 总耗时 - TTFT。首 token 之前的时间属于排队，不计入吞吐。
	generationMs := stats.totalMs - stats.firstTokenMs
	if generationMs > 0 && sample.OutputTokens >= speedMinTokensFor {
		sample.OutputTPS = float64(sample.OutputTokens) * 1000 / float64(generationMs)
	}
	return sample, nil
}

func medianFloat(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]float64(nil), values...)
	for i := 1; i < len(sorted); i++ {
		for j := i; j > 0 && sorted[j] < sorted[j-1]; j-- {
			sorted[j], sorted[j-1] = sorted[j-1], sorted[j]
		}
	}
	middle := len(sorted) / 2
	if len(sorted)%2 == 0 {
		return (sorted[middle-1] + sorted[middle]) / 2
	}
	return sorted[middle]
}

func spreadInt(values []int64) int64 {
	if len(values) == 0 {
		return 0
	}
	low, high := values[0], values[0]
	for _, value := range values {
		low, high = min(low, value), max(high, value)
	}
	return high - low
}

func spreadFloat(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	low, high := values[0], values[0]
	for _, value := range values {
		low, high = min(low, value), max(high, value)
	}
	return high - low
}

// 保留给后续调度器使用：把一次速度测试压缩成可直接落库的两个标量。
func (r SpeedReport) Headline() (ttftMs int64, tps float64, at time.Time) {
	return r.TTFTMs, r.OutputTPS, time.Now()
}
