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
		RandomPicks:        []int{42, 42, 42, 42, 47},
		LogicAnswers:       []string{"5", "5", "5"},
		LogicCorrect:       "5",
		StructureOK:        2, StructureTries: 2,
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
	for _, id := range []string{DimProtocol, DimKnowledge, DimFingerprint, DimLogic, DimStructure, DimHierarchy, DimStreaming, DimReasoning} {
		assert.Equal(t, VerdictMatch, byID[id].Verdict, "%s: %s", id, byID[id].Evidence)
	}
	assert.Equal(t, VerdictMatch, report.Verdict)
	assert.Equal(t, 100, report.Score)
	assert.NotEmpty(t, report.Disclaimer)
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
}

// 中转覆盖采样参数时，分布指纹必须自动降级，而不是给出一个假结论。
func TestEvaluateDropsFingerprintWhenTemperatureIsOverridden(t *testing.T) {
	obs := honestObservations()
	obs.TemperatureHonored = false
	finding := findingByID(t, Evaluate("gpt-4o", obs), DimFingerprint)
	assert.Equal(t, VerdictInconclusive, finding.Verdict)
	assert.Contains(t, finding.Evidence, "采样参数")
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
