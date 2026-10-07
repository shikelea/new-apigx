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
	// meowTransientAttempts 是「这一格根本没拿到可用响应」（网络错误、非 2xx，401/403 除外）
	// 时单个任务能重试几次。参考实现对这类失败不设每任务上限，只受全局 retry_budget 约束；
	// 这里改成按任务计数，是因为在线检测还有总时限，一个一直撞限流的格子不能把别人的份额
	// 提前吃光——重试排队尾，额度按格子分。
	meowTransientAttempts = 6
	// meowPaceRestore 是连续多少份干净响应之后把请求间隔放回一半。
	meowPaceRestore = 10
)

var (
	// meowPaceBase/meowPaceCeiling 是限流闸门允许的请求间隔下限与上限。做成变量只为测试
	// 不必真等。上限压到 2 秒是量出来的：2026-10-07 那台中转的高档计划要 340 份请求，
	// 540 秒的采样时限要求平均间隔不高于 1.6 秒，再慢就注定采不完——上限若放到 500 毫秒
	// 以上，撞限流之后请求间隔一路翻到几分钟，样本反而全丢。
	meowPaceBase    = 50 * time.Millisecond
	meowPaceCeiling = 2 * time.Second
)

// meowTask 是一个待办采样任务，带着自己已经用掉的临时重试次数——重试要排队尾重跑，
// 次数必须跟着任务走，不能放在循环的局部变量里。
type meowTask struct {
	job       modelfp.MeowJob
	transient int
}

// meowQueue 是采样任务的公平队列：重试的任务回到队尾。
//
// 参考实现就是把失败的任务重新排队、一轮一轮跑（executor.py 的 retry_jobs），好处是每个格子
// 都先被试一次，重试摊平在所有格子之间。原来的写法是 worker 原地连着重试，一个撞限流的格子
// 会把整段时间占在自己身上。
//
// inFlight 是队列容易漏掉的那一半：队列一时空了不代表没活干——另一个 worker 手上那个任务
// 可能正要退回队尾。此时收工会把这个任务连同它的重试一起丢掉，所以取任务时要等它落定。
type meowQueue struct {
	mu       sync.Mutex
	cond     *sync.Cond
	tasks    []*meowTask
	inFlight int
}

func newMeowQueue(tasks []*meowTask) *meowQueue {
	q := &meowQueue{tasks: tasks}
	q.cond = sync.NewCond(&q.mu)
	return q
}

// pop 取下一个任务。没有可取的任务、但别的 worker 手上还有可能退回队尾的任务时会等待；
// 两者都没有时返回 nil，调用方收工。
func (q *meowQueue) pop() *meowTask {
	q.mu.Lock()
	defer q.mu.Unlock()
	for len(q.tasks) == 0 && q.inFlight > 0 {
		q.cond.Wait()
	}
	if len(q.tasks) == 0 {
		return nil
	}
	task := q.tasks[0]
	q.tasks = q.tasks[1:]
	q.inFlight++
	return task
}

// requeue 把手上的任务退回队尾。追加和交还额度必须在同一次加锁里完成：先让额度归零再追加
// 的话，另一个 worker 可能正好在这个缝里判定「没活干了」而收工。
func (q *meowQueue) requeue(task *meowTask) {
	q.mu.Lock()
	q.tasks = append(q.tasks, task)
	q.inFlight--
	q.mu.Unlock()
	q.cond.Broadcast()
}

// release 表示手上的任务不再有后续（拿到样本、落定失败或时限到了）。
func (q *meowQueue) release() {
	q.mu.Lock()
	q.inFlight--
	q.mu.Unlock()
	q.cond.Broadcast()
}

// pending 是到采样结束仍没轮到的任务数。
func (q *meowQueue) pending() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.tasks)
}

// meowGate 是 Meow 采样期间共享的限流闸门，对应参考实现的 rate_limit.RateLimitGate。
//
// 原来的写法是每个 worker 各自按指数退避重试：中转限流回的是 Retry-After: 18，而退避上限
// 只有几秒，于是两路 worker 每次都正好撞回同一个窗口，还把「答案不可用」的共享重试预算吃光
// ——预算一见底，后面每个 429 就一次都不再重试、直接记失败（2026-10-04 实测丢 137 格）。
//
// 只把 Retry-After 当成「整座闸门一起冻 18 秒」也不对：2026-10-07 实测这样仍丢 68 格，
// 21 个限流窗口就把 540 秒的采样时限睡光了。现在的做法是把限流换算成请求间隔——这一个窗口
// 里我们成功发了 since 份，中转能接受的平均间隔就约等于 retryAfter/since，照它拉开发请求
// 节奏，而不是停下来什么都不做。
type meowGate struct {
	mu           sync.Mutex
	pace         time.Duration
	nextAllowed  time.Time
	streak       int
	sinceLimited int
	rateLimited  int
}

// hold 阻塞到闸门允许发下一次请求。ctx 取消时返回 false，调用方把这一格记为失败。
func (g *meowGate) hold(ctx context.Context) bool {
	g.mu.Lock()
	now := time.Now()
	wait := g.nextAllowed.Sub(now)
	if wait < 0 {
		wait = 0
	}
	g.nextAllowed = now.Add(wait + g.pace)
	g.mu.Unlock()
	if wait == 0 {
		return ctx.Err() == nil
	}
	return sleepContext(ctx, wait)
}

// clean 记一份成功响应；攒够 meowPaceRestore 份就把间隔放回一半，逐步试探中转的余量。
func (g *meowGate) clean() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.streak++
	g.sinceLimited++
	if g.streak < meowPaceRestore {
		return
	}
	g.streak = 0
	g.pace = max(g.pace/2, meowPaceBase)
}

// limited 记一次限流：按「窗口长度 ÷ 这个窗口里成功发出的份数」估出可接受的间隔，与当前
// 间隔翻倍取较大值，两头夹在 meowPaceBase 与 meowPaceCeiling 之间；再按这个间隔错开下一次
// 请求。等待是全局的，等于临时把并发降到零，连续干净若干份之后自动放回。
func (g *meowGate) limited(retryAfter time.Duration) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.rateLimited++
	g.streak = 0
	since := max(g.sinceLimited, 1)
	g.sinceLimited = 0
	gap := retryAfter / time.Duration(since)
	g.pace = min(max(gap, g.pace*2, meowPaceBase), meowPaceCeiling)
	g.push(g.pace)
}

// pause 让整座闸门一起等一段时间，但不改间隔、也不计入限流次数：5xx 与网络抖动不代表中转
// 挤，只需要错开一下。等待是共享的，比每个 worker 各睡各的省时间。
func (g *meowGate) pause(wait time.Duration) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.push(wait)
}

// push 把允许发送的时间推到 now+wait 之后（已经排到更远的等待不收回）。调用方须持锁。
func (g *meowGate) push(wait time.Duration) {
	if until := time.Now().Add(wait); until.After(g.nextAllowed) {
		g.nextAllowed = until
	}
}

func (g *meowGate) count() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.rateLimited
}

// meowMaxBackoff 是重试退避的上限。参考实现是 min(2**attempt, 8) 秒；在线检测有总时限，
// 这里压到 2 秒，只影响等待时长，不影响计数。是变量只为了让测试不必真等。
var meowMaxBackoff = 2 * time.Second

// collectMeow 按冻结计划并发采样，返回每道题归一化后的类目计数。
//
// 与参考实现一致的部分：
//   - 请求体是 protocol.build_payload 的 chat 模式：system + user 两条消息、
//     max_tokens=128、reasoning_effort=题目的 effort；不带 temperature（用上游缺省值，
//     基准也是这样采的）。
//   - finish_reason 不是正常结束（被截断、被过滤）算请求失败，不算样本。
//   - 归一化失败（空答案、超长）先重试；重试预算用完仍失败，则计为 __INVALID_OUTPUT__。
//   - 这类「答案不可用」的重试预算是全局共享的 (计划份数+1)/2，对应 detection_options 的
//     retry_budget；限流与网络抖动的重试不占这份额度，走 meowGate，见上面的说明。
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
	// answerBudget 对应参考实现的 retry_budget：拿回了响应但答案不能用（被截断、归一化成
	// __INVALID_OUTPUT__）时才花它。这类重试直接关系到这一格能不能成为证据，额度要用在
	// 刀刃上；限流和网络抖动不花这份额度（见 meowGate），否则额度全烧在跟中转较劲上，
	// 后面的 429 只能一次不试直接记失败。
	answerBudget := (len(plan.Jobs) + 1) / 2
	answerRetries := 0
	gate := &meowGate{pace: meowPaceBase}
	tasks := make([]*meowTask, 0, len(plan.Jobs))
	for _, job := range plan.Jobs {
		tasks = append(tasks, &meowTask{job: job})
	}
	queue := newMeowQueue(tasks)

	takeAnswer := func() bool {
		mu.Lock()
		defer mu.Unlock()
		if answerRetries >= answerBudget {
			return false
		}
		answerRetries++
		return true
	}
	// finish 落这一格的最终结果并交还队列额度：每个任务只会走到一次。
	finish := func(task *meowTask, category string, failed bool) {
		queue.release()
		mu.Lock()
		defer mu.Unlock()
		if failed {
			obs.Failed++
			return
		}
		if obs.Counts[task.job.Cell.ID] == nil {
			obs.Counts[task.job.Cell.ID] = map[string]int{}
		}
		obs.Counts[task.job.Cell.ID][category]++
	}
	// missed 落「采样时限到了才停手」的那几格。它计入 Failed，但单独一笔：这类失败不是中转
	// 拒绝了我们，是时间不够，处理方式相反（加时限 vs 换中转）。
	missed := func(task *meowTask) {
		queue.release()
		mu.Lock()
		defer mu.Unlock()
		obs.Failed++
		obs.TimedOut++
	}

	// send 发一次请求。上游拒收推理强度参数时去掉该字段就地再发一次：这是参数兼容性问题，
	// 不占任何重试额度，而且全局只会发生一次。
	send := func(job modelfp.MeowJob) (*chatResponse, error) {
		for {
			if !gate.hold(ctx) {
				return nil, ctx.Err()
			}
			mu.Lock()
			withEffort := sendEffort
			mu.Unlock()
			resp, err := target.meowRequest(ctx, mode, job.Cell, withEffort)
			if err == nil || !withEffort || !meowEffortRejected(mode, resp) {
				return resp, err
			}
			mu.Lock()
			sendEffort = false
			obs.ReasoningEffortDropped = true
			mu.Unlock()
		}
	}

	run := func(task *meowTask) {
		resp, err := send(task.job)
		if err != nil {
			// 闸门不再放行只可能是采样时限到了：这一格不是被中转拒绝，是没轮到/没跑完。
			if resp == nil && ctx.Err() != nil {
				missed(task)
				return
			}
			if resp != nil && resp.HTTPStatus == http.StatusTooManyRequests {
				// 限流：按中转给的窗口拉开整座闸门的请求间隔，这一格退回队尾，让别的题先跑。
				gate.limited(withRetryAfter(resp, 0))
			} else {
				gate.pause(exponentialBackoff(task.transient+1, time.Second, meowMaxBackoff))
			}
			// 鉴权失败重试也没用。
			credentialRejected := resp != nil &&
				(resp.HTTPStatus == http.StatusUnauthorized || resp.HTTPStatus == http.StatusForbidden)
			if !credentialRejected && task.transient < meowTransientAttempts {
				task.transient++
				queue.requeue(task)
				return
			}
			finish(task, "", true)
			return
		}
		gate.clean()
		mu.Lock()
		record(resp)
		mu.Unlock()
		category := ""
		if normalFinish(resp.FinishReason) {
			category = task.job.Cell.Normalize(resp.Content)
			if category != modelfp.MeowInvalidOutput {
				finish(task, category, false)
				return
			}
		}
		if ctx.Err() == nil && takeAnswer() {
			queue.requeue(task)
			return
		}
		// 答案预算用完：归一化不出类目是「这一格答不出来」，本身是证据；被截断不是，记失败。
		finish(task, category, category != modelfp.MeowInvalidOutput)
	}

	workers := concurrency
	if workers <= 0 {
		workers = 2
	}
	var wg sync.WaitGroup
	for range min(workers, len(plan.Jobs)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for task := queue.pop(); task != nil; task = queue.pop() {
				run(task)
			}
		}()
	}
	wg.Wait()
	// 时限内没轮到的请求也算失败，否则「N 次请求失败」会少报；单独记一笔是因为这两种失败
	// 的处理方式相反——没轮到是时限不够，重试过了还不行是中转真的坏。
	obs.TimedOut = queue.pending()
	obs.Failed += obs.TimedOut
	obs.RateLimited = gate.count()
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
