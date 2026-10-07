package relayprobe

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/apigx/modelfp"
	"github.com/QuantumNous/new-api/common"
)

// 原生协议基准包的请求头，与参考项目 protocol.py / transport.py 一致：基准就是用这两个
// 客户端画像采的，部分上游会按 User-Agent 分流，画像不同分布就不可比。
const (
	meowGPTUserAgent       = "Codex Desktop/0.147.0-alpha.1.2 (Windows 10.0.26200; x86_64) unknown (codex_exec; 0.147.0-alpha.1.2)"
	meowClaudeUserAgent    = "claude-cli/2.1.251 (external, cli)"
	meowAnthropicVersion   = "2023-06-01"
	meowResponsesPath      = "/v1/responses"
	meowMessagesPath       = "/v1/messages"
	meowChatCompletionPath = "/v1/chat/completions"
)

// errNativeShape 表示上游 2xx 了，但回的不是该原生协议的响应结构——多半是中转没实现
// 这个接口、把请求转成了别的东西。
var errNativeShape = errors.New("响应不是该原生协议的格式")

// meowRequest 按基准包的协议发一道题，返回统一的 chatResponse（Content 为正文，
// FinishReason 为 "stop" 表示正常结束）。请求体对应参考项目 build_payload 的三种 mode，
// 只是改成非流式。withEffort=false 时去掉推理强度相关字段。
func (t auditTarget) meowRequest(ctx context.Context, mode string, cell modelfp.MeowCell, withEffort bool) (*chatResponse, error) {
	switch mode {
	case modelfp.MeowModeGPT:
		payload := map[string]any{
			"model": t.model, "stream": false, "store": false,
			"input":             userMessages(cell.System, cell.Prompt),
			"max_output_tokens": cell.MaxOutputTokens,
		}
		if withEffort {
			payload["reasoning"] = map[string]any{"effort": cell.Effort}
		}
		return t.nativeRequest(ctx, meowResponsesPath, payload, map[string]string{"User-Agent": meowGPTUserAgent}, parseResponsesBody)
	case modelfp.MeowModeClaude:
		payload := map[string]any{
			"model": t.model, "stream": false, "system": cell.System,
			"messages":   []map[string]any{{"role": "user", "content": cell.Prompt}},
			"max_tokens": cell.MaxOutputTokens,
		}
		if withEffort {
			payload["thinking"] = map[string]any{"type": "adaptive"}
			payload["output_config"] = map[string]any{"effort": cell.Effort}
		}
		// 参考实现只发 Bearer；多带一个 x-api-key 是为了兼容只认 Anthropic 头的中转，不影响模型输出。
		headers := map[string]string{"User-Agent": meowClaudeUserAgent, "anthropic-version": meowAnthropicVersion}
		if t.key != "" {
			headers["x-api-key"] = t.key
		}
		return t.nativeRequest(ctx, meowMessagesPath, payload, headers, parseMessagesBody)
	default:
		payload := map[string]any{
			"messages":          userMessages(cell.System, cell.Prompt),
			cell.ChatTokenField: cell.MaxOutputTokens,
		}
		if withEffort {
			payload["reasoning_effort"] = cell.Effort
		}
		return t.chat(ctx, payload)
	}
}

func (t auditTarget) nativeRequest(ctx context.Context, path string, payload map[string]any, headers map[string]string,
	parse func(*chatResponse) error) (*chatResponse, error) {
	body, err := common.Marshal(payload)
	if err != nil {
		return nil, err
	}
	resp, err := t.postJSONWithHeaders(ctx, path, "application/json", body, true, headers)
	if err != nil {
		return nil, err
	}
	if resp.HTTPStatus < 200 || resp.HTTPStatus >= 300 {
		return resp, fmt.Errorf("上游返回 HTTP %d", resp.HTTPStatus)
	}
	if err := parse(resp); err != nil {
		return resp, err
	}
	return resp, nil
}

// parseResponsesBody 对应 StreamParser 的 gpt 分支：status=completed 才算正常结束，
// 正文取 output 里 message 的 output_text；出现 refusal 算拒答。
func parseResponsesBody(resp *chatResponse) error {
	var parsed struct {
		ID                string         `json:"id"`
		Object            string         `json:"object"`
		Model             string         `json:"model"`
		Status            string         `json:"status"`
		Usage             map[string]any `json:"usage"`
		OutputText        *string        `json:"output_text"`
		IncompleteDetails *struct {
			Reason string `json:"reason"`
		} `json:"incomplete_details"`
		Output []struct {
			Type    string `json:"type"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
	}
	if err := common.Unmarshal(resp.Body, &parsed); err != nil {
		return errNativeShape
	}
	if parsed.Status == "" || (parsed.Object != "" && parsed.Object != "response") {
		return errNativeShape
	}
	resp.ID, resp.Model, resp.Usage = parsed.ID, parsed.Model, parsed.Usage
	var parts []string
	refused := false
	for _, item := range parsed.Output {
		for _, part := range item.Content {
			switch part.Type {
			case "output_text":
				parts = append(parts, part.Text)
			case "refusal":
				refused = true
			}
		}
	}
	// 参考实现只在 output 缺失时才退回别处的文本；这里对应 SDK 的 output_text 便捷字段。
	if len(parsed.Output) == 0 && parsed.OutputText != nil {
		parts = append(parts, *parsed.OutputText)
	}
	resp.Content = strings.Join(parts, "")
	switch {
	case refused:
		resp.FinishReason = "refusal"
	case parsed.Status == "completed":
		resp.FinishReason = "stop"
	case parsed.IncompleteDetails != nil && parsed.IncompleteDetails.Reason == "max_output_tokens":
		resp.FinishReason = "length"
	case parsed.IncompleteDetails != nil && parsed.IncompleteDetails.Reason != "":
		resp.FinishReason = parsed.IncompleteDetails.Reason
	default:
		resp.FinishReason = parsed.Status
	}
	return nil
}

// parseMessagesBody 对应 StreamParser 的 claude 分支：stop_reason 为 end_turn /
// stop_sequence 才算正常结束，出现工具调用直接判失败。
func parseMessagesBody(resp *chatResponse) error {
	var parsed struct {
		ID         string         `json:"id"`
		Type       string         `json:"type"`
		Model      string         `json:"model"`
		StopReason string         `json:"stop_reason"`
		Usage      map[string]any `json:"usage"`
		Content    []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := common.Unmarshal(resp.Body, &parsed); err != nil || parsed.Type != "message" {
		return errNativeShape
	}
	resp.ID, resp.Model, resp.Usage = parsed.ID, parsed.Model, parsed.Usage
	var parts []string
	tool := false
	for _, block := range parsed.Content {
		switch block.Type {
		case "text":
			parts = append(parts, block.Text)
		case "tool_use", "server_tool_use":
			tool = true
		}
	}
	resp.Content = strings.Join(parts, "")
	switch {
	case tool:
		resp.FinishReason = "tool_use"
	case parsed.StopReason == "end_turn" || parsed.StopReason == "stop_sequence":
		resp.FinishReason = "stop"
	case parsed.StopReason == "max_tokens":
		resp.FinishReason = "length"
	case parsed.StopReason == "":
		resp.FinishReason = "incomplete"
	default:
		resp.FinishReason = parsed.StopReason
	}
	return nil
}

// meowEffortRejected 判断错误是否因推理强度参数引起：这种情况去掉参数重发，不算失败。
// 检查 HTTP 400/422/500 且响应体包含相关关键词，或响应体明确提到参数错误。
func meowEffortRejected(mode string, resp *chatResponse) bool {
	if resp == nil {
		return false
	}
	// 扩展到 400/422/500，因为有些上游用 500 报告参数错误
	status := resp.HTTPStatus
	if status != http.StatusBadRequest && status != http.StatusUnprocessableEntity && status != http.StatusInternalServerError {
		return false
	}
	body := strings.ToLower(string(resp.Body))
	keywords := []string{"reasoning"}
	if mode == modelfp.MeowModeClaude {
		keywords = []string{"thinking", "output_config", "effort"}
	}
	// 检查是否提到相关参数
	for _, keyword := range keywords {
		if strings.Contains(body, keyword) {
			return true
		}
	}
	// 额外检查常见的参数错误提示
	paramErrors := []string{"unknown parameter", "invalid parameter", "unexpected parameter",
		"不支持的参数", "未知参数", "参数错误", "invalid field", "unknown field"}
	for _, hint := range paramErrors {
		if strings.Contains(body, hint) {
			return true
		}
	}
	return false
}

// meowNativeUnsupported 判断原生接口是不是中转压根没开：路由不存在、方法不允许、
// 回了别的协议的结构，或者 400/422 里明说不支持。鉴权失败、限流、5xx 不算——那是
// 临时状况或 Key 的问题，换协议也解决不了。
func meowNativeUnsupported(resp *chatResponse, err error) bool {
	if errors.Is(err, errNativeShape) {
		return true
	}
	if resp == nil {
		return false
	}
	switch resp.HTTPStatus {
	case http.StatusNotFound, http.StatusMethodNotAllowed, http.StatusNotImplemented:
		return true
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		body := strings.ToLower(string(resp.Body))
		for _, hint := range []string{"not support", "unsupported", "no route", "invalid url", "not found", "不支持", "未开放", "not implemented"} {
			if strings.Contains(body, hint) {
				return true
			}
		}
	}
	return false
}
