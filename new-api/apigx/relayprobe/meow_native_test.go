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

// nativeRequest 记录一次到达假上游的请求。
type nativeRequest struct {
	path    string
	headers http.Header
	body    map[string]any
}

// nativeUpstream 按路径回三种协议的响应。handle 返回非 0 状态码时直接用它和 raw 作答。
type nativeUpstream struct {
	mu       sync.Mutex
	requests []nativeRequest
	answer   string
	// finish 覆盖正常结束标记：responses 用 status，messages 用 stop_reason，chat 用 finish_reason。
	finish string
	handle func(path string, body map[string]any) (status int, raw string)
}

func (u *nativeUpstream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	_ = common.DecodeJson(r.Body, &body)
	u.mu.Lock()
	u.requests = append(u.requests, nativeRequest{path: r.URL.Path, headers: r.Header.Clone(), body: body})
	u.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if u.handle != nil {
		if status, raw := u.handle(r.URL.Path, body); status != 0 {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(raw))
			return
		}
	}
	var payload any
	switch r.URL.Path {
	case meowResponsesPath:
		status := "completed"
		if u.finish != "" {
			status = u.finish
		}
		payload = map[string]any{"id": "resp_1", "object": "response", "status": status,
			"incomplete_details": map[string]any{"reason": "max_output_tokens"},
			"output": []map[string]any{
				{"type": "reasoning", "content": []any{}},
				{"type": "message", "content": []map[string]any{{"type": "output_text", "text": u.answer}}},
			},
			"usage": map[string]any{"input_tokens": 10, "output_tokens": 2}}
	case meowMessagesPath:
		stop := "end_turn"
		if u.finish != "" {
			stop = u.finish
		}
		payload = map[string]any{"id": "msg_1", "type": "message", "role": "assistant", "stop_reason": stop,
			"content": []map[string]any{{"type": "thinking", "thinking": "..."}, {"type": "text", "text": u.answer}},
			"usage":   map[string]any{"input_tokens": 10, "output_tokens": 2}}
	default:
		payload = map[string]any{"id": "chatcmpl-1", "object": "chat.completion",
			"choices": []map[string]any{{"index": 0, "finish_reason": "stop", "message": map[string]any{"role": "assistant", "content": u.answer}}},
			"usage":   map[string]any{"prompt_tokens": 10, "completion_tokens": 2}}
	}
	data, _ := common.Marshal(payload)
	_, _ = w.Write(data)
}

func (u *nativeUpstream) paths() map[string]int {
	u.mu.Lock()
	defer u.mu.Unlock()
	out := map[string]int{}
	for _, req := range u.requests {
		out[req.path]++
	}
	return out
}

func nativeTarget(t *testing.T, upstream *nativeUpstream, model string) (auditTarget, []modelfp.MeowPlan) {
	t.Helper()
	server := httptest.NewServer(upstream)
	t.Cleanup(server.Close)
	plans := modelfp.PlanMeowAll(model, modelfp.MeowDefaultTier)
	require.Len(t, plans, 2)
	return auditTarget{client: server.Client(), base: server.URL, key: testRelayKey, model: model}, plans
}

type meowCounters struct {
	mu            sync.Mutex
	chat, counted int
}

func (c *meowCounters) record(*chatResponse) { c.mu.Lock(); c.chat++; c.mu.Unlock() }
func (c *meowCounters) count()               { c.mu.Lock(); c.counted++; c.mu.Unlock() }

// 中转支持 Responses 时按 GPT 原生包采样：请求体、请求头对齐参考实现的 gpt 模式，
// 预检多发一次、不计样本，原生响应不经 Chat 的协议统计。
func TestCollectMeowPlansUsesGPTResponses(t *testing.T) {
	upstream := &nativeUpstream{answer: " 43\n"}
	target, plans := nativeTarget(t, upstream, "gpt-6-sol")
	var counters meowCounters
	obs := collectMeowPlans(context.Background(), target, plans, counters.record, counters.count)
	require.NotNil(t, obs)

	native := plans[0]
	assert.Equal(t, modelfp.MeowModeGPT, obs.Mode)
	assert.Equal(t, native.PackageID, obs.PackageID)
	assert.Empty(t, obs.NativeFallback)
	assert.Equal(t, map[string]int{meowResponsesPath: len(native.Jobs) + 1}, upstream.paths())
	assert.Zero(t, counters.chat)
	assert.Equal(t, len(native.Jobs)+1, counters.counted)
	for _, req := range upstream.requests {
		assert.Equal(t, meowGPTUserAgent, req.headers.Get("User-Agent"))
		assert.Equal(t, "Bearer "+testRelayKey, req.headers.Get("Authorization"))
		assert.Equal(t, "gpt-6-sol", req.body["model"])
		assert.Equal(t, false, req.body["store"])
		assert.EqualValues(t, 128, req.body["max_output_tokens"])
		assert.Equal(t, map[string]any{"effort": "low"}, req.body["reasoning"])
		assert.NotContains(t, req.body, "temperature")
		input := req.body["input"].([]any)
		require.Len(t, input, 2)
		assert.Equal(t, map[string]any{"role": "system", "content": "."}, input[0])
	}
	valid, invalid := totalCounts(obs)
	assert.Equal(t, len(native.Jobs), valid)
	assert.Zero(t, invalid)
	result, ok := modelfp.ScoreMeow(*obs)
	require.True(t, ok)
	assert.Equal(t, len(native.Jobs), result.ValidSamples)
}

// Claude 原生包走 /v1/messages：system 单独放、thinking=adaptive、output_config.effort、
// anthropic-version 头；只取 text 块。
func TestCollectMeowPlansUsesClaudeMessages(t *testing.T) {
	upstream := &nativeUpstream{answer: "37"}
	target, plans := nativeTarget(t, upstream, "claude-opus-5.5")
	var counters meowCounters
	obs := collectMeowPlans(context.Background(), target, plans, counters.record, counters.count)
	require.NotNil(t, obs)

	assert.Equal(t, modelfp.MeowModeClaude, obs.Mode)
	assert.Equal(t, map[string]int{meowMessagesPath: len(plans[0].Jobs) + 1}, upstream.paths())
	for _, req := range upstream.requests {
		assert.Equal(t, meowClaudeUserAgent, req.headers.Get("User-Agent"))
		assert.Equal(t, meowAnthropicVersion, req.headers.Get("anthropic-version"))
		assert.Equal(t, ".", req.body["system"])
		assert.EqualValues(t, 128, req.body["max_tokens"])
		assert.Equal(t, map[string]any{"type": "adaptive"}, req.body["thinking"])
		assert.Equal(t, map[string]any{"effort": "low"}, req.body["output_config"])
		messages := req.body["messages"].([]any)
		require.Len(t, messages, 1)
		assert.Equal(t, "user", messages[0].(map[string]any)["role"])
	}
	for _, counts := range obs.Counts {
		assert.Equal(t, map[string]int{"37": sumCounts(counts)}, counts)
	}
}

// 中转没开原生接口（404）时回退到 Chat 兼容包，并在观测里记下回退。
func TestCollectMeowPlansFallsBackToChat(t *testing.T) {
	upstream := &nativeUpstream{answer: "43", handle: func(path string, _ map[string]any) (int, string) {
		if path == meowResponsesPath {
			return http.StatusNotFound, `{"error":{"message":"Invalid URL (POST /v1/responses)"}}`
		}
		return 0, ""
	}}
	target, plans := nativeTarget(t, upstream, "gpt-6-sol")
	var counters meowCounters
	obs := collectMeowPlans(context.Background(), target, plans, counters.record, counters.count)
	require.NotNil(t, obs)

	chat := plans[1]
	assert.Equal(t, modelfp.MeowModeChat, obs.Mode)
	assert.Equal(t, chat.PackageID, obs.PackageID)
	assert.Equal(t, modelfp.MeowModeGPT, obs.NativeFallback)
	assert.Equal(t, map[string]int{meowResponsesPath: 1, meowChatCompletionPath: len(chat.Jobs)}, upstream.paths())
	assert.Equal(t, len(chat.Jobs), counters.chat)
	assert.Equal(t, 0, counters.counted) // 预检 404 不算成功请求。
	valid, _ := totalCounts(obs)
	assert.Equal(t, len(chat.Jobs), valid)
}

// 2xx 但回的是 Chat 结构（中转把 /v1/responses 当成了 chat）也算不支持原生接口。
func TestCollectMeowPlansRejectsWrongNativeShape(t *testing.T) {
	upstream := &nativeUpstream{answer: "37", handle: func(path string, _ map[string]any) (int, string) {
		if path == meowMessagesPath {
			return http.StatusOK, `{"id":"chatcmpl-1","object":"chat.completion","choices":[{"message":{"content":"37"},"finish_reason":"stop"}]}`
		}
		return 0, ""
	}}
	target, plans := nativeTarget(t, upstream, "claude-opus-5.5")
	var counters meowCounters
	obs := collectMeowPlans(context.Background(), target, plans, counters.record, counters.count)
	require.NotNil(t, obs)
	assert.Equal(t, modelfp.MeowModeChat, obs.Mode)
	assert.Equal(t, modelfp.MeowModeClaude, obs.NativeFallback)
	assert.Equal(t, 1, upstream.paths()[meowMessagesPath])
}

// 上游拒收 thinking/output_config 时去掉推理强度参数继续用原生协议采样。
func TestCollectMeowPlansDropsRejectedClaudeEffort(t *testing.T) {
	upstream := &nativeUpstream{answer: "37", handle: func(_ string, body map[string]any) (int, string) {
		if _, ok := body["thinking"]; ok {
			return http.StatusBadRequest, `{"type":"error","error":{"type":"invalid_request_error","message":"thinking: Extra inputs are not permitted"}}`
		}
		return 0, ""
	}}
	target, plans := nativeTarget(t, upstream, "claude-opus-5.5")
	var counters meowCounters
	obs := collectMeowPlans(context.Background(), target, plans, counters.record, counters.count)
	require.NotNil(t, obs)
	assert.Equal(t, modelfp.MeowModeClaude, obs.Mode)
	assert.True(t, obs.ReasoningEffortDropped)
	assert.Empty(t, obs.NativeFallback)
	// 预检：带参数被拒 1 次 + 去掉参数成功 1 次；正式采样全部不带参数。
	assert.Equal(t, len(plans[0].Jobs)+2, upstream.paths()[meowMessagesPath])
	valid, _ := totalCounts(obs)
	assert.Equal(t, len(plans[0].Jobs), valid)
}

// 原生响应没有正常结束（Responses 的 incomplete、Messages 的 max_tokens）算请求失败，不算样本。
func TestCollectMeowNativeTruncationIsFailure(t *testing.T) {
	previous := meowMaxBackoff
	meowMaxBackoff = time.Millisecond
	t.Cleanup(func() { meowMaxBackoff = previous })
	for model, finish := range map[string]string{"gpt-6-luna": "incomplete", "claude-haiku-4.5": "max_tokens"} {
		upstream := &nativeUpstream{answer: "37", finish: finish}
		target, plans := nativeTarget(t, upstream, model)
		var counters meowCounters
		obs := collectMeowPlans(context.Background(), target, plans, counters.record, counters.count)
		require.NotNil(t, obs, model)
		// 截断仍说明中转支持该原生接口，不回退。
		assert.NotEqual(t, modelfp.MeowModeChat, obs.Mode, model)
		valid, invalid := totalCounts(obs)
		assert.Zero(t, valid+invalid, model)
		assert.Equal(t, len(plans[0].Jobs), obs.Failed, model)
	}
}

func TestParseNativeBodies(t *testing.T) {
	resp := &chatResponse{Body: []byte(`{"id":"resp_2","object":"response","status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"a"},{"type":"output_text","text":"b"}]}]}`)}
	require.NoError(t, parseResponsesBody(resp))
	assert.Equal(t, "ab", resp.Content)
	assert.Equal(t, "stop", resp.FinishReason)

	resp = &chatResponse{Body: []byte(`{"object":"response","status":"completed","output":[{"type":"message","content":[{"type":"refusal","refusal":"no"}]}]}`)}
	require.NoError(t, parseResponsesBody(resp))
	assert.Equal(t, "refusal", resp.FinishReason)

	resp = &chatResponse{Body: []byte(`{"object":"response","status":"completed","output_text":"43"}`)}
	require.NoError(t, parseResponsesBody(resp))
	assert.Equal(t, "43", resp.Content)

	resp = &chatResponse{Body: []byte(`{"type":"message","stop_reason":"tool_use","content":[{"type":"tool_use","name":"x"}]}`)}
	require.NoError(t, parseMessagesBody(resp))
	assert.Equal(t, "tool_use", resp.FinishReason)

	for _, body := range []string{`not json`, `{"object":"chat.completion","choices":[]}`, `{"status":"completed","object":"chat.completion"}`} {
		assert.ErrorIs(t, parseResponsesBody(&chatResponse{Body: []byte(body)}), errNativeShape, body)
		assert.ErrorIs(t, parseMessagesBody(&chatResponse{Body: []byte(body)}), errNativeShape, body)
	}
}

func sumCounts(counts map[string]int) int {
	total := 0
	for _, n := range counts {
		total += n
	}
	return total
}
