package relayprobe

import (
	"context"
	"fmt"
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
// 采样 3 次取中位数，同时给出极差；单次数值受机房位置和瞬时负载影响太大，中位数加
// 离散度才有参考意义。伪流式（整段一次返回）会让 TTFT 接近总耗时、TPS 虚高，所以
// 这里同时标注分片数。
const (
	speedSamples      = 3
	speedPrompt       = "Write a single paragraph of about 120 words explaining what a load balancer does. Plain prose, no lists."
	speedMaxTokens    = 200
	speedMinChunks    = 3
	speedMinTokensFor = 16
)

type SpeedSample struct {
	TTFTMs          int64   `json:"ttft_ms"`
	TotalMs         int64   `json:"total_ms"`
	OutputTokens    int64   `json:"output_tokens"`
	OutputTPS       float64 `json:"output_tps"`
	ContentChunks   int     `json:"content_chunks"`
	ReasoningChunks int     `json:"reasoning_chunks"`
	TokensEstimated bool    `json:"tokens_estimated"`
}

type SpeedReport struct {
	Model         string        `json:"model"`
	Samples       []SpeedSample `json:"samples"`
	TTFTMs        int64         `json:"ttft_ms"`
	TTFTSpreadMs  int64         `json:"ttft_spread_ms"`
	OutputTPS     float64       `json:"output_tps"`
	TPSSpread     float64       `json:"tps_spread"`
	Pseudostream  bool          `json:"pseudostream"`
	TokensExact   bool          `json:"tokens_exact"`
	Note          string        `json:"note"`
	FailedSamples int           `json:"failed_samples"`
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
	ctx, cancel := withProbeTimeout(ctx, speedSamples*streamProbeTimeout)
	defer cancel()
	report := SpeedReport{Model: target.model, TokensExact: true, Disclaimer: SpeedDisclaimer}
	var lastErr error
	for range speedSamples {
		sample, err := target.speedSample(ctx)
		if err != nil {
			report.FailedSamples++
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
	ttfts := make([]int64, 0, len(report.Samples))
	rates := make([]float64, 0, len(report.Samples))
	chunks, reasoningOnly := 0, 0
	for _, sample := range report.Samples {
		ttfts = append(ttfts, sample.TTFTMs)
		rates = append(rates, sample.OutputTPS)
		// 伪流式看的是「输出有没有被分片」，推理分片也算，否则推理模型会被误判。
		chunks += sample.ContentChunks + sample.ReasoningChunks
		if sample.ContentChunks == 0 && sample.ReasoningChunks > 0 {
			reasoningOnly++
		}
	}
	report.TTFTMs, _, _ = latencyStats(ttfts)
	report.TTFTSpreadMs = spreadInt(ttfts)
	report.OutputTPS = medianFloat(rates)
	report.TPSSpread = spreadFloat(rates)
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
	report.Note = strings.Join(notes, "；")
	return report, nil
}

// speedSample 跑一次流式请求，分别记录首个含内容分片的时刻与全流结束的时刻。
func (t auditTarget) speedSample(ctx context.Context) (SpeedSample, error) {
	stats, status, err := t.streamPrompt(ctx, speedPrompt, speedMaxTokens)
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
