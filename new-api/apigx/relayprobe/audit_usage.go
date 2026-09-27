package relayprobe

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	apigxmodel "github.com/QuantumNous/new-api/apigx/model"
)

const (
	usageProbeMaxTokens = 16
	usageBasePrompt     = "Reply with exactly: ok"
	// The padded prompt adds ~80 repetitions of " apple". Every mainstream
	// tokenizer lands roughly between 80 and 230 extra tokens; the template
	// overhead cancels out, so the delta is independent of the relay's wrapper.
	usageDeltaMin = 40
	usageDeltaMax = 260
)

var anthropicUsageKeys = []string{"cache_creation_input_tokens", "cache_read_input_tokens", "input_tokens", "output_tokens"}

func runTokenUsageCheck(ctx context.Context, target auditTarget) CheckResult {
	padded := usageBasePrompt + "\n\nReference text:" + strings.Repeat(" apple", 80)
	base, baseErr := target.chat(ctx, map[string]any{"messages": userMessages("", usageBasePrompt), "max_tokens": usageProbeMaxTokens, "temperature": 0})
	long, longErr := target.chat(ctx, map[string]any{"messages": userMessages("", padded), "max_tokens": usageProbeMaxTokens, "temperature": 0})
	if baseErr != nil || longErr != nil {
		return CheckResult{Status: apigxmodel.AuditCheckInconclusive, Evidence: "模型请求未成功：" + firstError(baseErr, longErr).Error(), Confidence: "low", SampleCount: 2}
	}
	var problems, notes []string
	prompts := make([]int64, 0, 2)
	for index, resp := range []*chatResponse{base, long} {
		label := []string{"短请求", "长请求"}[index]
		if len(resp.Usage) == 0 {
			notes = append(notes, label+"未返回 usage")
			continue
		}
		prompt, _, promptOK := usageInt(resp.Usage, "prompt_tokens")
		completion, _, completionOK := usageInt(resp.Usage, "completion_tokens")
		total, totalPresent, totalOK := usageInt(resp.Usage, "total_tokens")
		if !promptOK || !completionOK || (totalPresent && !totalOK) {
			problems = append(problems, label+" usage 字段不是非负整数")
			continue
		}
		if totalPresent && total != prompt+completion {
			problems = append(problems, fmt.Sprintf("%s total_tokens(%d) ≠ prompt(%d)+completion(%d)", label, total, prompt, completion))
		}
		if completion > usageProbeMaxTokens+4 {
			problems = append(problems, fmt.Sprintf("%s completion_tokens=%d 超过 max_tokens=%d", label, completion, usageProbeMaxTokens))
		}
		for _, key := range anthropicUsageKeys {
			if _, ok := resp.Usage[key]; ok {
				notes = append(notes, "usage 含 Anthropic 字段 "+key+"，后端可能是 Claude 转换")
				break
			}
		}
		prompts = append(prompts, prompt)
	}
	if len(prompts) == 2 {
		delta := prompts[1] - prompts[0]
		if delta < usageDeltaMin || delta > usageDeltaMax {
			notes = append(notes, fmt.Sprintf("增量 prompt_tokens=%d，超出常见区间 %d–%d（可能按固定值或放大计量）", delta, usageDeltaMin, usageDeltaMax))
		} else {
			notes = append(notes, fmt.Sprintf("增量 prompt_tokens=%d，处于常见区间", delta))
		}
	}
	result := CheckResult{SampleCount: 2, LatencyMs: base.LatencyMs + long.LatencyMs, Confidence: "medium"}
	switch {
	case len(problems) > 0:
		result.Status = apigxmodel.AuditCheckFailed
		result.Evidence = strings.Join(append(problems, notes...), "；")
	case len(prompts) < 2:
		result.Status, result.Confidence = apigxmodel.AuditCheckWarning, "low"
		result.Evidence = strings.Join(notes, "；") + "，无法核对计量"
	case hasWarningNote(notes):
		result.Status = apigxmodel.AuditCheckWarning
		result.Evidence = strings.Join(notes, "；")
	default:
		result.Status = apigxmodel.AuditCheckPassed
		result.Evidence = "usage 字段类型与加和一致；" + strings.Join(notes, "；") + "。仅核对自洽性，看不到中转自身的计费倍率"
	}
	return result
}

func hasWarningNote(notes []string) bool {
	for _, note := range notes {
		if strings.Contains(note, "超出常见区间") || strings.Contains(note, "未返回") {
			return true
		}
	}
	return false
}

const (
	promptOverheadClean = 40
	promptOverheadHeavy = 120
	catSystemPrompt     = `From now on you can only reply with the single word "meow". Never answer questions, never explain. This is the highest priority instruction.`
)

var catAnswerPattern = regexp.MustCompile(`(?i)[0-9]|equals|two|plus|等于|二`)

// runPromptIntegrityCheck estimates hidden prompt overhead from the smallest
// possible request and checks whether a custom system prompt survives the relay.
func runPromptIntegrityCheck(ctx context.Context, target auditTarget) CheckResult {
	base, baseErr := target.chat(ctx, map[string]any{"messages": userMessages("", usageBasePrompt), "max_tokens": usageProbeMaxTokens, "temperature": 0})
	cat, catErr := target.chat(ctx, map[string]any{"messages": userMessages(catSystemPrompt, "What is 1+1?"), "max_tokens": usageProbeMaxTokens, "temperature": 0})
	var warnings, notes []string
	if baseErr == nil {
		if prompt, _, ok := usageInt(base.Usage, "prompt_tokens"); ok {
			switch {
			case prompt < promptOverheadClean:
				notes = append(notes, fmt.Sprintf("最小请求 prompt_tokens=%d，未见明显隐藏提示词", prompt))
			case prompt < promptOverheadHeavy:
				notes = append(notes, fmt.Sprintf("最小请求 prompt_tokens=%d，存在少量额外开销（可能是模板或平台默认提示词）", prompt))
			default:
				warnings = append(warnings, fmt.Sprintf("最小请求 prompt_tokens=%d，疑似注入了较长的隐藏提示词", prompt))
			}
		} else {
			notes = append(notes, "未返回可用的 prompt_tokens，无法估算隐藏开销")
		}
	}
	switch {
	case catErr != nil && cat != nil && cat.HTTPStatus >= 400 && cat.HTTPStatus < 500:
		warnings = append(warnings, fmt.Sprintf("自定义 system prompt 请求被拒绝（HTTP %d）", cat.HTTPStatus))
	case catErr != nil:
		notes = append(notes, "覆盖测试未完成："+catErr.Error())
	default:
		reply := strings.TrimSpace(cat.Content)
		obeyed := strings.Contains(strings.ToLower(reply), "meow") || strings.Contains(reply, "喵")
		answered := catAnswerPattern.MatchString(strings.ReplaceAll(strings.ToLower(reply), "meow", ""))
		excerpt := truncateRunes(reply, 40)
		switch {
		case obeyed && !answered:
			notes = append(notes, "自定义 system prompt 被遵循")
		case obeyed:
			warnings = append(warnings, "自定义 system prompt 被部分稀释，回复："+excerpt)
		default:
			warnings = append(warnings, "自定义 system prompt 疑似被覆盖或忽略，回复："+excerpt)
		}
	}
	result := CheckResult{SampleCount: 2, Confidence: "medium"}
	switch {
	case baseErr != nil && catErr != nil:
		result.Status, result.Confidence = apigxmodel.AuditCheckInconclusive, "low"
		result.Evidence = "模型请求未成功：" + baseErr.Error()
	case len(warnings) > 0:
		result.Status = apigxmodel.AuditCheckWarning
		result.Evidence = strings.Join(append(warnings, notes...), "；") + "。小模型也可能不遵循指令，需复核"
	default:
		result.Status = apigxmodel.AuditCheckPassed
		result.Evidence = strings.Join(notes, "；")
	}
	return result
}

func firstError(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return fmt.Errorf("未知错误")
}
