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
	// Weight 决定它在总体判定里的分量：权重 3 是「客观可核对或高样本量实测」的维度
	// （协议字段、结构约束、推理强度、型号指纹），单项不符即可定论；权重 2 是依赖单次
	// 采样或模型能力波动的维度，只作辅助信号。
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
	{DimFingerprint, "型号指纹匹配", 3},
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
	// ReasoningOptional 表示该型号自己决定要不要推理（混合推理 / 自适应思维链）。这类型
	// 号上报 reasoning_tokens 是常态，不上报也可能是把思维链折叠了，所以两个方向都不作为
	// 掺水证据：命中时推理强度维度只在「有推理开销」时记相符，缺失时记证据不足。
	ReasoningOptional bool
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
	// R1 必须单独一条：判定用的是这里的 Reasoning，而不是收录表里那条（那里的 Profile
	// 只用于展示，不参与计算）。少了这一条，deepseek-reasoner 会落到下面的通用 DeepSeek，
	// 两个方向全错——诚实的 R1 中转因为返回 reasoning_tokens 被判「可能换成了推理模型」，
	// 而把 R1 换成 V3 的中转因为「无推理开销」被判与该型号相符。
	{
		Family: "DeepSeek R1", Hints: []string{"deepseek-reasoner", "deepseek-r1"},
		KnowledgeCutoffYear: 2024, Reasoning: true,
	},
	// v4 系（现役请求名 deepseek-flash，正式名 deepseek-v4.1-flash；官方文档说旧名
	// deepseek-v4-flash 已下线，请求由 V4.1-Flash 承接）是可按需推理的型号：2026-10-04 从
	// 官方入口（经中转）实测，同一道 Meow 题连采 12 次全部正常结束，每次都带
	// reasoning_tokens=47～106。把它留在下面的通用 DeepSeek 条目里（Reasoning 为 false），
	// 诚实的中转会因为「非推理型号却返回 reasoning_tokens」被判可能换成了推理模型——正是
	// 最容易误伤正常链路的一条指控。
	//
	// 用 ReasoningOptional 而不是 Reasoning：第三方口径把 V4.1-Flash 记为 reasoning-focused，
	// 但它同时接受 reasoning_effort 调节，中转也可能不支持该参数（采集端会退化成缺省强度），
	// 这时「没有推理开销」是正常配置而不是换模型的证据。deepseek-v4-pro 会经
	// 「deepseek-v4」提示落到同一条——目前只有 flash 的实测依据，但按「宁可少指控」的方向，
	// 把整条 v4 线都当可推理是安全的一侧。
	//
	// 知识截止年份留空：2026-10-07 查过官方 API 文档（api-docs.deepseek.com）与各模型页，
	// 都没有公布 v4 系的训练数据截止时间。没有出处就不要填年份，否则知识维度会拿猜的数字
	// 去判 suspect。
	{
		Family: "DeepSeek V4", Hints: []string{"deepseek-flash", "deepseek-v4"},
		ReasoningOptional: true,
	},
	{
		// 不声明 IDPrefix：官方返回 chatcmpl- 开头的 id，但中转改写 id 很常见，断言前缀只会
		// 把正常链路判成协议字段不符。
		Family: "DeepSeek", Hints: []string{"deepseek"}, KnowledgeCutoffYear: 2024,
	},
}

// containsHintFloor 是「允许按包含匹配」的最短提示长度。o1、o3 这类两字提示一旦允许
// 包含匹配，任何恰好连着这两个字符的型号名都会被认成 o 系推理，所以短提示只按前缀匹配。
const containsHintFloor = 5

// LookupProfile 从模型 ID 推断公开特征；无法识别时返回 false，调用方应把相关维度
// 记为证据不足而不是失败。
func LookupProfile(model string) (Profile, bool) {
	id := strings.ToLower(strings.TrimSpace(model))
	if slash := strings.LastIndex(id, "/"); slash >= 0 {
		id = id[slash+1:]
	}
	best, found, bestLen := Profile{}, false, 0
	for _, profile := range profiles {
		for _, hint := range profile.Hints {
			matched := strings.HasPrefix(id, hint) ||
				(len(hint) >= containsHintFloor && strings.Contains(id, hint))
			if !matched {
				continue
			}
			// 更长的提示更具体（gpt-4.1 优于 gpt-4-），优先保留。比的是「本次命中的提示」
			// 长度，不能拿该族其它没命中的提示来比，否则一个更长的无关提示会挤掉正确型号。
			if !found || len(hint) > bestLen {
				best, found, bestLen = profile, true, len(hint)
			}
		}
	}
	return best, found
}
