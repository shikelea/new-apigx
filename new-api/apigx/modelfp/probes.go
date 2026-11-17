package modelfp

import (
	"slices"
	"strings"
)

// 分布探针。每道题都必须满足四个条件，否则不能用来做指纹：
//
//  1. 答案空间有限且可枚举，便于归一化成类目；
//  2. 没有唯一正确答案，模型只能按自身偏好挑，偏好才是指纹；
//  3. 回答极短，单次成本低到可以重复几十次；
//  4. 题面不含时间、地域、时事，换个月份重测结果不应漂移。
//
// 区间和取值范围都刻意选成不整齐的（17–83 而不是 1–100），理由是：整齐区间上模型会
// 大量落在中点或幸运数，不同型号挤在同一个答案上，区分度反而差。
const (
	ProbeIntegerPick  = "pick_integer_17_83"
	ProbeLetterPick   = "pick_letter"
	ProbeFruitPick    = "pick_fruit"
	ProbeColorPick    = "pick_color"
	ProbeWeekdayPick  = "pick_weekday"
	ProbeListPosition = "pick_from_list"
)

// Probe 是一道分布题。
type Probe struct {
	ID string
	// System 故意留一个无意义的占位符，而不是空串：部分上游对空 system 的处理不一致，
	// 会让同一型号在不同中转上表现不同，那不是我们想测的东西。
	System string
	Prompt string
	// MaxTokens 给得比答案需要的多一些，留给会先说一句废话的模型。
	//
	// 预算必须容得下整段思维链：2026-10-04 实测，先推理后作答的型号在这几道题上要花
	// 100～300 个 reasoning token，32 的旧预算会让 180 次采样全部返回空正文——分布指纹
	// 就此采不到任何东西。512 是同一次实测里 179/180 份有效答案的预算。
	// 对不推理的型号，这个值只影响上限，不影响首 token 的偏好分布，新旧观测仍可比。
	MaxTokens int
}

// Probes 是当前启用的探针集。ID 会落进基准数据，一旦发布不可改；要改题面必须换新 ID
// 并重测基准，否则新旧观测会被混在一起比较。
var Probes = []Probe{
	{
		ID:        ProbeIntegerPick,
		System:    ".",
		Prompt:    "Pick one integer between 17 and 83. Reply with the number only, nothing else.",
		MaxTokens: 512,
	},
	{
		ID:        ProbeLetterPick,
		System:    ".",
		Prompt:    "Pick one letter of the English alphabet. Reply with the single letter only, nothing else.",
		MaxTokens: 512,
	},
	{
		ID:        ProbeFruitPick,
		System:    ".",
		Prompt:    "Name one fruit. Reply with the fruit name only, nothing else.",
		MaxTokens: 512,
	},
	{
		ID:        ProbeColorPick,
		System:    ".",
		Prompt:    "Name one colour. Reply with the colour name only, nothing else.",
		MaxTokens: 512,
	},
	{
		ID:        ProbeWeekdayPick,
		System:    ".",
		Prompt:    "Pick one day of the week. Reply with the day only, nothing else.",
		MaxTokens: 512,
	},
	{
		// 位置偏好：选项本身无意义，测的是模型偏向列表的哪个位置。
		ID:        ProbeListPosition,
		System:    ".",
		Prompt:    "Choose one: alpha, bravo, charlie, delta, echo, foxtrot, golf. Reply with the chosen word only, nothing else.",
		MaxTokens: 512,
	},
}

// NormalizeAnswer 把一次回答折成类目。只做保守处理：剥掉思维链、逐行跳过客套引导句、
// 去包裹标点、转小写。取不到答案时返回空串，调用方按「这次没采到样本」丢弃（见
// relayprobe.collectLegacyProbes），不要把它当成一个类目。
//
// 不做同义词合并（"crimson" 不并进 "red"）：合并靠的是我们的主观判断，会把两个型号的
// 真实差异抹平，而抹平的方向恰好是「让不同模型看起来一样」，正是我们要避免的。
//
// 逐行找而不是只看第一行：先推理的型号把答案放在思维链之后，爱客套的型号会先写一句
// 引导语，上一版直接截第一行，于是 "<think>" 或 "Sure, here you go:" 本身被当成答案，
// 真实选项全部落进未见类目，分布指纹被这批脏类目污染。
func NormalizeAnswer(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	lower := strings.ToLower(trimmed)
	if end := strings.Index(lower, "</think>"); end >= 0 {
		trimmed = strings.TrimSpace(trimmed[end+len("</think>"):])
	} else if strings.HasPrefix(lower, "<think>") {
		// 思维链没有闭合：这次采样全花在思考上，正文为空。空正文不是「答了别的」。
		return ""
	}
	// 引导句不是答案，继续看下一行。markdown 围栏与引号由下面的包裹标点一并剥掉
	// （"```42```" 会得到 "42"，单独一行的 "```" 会变成空行被跳过）。
	leadIns := []string{"sure", "here is", "here's", "the answer is", "i choose", "i pick", "my choice", "i would pick"}
	for line := range strings.SplitSeq(trimmed, "\n") {
		answer := strings.ToLower(strings.Trim(strings.TrimSpace(line), " \t\r`\"'*.,!?:;()[]{}“”‘’「」。，、"))
		if answer == "" {
			continue
		}
		if slices.ContainsFunc(leadIns, func(prefix string) bool { return strings.HasPrefix(answer, prefix) }) {
			continue
		}
		return answer
	}
	return ""
}
