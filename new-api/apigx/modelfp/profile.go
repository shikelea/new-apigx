// Package modelfp 做「模型掺水」检测：判断一个中转上声称的型号，行为特征是否和该型号
// 的公开特征相符。
//
// 方法论上参考了公开的模型指纹研究（LLM behavioural fingerprinting、知识边界指纹
// KBF、量化模型的结构化输出退化观测），此处按自己的口径重新实现，没有拷贝任何代码。
//
// 必须明确的边界：
//
//   - 没有可信参照模型，就无法证明「这其实是模型 X」。本包只输出「与所声称型号的公开
//     特征不符」这一类信号，永远不给出替换结论。
//   - 中转如果强制覆盖 temperature，指纹类分布会被扰动；此时相关维度自动降级为证据不足。
//   - 基于 logprobs 的秩一致性检验更严谨，但中转普遍不透出 logprobs，这里不采用。
package modelfp

import "strings"

// Dimension 是掺水检测的一个维度。ID 会落库，必须稳定。
type Dimension struct {
	ID   string
	Name string
	// Weight 决定它在总体判定里的分量：协议字段这类客观项权重高，
	// 依赖模型自身能力波动的项权重低。
	Weight int
}

const (
	DimKnowledge   = "knowledge_recency"
	DimFingerprint = "model_fingerprint"
	DimLogic       = "logic_stability"
	DimStructure   = "structure_compliance"
	DimHierarchy   = "instruction_hierarchy"
	DimProtocol    = "protocol_fields"
	DimStreaming   = "streaming_integrity"
	DimReasoning   = "reasoning_effort"
)

// Dimensions 按展示顺序列出八个维度。
var Dimensions = []Dimension{
	{DimKnowledge, "知识时效核验", 3},
	{DimFingerprint, "型号指纹匹配", 2},
	{DimLogic, "逻辑求解稳定性", 2},
	{DimStructure, "结构约束遵循", 3},
	{DimHierarchy, "指令层级遵循", 2},
	{DimProtocol, "协议字段规范", 3},
	{DimStreaming, "流式响应完整性", 2},
	{DimReasoning, "推理强度匹配", 3},
}

// Profile 记录一个型号族的公开可核对特征。只写有据可查的事实；拿不准的字段留空，
// 对应维度就会返回证据不足，而不是给出一个编造的判断。
type Profile struct {
	Family string
	// Hints 用于从模型 ID 识别族；短提示只按前缀匹配。
	Hints []string
	// IDPrefix 是响应 id 的规范前缀（OpenAI chatcmpl-、Anthropic msg_）。
	IDPrefix string
	// SystemFingerprint 表示该族是否会返回 system_fingerprint 字段。
	SystemFingerprint bool
	// KnowledgeCutoffYear 是公开的训练数据截止年份；0 表示未知。
	KnowledgeCutoffYear int
	// Reasoning 表示这是推理型号，usage 里应当出现 reasoning_tokens。
	Reasoning bool
	// TokenizerFamily 标记分词器族，用于解释 prompt_tokens 的量级差异。
	TokenizerFamily string
}

// profiles 只覆盖当前检测重点（GPT 系）与常见对照族。保持数据驱动，后续修订只改这里。
var profiles = []Profile{
	{
		Family: "OpenAI GPT-4o", Hints: []string{"gpt-4o", "chatgpt-4o"},
		IDPrefix: "chatcmpl-", SystemFingerprint: true,
		KnowledgeCutoffYear: 2023, TokenizerFamily: "o200k_base",
	},
	{
		Family: "OpenAI GPT-4.1", Hints: []string{"gpt-4.1"},
		IDPrefix: "chatcmpl-", SystemFingerprint: true,
		KnowledgeCutoffYear: 2024, TokenizerFamily: "o200k_base",
	},
	{
		Family: "OpenAI GPT-5", Hints: []string{"gpt-5"},
		IDPrefix: "chatcmpl-", SystemFingerprint: true,
		KnowledgeCutoffYear: 2024, TokenizerFamily: "o200k_base",
	},
	{
		Family: "OpenAI o 系推理", Hints: []string{"o1", "o3", "o4"},
		IDPrefix: "chatcmpl-", SystemFingerprint: true,
		KnowledgeCutoffYear: 2023, Reasoning: true, TokenizerFamily: "o200k_base",
	},
	{
		Family: "OpenAI GPT-4", Hints: []string{"gpt-4-", "gpt-4t"},
		IDPrefix: "chatcmpl-", SystemFingerprint: true,
		KnowledgeCutoffYear: 2023, TokenizerFamily: "cl100k_base",
	},
	{
		Family: "OpenAI GPT-3.5", Hints: []string{"gpt-3.5"},
		IDPrefix: "chatcmpl-", SystemFingerprint: false,
		KnowledgeCutoffYear: 2021, TokenizerFamily: "cl100k_base",
	},
	{
		Family: "Anthropic Claude", Hints: []string{"claude"},
		IDPrefix: "msg_", KnowledgeCutoffYear: 2024,
	},
	{
		Family: "Google Gemini", Hints: []string{"gemini"}, KnowledgeCutoffYear: 2024,
	},
	{
		Family: "DeepSeek", Hints: []string{"deepseek"}, IDPrefix: "chatcmpl-", KnowledgeCutoffYear: 2024,
	},
}

// LookupProfile 从模型 ID 推断公开特征；无法识别时返回 false，调用方应把相关维度
// 记为证据不足而不是失败。
func LookupProfile(model string) (Profile, bool) {
	id := strings.ToLower(strings.TrimSpace(model))
	if slash := strings.LastIndex(id, "/"); slash >= 0 {
		id = id[slash+1:]
	}
	best, found := Profile{}, false
	for _, profile := range profiles {
		for _, hint := range profile.Hints {
			if !strings.HasPrefix(id, hint) && !strings.Contains(id, hint) {
				continue
			}
			// 更长的提示更具体（gpt-4.1 优于 gpt-4-），优先保留。
			if !found || len(hint) > longestHint(best) {
				best, found = profile, true
			}
		}
	}
	return best, found
}

func longestHint(profile Profile) int {
	longest := 0
	for _, hint := range profile.Hints {
		longest = max(longest, len(hint))
	}
	return longest
}
