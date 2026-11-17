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
	// retryAfter 非空时，非 2xx 响应带上这个 Retry-After 头（模拟中转限流）。
	retryAfter string
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
	if u.retryAfter != "" && status != http.StatusOK {
		w.Header().Set("Retry-After", u.retryAfter)
	}
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

// compressMeowPacing 把限流闸门的节奏压到亚毫秒。这些用例一个计划要发几百份请求，验证的是
// 「撞了限流还采得满」这套逻辑，不是真的等时间；闸门自身的加减速由 TestMeowGate… 单独验证。
func compressMeowPacing(t *testing.T) {
	t.Helper()
	base, ceiling, after := meowPaceBase, meowPaceCeiling, retryAfterCeiling
	meowPaceBase, meowPaceCeiling, retryAfterCeiling = time.Microsecond, 2*time.Microsecond, 2*time.Millisecond
	t.Cleanup(func() {
		meowPaceBase, meowPaceCeiling, retryAfterCeiling = base, ceiling, after
	})
}

func meowTarget(t *testing.T, upstream *meowUpstream, model string) (auditTarget, modelfp.MeowPlan) {
	t.Helper()
	compressMeowPacing(t)
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

// 中转限流时一个样本都不该丢：撞 429 的格子退回队尾，等整座闸门过了 Retry-After 的窗口
// 再试。2026-10-07 实测某中转在 340 格的计划上丢了 73 格，根子是限流重试和「答案不可用」
// 在抢同一份 retry_budget，额度一烧光，后面的 429 就一次都不再试、直接记失败。这里让前
// 200 份请求全部限流——比 retry_budget(170) 还多，旧实现必然丢格子。
func TestCollectMeowRetriesRateLimitedWithoutLosingSamples(t *testing.T) {
	const limitedRequests = 200
	upstream := &meowUpstream{retryAfter: "18"}
	upstream.answer = func(_ map[string]any, n int) (int, string, string) {
		if n <= limitedRequests {
			return http.StatusTooManyRequests, `{"error":{"message":"Too many requests"}}`, ""
		}
		return 200, "37", "stop"
	}
	target, plan := meowTarget(t, upstream, "gpt-6-sol")
	require.Greater(t, limitedRequests, (len(plan.Jobs)+1)/2)

	obs := collectMeow(context.Background(), target, plan, func(*chatResponse) {})
	assert.Equal(t, len(plan.Jobs)+limitedRequests, upstream.requests)
	assert.Equal(t, limitedRequests, obs.RateLimited)
	assert.Zero(t, obs.Failed)
	assert.Zero(t, obs.TimedOut)
	valid, invalid := totalCounts(obs)
	assert.Equal(t, len(plan.Jobs), valid)
	assert.Zero(t, invalid)
	result, ok := modelfp.ScoreMeow(*obs)
	require.True(t, ok)
	assert.Equal(t, len(plan.Jobs), result.ValidSamples)
}

// 限流闸门的加减速：撞一次限流就把请求间隔拉到「窗口长度 ÷ 这个窗口里成功发出的份数」，
// 也就是照中转能接受的频率发，而不是把整座闸门冻在窗口里睡 18 秒（2026-10-07 实测那样
// 340 份请求永远采不完，21 个窗口就把采样时限睡光了）。间隔翻倍、有上限，攒够 meowPaceRestore
// 份干净响应放回一半；等待也随时限中止——宁可这一格失败，不能把整条检测睡过去。
func TestMeowGateSlowsOnLimitAndRecovers(t *testing.T) {
	g := &meowGate{pace: 50 * time.Millisecond}
	for range 15 {
		g.clean()
	}
	g.limited(18 * time.Second)
	assert.Equal(t, 18*time.Second/15, g.pace)
	assert.Equal(t, 1, g.count())

	// 恢复期的第一手信息不可靠（窗口里一份都没发出去就又被拒），按翻倍走并撞到上限。
	g.limited(18 * time.Second)
	assert.Equal(t, meowPaceCeiling, g.pace)

	for range meowPaceRestore {
		g.clean()
	}
	assert.Equal(t, meowPaceCeiling/2, g.pace)

	g.nextAllowed = time.Now().Add(time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()
	started := time.Now()
	assert.False(t, g.hold(ctx))
	assert.Less(t, time.Since(started), time.Second)
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

// 中转把同一个型号叫成别名时：请求体里发的是用户填的请求名，计划与判定用规范型号的基准。
// 别名折算（deepseek-v4.1-flash → deepseek-flash）与 claimedModel 的分流串起来验一次。
func TestCollectMeowSendsRequestNameAndJudgesByBaseline(t *testing.T) {
	upstream := &meowUpstream{answer: func(map[string]any, int) (int, string, string) { return 200, " 37\n", "stop" }}
	target, _ := meowTarget(t, upstream, "deepseek-v4.1-flash")
	target.baseline = "deepseek-flash"

	plans := modelfp.PlanMeowAll(target.claimedModel(), modelfp.MeowDefaultTier)
	require.Len(t, plans, 1)
	require.Equal(t, "deepseek-flash", plans[0].ClaimedModel)

	obs := collectMeowPlans(context.Background(), target, plans, func(*chatResponse) {}, func() {})
	require.NotNil(t, obs)
	require.Len(t, upstream.bodies, len(plans[0].Jobs))
	for _, body := range upstream.bodies {
		assert.Equal(t, "deepseek-v4.1-flash", body["model"], "请求名按用户填的那个发")
	}
	assert.Equal(t, "deepseek-flash", obs.ClaimedModel)
	assert.Equal(t, "meowdata/f44a29d558ce6899.meow.json", obs.PackageFile)
}
