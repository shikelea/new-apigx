package relayprobe

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	apigxmodel "github.com/QuantumNous/new-api/apigx/model"
	"github.com/QuantumNous/new-api/common"
)

const (
	streamPrompt    = "Count from 1 to 40, separated by single spaces. Output only the numbers."
	streamMaxTokens = 160
	streamCountTo   = 40
)

type streamStats struct {
	contentType   string
	dataEvents    int
	contentChunks int
	invalidJSON   int
	wrongObject   int
	nonSSELines   int
	afterDone     int
	done          bool
	usage         map[string]any
	text          strings.Builder
	// reasoningChunks 单独计数：推理模型会先吐一长串 reasoning_content 才给正文，
	// 这些分片同样是真实输出，用来判断上游是否在流、以及首 token 何时到达。
	reasoningChunks int
	reasoningLen    int
	firstTokenMs    int64
	firstChunkMs    int64
	totalMs         int64
	gaps            []int64
	truncatedBytes  bool
}

func runSSEIntegrityCheck(ctx context.Context, target auditTarget) CheckResult {
	stats, status, err := target.stream(ctx)
	if err != nil {
		return CheckResult{Status: apigxmodel.AuditCheckInconclusive, Evidence: "流式请求未成功：" + err.Error(), Confidence: "low", SampleCount: 1}
	}
	if status < 200 || status >= 300 {
		return CheckResult{Status: apigxmodel.AuditCheckInconclusive, Evidence: fmt.Sprintf("流式请求返回 HTTP %d", status), Confidence: "low", SampleCount: 1}
	}
	var failures, warnings []string
	if !strings.HasPrefix(strings.ToLower(stats.contentType), "text/event-stream") {
		failures = append(failures, "Content-Type 不是 text/event-stream（"+truncateRunes(stats.contentType, 40)+"）")
	}
	if stats.dataEvents == 0 {
		failures = append(failures, "没有收到任何 data 事件")
	}
	if stats.invalidJSON > 0 {
		failures = append(failures, fmt.Sprintf("%d 个 data 事件不是合法 JSON", stats.invalidJSON))
	}
	if !stats.done && !stats.truncatedBytes {
		failures = append(failures, "流未以 data: [DONE] 结束")
	}
	if stats.wrongObject > 0 {
		warnings = append(warnings, fmt.Sprintf("%d 个事件的 object 不是 chat.completion.chunk", stats.wrongObject))
	}
	if stats.afterDone > 0 {
		warnings = append(warnings, "[DONE] 之后仍有数据")
	}
	if stats.nonSSELines > 0 {
		warnings = append(warnings, fmt.Sprintf("%d 行不符合 SSE 格式", stats.nonSSELines))
	}
	if len(stats.usage) == 0 {
		warnings = append(warnings, "请求了 include_usage 但流中没有 usage")
	} else {
		prompt, _, promptOK := usageInt(stats.usage, "prompt_tokens")
		completion, _, completionOK := usageInt(stats.usage, "completion_tokens")
		total, totalPresent, totalOK := usageInt(stats.usage, "total_tokens")
		if !promptOK || !completionOK || (totalPresent && (!totalOK || total != prompt+completion)) {
			failures = append(failures, "流式 usage 字段不是整数或加和不一致")
		}
	}
	if missing := missingSequence(stats.text.String(), streamCountTo); missing > 5 {
		warnings = append(warnings, fmt.Sprintf("计数内容缺失或乱序 %d 处（可能被截断或改写）", missing))
	}
	p95 := percentile(stats.gaps, 95)
	simulated := stats.totalMs > 0 && stats.contentChunks >= 3 && float64(stats.firstChunkMs) > 0.8*float64(stats.totalMs) && p95 < 5
	if simulated {
		warnings = append(warnings, "疑似伪流式：首包占总耗时 80% 以上且后续分片几乎同时到达")
	} else if stats.contentChunks > 0 && stats.contentChunks <= 2 && stats.text.Len() > 60 {
		warnings = append(warnings, "疑似伪流式：整段内容只分 1–2 个分片返回")
	}
	metrics := fmt.Sprintf("首包 %d ms，总耗时 %d ms，内容分片 %d，p95 间隔 %d ms", stats.firstChunkMs, stats.totalMs, stats.contentChunks, p95)
	result := CheckResult{SampleCount: 1, LatencyMs: stats.totalMs, Confidence: "medium"}
	switch {
	case len(failures) > 0:
		result.Status, result.Confidence = apigxmodel.AuditCheckFailed, "high"
		result.Evidence = strings.Join(append(failures, warnings...), "；") + "；" + metrics
	case len(warnings) > 0:
		result.Status = apigxmodel.AuditCheckWarning
		result.Evidence = strings.Join(warnings, "；") + "；" + metrics + "。CDN 缓冲也可能造成聚合，需复核"
	default:
		result.Status = apigxmodel.AuditCheckPassed
		result.Evidence = "事件格式、结束标记与 usage 均正常；" + metrics
	}
	return result
}

func (t auditTarget) stream(ctx context.Context) (*streamStats, int, error) {
	return t.streamPrompt(ctx, streamPrompt, streamMaxTokens)
}

// streamPrompt 收集一次流式响应的格式与时序统计，供流式完整性与速度测试共用。
func (t auditTarget) streamPrompt(ctx context.Context, prompt string, maxTokens int) (*streamStats, int, error) {
	payload := map[string]any{
		"model": t.model, "stream": true, "stream_options": map[string]any{"include_usage": true},
		"messages": userMessages("", prompt), "max_tokens": maxTokens, "temperature": 0,
	}
	body, err := common.Marshal(payload)
	if err != nil {
		return nil, 0, err
	}
	ctx, cancel := withProbeTimeout(ctx, streamProbeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.base+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Authorization", "Bearer "+t.key)
	started := time.Now()
	resp, err := t.client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("连接失败或超时")
	}
	defer resp.Body.Close()
	stats := &streamStats{contentType: resp.Header.Get("Content-Type")}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return stats, resp.StatusCode, nil
	}
	limited := &io.LimitedReader{R: resp.Body, N: maxStreamBytes}
	scanner := bufio.NewScanner(limited)
	scanner.Buffer(make([]byte, 0, 64<<10), 256<<10)
	var last time.Time
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r")
		switch {
		case line == "" || strings.HasPrefix(line, ":") || strings.HasPrefix(line, "event:") || strings.HasPrefix(line, "id:") || strings.HasPrefix(line, "retry:"):
			continue
		case !strings.HasPrefix(line, "data:"):
			stats.nonSSELines++
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if stats.done {
			stats.afterDone++
			continue
		}
		if data == "[DONE]" {
			stats.done = true
			continue
		}
		stats.dataEvents++
		var chunk struct {
			Object  string         `json:"object"`
			Usage   map[string]any `json:"usage"`
			Choices []struct {
				Delta struct {
					Content          any `json:"content"`
					ReasoningContent any `json:"reasoning_content"`
					Reasoning        any `json:"reasoning"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if err := common.Unmarshal([]byte(data), &chunk); err != nil {
			stats.invalidJSON++
			continue
		}
		if chunk.Object != "" && chunk.Object != "chat.completion.chunk" {
			stats.wrongObject++
		}
		if len(chunk.Usage) > 0 {
			stats.usage = chunk.Usage
		}
		for _, choice := range chunk.Choices {
			reasoning := contentText(choice.Delta.ReasoningContent)
			if reasoning == "" {
				reasoning = contentText(choice.Delta.Reasoning)
			}
			if reasoning != "" {
				stats.reasoningChunks++
				stats.reasoningLen += len(reasoning)
				if stats.firstTokenMs == 0 {
					stats.firstTokenMs = time.Since(started).Milliseconds()
				}
			}
			text := contentText(choice.Delta.Content)
			if text == "" {
				continue
			}
			now := time.Now()
			if stats.contentChunks == 0 {
				stats.firstChunkMs = now.Sub(started).Milliseconds()
			} else {
				stats.gaps = append(stats.gaps, now.Sub(last).Milliseconds())
			}
			if stats.firstTokenMs == 0 {
				stats.firstTokenMs = stats.firstChunkMs
			}
			last = now
			stats.contentChunks++
			stats.text.WriteString(text)
		}
	}
	stats.truncatedBytes = limited.N <= 0
	stats.totalMs = time.Since(started).Milliseconds()
	if err := scanner.Err(); err != nil && !stats.done {
		return stats, resp.StatusCode, fmt.Errorf("读取流失败")
	}
	return stats, resp.StatusCode, nil
}

// missingSequence counts how many of 1..n are absent from the streamed text.
func missingSequence(text string, n int) int {
	seen := make(map[int]bool, n)
	for _, field := range strings.FieldsFunc(text, func(r rune) bool { return r < '0' || r > '9' }) {
		if value, err := strconv.Atoi(field); err == nil {
			seen[value] = true
		}
	}
	missing := 0
	for i := 1; i <= n; i++ {
		if !seen[i] {
			missing++
		}
	}
	return missing
}

func percentile(values []int64, p int) int64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]int64(nil), values...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	index := (len(sorted)*p + 99) / 100
	if index < 1 {
		index = 1
	}
	return sorted[index-1]
}
