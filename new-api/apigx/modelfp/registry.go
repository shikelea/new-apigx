package modelfp

import (
	"slices"
	"sort"
	"strings"
)

// 收录型号注册表。
//
// 这里是「只收录我们能确定查询的型号」这条规则的落点。一个型号要进名单，必须满足：
//
//   - 确定性事实有公开出处（响应 id 前缀、是否返回 system_fingerprint、是否推理型号、
//     分词器族等），拿不准的字段一律留零值，对应维度自动记证据不足；
//   - 分布基准由我们自己实测并落库，没测过就是 BaselineStatePending。
//
// 没测过基准的型号依然可以做确定性检查，但不做分布指纹——那一维会明确告诉用户
// 「该型号尚无实测基准」，而不是拿一个空基准去硬算出一个像是结论的东西。

// 基准状态。
const (
	// BaselineStatePending 表示尚未实测分布基准。
	BaselineStatePending = "pending"
	// BaselineStateReady 表示已有实测基准，可做分布指纹。
	BaselineStateReady = "ready"
)

// BaselineMeta 记录一份基准的来源，用于展示与复核。基准是判定的全部依据，来源必须可追溯。
type BaselineMeta struct {
	// MeasuredAt 是实测日期（YYYY-MM-DD）。
	MeasuredAt string `json:"measured_at"`
	// Source 是实测时直连的渠道说明，例如「官方 API」。不写具体地址与凭据。
	Source string `json:"source"`
	// SamplesPerProbe 是每题的实测次数。
	SamplesPerProbe int `json:"samples_per_probe"`
}

// RegistryEntry 是一个收录型号。
type RegistryEntry struct {
	// ID 是我们的内部稳定标识，会落进基准数据与检测记录。
	ID string `json:"id"`
	// DisplayName 是展示名。
	DisplayName string `json:"display_name"`
	// Vendor 用于前端按厂商分组。
	Vendor string `json:"vendor"`
	// RequestNames 是常见的实际请求名。中转各家命名不一，这里只作为选择器的提示，
	// 不参与判定：请求名由中转自己决定，天然不可信。
	RequestNames []string `json:"request_names"`
	// Profile 只随收录项一起展示，不参与判定：判定用的公开特征一律来自 profile.go 的
	// profiles 表（Evaluate 走 LookupProfile，按实际请求名匹配）。在这里改这个字段不会
	// 改变任何结论，两张表不一致时以 profiles 为准。
	Profile Profile `json:"-"`
	// BaselineState 是基准状态。
	BaselineState string `json:"baseline_state"`
	// BaselineMeta 仅在 BaselineStateReady 时有意义。
	BaselineMeta BaselineMeta `json:"baseline_meta"`
	// ReadyTiers 记录该型号已通过校准的档位（"low", "medium", "high"）。
	// 前端据此做档位联动，未校准该档位的型号不在此档位下列出。
	ReadyTiers []string `json:"ready_tiers,omitempty"`
	// Baseline 是各题的实测答案分布，probe ID -> 答案计数。
	Baseline map[string]ProbeCounts `json:"-"`
	// Threshold 是给出强指向所需的最低 Confidence。基准越稀疏应当越保守。
	// 留 0 时取 defaultThreshold。
	Threshold float64 `json:"threshold"`
}

// defaultThreshold 是判定线的缺省值。0.62 对应约 0.49 的平均对数优势，意思是「平均每份
// 答案都要明显更支持首选候选」才给结论。首次校准前刻意取保守值：宁可多报证据不足。
const defaultThreshold = 0.62

// registry 是收录名单。分布基准全部留空，等实测数据落库后再逐条填。
//
// 确定性事实的出处：OpenAI 的 system_fingerprint 与 max_completion_tokens 行为、o 系
// 拒绝 temperature 的报错、Anthropic 的 msg_ 前缀与 stop_reason 取值、智谱 GLM 不返回
// system_fingerprint 且用 reasoning_content 暴露思维链。拿不准的一律留零值。
var registry = []RegistryEntry{
	{
		ID: "openai-gpt-4o", DisplayName: "GPT-4o", Vendor: "OpenAI",
		RequestNames: []string{"gpt-4o", "gpt-4o-2024-11-20", "chatgpt-4o-latest"},
		Profile: Profile{
			Family: "OpenAI GPT-4o", Hints: []string{"gpt-4o", "chatgpt-4o"},
			IDPrefix: "chatcmpl-", SystemFingerprint: true,
			KnowledgeCutoffYear: 2023, TokenizerFamily: "o200k_base",
		},
		BaselineState: BaselineStatePending,
	},
	{
		ID: "openai-gpt-4-1", DisplayName: "GPT-4.1", Vendor: "OpenAI",
		RequestNames: []string{"gpt-4.1", "gpt-4.1-2025-04-14"},
		Profile: Profile{
			Family: "OpenAI GPT-4.1", Hints: []string{"gpt-4.1"},
			IDPrefix: "chatcmpl-", SystemFingerprint: true,
			KnowledgeCutoffYear: 2024, TokenizerFamily: "o200k_base",
		},
		BaselineState: BaselineStatePending,
	},
	{
		ID: "anthropic-claude-sonnet", DisplayName: "Claude Sonnet", Vendor: "Anthropic",
		RequestNames: []string{"claude-3-7-sonnet", "claude-sonnet-4", "claude-sonnet-4-5"},
		Profile: Profile{
			Family: "Anthropic Claude Sonnet", Hints: []string{"claude-3-7-sonnet", "claude-sonnet"},
			IDPrefix: "msg_", KnowledgeCutoffYear: 2024,
		},
		BaselineState: BaselineStatePending,
	},
	{
		ID: "anthropic-claude-opus", DisplayName: "Claude Opus", Vendor: "Anthropic",
		RequestNames: []string{"claude-opus-4", "claude-opus-4-1", "claude-3-opus"},
		Profile: Profile{
			Family: "Anthropic Claude Opus", Hints: []string{"claude-opus", "claude-3-opus"},
			IDPrefix: "msg_", KnowledgeCutoffYear: 2024,
		},
		BaselineState: BaselineStatePending,
	},
	{
		ID: "deepseek-chat", DisplayName: "DeepSeek V3", Vendor: "DeepSeek",
		RequestNames: []string{"deepseek-chat", "deepseek-v3"},
		Profile: Profile{
			Family: "DeepSeek V3", Hints: []string{"deepseek-chat", "deepseek-v3"},
		},
		BaselineState: BaselineStatePending,
	},
	{
		// 2026-09-28 那份分布原本记在 deepseek-chat 上，但采集时的请求名是 deepseek-flash，
		// 也就是 flash 型号（正式名 deepseek-v4.1-flash），不是 V3。基准挂错型号会让所有
		// 声称 deepseek-chat 的链路拿 flash 的偏好做参照，所以按真实型号重新归属。
		//
		// 请求名 deepseek-flash 归 Meow 基准包那条收录项（已校准、题量更大），这里刻意不
		// 抢同一个名字，避免反查时两条互相遮蔽。
		//
		// 基准的时效要如实标注：它是 2026-09-28 在 max_tokens=32 下采的。2026-10-04 从同
		// 一入口重测，32 的预算已采不到任何正文（该入口改为先输出思维链），512 预算下取到
		// 的 179 份有效答案里 pick_letter / pick_weekday / pick_from_list 三题分布明显漂
		// 移，整体判给「未建模来源」。所以这份只能当作当时的快照，重测落库前不宜视为权威。
		//
		// 响应 id 前缀不写：profile.go 对 DeepSeek 族刻意留空，实测到的 id 是 48 位十六进
		// 制而非 chatcmpl-，没有依据断言任何一种前缀。
		ID: "deepseek-v4.1-flash", DisplayName: "DeepSeek V4.1 Flash", Vendor: "DeepSeek",
		RequestNames: []string{"deepseek-v4.1-flash"},
		Profile: Profile{
			Family: "DeepSeek V4.1 Flash", Hints: []string{"deepseek-v4.1-flash"},
		},
		BaselineState: BaselineStateReady,
		BaselineMeta: BaselineMeta{
			MeasuredAt:      "2026-09-28",
			Source:          "官方 API（经中转直连）",
			SamplesPerProbe: 30,
		},
		Baseline: map[string]ProbeCounts{
			ProbeColorPick: {
				"blue": 29,
				"red":  1,
			},
			ProbeListPosition: {
				"charlie": 3,
				"delta":   14,
				"echo":    3,
				"golf":    10,
			},
			ProbeFruitPick: {
				"apple": 30,
			},
			ProbeIntegerPick: {
				"26": 1,
				"41": 1,
				"42": 19,
				"46": 1,
				"47": 1,
				"49": 1,
				"51": 1,
				"52": 1,
				"53": 3,
				"57": 1,
			},
			ProbeLetterPick: {
				"j": 1,
				"k": 8,
				"m": 3,
				"q": 16,
				"r": 2,
			},
			// 30 次里有 1 次返回空内容。实时采集丢弃空答案（见 collectLegacyProbes），
			// 基准也必须丢，否则这里会多出一个观测侧永远打不中的类目，稀释该题的先验。
			ProbeWeekdayPick: {
				"thursday":  2,
				"tuesday":   6,
				"wednesday": 21,
			},
		},
	},
	{
		ID: "deepseek-reasoner", DisplayName: "DeepSeek R1", Vendor: "DeepSeek",
		RequestNames: []string{"deepseek-reasoner", "deepseek-r1"},
		Profile: Profile{
			Family: "DeepSeek R1", Hints: []string{"deepseek-reasoner", "deepseek-r1"},
			Reasoning: true,
		},
		BaselineState: BaselineStatePending,
	},
	{
		// GLM 的确定性事实出处最完整：官方 OpenAPI spec 里明确没有 system_fingerprint，
		// 思维链走 reasoning_content，finish_reason 额外有 sensitive 等取值。
		ID: "zhipu-glm-4-6", DisplayName: "GLM-4.6", Vendor: "智谱 / Z.ai",
		RequestNames: []string{"glm-4.6"},
		Profile: Profile{
			Family: "Zhipu GLM-4.6", Hints: []string{"glm-4.6"},
			SystemFingerprint: false, TokenizerFamily: "chatglm-151552",
		},
		BaselineState: BaselineStatePending,
	},
	{
		ID: "zhipu-glm-4-5", DisplayName: "GLM-4.5", Vendor: "智谱 / Z.ai",
		RequestNames: []string{"glm-4.5", "glm-4.5-air"},
		Profile: Profile{
			Family: "Zhipu GLM-4.5", Hints: []string{"glm-4.5"},
			SystemFingerprint: false, TokenizerFamily: "chatglm-151552",
		},
		BaselineState: BaselineStatePending,
	},
	{
		ID: "moonshot-kimi-k2", DisplayName: "Kimi K2", Vendor: "Moonshot",
		RequestNames: []string{"kimi-k2", "moonshot-v1-128k"},
		Profile: Profile{
			Family: "Moonshot Kimi K2", Hints: []string{"kimi-k2", "moonshot-v1"},
			IDPrefix: "chatcmpl-",
		},
		BaselineState: BaselineStatePending,
	},
	{
		ID: "qwen-max", DisplayName: "Qwen Max", Vendor: "阿里云百炼",
		RequestNames: []string{"qwen-max", "qwen-plus"},
		Profile: Profile{
			Family: "Qwen Max", Hints: []string{"qwen-max", "qwen-plus"},
			IDPrefix: "chatcmpl-",
		},
		BaselineState: BaselineStatePending,
	},
}

// Registry 返回收录名单的副本，按厂商与展示名排序，供接口与前端选择器使用。
func Registry() []RegistryEntry {
	out := allRegistry()
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Vendor == out[j].Vendor {
			return out[i].DisplayName < out[j].DisplayName
		}
		return out[i].Vendor < out[j].Vendor
	})
	return out
}

// LookupRegistry 按内部 ID 精确查收录项。
func LookupRegistry(id string) (RegistryEntry, bool) {
	for _, entry := range allRegistry() {
		if entry.ID == id {
			return entry, true
		}
	}
	return RegistryEntry{}, false
}

// ResolveRequestName 从用户填写的实际请求名反查收录项，用于「中转上叫什么」和
// 「我们内部认哪一个」之间的对应。匹配不上返回 false，调用方应要求用户显式选择型号。
func ResolveRequestName(name string) (RegistryEntry, bool) {
	id := strings.ToLower(strings.TrimSpace(name))
	if slash := strings.LastIndex(id, "/"); slash >= 0 {
		id = id[slash+1:]
	}
	if id == "" {
		return RegistryEntry{}, false
	}
	best, found, bestLen := RegistryEntry{}, false, 0
	for _, entry := range allRegistry() {
		for _, candidate := range entry.RequestNames {
			lowered := strings.ToLower(candidate)
			if id != lowered && !strings.HasPrefix(id, lowered) {
				continue
			}
			// 更长的请求名更具体（glm-4.6 优于 glm-4），优先保留。
			if !found || len(lowered) > bestLen {
				best, found, bestLen = entry, true, len(lowered)
			}
		}
	}
	return best, found
}

// fingerprintCandidates 装配一次判定的候选集：所有已有基准的收录项，外加兜底候选。
//
// 兜底候选的基准由已建模型号合成——把每题上各型号见过的类目并起来、各计一份。它代表
// 「一个我们没建模、但行为落在已知答案空间里的来源」。这样当观测既不像 A 也不像 B 时，
// 胜出的是兜底候选，而不是被硬分给稍微近一点的那个。
func fingerprintCandidates() []Candidate {
	candidates := make([]Candidate, 0, len(registry)+1)
	reference := Candidate{ID: referenceCandidate, Baseline: map[string]ProbeCounts{}}
	for _, entry := range registry {
		if entry.BaselineState != BaselineStateReady || len(entry.Baseline) == 0 {
			continue
		}
		candidates = append(candidates, Candidate{ID: entry.ID, Baseline: entry.Baseline})
		for probeID, counts := range entry.Baseline {
			if reference.Baseline[probeID] == nil {
				reference.Baseline[probeID] = ProbeCounts{}
			}
			for answer := range counts {
				reference.Baseline[probeID][answer] = 1
			}
		}
	}
	if len(candidates) == 0 {
		return nil
	}
	return append(candidates, reference)
}

// BaselineReady 报告当前是否已有足够基准做分布指纹（至少两个已建模型号才谈得上比对）。
func BaselineReady() bool {
	return len(loadedMeowPackages()) > 0 || len(fingerprintCandidates()) >= 3
}

// allRegistry 是手写收录名单加上由 Meow 基准包生成的收录项。
func allRegistry() []RegistryEntry {
	out := make([]RegistryEntry, 0, len(registry)+10)
	out = append(out, registry...)
	return append(out, meowRegistryEntries()...)
}

// meowRegistryEntries 从基准包直接生成收录项：型号 ID、请求名、实测日期、每题实测
// 份数全部取自包本身，不手写，避免和基准脱节。确定性事实（id 前缀、截止年份等）包里
// 没有，留给 profile.go 按型号名匹配。
//
// 同一型号可能同时出现在原生包和 Chat 兼容包里，这里按型号 ID 合并成一条，来源列出全部包。
// 只有收录它的包里至少有一个档位通过校准，才标为 ready；否则仍按基准包采样，但评分
// 只会给出「未校准、证据不足」，收录状态如实标为 pending。
func meowRegistryEntries() []RegistryEntry {
	entries := []RegistryEntry{}
	index := map[string]int{}
	for _, pkg := range loadedMeowPackages() {
		measured := pkg.Metadata.CreatedAt
		if len(measured) >= 10 {
			measured = measured[:10]
		}
		source := meowSourceLabel(pkg)
		calibrated := pkg.hasCalibratedTier()
		var pkgReadyTiers []string
		for _, t := range meowTierOrder {
			if pkg.calibrated[t] {
				pkgReadyTiers = append(pkgReadyTiers, t)
			}
		}
		for _, model := range pkg.Models {
			if model.ReferenceOnly || model.ID == MeowOtherID {
				continue
			}
			samples := 0
			for _, cell := range pkg.Fitted.Cells {
				if n := cell.SourceSamples[model.ID]; samples == 0 || n < samples {
					samples = n
				}
			}
			if at, ok := index[model.ID]; ok {
				if calibrated {
					entries[at].BaselineState = BaselineStateReady
				}
				for _, t := range pkgReadyTiers {
					if !slices.Contains(entries[at].ReadyTiers, t) {
						entries[at].ReadyTiers = append(entries[at].ReadyTiers, t)
					}
				}
				meta := &entries[at].BaselineMeta
				if !strings.Contains(meta.Source, source) {
					meta.Source += "、" + source
				}
				if measured > meta.MeasuredAt {
					meta.MeasuredAt = measured
				}
				if samples > 0 && (meta.SamplesPerProbe == 0 || samples < meta.SamplesPerProbe) {
					meta.SamplesPerProbe = samples
				}
				continue
			}
			state := BaselineStatePending
			if calibrated {
				state = BaselineStateReady
			}
			index[model.ID] = len(entries)
			entries = append(entries, RegistryEntry{
				ID: model.ID, DisplayName: model.Name, Vendor: meowVendor(model),
				RequestNames:  []string{model.ID},
				BaselineState: state,
				BaselineMeta: BaselineMeta{MeasuredAt: measured,
					Source: source, SamplesPerProbe: samples},
				ReadyTiers: slices.Clone(pkgReadyTiers),
			})
		}
	}
	return entries
}

// meowSourceLabel 写明基准出处，所有嵌入包都是官方基准。
func meowSourceLabel(pkg *meowPackage) string {
	return "meow-llm-detector 官方基准 " + pkg.ID + " " + pkg.Version
}

// meowVendor 先看 request_model 的「vendor/」前缀，再按型号 ID 的系列前缀猜厂商。
func meowVendor(model meowModel) string {
	prefixes := map[string]string{"openai": "OpenAI", "anthropic": "Anthropic"}
	if slash := strings.Index(model.RequestModel, "/"); slash > 0 {
		if name, ok := prefixes[model.RequestModel[:slash]]; ok {
			return name
		}
	}
	families := []struct{ prefix, vendor string }{
		// 与上面手写收录名单的厂商写法保持一致。
		{"gpt-", "OpenAI"}, {"claude-", "Anthropic"}, {"qwen", "阿里云百炼"}, {"deepseek", "DeepSeek"},
		{"glm-", "智谱 / Z.ai"}, {"mimo-", "小米 MiMo"}, {"kimi-", "Moonshot"},
	}
	id := strings.ToLower(model.ID)
	for _, family := range families {
		if strings.HasPrefix(id, family.prefix) {
			return family.vendor
		}
	}
	return "Meow"
}

func thresholdFor(entry RegistryEntry) float64 {
	if entry.Threshold > 0 {
		return entry.Threshold
	}
	return defaultThreshold
}
