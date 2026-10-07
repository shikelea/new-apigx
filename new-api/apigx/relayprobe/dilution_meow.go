package relayprobe

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/apigx/modelfp"
)

// Meow 采样参数，对齐 gpt56_vnext/executor.py 的缺省值。
const (
	// meowWorkers 对应 runtime_options 的 workers=8。
	meowWorkers = 8
)

// meowMaxBackoff 是重试退避的上限。参考实现是 min(2**attempt, 8) 秒；在线检测有总时限，
// 这里压到 2 秒，只影响等待时长，不影响计数。是变量只为了让测试不必真等。
var meowMaxBackoff = 2 * time.Second

// meowRateLimitedBackoff 是 429 时的指数退避上限，比一般退避宽一档。和 meowMaxBackoff 一样
// 做成变量，测试里压到毫秒级即可，不必真等。上游回了 Retry-After 时以那段为准，
// 见 withRetryAfter。
var meowRateLimitedBackoff = 6 * time.Second

// collectMeow 按冻结计划并发采样，返回每道题归一化后的类目计数。
//
// 与参考实现一致的部分：
//   - 请求体是 protocol.build_payload 的 chat 模式：system + user 两条消息、
//     max_tokens=128、reasoning_effort=题目的 effort；不带 temperature（用上游缺省值，
//     基准也是这样采的）。
//   - finish_reason 不是正常结束（被截断、被过滤）算请求失败，不算样本。
//   - 归一化失败（空答案、超长）先重试；重试预算用完仍失败，则计为 __INVALID_OUTPUT__。
//   - 重试预算是全局共享的 (计划份数+1)/2，对应 detection_options 的 retry_budget。
//
// 与参考实现不同的部分：用非流式请求（apigx 的其余维度也都是非流式）；上游对
// 推理强度参数报 400 时去掉该字段重试，免得不支持该参数的中转整项作废。
//
// 请求走计划的协议（plan.Mode）：chat / gpt 原生 / claude 原生，见 meow_native.go。
func collectMeow(ctx context.Context, target auditTarget, plan modelfp.MeowPlan, record func(*chatResponse)) *modelfp.MeowObservation {
	return collectMeowWith(ctx, target, plan, record, true, 2)
}

// meowPreflightAttempts 是原生接口预检的尝试次数（限流、5xx 这类临时错误会再试）。
const meowPreflightAttempts = 3

// collectMeowPlans 按 PlanMeowAll 的顺序选协议：原生包先用第一道题预检一次，中转支持
// 该原生接口才按原生包采样，否则换下一个包，最后是 Chat 兼容包。都用不了返回 nil，
// 调用方回退到旧探针。
//
// record 只用于 Chat 响应（会统计 chat.completion 的协议字段）；原生响应的结构本来就
// 不同，只经 count 计数，避免被协议维度误判。
func collectMeowPlans(ctx context.Context, target auditTarget, plans []modelfp.MeowPlan, record func(*chatResponse), count func()) *modelfp.MeowObservation {
	return collectMeowPlansWith(ctx, target, plans, record, count, 2)
}

func collectMeowPlansWith(ctx context.Context, target auditTarget, plans []modelfp.MeowPlan, record func(*chatResponse), count func(), concurrency int) *modelfp.MeowObservation {
	fallback := ""
	for _, plan := range plans {
		if plan.Mode == "" || plan.Mode == modelfp.MeowModeChat {
			obs := collectMeowWith(ctx, target, plan, record, true, concurrency)
			obs.NativeFallback = fallback
			return obs
		}
		supported, withEffort := probeMeowNative(ctx, target, plan, count)
		if supported {
			return collectMeowWith(ctx, target, plan, func(*chatResponse) { count() }, withEffort, concurrency)
		}
		if ctx.Err() != nil {
			return nil
		}
		if fallback == "" {
			fallback = plan.Mode
		}
	}
	return nil
}

// probeMeowNative 用计划里第一道题按原生协议发一次。只要上游按该协议正常应答（哪怕答案
// 被截断）就算支持；预检本身不计入样本。第二个返回值表示是否还发送推理强度参数。
func probeMeowNative(ctx context.Context, target auditTarget, plan modelfp.MeowPlan, count func()) (bool, bool) {
	if len(plan.Jobs) == 0 {
		return false, true
	}
	cell := plan.Jobs[0].Cell
	withEffort := true
	for attempt := 1; attempt <= meowPreflightAttempts; {
		if ctx.Err() != nil {
			return false, withEffort
		}
		resp, err := target.meowRequest(ctx, plan.Mode, cell, withEffort)
		if err == nil {
			count() // 只统计成功的请求，与 record 的口径一致。
			return true, withEffort
		}
		if withEffort && meowEffortRejected(plan.Mode, resp) {
			withEffort = false // 参数兼容性问题，不占尝试次数；只会发生一次。
			continue
		}
		attempt++
		if meowNativeUnsupported(resp, err) {
			return false, withEffort
		}
		if resp != nil && (resp.HTTPStatus == http.StatusUnauthorized || resp.HTTPStatus == http.StatusForbidden) {
			return false, withEffort
		}
		if attempt <= meowPreflightAttempts && !sleepContext(ctx, withRetryAfter(resp, exponentialBackoff(attempt, time.Second, meowMaxBackoff))) {
			return false, withEffort
		}
	}
	return false, withEffort
}

func collectMeowWith(ctx context.Context, target auditTarget, plan modelfp.MeowPlan, record func(*chatResponse), sendEffort bool, concurrency int) *modelfp.MeowObservation {
	mode := plan.Mode
	if mode == "" {
		mode = modelfp.MeowModeChat
	}
	obs := &modelfp.MeowObservation{
		PackageFile: plan.PackageFile, PackageID: plan.PackageID, PackageVersion: plan.PackageVersion,
		Tier: plan.Tier, ClaimedModel: plan.ClaimedModel, Mode: mode,
		Counts:                 map[string]map[string]int{},
		ReasoningEffortDropped: !sendEffort,
	}
	var mu sync.Mutex
	retryBudget := (len(plan.Jobs) + 1) / 2
	retriesUsed := 0

	takeRetry := func() bool {
		mu.Lock()
		defer mu.Unlock()
		if retriesUsed >= retryBudget {
			return false
		}
		retriesUsed++
		return true
	}
	finish := func(cell, category string, failed bool) {
		mu.Lock()
		defer mu.Unlock()
		if failed {
			obs.Failed++
			return
		}
		if obs.Counts[cell] == nil {
			obs.Counts[cell] = map[string]int{}
		}
		obs.Counts[cell][category]++
	}

	run := func(job modelfp.MeowJob) {
		for attempt := 1; ; attempt++ {
			if ctx.Err() != nil {
				finish(job.Cell.ID, "", true)
				return
			}
			mu.Lock()
			withEffort := sendEffort
			mu.Unlock()
			resp, err := target.meowRequest(ctx, mode, job.Cell, withEffort)
			if err != nil && withEffort && meowEffortRejected(mode, resp) {
				mu.Lock()
				sendEffort = false
				obs.ReasoningEffortDropped = true
				mu.Unlock()
				continue // 参数兼容性问题，不消耗重试预算。
			}
			category, retryable := "", true
			switch {
			case err != nil:
				// 鉴权失败重试也没用。
				if resp != nil && (resp.HTTPStatus == http.StatusUnauthorized || resp.HTTPStatus == http.StatusForbidden) {
					retryable = false
				}
			case !normalFinish(resp.FinishReason):
				mu.Lock()
				record(resp)
				mu.Unlock()
			default:
				mu.Lock()
				record(resp)
				mu.Unlock()
				category = job.Cell.Normalize(resp.Content)
				if category != modelfp.MeowInvalidOutput {
					finish(job.Cell.ID, category, false)
					return
				}
			}
			if retryable && takeRetry() {
				backoff := exponentialBackoff(attempt, time.Second, meowMaxBackoff)
				if resp != nil && resp.HTTPStatus == http.StatusTooManyRequests {
					backoff = exponentialBackoff(attempt, 2*time.Second, meowRateLimitedBackoff)
				}
				if !sleepContext(ctx, withRetryAfter(resp, backoff)) {
					finish(job.Cell.ID, "", true)
					return
				}
				continue
			}
			// 预算用完：答案无效计为 __INVALID_OUTPUT__，请求失败则不计样本。
			finish(job.Cell.ID, category, category != modelfp.MeowInvalidOutput)
			return
		}
	}

	workers := concurrency
	if workers <= 0 {
		workers = 2
	}
	jobs := make(chan modelfp.MeowJob)
	var wg sync.WaitGroup
	for range min(workers, len(plan.Jobs)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobs {
				run(job)
			}
		}()
	}
	fed := 0
feed:
	for _, job := range plan.Jobs {
		select {
		case jobs <- job:
			fed++
		case <-ctx.Done():
			break feed
		}
	}
	close(jobs)
	wg.Wait()
	// 超时没来得及发出的请求也算失败，否则「N 次请求失败」会少报。
	obs.Failed += len(plan.Jobs) - fed
	return obs
}

// normalFinish 对应 protocol.py 只接受 finish_reason == "stop"。部分中转把 Anthropic
// 的 end_turn 原样透传，或干脆不填，这两种也视为正常结束。拒答、工具调用在 chat() 与
// 原生解析里已映射成 refusal / tool_calls / tool_use，不会被这里放行。
func normalFinish(reason string) bool {
	switch reason {
	case "stop", "end_turn", "":
		return true
	}
	return false
}

func sleepContext(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// collectLegacyProbes 是不在 Meow 基准内的型号沿用的原有探针采样。
//
// 这里必须用 temperature=1，不能用 0。我们要测的是型号自身的答案偏好分布，
// 温度压到 0 只会拿到一个固定答案，分布信息全部丢失。
func collectLegacyProbes(ctx context.Context, target auditTarget, obs *modelfp.Observations, record func(*chatResponse)) {
	obs.ProbeAnswers = make(map[string]modelfp.ProbeCounts, len(modelfp.Probes))
	for _, probe := range modelfp.Probes {
		counts := modelfp.ProbeCounts{}
		for range dilutionProbeRepeats {
			if ctx.Err() != nil {
				break
			}
			resp, err := target.chat(ctx, map[string]any{
				"messages":    userMessages(probe.System, probe.Prompt),
				"max_tokens":  probe.MaxTokens,
				"temperature": 1,
			})
			if err != nil {
				continue
			}
			record(resp)
			// 截断（多为思维链吃满预算）不是一份答案，重试也不会更好，直接下一题。
			if !normalFinish(resp.FinishReason) {
				continue
			}
			if answer := modelfp.NormalizeAnswer(resp.Content); answer != "" {
				counts[answer]++
			}
		}
		if len(counts) > 0 {
			obs.ProbeAnswers[probe.ID] = counts
		}
	}
}
