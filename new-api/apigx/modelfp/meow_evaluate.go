package modelfp

import (
	"fmt"
	"strings"
)

var meowTierNames = map[string]string{"low": "低档", "medium": "中档", "high": "高档"}

// meowReasonText 对应参考项目 score_counts 的 reasons。
var meowReasonText = map[string]string{
	"baseline_cell_missing": "基准缺少某道题的参照数据",
	// 这条既可能在总体样本不够时给出，也可能只在某一道题上给出（meow_score.go 逐格检查），
	// 所以措辞要说清「总体或单题」，否则头部明明显示 291/340，理由却写「未达 60%」，
	// 看着自相矛盾。
	"samples_incomplete":    "总体或单题的有效样本未达其计划的 60%",
	"samples_exceed_plan":   "样本数超出计划",
	"no_valid_samples":      "没有有效样本",
	"uncalibrated":          "该档位未通过校准校验",
	"unknown_claimed_model": "声称型号不在基准内",
	"multiple_thresholds":   "多个型号并列最像",
	"no_threshold":          "最像的型号未过校准阈值",
}

// meowModeName 是采集协议的展示名。
func meowModeName(mode string) string {
	switch mode {
	case MeowModeGPT:
		return "OpenAI Responses 原生接口"
	case MeowModeClaude:
		return "Anthropic Messages 原生接口"
	default:
		return "Chat Completions 接口"
	}
}

func meowModelName(id string) string {
	if id == MeowOtherID {
		return "其他已知外部型号"
	}
	return id
}

// evaluateMeowFingerprint 把 score_counts 的三色结论映射到本维度：绿=一致，红=不符，
// 黄=证据不足。参考项目的判定不产生「可疑」，这里也不造。
func evaluateMeowFingerprint(obs MeowObservation) Finding {
	result, ok := ScoreMeow(obs)
	if !ok {
		return Finding{Verdict: VerdictInconclusive, Evidence: "基准包或档位不可用，无法做分布指纹"}
	}
	tierName := meowTierNames[obs.Tier]
	if tierName == "" {
		tierName = obs.Tier
	}
	head := fmt.Sprintf("Meow 官方基准 %s（%s，%s），有效样本 %d/%d", obs.PackageVersion, meowModeName(obs.Mode), tierName,
		result.ValidSamples, result.PlannedSamples)
	if obs.Failed > 0 {
		head += fmt.Sprintf("，%d 次请求失败", obs.Failed)
	}
	var tail []string
	if obs.NativeFallback != "" {
		tail = append(tail, "中转不支持"+meowModeName(obs.NativeFallback)+"，已改用 Chat 兼容基准")
	}
	if obs.ReasoningEffortDropped {
		tail = append(tail, "上游不接受推理强度参数，已按缺省推理强度采样")
	}
	suffix := ""
	if len(tail) > 0 {
		suffix = "（" + strings.Join(tail, "；") + "）"
	}

	switch result.Verdict {
	case "match":
		return Finding{Verdict: VerdictMatch, Evidence: fmt.Sprintf("%s：最像 %s（匹配度 %.3f，阈值 %.3f），与所声称型号一致%s",
			head, result.Model, result.Matches[result.Model], result.Thresholds[result.Model], suffix)}
	case "mismatch":
		return Finding{Verdict: VerdictMismatch, Evidence: fmt.Sprintf("%s：最像 %s（匹配度 %.3f，阈值 %.3f），与所声称的 %s 不符%s",
			head, meowModelName(result.Model), result.Matches[result.Model], result.Thresholds[result.Model], obs.ClaimedModel, suffix)}
	}
	reasons := make([]string, 0, len(result.Reasons))
	for _, reason := range result.Reasons {
		if text, ok := meowReasonText[reason]; ok {
			reasons = append(reasons, text)
		} else {
			reasons = append(reasons, reason)
		}
	}
	detail := ""
	if result.ValidSamples > 0 && len(result.Models) > 0 {
		best := result.Models[0]
		for _, model := range result.Models[1:] {
			if result.Matches[model] > result.Matches[best] {
				best = model
			}
		}
		detail = fmt.Sprintf("；目前最像 %s（匹配度 %.3f，阈值 %.3f）", meowModelName(best), result.Matches[best], result.Thresholds[best])
	}
	return Finding{Verdict: VerdictInconclusive, Evidence: fmt.Sprintf("%s：证据不足（%s）%s%s",
		head, strings.Join(reasons, "、"), detail, suffix)}
}
