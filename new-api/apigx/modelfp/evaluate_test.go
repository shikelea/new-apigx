package modelfp

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLookupProfilePrefersTheMoreSpecificHint(t *testing.T) {
	gpt41, ok := LookupProfile("gpt-4.1-mini")
	require.True(t, ok)
	assert.Equal(t, "OpenAI GPT-4.1", gpt41.Family)
	assert.Equal(t, 2024, gpt41.KnowledgeCutoffYear)

	reasoning, ok := LookupProfile("o3-mini")
	require.True(t, ok)
	assert.True(t, reasoning.Reasoning)

	vendorPrefixed, ok := LookupProfile("openai/gpt-4o-2024-08-06")
	require.True(t, ok)
	assert.Equal(t, "OpenAI GPT-4o", vendorPrefixed.Family)

	_, ok = LookupProfile("neohorse-1-9b")
	assert.False(t, ok)

	// R1 要盖过通用 DeepSeek 那条，V3 不能被判成推理型号。
	r1, ok := LookupProfile("deepseek-reasoner")
	require.True(t, ok)
	assert.Equal(t, "DeepSeek R1", r1.Family)
	assert.True(t, r1.Reasoning)

	chat, ok := LookupProfile("deepseek-chat")
	require.True(t, ok)
	assert.False(t, chat.Reasoning)

	// o1、o3 这类短提示只按前缀匹配，否则名字里正好连着这两个字符的型号会被认成 o 系。
	cosmo, ok := LookupProfile("cosmo3-7b")
	assert.False(t, ok)
	assert.Empty(t, cosmo.Family)
}

// 观测全部正常时，八个维度都应当判定为一致。
func honestObservations() Observations {
	return Observations{
		ResponseIDs:        []string{"chatcmpl-1", "chatcmpl-2", "chatcmpl-3", "chatcmpl-4"},
		ObjectTypes:        []string{"chat.completion", "chat.completion"},
		HasSystemFinger:    true,
		SystemFingerPrints: []string{"fp_abc", "fp_abc"},
		ClaimedCutoffYear:  2023,
		FutureEventProbes:  2,
		KnewFutureEvents:   0,
		ProbeAnswers: map[string]ProbeCounts{
			ProbeIntegerPick: {"42": 5, "47": 1},
			ProbeFruitPick:   {"apple": 4, "banana": 2},
		},
		LogicAnswers: []string{"5", "5", "5"},
		LogicCorrect: "5",
		StructureOK:  2, StructureTries: 2,
		HierarchyOK: 2, HierarchyTries: 2,
		StreamChunks: 40, StreamDone: true, StreamTokens: 120,
		CompletionTokens:   120,
		TemperatureHonored: true,
	}
}

func TestEvaluateAcceptsAnHonestGPTRelay(t *testing.T) {
	report := Evaluate("gpt-4o", honestObservations())
	require.Len(t, report.Findings, len(Dimensions))
	byID := make(map[string]Finding, len(report.Findings))
	for _, finding := range report.Findings {
		byID[finding.ID] = finding
		assert.NotEmpty(t, finding.Name, finding.ID)
		assert.NotEmpty(t, finding.Evidence, finding.ID)
	}
	for _, id := range []string{DimProtocol, DimKnowledge, DimLogic, DimStructure, DimHierarchy, DimStreaming, DimReasoning} {
		assert.Equal(t, VerdictMatch, byID[id].Verdict, "%s: %s", id, byID[id].Evidence)
	}
	// 分布指纹在有实测基准之前必须是证据不足，不能凭空给「一致」。
	assert.Equal(t, VerdictInconclusive, byID[DimFingerprint].Verdict, byID[DimFingerprint].Evidence)
	assert.Equal(t, VerdictMatch, report.Verdict)
	assert.Equal(t, 100, report.Score)
	assert.NotEmpty(t, report.Disclaimer)
}

// 收录名单里还没有任何实测基准，所以指纹维度只能报证据不足，且要说清原因。
func TestEvaluateFingerprintWaitsForMeasuredBaselines(t *testing.T) {
	// gpt-4o 既没有手写基准，也不在 Meow 基准包里：指纹维度只能报证据不足。
	finding := findingByID(t, Evaluate("gpt-4o", honestObservations()), DimFingerprint)
	assert.Equal(t, VerdictInconclusive, finding.Verdict)
	assert.Contains(t, finding.Evidence, "基准")
}

// 不在收录名单里的型号，指纹维度不参与判定。
func TestEvaluateFingerprintSkipsUnlistedModels(t *testing.T) {
	finding := findingByID(t, Evaluate("neohorse-1-9b", honestObservations()), DimFingerprint)
	assert.Equal(t, VerdictInconclusive, finding.Verdict)
	assert.Contains(t, finding.Evidence, "收录名单")
}

func TestEvaluateFlagsASubstitutedBackend(t *testing.T) {
	obs := honestObservations()
	// 一个换了后端的中转：id 前缀不对、没有 system_fingerprint、结构化输出大面积不达标、
	// 同题答案漂移、伪流式重新切块。
	obs.ResponseIDs = []string{"gen-1", "gen-2", "gen-3"}
	obs.HasSystemFinger = false
	obs.SystemFingerPrints = nil
	obs.StructureOK, obs.StructureTries = 0, 2
	obs.HierarchyOK, obs.HierarchyTries = 0, 2
	obs.LogicAnswers = []string{"5", "10", "100"}
	obs.StreamChunks, obs.StreamTokens = 2, 120

	report := Evaluate("gpt-4o", obs)
	byID := make(map[string]Finding, len(report.Findings))
	for _, finding := range report.Findings {
		byID[finding.ID] = finding
	}
	assert.Equal(t, VerdictMismatch, byID[DimProtocol].Verdict, byID[DimProtocol].Evidence)
	assert.Contains(t, byID[DimProtocol].Evidence, "chatcmpl-")
	assert.Equal(t, VerdictMismatch, byID[DimStructure].Verdict, byID[DimStructure].Evidence)
	assert.Equal(t, VerdictMismatch, byID[DimHierarchy].Verdict, byID[DimHierarchy].Evidence)
	assert.Equal(t, VerdictMismatch, byID[DimLogic].Verdict, byID[DimLogic].Evidence)
	assert.Equal(t, VerdictSuspect, byID[DimStreaming].Verdict, byID[DimStreaming].Evidence)
	assert.Equal(t, VerdictMismatch, report.Verdict)
	assert.Less(t, report.Score, 70)
}

// 推理型号缺少 reasoning_tokens 是强信号；非推理型号多出 reasoning_tokens 也要提示。
func TestEvaluateChecksReasoningEffortBothWays(t *testing.T) {
	obs := honestObservations()
	report := Evaluate("o3-mini", obs)
	finding := findingByID(t, report, DimReasoning)
	assert.Equal(t, VerdictMismatch, finding.Verdict, finding.Evidence)
	assert.Contains(t, finding.Evidence, "reasoning_tokens")

	obs.ReasoningPresent, obs.ReasoningTokens = true, 512
	reasoning := findingByID(t, Evaluate("o3-mini", obs), DimReasoning)
	assert.Equal(t, VerdictMatch, reasoning.Verdict, reasoning.Evidence)

	unexpected := findingByID(t, Evaluate("gpt-4o", obs), DimReasoning)
	assert.Equal(t, VerdictSuspect, unexpected.Verdict, unexpected.Evidence)

	// deepseek-reasoner 的推理特征只能从 profile.go 的公开特征表里取到：收录表那条的
	// Profile 不参与判定。曾经因为表里缺这一条，两个方向都判反了。
	obs = honestObservations()
	obs.ReasoningPresent, obs.ReasoningTokens = true, 768
	honestR1 := findingByID(t, Evaluate("deepseek-reasoner", obs), DimReasoning)
	assert.Equal(t, VerdictMatch, honestR1.Verdict, honestR1.Evidence)

	noOverhead := findingByID(t, Evaluate("deepseek-r1", honestObservations()), DimReasoning)
	assert.Equal(t, VerdictMismatch, noOverhead.Verdict, noOverhead.Evidence)

	// deepseek-chat 是非推理型号，多出推理开销只提示，不直接判掺水。
	chat := findingByID(t, Evaluate("deepseek-chat", obs), DimReasoning)
	assert.Equal(t, VerdictSuspect, chat.Verdict, chat.Evidence)

	// flash 是混合推理型号：官方入口实测每次都上报 reasoning_tokens，所以「上报了」不能
	// 当成换模型的证据；反过来「没上报」也不能反证，只能记证据不足。
	flash := findingByID(t, Evaluate("deepseek-flash", obs), DimReasoning)
	assert.Equal(t, VerdictMatch, flash.Verdict, flash.Evidence)
	assert.Contains(t, flash.Evidence, "可推理型号")

	quietFlash := findingByID(t, Evaluate("deepseek-v4.1-flash", honestObservations()), DimReasoning)
	assert.Equal(t, VerdictInconclusive, quietFlash.Verdict, quietFlash.Evidence)
	assert.Contains(t, quietFlash.Evidence, "未见推理开销")
}

// 中转覆盖采样参数时，分布指纹必须自动降级，而不是给出一个假结论。
func TestEvaluateDropsFingerprintWhenTemperatureIsOverridden(t *testing.T) {
	obs := honestObservations()
	obs.TemperatureHonored = false
	finding := findingByID(t, Evaluate("gpt-4o", obs), DimFingerprint)
	assert.Equal(t, VerdictInconclusive, finding.Verdict)
	assert.Contains(t, finding.Evidence, "采样参数")
}

// 请求名反查：更长更具体的名字优先，带厂商前缀也要能认出来。
func TestResolveRequestNamePrefersTheMoreSpecificName(t *testing.T) {
	glm, ok := ResolveRequestName("glm-4.6")
	require.True(t, ok)
	assert.Equal(t, "zhipu-glm-4-6", glm.ID)

	prefixed, ok := ResolveRequestName("openai/gpt-4o-2024-08-06")
	require.True(t, ok)
	assert.Equal(t, "openai-gpt-4o", prefixed.ID)

	_, ok = ResolveRequestName("neohorse-1-9b")
	assert.False(t, ok)

	_, ok = ResolveRequestName("  ")
	assert.False(t, ok)
}

// 收录项必须有完整元信息，且在没实测之前一律是 pending——避免有人填了半份基准就上线。
func TestRegistryEntriesAreWellFormed(t *testing.T) {
	entries := Registry()
	require.NotEmpty(t, entries)
	probeIDs := make(map[string]bool, len(Probes))
	for _, probe := range Probes {
		probeIDs[probe.ID] = true
	}
	seen := make(map[string]bool, len(entries))
	for _, entry := range entries {
		assert.NotEmpty(t, entry.ID)
		assert.NotEmpty(t, entry.DisplayName, entry.ID)
		assert.NotEmpty(t, entry.Vendor, entry.ID)
		assert.NotEmpty(t, entry.RequestNames, entry.ID)
		assert.False(t, seen[entry.ID], "重复的收录 ID：%s", entry.ID)
		seen[entry.ID] = true
		if entry.BaselineState == BaselineStateReady {
			// 基准要么手写在收录项里，要么来自嵌入的 Meow 基准包。
			_, meow := PlanMeow(entry.ID, "")
			assert.True(t, len(entry.Baseline) > 0 || meow, "%s 标为 ready 却没有基准", entry.ID)
			assert.NotEmpty(t, entry.BaselineMeta.MeasuredAt, "%s 缺实测日期", entry.ID)
			assert.Positive(t, entry.BaselineMeta.SamplesPerProbe, "%s 缺实测次数", entry.ID)
			// 手写基准的题号必须是启用中的探针：拼错的题号在比对时会被整题跳过，看起来
			// 却仍是「已实测」。空答案类目同理——那是一次没采到，不是模型偏好。
			for probeID, counts := range entry.Baseline {
				assert.True(t, probeIDs[probeID], "%s 基准含未知题目 %s", entry.ID, probeID)
				for answer := range counts {
					assert.NotEmpty(t, answer, "%s 基准 %s 含空答案类目", entry.ID, probeID)
				}
			}
			continue
		}
		assert.Equal(t, BaselineStatePending, entry.BaselineState, entry.ID)
		assert.Empty(t, entry.Baseline, "%s 还是 pending 却带了基准", entry.ID)
	}
}

// 无法识别的型号不能被判成掺水：没有公开特征可比，只能是证据不足。
func TestEvaluateStaysInconclusiveForUnknownModels(t *testing.T) {
	obs := Observations{TemperatureHonored: true, ResponseIDs: []string{"resp-1"}, ObjectTypes: []string{"chat.completion"}}
	report := Evaluate("neohorse-1-9b", obs)
	assert.Equal(t, "未知型号", report.Family)
	assert.Equal(t, VerdictInconclusive, report.Verdict)
	for _, finding := range report.Findings {
		assert.NotEqual(t, VerdictMismatch, finding.Verdict, finding.ID)
	}
}

// 知识时效：声称知道截止之后的事，应当标为可疑。
func TestEvaluateFlagsKnowledgeBeyondCutoff(t *testing.T) {
	obs := honestObservations()
	obs.KnewFutureEvents, obs.FutureEventProbes = 2, 2
	finding := findingByID(t, Evaluate("gpt-4o", obs), DimKnowledge)
	assert.Equal(t, VerdictSuspect, finding.Verdict, finding.Evidence)
}

func findingByID(t *testing.T, report Report, id string) Finding {
	t.Helper()
	for _, finding := range report.Findings {
		if finding.ID == id {
			return finding
		}
	}
	require.FailNowf(t, "missing finding", "%s", id)
	return Finding{}
}
