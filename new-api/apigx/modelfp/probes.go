package modelfp

import "strings"

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
	MaxTokens int
}

// Probes 是当前启用的探针集。ID 会落进基准数据，一旦发布不可改；要改题面必须换新 ID
// 并重测基准，否则新旧观测会被混在一起比较。
var Probes = []Probe{
	{
		ID:        ProbeIntegerPick,
		System:    ".",
		Prompt:    "Pick one integer between 17 and 83. Reply with the number only, nothing else.",
		MaxTokens: 32,
	},
	{
		ID:        ProbeLetterPick,
		System:    ".",
		Prompt:    "Pick one letter of the English alphabet. Reply with the single letter only, nothing else.",
		MaxTokens: 32,
	},
	{
		ID:        ProbeFruitPick,
		System:    ".",
		Prompt:    "Name one fruit. Reply with the fruit name only, nothing else.",
		MaxTokens: 32,
	},
	{
		ID:        ProbeColorPick,
		System:    ".",
		Prompt:    "Name one colour. Reply with the colour name only, nothing else.",
		MaxTokens: 32,
	},
	{
		ID:        ProbeWeekdayPick,
		System:    ".",
		Prompt:    "Pick one day of the week. Reply with the day only, nothing else.",
		MaxTokens: 32,
	},
	{
		// 位置偏好：选项本身无意义，测的是模型偏向列表的哪个位置。
		ID:        ProbeListPosition,
		System:    ".",
		Prompt:    "Choose one: alpha, bravo, charlie, delta, echo, foxtrot, golf. Reply with the chosen word only, nothing else.",
		MaxTokens: 32,
	},
}

// NormalizeAnswer 把一次回答折成类目。只做保守处理：去空白、去包裹标点、转小写。
//
// 不做同义词合并（"crimson" 不并进 "red"）：合并靠的是我们的主观判断，会把两个型号
// 的真实差异抹平，而抹平的方向恰好是「让不同模型看起来一样」，正是我们要避免的。
func NormalizeAnswer(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	// 只取第一行：有些模型会在答案后面另起一行解释。
	if newline := strings.IndexAny(trimmed, "\r\n"); newline >= 0 {
		trimmed = trimmed[:newline]
	}
	trimmed = strings.Trim(trimmed, " \t\"'`*.,!?:;()[]{}“”‘’「」。，、")
	return strings.ToLower(strings.TrimSpace(trimmed))
}
