package relayprobe

import (
	"context"
	"fmt"
	"strings"

	apigxmodel "github.com/QuantumNous/new-api/apigx/model"
	"github.com/QuantumNous/new-api/common"
)

// 上下文窗口：针插式（needle-in-a-haystack）检测。把一串随机口令放在长填充文本的
// 靠前位置，再在末尾提问。偷偷裁剪上下文的中转会把开头丢掉，模型就答不出口令；
// 同时核对 prompt_tokens 是否明显小于实际发送量，可以直接看出裁剪。
//
// 填充量固定在约 3000 token，够触发裁剪又不至于太贵；不是在测模型的真实窗口上限。
const (
	contextNeedle      = "PLUM-7734-ZEBRA"
	contextFillerLine  = "Routine log line: the warehouse inventory was reconciled without incident.\n"
	contextFillerLines = 210
	contextMinTokens   = 1200
)

func runContextWindowCheck(ctx context.Context, target auditTarget) CheckResult {
	var haystack strings.Builder
	haystack.WriteString("Archive of internal notes. Read carefully.\n\n")
	haystack.WriteString("Note 004: the access passphrase is " + contextNeedle + ". Remember it.\n\n")
	for range contextFillerLines {
		haystack.WriteString(contextFillerLine)
	}
	prompt := haystack.String() + "\nWhat is the access passphrase recorded in Note 004? Reply with the passphrase only."
	resp, err := target.chat(ctx, map[string]any{"messages": userMessages("", prompt), "max_tokens": 24, "temperature": 0})
	result := CheckResult{SampleCount: 1, Confidence: "medium"}
	switch {
	case err != nil && resp != nil && resp.HTTPStatus >= 400 && resp.HTTPStatus < 500:
		result.Status = apigxmodel.AuditCheckWarning
		result.Evidence = fmt.Sprintf("约 3000 token 的请求被拒绝（HTTP %d），可用上下文可能远小于模型标称值", resp.HTTPStatus)
		return result
	case err != nil:
		result.Status, result.Confidence = apigxmodel.AuditCheckInconclusive, "low"
		result.Evidence = "模型请求未成功：" + err.Error()
		return result
	}
	result.LatencyMs = resp.LatencyMs
	found := strings.Contains(strings.ToUpper(resp.Content), contextNeedle)
	notes := make([]string, 0, 2)
	truncated := false
	if prompt, _, ok := usageInt(resp.Usage, "prompt_tokens"); ok {
		notes = append(notes, fmt.Sprintf("prompt_tokens=%d", prompt))
		if prompt < contextMinTokens {
			truncated = true
		}
	}
	switch {
	case found && !truncated:
		result.Status = apigxmodel.AuditCheckPassed
		result.Evidence = "长上下文靠前位置的口令被正确取回，未见截断；" + strings.Join(notes, "；")
	case found:
		result.Status = apigxmodel.AuditCheckWarning
		result.Evidence = fmt.Sprintf("口令取回成功，但计费 prompt_tokens 明显低于发送量（阈值 %d），计量或上下文处理存疑；", contextMinTokens) + strings.Join(notes, "；")
	default:
		result.Status, result.Confidence = apigxmodel.AuditCheckWarning, "high"
		result.Evidence = "未取回长上下文靠前位置的口令，中转可能裁剪了上下文；回复：" + truncateRunes(strings.TrimSpace(resp.Content), 48) + "。弱模型也可能检索失败，需复核；" + strings.Join(notes, "；")
	}
	return result
}

// 工具改写：发一个结构固定的 function calling 请求，看中转是否改写了工具定义或
// 调用结果。恶意中转可以替换参数、丢弃工具、或把工具调用降级成普通文本；这里检查
// 函数名、参数值与 finish_reason 是否与发出的定义一致。
const toolProbeName = "get_shipment_status"

func runToolRewriteCheck(ctx context.Context, target auditTarget) CheckResult {
	tools := []map[string]any{{
		"type": "function",
		"function": map[string]any{
			"name":        toolProbeName,
			"description": "Look up the delivery status of one shipment by its tracking id.",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"tracking_id": map[string]any{"type": "string", "description": "The tracking id, e.g. QX-1001"},
				},
				"required":             []string{"tracking_id"},
				"additionalProperties": false,
			},
		},
	}}
	resp, err := target.chat(ctx, map[string]any{
		"messages":    userMessages("Use the provided tool when the user asks about a shipment.", "Where is shipment QX-1001 right now?"),
		"tools":       tools,
		"tool_choice": "auto",
		"max_tokens":  96,
		"temperature": 0,
	})
	result := CheckResult{SampleCount: 1, Confidence: "medium"}
	switch {
	case err != nil && resp != nil && resp.HTTPStatus >= 400 && resp.HTTPStatus < 500:
		result.Status = apigxmodel.AuditCheckWarning
		result.Evidence = fmt.Sprintf("带 tools 的请求被拒绝（HTTP %d），中转可能不转发工具定义", resp.HTTPStatus)
		return result
	case err != nil:
		result.Status, result.Confidence = apigxmodel.AuditCheckInconclusive, "low"
		result.Evidence = "模型请求未成功：" + err.Error()
		return result
	}
	result.LatencyMs = resp.LatencyMs
	calls, finish, parseErr := parseToolCalls(resp.Body)
	if parseErr != nil {
		result.Status, result.Confidence = apigxmodel.AuditCheckInconclusive, "low"
		result.Evidence = "响应结构无法解析，无法核对工具调用"
		return result
	}
	var problems, notes []string
	switch {
	case len(calls) == 0:
		notes = append(notes, "模型未发起工具调用（finish_reason="+finish+"）")
	default:
		for _, call := range calls {
			if call.Function.Name != toolProbeName {
				problems = append(problems, "返回的函数名为 "+truncateRunes(call.Function.Name, 40)+"，与发送的定义不一致")
				continue
			}
			var args map[string]any
			if err := common.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
				problems = append(problems, "工具参数不是合法 JSON")
				continue
			}
			tracking, _ := args["tracking_id"].(string)
			if !strings.Contains(strings.ToUpper(tracking), "QX-1001") {
				problems = append(problems, "工具参数 tracking_id="+truncateRunes(tracking, 32)+"，与请求中的单号不一致（疑似被改写）")
				continue
			}
			if extra := len(args) - 1; extra > 0 {
				notes = append(notes, fmt.Sprintf("工具参数多出 %d 个未定义字段", extra))
			}
			notes = append(notes, "函数名与参数与发送的定义一致")
		}
		if finish != "" && finish != "tool_calls" && finish != "function_call" {
			notes = append(notes, "finish_reason="+finish+"，不是 tool_calls")
		}
	}
	switch {
	case len(problems) > 0:
		result.Status, result.Confidence = apigxmodel.AuditCheckFailed, "high"
		result.Evidence = strings.Join(append(problems, notes...), "；") + "。工具定义在链路中被改写"
	case len(calls) == 0:
		result.Status = apigxmodel.AuditCheckWarning
		result.Evidence = strings.Join(notes, "；") + "，无法确认工具链路；也可能是模型本身不支持 function calling，需复核"
	case len(notes) > 1:
		result.Status = apigxmodel.AuditCheckWarning
		result.Evidence = strings.Join(notes, "；")
	default:
		result.Status = apigxmodel.AuditCheckPassed
		result.Evidence = strings.Join(notes, "；") + "，工具定义原样透传"
	}
	return result
}

type toolCall struct {
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

func parseToolCalls(body []byte) ([]toolCall, string, error) {
	var parsed struct {
		Choices []struct {
			FinishReason string `json:"finish_reason"`
			Message      struct {
				ToolCalls    []toolCall `json:"tool_calls"`
				FunctionCall *struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function_call"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := common.Unmarshal(body, &parsed); err != nil {
		return nil, "", err
	}
	if len(parsed.Choices) == 0 {
		return nil, "", nil
	}
	choice := parsed.Choices[0]
	calls := choice.Message.ToolCalls
	// 兼容仍返回旧版 function_call 的上游。
	if len(calls) == 0 && choice.Message.FunctionCall != nil {
		var legacy toolCall
		legacy.Function.Name = choice.Message.FunctionCall.Name
		legacy.Function.Arguments = choice.Message.FunctionCall.Arguments
		calls = append(calls, legacy)
	}
	return calls, choice.FinishReason, nil
}
