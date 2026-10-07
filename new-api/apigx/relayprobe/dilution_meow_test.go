package relayprobe

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/apigx/modelfp"
	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// meowUpstream 是一个只回答 Meow 探针的假上游，记录每个请求体。
type meowUpstream struct {
	mu       sync.Mutex
	bodies   []map[string]any
	answer   func(body map[string]any, n int) (status int, content, finish string)
	requests int
}

func (u *meowUpstream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	_ = common.DecodeJson(r.Body, &body)
	u.mu.Lock()
	u.requests++
	n := u.requests
	u.bodies = append(u.bodies, body)
	u.mu.Unlock()
	status, content, finish := u.answer(body, n)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if status != http.StatusOK {
		_, _ = w.Write([]byte(content))
		return
	}
	payload, _ := common.Marshal(map[string]any{
		"id": "chatcmpl-1", "object": "chat.completion",
		"choices": []map[string]any{{"index": 0, "finish_reason": finish, "message": map[string]any{"role": "assistant", "content": content}}},
		"usage":   map[string]any{"prompt_tokens": 10, "completion_tokens": 2},
	})
	_, _ = w.Write(payload)
}

func meowTarget(t *testing.T, upstream *meowUpstream, model string) (auditTarget, modelfp.MeowPlan) {
	t.Helper()
	server := httptest.NewServer(upstream)
	t.Cleanup(server.Close)
	plan, ok := modelfp.PlanMeow(model, modelfp.MeowDefaultTier)
	require.True(t, ok)
	return auditTarget{client: server.Client(), base: server.URL, key: testRelayKey, model: model}, plan
}

func totalCounts(obs *modelfp.MeowObservation) (valid, invalid int) {
	for _, counts := range obs.Counts {
		for category, n := range counts {
			if category == modelfp.MeowInvalidOutput {
				invalid += n
			} else {
				valid += n
			}
		}
	}
	return
}

// 请求体对齐参考实现的 chat 模式：system="."、max_tokens=128、reasoning_effort=low、
// 不带 temperature；请求份数与档位计划一致。
func TestCollectMeowFollowsReferencePayloadAndPlan(t *testing.T) {
	upstream := &meowUpstream{answer: func(map[string]any, int) (int, string, string) { return 200, " 37\n", "stop" }}
	target, plan := meowTarget(t, upstream, "gpt-6-sol")
	records := 0
	obs := collectMeow(context.Background(), target, plan, func(*chatResponse) { records++ })

	require.Len(t, upstream.bodies, len(plan.Jobs))
	assert.Equal(t, len(plan.Jobs), records)
	for _, body := range upstream.bodies {
		assert.Equal(t, "gpt-6-sol", body["model"])
		assert.Equal(t, "low", body["reasoning_effort"])
		assert.EqualValues(t, 128, body["max_tokens"])
		assert.NotContains(t, body, "temperature")
		messages := body["messages"].([]any)
		require.Len(t, messages, 2)
		assert.Equal(t, ".", messages[0].(map[string]any)["content"])
	}
	valid, invalid := totalCounts(obs)
	assert.Equal(t, len(plan.Jobs), valid)
	assert.Zero(t, invalid)
	assert.Zero(t, obs.Failed)
	for _, counts := range obs.Counts {
		assert.Contains(t, counts, "37")
	}
	result, ok := modelfp.ScoreMeow(*obs)
	require.True(t, ok)
	assert.Equal(t, len(plan.Jobs), result.ValidSamples)
}

// 上游拒收 reasoning_effort 时去掉该字段继续采，不让整项作废。
func TestCollectMeowDropsRejectedReasoningEffort(t *testing.T) {
	upstream := &meowUpstream{answer: func(body map[string]any, _ int) (int, string, string) {
		if _, ok := body["reasoning_effort"]; ok {
			return 400, `{"error":{"message":"Unrecognized request argument supplied: reasoning_effort"}}`, ""
		}
		return 200, "37", "stop"
	}}
	target, plan := meowTarget(t, upstream, "claude-opus-5.5")
	obs := collectMeow(context.Background(), target, plan, func(*chatResponse) {})
	assert.True(t, obs.ReasoningEffortDropped)
	valid, _ := totalCounts(obs)
	assert.Equal(t, len(plan.Jobs), valid)
	assert.Zero(t, obs.Failed)
}

// 空答案先重试，共享重试预算 (计划份数+1)/2 用完后计为 __INVALID_OUTPUT__；被截断的
// 响应不算样本。
func TestCollectMeowRetryBudgetAndInvalidOutputs(t *testing.T) {
	previous := meowMaxBackoff
	meowMaxBackoff = time.Millisecond
	t.Cleanup(func() { meowMaxBackoff = previous })

	upstream := &meowUpstream{answer: func(map[string]any, int) (int, string, string) { return 200, "   ", "stop" }}
	target, plan := meowTarget(t, upstream, "gpt-6-luna")
	obs := collectMeow(context.Background(), target, plan, func(*chatResponse) {})
	budget := (len(plan.Jobs) + 1) / 2
	assert.Equal(t, len(plan.Jobs)+budget, upstream.requests)
	valid, invalid := totalCounts(obs)
	assert.Zero(t, valid)
	assert.Equal(t, len(plan.Jobs), invalid)
	result, _ := modelfp.ScoreMeow(*obs)
	assert.Equal(t, "insufficient", result.Verdict)
	assert.Contains(t, result.Reasons, "no_valid_samples")

	truncated := &meowUpstream{answer: func(map[string]any, int) (int, string, string) { return 200, "37", "length" }}
	target, plan = meowTarget(t, truncated, "gpt-6-luna")
	obs = collectMeow(context.Background(), target, plan, func(*chatResponse) {})
	valid, invalid = totalCounts(obs)
	assert.Zero(t, valid+invalid)
	assert.Equal(t, len(plan.Jobs), obs.Failed)
}

// 时限到了还没发出的请求也计入 Failed，加上已发出的样本正好等于计划份数。
func TestCollectMeowCountsUnsentJobsAsFailed(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	upstream := &meowUpstream{answer: func(_ map[string]any, n int) (int, string, string) {
		if n == 1 {
			cancel()
		}
		return 200, "37", "stop"
	}}
	target, plan := meowTarget(t, upstream, "gpt-6-sol")
	obs := collectMeow(ctx, target, plan, func(*chatResponse) {})
	valid, invalid := totalCounts(obs)
	assert.Less(t, valid+invalid, len(plan.Jobs))
	assert.Equal(t, len(plan.Jobs), valid+invalid+obs.Failed)
}

// Chat 响应里带 refusal 或 tool_calls 时不算正常作答，即便 finish_reason 为空。
func TestChatMapsRefusalAndToolCalls(t *testing.T) {
	for name, message := range map[string]map[string]any{
		"refusal":    {"role": "assistant", "content": "", "refusal": "I can't help with that."},
		"tool_calls": {"role": "assistant", "content": nil, "tool_calls": []any{map[string]any{"id": "c1", "type": "function"}}},
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				payload, _ := common.Marshal(map[string]any{"id": "x", "choices": []map[string]any{{"finish_reason": "", "message": message}}})
				_, _ = w.Write(payload)
			}))
			t.Cleanup(server.Close)
			target := auditTarget{client: server.Client(), base: server.URL, key: testRelayKey, model: "m"}
			resp, err := target.chat(context.Background(), map[string]any{"messages": userMessages("", "hi")})
			require.NoError(t, err)
			assert.Equal(t, name, resp.FinishReason)
			assert.False(t, normalFinish(resp.FinishReason))
		})
	}
}
