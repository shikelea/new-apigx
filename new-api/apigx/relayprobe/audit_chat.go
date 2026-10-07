package relayprobe

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
)

type chatResponse struct {
	HTTPStatus int
	Header     http.Header
	Body       []byte
	ID         string
	Model      string
	Content    string
	Usage      map[string]any
	LatencyMs  int64
	// FinishReason 让调用方区分「答错」和「被 max_tokens 截断」。推理模型常把整个
	// 预算花在思维链上，正文一个字都没来，那不是答错。
	FinishReason string
}

// requestTimeoutFor scales the deadline with the requested output budget.
//
// The flat 20 s ceiling fits the short audit probes, but a benchmark that asks
// for 1600 tokens from a reasoning model routinely needs longer, and cutting it
// off surfaces as "连接失败或超时" — indistinguishable from a dead relay. The
// budget is read back out of the marshalled body so callers do not each have to
// thread a timeout through.
func requestTimeoutFor(body []byte) time.Duration {
	var payload struct {
		MaxTokens       int `json:"max_tokens"`
		MaxOutputTokens int `json:"max_output_tokens"` // Responses 原生接口的字段名。
	}
	if err := common.Unmarshal(body, &payload); err != nil {
		return probeRequestTimeout
	}
	payload.MaxTokens = max(payload.MaxTokens, payload.MaxOutputTokens)
	if payload.MaxTokens <= 0 {
		return probeRequestTimeout
	}
	// 约 6 token/s 的保守下限，再留 15 秒给排队和首 token。
	timeout := time.Duration(payload.MaxTokens/6)*time.Second + 15*time.Second
	if timeout < probeRequestTimeout {
		return probeRequestTimeout
	}
	if timeout > maxRequestTimeout {
		return maxRequestTimeout
	}
	return timeout
}

// postJSON sends one bounded request. Only the relay key is attached; the
// caller decides the path so error probes can hit unknown routes too.
func (t auditTarget) postJSON(ctx context.Context, path, contentType string, body []byte, auth bool) (*chatResponse, error) {
	return t.postJSONWithHeaders(ctx, path, contentType, body, auth, nil)
}

// postJSONWithHeaders 同 postJSON，另外附加调用方给的请求头（原生协议的 User-Agent、
// anthropic-version 等）。extra 里不应该放凭据，凭据只由 auth 控制。
func (t auditTarget) postJSONWithHeaders(ctx context.Context, path, contentType string, body []byte, auth bool, extra map[string]string) (*chatResponse, error) {
	ctx, cancel := withProbeTimeout(ctx, requestTimeoutFor(body))
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.base+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Accept", "application/json")
	for name, value := range extra {
		req.Header.Set(name, value)
	}
	if auth && t.key != "" {
		req.Header.Set("Authorization", "Bearer "+t.key)
	}
	started := time.Now()
	resp, err := t.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("连接失败或超时")
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("读取响应失败")
	}
	return &chatResponse{HTTPStatus: resp.StatusCode, Header: resp.Header, Body: data, LatencyMs: time.Since(started).Milliseconds()}, nil
}

// chat sends a non-streaming chat completion for the audited model.
func (t auditTarget) chat(ctx context.Context, payload map[string]any) (*chatResponse, error) {
	payload["model"] = t.model
	payload["stream"] = false
	body, err := common.Marshal(payload)
	if err != nil {
		return nil, err
	}
	resp, err := t.postJSON(ctx, "/v1/chat/completions", "application/json", body, true)
	if err != nil {
		return nil, err
	}
	if resp.HTTPStatus < 200 || resp.HTTPStatus >= 300 {
		return resp, fmt.Errorf("上游返回 HTTP %d", resp.HTTPStatus)
	}
	var parsed struct {
		ID      string         `json:"id"`
		Model   string         `json:"model"`
		Usage   map[string]any `json:"usage"`
		Choices []struct {
			FinishReason string `json:"finish_reason"`
			Message      struct {
				Content   any    `json:"content"`
				Refusal   string `json:"refusal"`
				ToolCalls []any  `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := common.Unmarshal(resp.Body, &parsed); err != nil {
		return resp, fmt.Errorf("响应不是合法 JSON")
	}
	resp.ID, resp.Model, resp.Usage = parsed.ID, parsed.Model, parsed.Usage
	if len(parsed.Choices) > 0 {
		message := parsed.Choices[0].Message
		resp.Content = contentText(message.Content)
		resp.FinishReason = parsed.Choices[0].FinishReason
		// 对应 protocol.py：拒答、工具调用都不是正常作答，不能被当作空 finish_reason 放行。
		switch {
		case message.Refusal != "":
			resp.FinishReason = "refusal"
		case len(message.ToolCalls) > 0 && resp.FinishReason != "length":
			resp.FinishReason = "tool_calls"
		}
	}
	return resp, nil
}

// chatWithRetry sends a non-streaming chat request with exponential backoff on 429 / 5xx errors.
func (t auditTarget) chatWithRetry(ctx context.Context, payload map[string]any, maxRetries int) (*chatResponse, error) {
	for attempt := 0; attempt <= maxRetries; attempt++ {
		resp, err := t.chat(ctx, payload)
		if err == nil {
			return resp, nil
		}
		if ctx.Err() != nil {
			return resp, err
		}
		if resp != nil && (resp.HTTPStatus == http.StatusTooManyRequests || resp.HTTPStatus >= 500) && attempt < maxRetries {
			backoff := exponentialBackoff(attempt, time.Second, 4*time.Second)
			if !sleepContext(ctx, withRetryAfter(resp, backoff)) {
				return resp, err
			}
			continue
		}
		return resp, err
	}
	return nil, fmt.Errorf("重试次数耗尽")
}

// retryAfterCeiling 是「照上游说的等」的上限。上游把等待写成几分钟时宁可让这一格失败，
// 也不能把整条检测的预算睡光。做成变量只为测试不必真等。
var retryAfterCeiling = 30 * time.Second

// exponentialBackoff 算重试等待的指数部分。attempt 大到 6 就封顶：移位继续往上涨既撑不住
// 时限，也会在溢出后变成 0、反而越试越快。
func exponentialBackoff(attempt int, step, ceiling time.Duration) time.Duration {
	shift := min(attempt, 6)
	return min(step<<uint(shift), ceiling)
}

// withRetryAfter 在上游回了 Retry-After 时以它为准（与指数退避取较大值），否则沿用调用方
// 算好的退避。两种写法都支持：秒数与 HTTP-date。
//
// 2026-10-04 实测：中转限流时明确回 `Retry-After: 18`，而这里的退避上限原本只有 2 秒，
// 每次重试都正好撞回同一个限流窗口——340 格的高档基准包丢掉四成样本，型号指纹只能判证据
// 不足，逻辑/层级/流式三个维度一个样本都没采到。等待超过剩余预算时 sleepContext 会立刻返回，
// 所以这里不需要额外掐时限。
func withRetryAfter(resp *chatResponse, backoff time.Duration) time.Duration {
	if resp == nil {
		return backoff
	}
	raw := strings.TrimSpace(resp.Header.Get("Retry-After"))
	if raw == "" {
		return backoff
	}
	var wait time.Duration
	switch seconds, err := strconv.Atoi(raw); {
	case err == nil:
		if seconds < 0 {
			return backoff
		}
		wait = time.Duration(seconds) * time.Second
	default:
		when, parseErr := http.ParseTime(raw)
		if parseErr != nil {
			return backoff
		}
		if remaining := time.Until(when); remaining > 0 {
			wait = remaining
		}
	}
	if wait <= backoff {
		return backoff
	}
	return min(wait, retryAfterCeiling)
}

func userMessages(system, user string) []map[string]any {
	messages := make([]map[string]any, 0, 2)
	if system != "" {
		messages = append(messages, map[string]any{"role": "system", "content": system})
	}
	return append(messages, map[string]any{"role": "user", "content": user})
}

// contentText accepts both the string form and the content-part array form.
func contentText(content any) string {
	switch value := content.(type) {
	case string:
		return value
	case []any:
		parts := make([]string, 0, len(value))
		for _, part := range value {
			if item, ok := part.(map[string]any); ok {
				if text, ok := item["text"].(string); ok {
					parts = append(parts, text)
				}
			}
		}
		return strings.Join(parts, "")
	default:
		return ""
	}
}

// usageInt returns a usage counter only when it is a real non-negative
// integer. Strings, booleans and fractional numbers are reported as invalid
// because honest upstreams never emit them.
func usageInt(usage map[string]any, key string) (value int64, present, valid bool) {
	raw, ok := usage[key]
	if !ok || raw == nil {
		return 0, false, false
	}
	number, ok := raw.(float64)
	if !ok || number < 0 || number != math.Trunc(number) || number > 1e12 {
		return 0, true, false
	}
	return int64(number), true, true
}

var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`sk-[A-Za-z0-9\-_]{20,}`),
	regexp.MustCompile(`Bearer\s+[A-Za-z0-9\-._~+/]{20,}=*`),
	regexp.MustCompile(`(?:AKIA|ASIA)[0-9A-Z]{16}`),
	regexp.MustCompile(`AIza[0-9A-Za-z\-_]{35}`),
	regexp.MustCompile(`ya29\.[A-Za-z0-9_.~+/\-]{20,}`),
	regexp.MustCompile(`\beyJ[A-Za-z0-9_\-]{10,}\.[A-Za-z0-9_\-]+\.[A-Za-z0-9_\-]*`),
	regexp.MustCompile(`-----BEGIN[A-Z \-]*PRIVATE KEY-----`),
	// RE2 has no lookbehind: match "scheme://user:pass@" and mask the credentials.
	regexp.MustCompile(`://[^\s'"/@:]+:[^\s'"@/]+@`),
}

// maskSecrets removes the caller's own key and anything that looks like a
// credential before evidence is stored or returned.
func maskSecrets(text, key string) string {
	if key = strings.TrimSpace(key); len(key) >= 6 {
		text = strings.ReplaceAll(text, key, "[本次检测 Key]")
	}
	for _, pattern := range secretPatterns {
		text = pattern.ReplaceAllStringFunc(text, maskToken)
	}
	return text
}

func maskToken(token string) string {
	runes := []rune(token)
	if len(runes) <= 6 {
		return "***"
	}
	return string(runes[:4]) + "***"
}
