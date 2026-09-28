package modelfp

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 两个人造候选：A 偏好 42 与 apple，B 偏好 7 与 banana。真实基准还没实测，所以这里用
// 合成基准验证判定逻辑本身——纯函数，不碰网络。
func syntheticCandidates() []Candidate {
	return []Candidate{
		{ID: "cand-a", Baseline: map[string]ProbeCounts{
			ProbeIntegerPick: {"42": 34, "37": 5, "50": 1},
			ProbeFruitPick:   {"apple": 30, "orange": 8, "mango": 2},
		}},
		{ID: "cand-b", Baseline: map[string]ProbeCounts{
			ProbeIntegerPick: {"7": 33, "23": 6, "42": 1},
			ProbeFruitPick:   {"banana": 28, "apple": 9, "pear": 3},
		}},
		{ID: referenceCandidate, Baseline: map[string]ProbeCounts{
			ProbeIntegerPick: {"42": 1, "37": 1, "50": 1, "7": 1, "23": 1},
			ProbeFruitPick:   {"apple": 1, "orange": 1, "mango": 1, "banana": 1, "pear": 1},
		}},
	}
}

func TestScoreFingerprintPicksTheMatchingCandidate(t *testing.T) {
	observed := map[string]ProbeCounts{
		ProbeIntegerPick: {"42": 6},
		ProbeFruitPick:   {"apple": 5, "orange": 1},
	}
	outcome, ok := ScoreFingerprint(observed, syntheticCandidates())
	require.True(t, ok)
	assert.Equal(t, "cand-a", outcome.Best)
	assert.Equal(t, 12, outcome.Samples)
	assert.Positive(t, outcome.Advantage)
	assert.Greater(t, outcome.Confidence, 0.5)
	assert.Len(t, outcome.Ranked, 3)

	verdict, evidence := describeOutcome("cand-a", outcome, defaultThreshold)
	assert.Equal(t, VerdictMatch, verdict)
	assert.Contains(t, evidence, "12")
}

// 观测像 B 却声称是 A，必须判为不一致，而不是勉强算作一致。
func TestScoreFingerprintFlagsASubstitution(t *testing.T) {
	observed := map[string]ProbeCounts{
		ProbeIntegerPick: {"7": 6},
		ProbeFruitPick:   {"banana": 6},
	}
	outcome, ok := ScoreFingerprint(observed, syntheticCandidates())
	require.True(t, ok)
	assert.Equal(t, "cand-b", outcome.Best)

	verdict, evidence := describeOutcome("cand-a", outcome, defaultThreshold)
	assert.Equal(t, VerdictMismatch, verdict)
	assert.Contains(t, evidence, "更像")
}

// 答案落在已知答案空间之外时，应当归到兜底候选，而不是硬分给稍微近一点的那个。
func TestScoreFingerprintFallsBackToTheReferenceCandidate(t *testing.T) {
	observed := map[string]ProbeCounts{
		ProbeIntegerPick: {"99": 6},
		ProbeFruitPick:   {"durian": 6},
	}
	outcome, ok := ScoreFingerprint(observed, syntheticCandidates())
	require.True(t, ok)
	assert.Equal(t, referenceCandidate, outcome.Best)

	verdict, evidence := describeOutcome("cand-a", outcome, defaultThreshold)
	assert.Equal(t, VerdictMismatch, verdict)
	assert.Contains(t, evidence, "未建模")
}

// 观测既不明显像 A 也不明显像 B 时，优势达不到判定线，只能报证据不足。
func TestScoreFingerprintStaysInconclusiveOnAmbiguousEvidence(t *testing.T) {
	observed := map[string]ProbeCounts{
		ProbeIntegerPick: {"42": 1, "7": 1},
		ProbeFruitPick:   {"apple": 1, "banana": 1},
	}
	outcome, ok := ScoreFingerprint(observed, syntheticCandidates())
	require.True(t, ok)
	assert.Less(t, outcome.Confidence, defaultThreshold)

	verdict, evidence := describeOutcome("cand-a", outcome, defaultThreshold)
	assert.Equal(t, VerdictInconclusive, verdict)
	assert.Contains(t, evidence, "证据不足")
}

// 基准里没有的题目要整题跳过，不能把它当成「全是未见答案」去压低所有候选。
func TestScoreFingerprintIgnoresProbesWithoutBaseline(t *testing.T) {
	observed := map[string]ProbeCounts{
		ProbeIntegerPick: {"42": 6},
		ProbeColorPick:   {"teal": 20},
	}
	outcome, ok := ScoreFingerprint(observed, syntheticCandidates())
	require.True(t, ok)
	assert.Equal(t, 6, outcome.Samples, "未建模的题目不应计入样本量")
	assert.Equal(t, "cand-a", outcome.Best)
}

func TestScoreFingerprintNeedsEnoughInput(t *testing.T) {
	_, ok := ScoreFingerprint(map[string]ProbeCounts{}, syntheticCandidates())
	assert.False(t, ok, "没有观测时不应给出结果")

	_, ok = ScoreFingerprint(map[string]ProbeCounts{ProbeIntegerPick: {"42": 6}}, syntheticCandidates()[:1])
	assert.False(t, ok, "只有一个候选时无从比较")
}

// 同分时必须按 ID 稳定排序，否则同样的观测会给出漂移的结论。
func TestScoreFingerprintIsDeterministicOnTies(t *testing.T) {
	candidates := []Candidate{
		{ID: "cand-b", Baseline: map[string]ProbeCounts{ProbeIntegerPick: {"42": 10}}},
		{ID: "cand-a", Baseline: map[string]ProbeCounts{ProbeIntegerPick: {"42": 10}}},
	}
	observed := map[string]ProbeCounts{ProbeIntegerPick: {"42": 6}}
	for range 5 {
		outcome, ok := ScoreFingerprint(observed, candidates)
		require.True(t, ok)
		assert.Equal(t, "cand-a", outcome.Best)
		assert.Zero(t, outcome.Advantage)
	}
}

func TestNormalizeAnswerIsConservative(t *testing.T) {
	assert.Equal(t, "42", NormalizeAnswer("  42\n"))
	assert.Equal(t, "42", NormalizeAnswer("\"42\"."))
	assert.Equal(t, "apple", NormalizeAnswer("Apple"))
	assert.Equal(t, "42", NormalizeAnswer("42\nBut any number works."))
	assert.Empty(t, NormalizeAnswer("   "))
	// 不做同义词合并：crimson 不能并进 red，那是我们替模型编造的等价关系。
	assert.Equal(t, "crimson", NormalizeAnswer("Crimson"))
}

func TestProbesAreWellFormed(t *testing.T) {
	require.NotEmpty(t, Probes)
	seen := make(map[string]bool, len(Probes))
	for _, probe := range Probes {
		assert.NotEmpty(t, probe.ID)
		assert.NotEmpty(t, probe.Prompt, probe.ID)
		assert.Positive(t, probe.MaxTokens, probe.ID)
		assert.False(t, seen[probe.ID], "重复的题目 ID：%s", probe.ID)
		seen[probe.ID] = true
	}
}
