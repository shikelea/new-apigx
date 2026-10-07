package modelfp

import (
	"fmt"
	"sort"
	"strings"
)

// 判定档位。故意不用「确认/伪造」这种措辞：本包只给信号。
const (
	VerdictMatch        = "match"        // 与所声称型号的公开特征一致
	VerdictMismatch     = "mismatch"     // 与公开特征明显不符
	VerdictSuspect      = "suspect"      // 存在可疑迹象，需复核
	VerdictInconclusive = "inconclusive" // 证据不足
)

// Finding 是单个维度的结论。
type Finding struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Verdict  string `json:"verdict"`
	Evidence string `json:"evidence"`
	Weight   int    `json:"weight"`
}

// Report 是一次掺水检测的完整结果。
type Report struct {
	Model    string    `json:"model"`
	Family   string    `json:"family"`
	Findings []Finding `json:"findings"`
	// Verdict 是整体判定，由各维度加权得出。
	Verdict string `json:"verdict"`
	// Score 是 0–100 的一致性得分，仅用于排序与展示，不是概率。
	Score      int    `json:"score"`
	Disclaimer string `json:"disclaimer"`
}

const Disclaimer = "掺水检测只比对「是否符合所声称型号的公开特征」，不能证明实际是哪个模型；上游版本、量化部署、强制改写采样参数都会影响结果，请结合人工复核。"

// Observations 汇总一次检测里采集到的全部原始观测。零值表示未采集到，对应维度会记为
// 证据不足。
type Observations struct {
	// 协议字段
	ResponseIDs        []string
	ObjectTypes        []string
	HasSystemFinger    bool
	SystemFingerPrints []string
	// 知识时效：模型自报的知识截止年份，以及对一组截止年份之后事件的回答是否声称知情。
	ClaimedCutoffYear int
	KnewFutureEvents  int
	FutureEventProbes int
	// 型号指纹：每道探针题的答案计数（probe ID -> 归一化答案 -> 次数），由 probes.go 的
	// 题集重复采样得到。判定走 evidence.go 的 Dirichlet-multinomial 证据比较。
	ProbeAnswers map[string]ProbeCounts
	// Meow 非空时，型号指纹维度完全改用 meow-llm-detector 官方基准包判定（见 meow.go），
	// ProbeAnswers 不再参与。
	Meow *MeowObservation
	// 逻辑稳定性：同一道题重复问的答案。
	LogicAnswers []string
	LogicCorrect string
	// 结构约束：严格 JSON schema 的达标次数。
	StructureOK    int
	StructureTries int
	// 指令层级：嵌套优先级指令的达标次数。
	HierarchyOK    int
	HierarchyTries int
	// 流式：内容分片数与是否以 [DONE] 收尾。
	StreamChunks int
	StreamDone   bool
	StreamTokens int64
	// 推理强度：usage 里的 reasoning_tokens 与完成 token。
	ReasoningTokens  int64
	CompletionTokens int64
	ReasoningPresent bool
	// TemperatureHonored 为 false 时，说明中转覆盖了采样参数，指纹类维度不可用。
	TemperatureHonored bool
}

// Evaluate 把观测转成八个维度的结论。纯函数，便于用固定观测做单元测试。
func Evaluate(model string, obs Observations) Report {
	profile, known := LookupProfile(model)
	report := Report{Model: model, Family: profile.Family, Disclaimer: Disclaimer}
	if !known {
		report.Family = "未知型号"
		// 没有公开特征表、但 Meow 基准包收录了该型号时，标题用包里的型号名；
		// 协议字段、推理强度等依赖公开特征的维度仍按「无法识别」处理。
		if name := MeowDisplayName(model); name != "" {
			report.Family = name
		}
	}
	byID := map[string]Finding{
		DimProtocol:    evaluateProtocol(profile, known, obs),
		DimKnowledge:   evaluateKnowledge(profile, known, obs),
		DimFingerprint: evaluateFingerprint(model, obs),
		DimLogic:       evaluateLogic(obs),
		DimStructure:   evaluateRatio(obs.StructureOK, obs.StructureTries, "严格 JSON schema", "结构化输出达标率偏低，是量化或小模型替换的常见表现"),
		DimHierarchy:   evaluateRatio(obs.HierarchyOK, obs.HierarchyTries, "嵌套优先级指令", "指令层级遵循率偏低，可能是能力更弱的替换模型"),
		DimStreaming:   evaluateStreaming(obs),
		DimReasoning:   evaluateReasoning(profile, known, obs),
	}
	for _, dimension := range Dimensions {
		finding := byID[dimension.ID]
		finding.ID, finding.Name, finding.Weight = dimension.ID, dimension.Name, dimension.Weight
		report.Findings = append(report.Findings, finding)
	}
	report.Verdict, report.Score = aggregate(report.Findings)
	return report
}

func evaluateProtocol(profile Profile, known bool, obs Observations) Finding {
	if len(obs.ResponseIDs) == 0 {
		return Finding{Verdict: VerdictInconclusive, Evidence: "未采集到响应字段"}
	}
	var problems, notes []string
	for _, object := range obs.ObjectTypes {
		if object != "" && object != "chat.completion" {
			problems = append(problems, "object 字段为 "+object+"，不是 chat.completion")
			break
		}
	}
	if known && profile.IDPrefix != "" {
		mismatched := 0
		for _, id := range obs.ResponseIDs {
			if !strings.HasPrefix(id, profile.IDPrefix) {
				mismatched++
			}
		}
		switch {
		case mismatched == len(obs.ResponseIDs):
			problems = append(problems, fmt.Sprintf("响应 id 均不以 %s 开头（实际如 %s）", profile.IDPrefix, truncate(obs.ResponseIDs[0], 24)))
		case mismatched > 0:
			problems = append(problems, fmt.Sprintf("%d/%d 个响应 id 不以 %s 开头", mismatched, len(obs.ResponseIDs), profile.IDPrefix))
		default:
			notes = append(notes, "响应 id 前缀符合 "+profile.Family)
		}
	}
	if known && profile.SystemFingerprint && !obs.HasSystemFinger {
		problems = append(problems, "该型号应返回 system_fingerprint，实际缺失")
	}
	if len(uniqueNonEmpty(obs.SystemFingerPrints)) > 1 {
		problems = append(problems, "同一次检测内 system_fingerprint 出现多个值，后端可能在多个部署间轮转")
	}
	switch {
	case len(problems) > 0:
		return Finding{Verdict: VerdictMismatch, Evidence: strings.Join(problems, "；")}
	case !known:
		return Finding{Verdict: VerdictInconclusive, Evidence: "无法识别型号，仅核对了通用字段，未见异常"}
	default:
		return Finding{Verdict: VerdictMatch, Evidence: strings.Join(append(notes, "协议字段符合该型号规范"), "；")}
	}
}

func evaluateKnowledge(profile Profile, known bool, obs Observations) Finding {
	if !known || profile.KnowledgeCutoffYear == 0 {
		return Finding{Verdict: VerdictInconclusive, Evidence: "该型号没有可核对的公开知识截止时间"}
	}
	// 一次都没答上（正文被截断、请求全失败）时不能报「未见明显偏离」：那是拿没数据当数据。
	if obs.ClaimedCutoffYear == 0 && obs.FutureEventProbes == 0 {
		return Finding{Verdict: VerdictInconclusive, Evidence: "未采集到可用的知识时效回答"}
	}
	notes := make([]string, 0, 2)
	verdict := VerdictMatch
	if obs.ClaimedCutoffYear > 0 {
		gap := obs.ClaimedCutoffYear - profile.KnowledgeCutoffYear
		notes = append(notes, fmt.Sprintf("自报知识截止 %d 年，公开值 %d 年", obs.ClaimedCutoffYear, profile.KnowledgeCutoffYear))
		// 自报时间本身不可靠，只把差距两年以上当可疑信号。
		if gap <= -2 || gap >= 2 {
			verdict = VerdictSuspect
		}
	} else {
		notes = append(notes, "模型未给出明确的知识截止时间")
	}
	if obs.FutureEventProbes > 0 {
		notes = append(notes, fmt.Sprintf("截止之后的事件探针：%d/%d 声称知情", obs.KnewFutureEvents, obs.FutureEventProbes))
		// 声称知道截止之后的事，说明训练数据更新，和所声称的型号版本不符。
		if obs.KnewFutureEvents*2 > obs.FutureEventProbes {
			verdict = VerdictSuspect
		}
	}
	if verdict == VerdictSuspect {
		return Finding{Verdict: VerdictSuspect, Evidence: strings.Join(notes, "；") + "。知识边界受系统提示词影响，需复核"}
	}
	return Finding{Verdict: verdict, Evidence: strings.Join(notes, "；") + "，未见明显偏离"}
}

// evaluateFingerprint 把探针答案分布与收录型号的实测基准做证据比较。
//
// 前提是：语言模型不是随机数发生器。让它「随便选一个」，同一型号会给出一个可复现的答案
// 偏好分布。把观测分布拿去和各型号基准算 Dirichlet-multinomial 预测证据，谁的证据最高
// 就最像谁——但只有当首选唯一、且相对次优候选的优势过判定线时才给结论。
//
// 这一维只在有实测基准时才工作。没测过基准就明确说没有，不拿空基准硬算。
func evaluateFingerprint(model string, obs Observations) Finding {
	if obs.Meow != nil {
		return evaluateMeowFingerprint(*obs.Meow)
	}
	if len(obs.ProbeAnswers) == 0 {
		return Finding{Verdict: VerdictInconclusive, Evidence: "未采集到探针答案，无法做分布指纹"}
	}
	if !obs.TemperatureHonored {
		return Finding{Verdict: VerdictInconclusive, Evidence: "中转覆盖了采样参数，分布指纹不可用"}
	}
	entry, listed := ResolveRequestName(model)
	if !listed {
		return Finding{Verdict: VerdictInconclusive, Evidence: "目标型号不在收录名单内，没有可比对的实测基准"}
	}
	candidates := fingerprintCandidates()
	if len(candidates) < 3 {
		return Finding{Verdict: VerdictInconclusive, Evidence: "收录名单里已实测基准的型号不足两个，暂时无法做分布比对"}
	}
	if entry.BaselineState != BaselineStateReady {
		return Finding{
			Verdict:  VerdictInconclusive,
			Evidence: fmt.Sprintf("%s 尚无实测基准，本维度暂不支持该型号", entry.DisplayName),
		}
	}
	outcome, ok := ScoreFingerprint(obs.ProbeAnswers, candidates)
	if !ok {
		return Finding{Verdict: VerdictInconclusive, Evidence: "有效答案过少，无法算出分布证据"}
	}
	verdict, evidence := describeOutcome(entry.ID, outcome, thresholdFor(entry))
	return Finding{Verdict: verdict, Evidence: evidence}
}

func evaluateLogic(obs Observations) Finding {
	if len(obs.LogicAnswers) < 3 {
		return Finding{Verdict: VerdictInconclusive, Evidence: "有效采样不足，无法评估求解稳定性"}
	}
	correct, distinct := 0, uniqueNonEmpty(obs.LogicAnswers)
	for _, answer := range obs.LogicAnswers {
		if obs.LogicCorrect != "" && strings.EqualFold(strings.TrimSpace(answer), obs.LogicCorrect) {
			correct++
		}
	}
	detail := fmt.Sprintf("同题 %d 次：%d 个不同答案", len(obs.LogicAnswers), len(distinct))
	if obs.LogicCorrect != "" {
		detail += fmt.Sprintf("，正确 %d 次", correct)
	}
	switch {
	case len(distinct) > 1 && obs.LogicCorrect != "" && correct*2 <= len(obs.LogicAnswers):
		return Finding{Verdict: VerdictMismatch, Evidence: detail + "，确定性设置下答案既不稳定也多半不正确，能力明显低于常见旗舰型号"}
	case len(distinct) > 1:
		return Finding{Verdict: VerdictSuspect, Evidence: detail + "，确定性设置下答案不稳定，可能是量化部署或多后端轮转"}
	case obs.LogicCorrect != "" && correct == 0:
		return Finding{Verdict: VerdictSuspect, Evidence: detail + "，答案稳定但错误，能力弱于该型号的公开表现"}
	default:
		return Finding{Verdict: VerdictMatch, Evidence: detail + "，求解稳定"}
	}
}

// evaluateRatio 处理达标率型维度：结构约束与指令层级。公开观测里，量化或降级部署在这
// 两项上的退化最明显，所以低达标率是较强的信号。
func evaluateRatio(ok, tries int, label, warning string) Finding {
	if tries == 0 {
		return Finding{Verdict: VerdictInconclusive, Evidence: "未采集到" + label + "样本"}
	}
	detail := fmt.Sprintf("%s：%d/%d 达标", label, ok, tries)
	switch {
	case ok == tries:
		return Finding{Verdict: VerdictMatch, Evidence: detail}
	case ok*2 <= tries:
		return Finding{Verdict: VerdictMismatch, Evidence: detail + "，" + warning}
	default:
		return Finding{Verdict: VerdictSuspect, Evidence: detail + "，存在偶发不达标，需复核"}
	}
}

func evaluateStreaming(obs Observations) Finding {
	if obs.StreamChunks == 0 {
		return Finding{Verdict: VerdictInconclusive, Evidence: "未采集到流式样本"}
	}
	detail := fmt.Sprintf("内容分片 %d 个", obs.StreamChunks)
	if obs.StreamTokens > 0 {
		detail += fmt.Sprintf("，输出 %d token", obs.StreamTokens)
	}
	switch {
	case !obs.StreamDone:
		return Finding{Verdict: VerdictMismatch, Evidence: detail + "，流未以 [DONE] 收尾，链路被改写"}
	// 真实逐 token 流式下分片数与 token 数同量级；分片远少于 token 说明内容是先攒齐
	// 再切块下发的，常见于「非流式上游 + 中转伪造流式」。
	case obs.StreamTokens >= 32 && int64(obs.StreamChunks)*4 < obs.StreamTokens:
		return Finding{Verdict: VerdictSuspect, Evidence: detail + "，分片数远少于 token 数，疑似伪流式重新切块"}
	default:
		return Finding{Verdict: VerdictMatch, Evidence: detail + "，流式结构正常"}
	}
}

func evaluateReasoning(profile Profile, known bool, obs Observations) Finding {
	if !known {
		return Finding{Verdict: VerdictInconclusive, Evidence: "无法识别型号，不判断推理强度"}
	}
	if obs.CompletionTokens == 0 && !obs.ReasoningPresent {
		return Finding{Verdict: VerdictInconclusive, Evidence: "未采集到 usage，无法评估推理强度"}
	}
	switch {
	case profile.ReasoningOptional:
		// 混合推理型号：有推理开销属正常，没有也不能反证，因为思维链常被折叠掉。
		if obs.ReasoningTokens > 0 {
			return Finding{Verdict: VerdictMatch, Evidence: fmt.Sprintf("可推理型号，reasoning_tokens=%d 属正常上报", obs.ReasoningTokens)}
		}
		return Finding{Verdict: VerdictInconclusive, Evidence: "可推理型号本次未见推理开销，两个方向都不作为判据"}
	case profile.Reasoning && !obs.ReasoningPresent:
		return Finding{Verdict: VerdictMismatch, Evidence: "推理型号应返回 reasoning_tokens，实际缺失，后端可能不是推理模型"}
	case profile.Reasoning && obs.ReasoningTokens == 0:
		return Finding{Verdict: VerdictMismatch, Evidence: "推理型号的 reasoning_tokens 为 0，未见实际推理开销"}
	case profile.Reasoning:
		return Finding{Verdict: VerdictMatch, Evidence: fmt.Sprintf("reasoning_tokens=%d，与推理型号相符", obs.ReasoningTokens)}
	case obs.ReasoningTokens > 0:
		return Finding{Verdict: VerdictSuspect, Evidence: fmt.Sprintf("非推理型号却返回 reasoning_tokens=%d，后端可能换成了推理模型", obs.ReasoningTokens)}
	default:
		return Finding{Verdict: VerdictMatch, Evidence: "usage 中无推理开销，与非推理型号相符"}
	}
}

// aggregate 用维度权重折算总体判定。任一高权重维度 mismatch 即整体 mismatch：
// 协议字段、结构约束、推理强度这三项都是客观可核对的，不该被其他项平均掉。
func aggregate(findings []Finding) (string, int) {
	earned, possible, mismatchWeight, evaluated := 0, 0, 0, 0
	for _, finding := range findings {
		if finding.Verdict == VerdictInconclusive {
			continue
		}
		evaluated++
		possible += finding.Weight
		switch finding.Verdict {
		case VerdictMatch:
			earned += finding.Weight
		case VerdictSuspect:
			earned += finding.Weight / 2
		case VerdictMismatch:
			mismatchWeight += finding.Weight
		}
	}
	if evaluated == 0 || possible == 0 {
		return VerdictInconclusive, 0
	}
	score := earned * 100 / possible
	switch {
	case mismatchWeight >= 3:
		return VerdictMismatch, score
	case mismatchWeight > 0 || score < 70:
		return VerdictSuspect, score
	case evaluated < 4:
		return VerdictInconclusive, score
	default:
		return VerdictMatch, score
	}
}

func uniqueNonEmpty(items []string) []string {
	seen := make(map[string]bool, len(items))
	out := make([]string, 0, len(items))
	for _, item := range items {
		trimmed := strings.TrimSpace(item)
		if trimmed == "" || seen[trimmed] {
			continue
		}
		seen[trimmed] = true
		out = append(out, trimmed)
	}
	sort.Strings(out)
	return out
}

func truncate(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit]) + "…"
}
