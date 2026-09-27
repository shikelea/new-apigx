package relayprobe

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math"
	"net/http"
	"regexp"
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
}

// postJSON sends one bounded request. Only the relay key is attached; the
// caller decides the path so error probes can hit unknown routes too.
func (t auditTarget) postJSON(ctx context.Context, path, contentType string, body []byte, auth bool) (*chatResponse, error) {
	ctx, cancel := withProbeTimeout(ctx, probeRequestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.base+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Accept", "application/json")
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
			Message struct {
				Content any `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := common.Unmarshal(resp.Body, &parsed); err != nil {
		return resp, fmt.Errorf("响应不是合法 JSON")
	}
	resp.ID, resp.Model, resp.Usage = parsed.ID, parsed.Model, parsed.Usage
	if len(parsed.Choices) > 0 {
		resp.Content = contentText(parsed.Choices[0].Message.Content)
	}
	return resp, nil
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
