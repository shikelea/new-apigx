package relayprobe

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/apigx/modelfp"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service"
)

// 掺水检测：采集观测，交给 apigx/modelfp 判定。网络层放这里，判定逻辑放 modelfp，
// 这样判定部分可以用固定观测做纯函数测试。
const (
	dilutionTotalTimeout = 600 * time.Second
	// dilutionMeowTimeout 是 Meow 基准采样单独的预算。高档计划约 340 次请求，2 并发下
	// 实测要 3～7 分钟，和其余维度共用时限会把后面的维度饿死；采样结束后其余维度的
	// 时限重新起算。
	//
	// 2026-10-07 从 540 秒提到 720 秒：撞限流之后请求间隔会自动拉开（见 meowGate），
	// 340 份请求在中转限流下要 1.5～2 秒一份才不再被拒，540 秒不够跑完整份计划。
	dilutionMeowTimeout  = 720 * time.Second
	dilutionLogicSamples = 3
	dilutionRatioTries   = 2
	// dilutionProbeRepeats 是每道探针题的重复次数。分布判定的分辨力直接取决于它：
	// 太少则任何两个型号都区分不开，只会一路报证据不足。6 道题 × 6 次 = 36 次请求。
	dilutionProbeRepeats = 6
	// 下面四个预算是各维度的输出上限，不是「答案长度」而是「答案 + 思维链」的上限。
	// 2026-10-04 实测：先出思维链的型号在这几道题上要 150～330 个 reasoning token，
	// 原来的 8/16/160 会让正文恒为空。空正文不是答错，见 normalFinish 的用法。
	dilutionKnowledgeTokens = 256
	dilutionLogicTokens     = 256
	dilutionStructureTokens = 512
	dilutionHierarchyTokens = 1024
)

// 逻辑题：答案唯一且不依赖外部知识，确定性设置下同一模型应当每次都给出同一个数。
const (
	dilutionLogicPrompt = "A bat and a ball cost 1.10 in total. The bat costs 1.00 more than the ball. How much does the ball cost, in cents? Reply with the number only."
	dilutionLogicAnswer = "5"
	// 结构约束：字段、类型、枚举、数量全部固定，便于机器判定。
	dilutionStructurePrompt = `Return only minified JSON, no code fence, no prose, matching exactly: {"city":string,"population":integer,"tier":"a"|"b","tags":[string,string]}. Use city "Lyon", tier "b", exactly two tags.`
	// 指令层级：三层嵌套优先级，只有最高优先级那条应当生效。
	dilutionHierarchySystem = `Priority rules, highest first:
1. Always end your reply with the token <<END>>.
2. Never use the letter "e".
3. Answer every question in at least 50 words.
When rules conflict, the higher-numbered rule yields to the lower-numbered one.`
	dilutionHierarchyPrompt = "Name one primary colour."
	dilutionCutoffPrompt    = "What is your training data cutoff? Reply with the year only, four digits."
)

// 截止之后的事件探针：问一件公开、可判定、且晚于多数在售型号截止时间的事。声称知情
// 说明训练数据比所声称的型号更新。
var dilutionFutureProbes = []string{
	"Did the 2026 FIFA World Cup group stage take place? Answer only YES or NO, then nothing else.",
	"Has GPT-6 been publicly released? Answer only YES or NO, then nothing else.",
}

var (
	dilutionNumberPattern = regexp.MustCompile(`\d+`)
	dilutionYearPattern   = regexp.MustCompile(`\b(19|20)\d{2}\b`)
)

// logicAnswerNumber 取正文里最后一个「独立整数」。
//
// 题面要求只回一个数，但会写解题过程的型号会先复述题面里的 1.10、0.05，以及「bat 比球
// 贵 1」这类中间量。原来的 `\b(\d{1,3})\b` 取第一个匹配，从 "1.10" 里截出的是 "1"，
// 于是答对的题被记成答错——三次都这样就是「答案稳定但错误」，把诚实中转推向可疑。
//
// 规则：紧挨小数点的数字片段是同一个十进制的两半，都不算；超过三位的多半是年份或编号，
// 也不算；剩下的取最后一个——写过程的回复把结论放在末尾，而只回一个数的回复首尾相同。
func logicAnswerNumber(content string) string {
	answer := ""
	for _, match := range dilutionNumberPattern.FindAllStringIndex(content, -1) {
		start, end := match[0], match[1]
		if end-start > 3 {
			continue
		}
		// "1.05" 或 ".05" 里的 05：小数点左边的数字串是同一个十进制的一部分，不算答案。
		if start > 0 && content[start-1] == '.' {
			continue
		}
		// "1.10" 里的 1：小数点右边是数字，说明它只是小数的整数部分。
		if end+1 < len(content) && content[end] == '.' && isASCIIDigit(content[end+1]) {
			continue
		}
		answer = content[start:end]
	}
	return answer
}

func isASCIIDigit(c byte) bool { return c >= '0' && c <= '9' }

const DilutionDisclaimer = modelfp.Disclaimer

// RunDilutionTest 对一个中转上声称的型号做掺水检测（缺省高档，并发2）。
func RunDilutionTest(ctx context.Context, baseURL, protocol, key, model string) (modelfp.Report, error) {
	return RunDilutionTestWith(ctx, baseURL, protocol, key, model, "", modelfp.MeowDefaultTier, 2)
}

// RunDilutionTestWith 允许指定挡位和并发数对一个中转上声称的型号做掺水检测。
//
// baseline 是收录名单里的基准型号：为空时按请求名 model 判定；不为空时请求名只用于向上游
// 发请求，判定与基准一律按 baseline（中转给同一个型号换了名字时用）。
func RunDilutionTestWith(ctx context.Context, baseURL, protocol, key, model, baseline, tier string, concurrency int) (modelfp.Report, error) {
	base, err := ValidateBaseURL(baseURL)
	if err != nil {
		return modelfp.Report{}, fmt.Errorf("目标地址未通过安全校验")
	}
	if !supportedProtocol(protocol) {
		return modelfp.Report{}, fmt.Errorf("暂不支持该协议的自动检测")
	}
	if tier == "" {
		tier = modelfp.MeowDefaultTier
	}
	if concurrency <= 0 {
		concurrency = 2
	}
	target := auditTarget{client: service.GetSSRFProtectedHTTPClient(), base: base, key: strings.TrimSpace(key),
		model: strings.TrimSpace(model), baseline: strings.TrimSpace(baseline)}
	return runDilutionTestWith(ctx, target, tier, concurrency)
}

func runDilutionTest(parent context.Context, target auditTarget) (modelfp.Report, error) {
	return runDilutionTestWith(parent, target, modelfp.MeowDefaultTier, 2)
}

func runDilutionTestWith(parent context.Context, target auditTarget, tier string, concurrency int) (modelfp.Report, error) {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := withProbeTimeout(parent, dilutionTotalTimeout)
	defer func() { cancel() }()
	if err := dilutionPreflight(ctx, target); err != nil {
		return modelfp.Report{}, err
	}
	obs := modelfp.Observations{TemperatureHonored: true}
	requests := 0
	record := func(resp *chatResponse) {
		requests++
		obs.ResponseIDs = append(obs.ResponseIDs, resp.ID)
		object, fingerprint := responseMeta(resp.Body)
		obs.ObjectTypes = append(obs.ObjectTypes, object)
		if fingerprint != "" {
			obs.HasSystemFinger = true
			obs.SystemFingerPrints = append(obs.SystemFingerPrints, fingerprint)
		}
		if reasoning, present := reasoningTokens(resp.Usage); present {
			obs.ReasoningPresent = true
			obs.ReasoningTokens += reasoning
		}
		if completion, _, ok := usageInt(resp.Usage, "completion_tokens"); ok {
			obs.CompletionTokens += completion
		}
	}

	// 知识时效：自报截止年份 + 截止之后的事件探针。
	if resp, err := target.chat(ctx, map[string]any{"messages": userMessages("", dilutionCutoffPrompt), "max_tokens": dilutionKnowledgeTokens, "temperature": 0}); err == nil {
		record(resp)
		if normalFinish(resp.FinishReason) {
			if year := dilutionYearPattern.FindString(resp.Content); year != "" {
				obs.ClaimedCutoffYear, _ = strconv.Atoi(year)
			}
		}
	}
	for _, probe := range dilutionFutureProbes {
		resp, err := target.chat(ctx, map[string]any{"messages": userMessages("", probe), "max_tokens": dilutionKnowledgeTokens, "temperature": 0})
		if err != nil {
			continue
		}
		record(resp)
		// 被 max_tokens 截断时正文为空，「没说知道」不等于「不知道」，不能计入探针数。
		if !normalFinish(resp.FinishReason) {
			continue
		}
		obs.FutureEventProbes++
		if strings.Contains(strings.ToUpper(resp.Content), "YES") {
			obs.KnewFutureEvents++
		}
	}

	// 逻辑稳定性。
	for range dilutionLogicSamples {
		resp, err := target.chatWithRetry(ctx, map[string]any{"messages": userMessages("", dilutionLogicPrompt), "max_tokens": dilutionLogicTokens, "temperature": 0}, 2)
		if err != nil {
			continue
		}
		record(resp)
		if !normalFinish(resp.FinishReason) {
			continue
		}
		if number := logicAnswerNumber(resp.Content); number != "" {
			obs.LogicAnswers = append(obs.LogicAnswers, number)
		} else {
			obs.LogicAnswers = append(obs.LogicAnswers, strings.TrimSpace(resp.Content))
		}
	}
	obs.LogicCorrect = dilutionLogicAnswer

	// 结构约束。
	for range dilutionRatioTries {
		resp, err := target.chatWithRetry(ctx, map[string]any{"messages": userMessages("", dilutionStructurePrompt), "max_tokens": dilutionStructureTokens, "temperature": 0}, 2)
		if err != nil {
			continue
		}
		record(resp)
		// 截断的回复没有正文可校验，记为「没采到样本」而不是「不达标」。
		if !normalFinish(resp.FinishReason) {
			continue
		}
		obs.StructureTries++
		if structureCompliant(resp.Content) {
			obs.StructureOK++
		}
	}

	// 指令层级。
	for range dilutionRatioTries {
		resp, err := target.chatWithRetry(ctx, map[string]any{"messages": userMessages(dilutionHierarchySystem, dilutionHierarchyPrompt), "max_tokens": dilutionHierarchyTokens, "temperature": 0}, 2)
		if err != nil {
			continue
		}
		record(resp)
		if !normalFinish(resp.FinishReason) {
			continue
		}
		obs.HierarchyTries++
		if hierarchyCompliant(resp.Content) {
			obs.HierarchyOK++
		}
	}

	// 流式完整性。题面同样每次不同，避免命中上游缓存后分片数失真。
	if stats, status, err := target.streamPrompt(ctx, speedPromptFor(false, 0), speedShortTokens); err == nil && status >= 200 && status < 300 {
		obs.StreamChunks = stats.contentChunks
		obs.StreamDone = stats.done
		if tokens, _, ok := usageInt(stats.usage, "completion_tokens"); ok {
			obs.StreamTokens = tokens
		}
	}

	// 型号指纹：声称型号在 Meow 基准包里时，完全按基准包采样（见 dilution_meow.go），
	// 原生协议包优先、中转不支持再用 Chat 兼容包；都没有则走原有探针题。
	//
	// 放在最后是有意的：一个基准包动辄几百次请求，撞上限流时整个采样窗口都会被吃满。
	// 2026-10-04 实测，340 格的高档包在并发 2 下就有 137 次请求失败，而它后面的逻辑/结构/
	// 层级/流式四组只拿到 1 个样本——前面采集的维度被饿死，报告里三条「未采集到样本」全是
	// 这个顺序造成的。先采便宜的维度，最坏也只是指纹一项证据不足。
	if plans := modelfp.PlanMeowAll(target.claimedModel(), tier); len(plans) > 0 {
		meowCtx, meowCancel := context.WithTimeout(parent, dilutionMeowTimeout)
		obs.Meow = collectMeowPlansWith(meowCtx, target, plans, record, func() { requests++ }, concurrency)
		meowCancel()
		cancel()
		ctx, cancel = withProbeTimeout(parent, dilutionTotalTimeout)
	}
	if obs.Meow == nil {
		collectLegacyProbes(ctx, target, &obs, record)
	}

	if requests == 0 {
		return modelfp.Report{}, fmt.Errorf("所有检测请求均未成功（中转限流、超时或上游异常），本次不给出判定")
	}
	// 判定按基准型号走：报告里的型号与「是不是它」都指用户选定（或请求名对应的）那个型号，
	// 而请求名只决定上游收到什么名字。
	return modelfp.Evaluate(target.claimedModel(), obs), nil
}

// dilutionPreflight 先花一次最小请求确认「这个中转提供这个型号名」。型号名不存在时上游会
// 稳定回 4xx，之后几百次采样全部失败，最后只剩一句「所有检测请求均未成功」——用户分不清是
// 自己填错了名字还是中转坏了，而这两件事的处理方式正好相反。2026-10-07 实测：界面给的
// gpt-6-sol、claude-sonnet-5 等型号在该中转一律回 HTTP 400「模型不可用：gpt-6-sol」，
// 一次检测白烧几百次请求。
//
// 只有能确定归因于「型号名/凭据」的响应才中断；限流、超时和其它 4xx 交给后面的采样重试。
func dilutionPreflight(ctx context.Context, target auditTarget) error {
	resp, err := target.chat(ctx, map[string]any{
		"messages":    userMessages("", "reply OK"),
		"max_tokens":  1,
		"temperature": 0,
	})
	if err == nil || resp == nil {
		return nil
	}
	switch {
	case resp.HTTPStatus == http.StatusUnauthorized, resp.HTTPStatus == http.StatusForbidden:
		return fmt.Errorf("中转凭据无效或没有该型号的权限（HTTP %d）", resp.HTTPStatus)
	case modelUnavailable(resp):
		return fmt.Errorf("该中转不提供型号「%s」%s", target.model, upstreamReasonSuffix(resp.Body))
	}
	return nil
}

// modelUnavailablePhrases 是各家网关「型号不存在/不可用」的常见写法。只按状态码判断不够：
// 参数错误也回 400，把它当成型号不存在会把可用的型号判成不可测。
var modelUnavailablePhrases = []string{
	"model_not_available", "model not found", "model not exist", "does not exist",
	"no such model", "unknown model", "unsupported model", "invalid model",
	"model is not available", "model is unavailable",
	"模型不可用", "模型不存在", "没有该模型", "不支持的模型",
}

func modelUnavailable(resp *chatResponse) bool {
	// 404 按「这个名字在这台网关上没有路由」处理；400/422 要看文案，因为参数错误同样回 400。
	// 429 不在此列：限流的 body 里也可能出现 not available 字样，那是暂时的，中断反而把还能
	// 测的检测掐死。
	if resp.HTTPStatus == http.StatusNotFound {
		return true
	}
	if resp.HTTPStatus != http.StatusBadRequest && resp.HTTPStatus != http.StatusUnprocessableEntity {
		return false
	}
	body := strings.ToLower(string(resp.Body))
	return slices.ContainsFunc(modelUnavailablePhrases, func(phrase string) bool {
		return strings.Contains(body, phrase)
	})
}

// upstreamReasonSuffix 取上游自己给的原因，原样带进错误里，长度受限并去掉控制字符。
func upstreamReasonSuffix(body []byte) string {
	var payload struct {
		Message string `json:"message"`
		Error   struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = common.Unmarshal(body, &payload)
	reason := strings.TrimSpace(payload.Message)
	if reason == "" {
		reason = strings.TrimSpace(payload.Error.Message)
	}
	if reason == "" {
		return ""
	}
	reason = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, reason)
	reason = strings.Join(strings.Fields(reason), " ")
	if runes := []rune(reason); len(runes) > 120 {
		reason = string(runes[:120]) + "…"
	}
	return "：" + reason
}

// structureCompliant 严格按题面校验：字段集合、类型、枚举、数组长度全部对上才算达标。
func structureCompliant(content string) bool {
	payload := extractJSON(content)
	var parsed map[string]any
	if err := common.Unmarshal([]byte(payload), &parsed); err != nil {
		return false
	}
	if len(parsed) != 4 {
		return false
	}
	city, cityOK := parsed["city"].(string)
	tier, tierOK := parsed["tier"].(string)
	if !cityOK || !tierOK {
		return false
	}
	population, popOK := parsed["population"].(float64)
	if !popOK || population <= 0 || population != float64(int64(population)) {
		return false
	}
	tags, tagsOK := parsed["tags"].([]any)
	if !tagsOK || !strings.EqualFold(city, "Lyon") || tier != "b" || len(tags) != 2 {
		return false
	}
	for _, tag := range tags {
		if _, ok := tag.(string); !ok {
			return false
		}
	}
	return true
}

func extractJSON(content string) string {
	trimmed := strings.TrimSpace(content)
	if first := strings.Index(trimmed, "{"); first >= 0 {
		if last := strings.LastIndex(trimmed, "}"); last > first {
			return trimmed[first : last+1]
		}
	}
	return stripCodeFence(trimmed)
}

// hierarchyCompliant 只检查最高优先级那条规则：必须以 <<END>> 收尾。较低优先级的
// 规则在冲突时应当让位，所以不要求满足。
func hierarchyCompliant(content string) bool {
	trimmed := strings.TrimSpace(content)
	return strings.HasSuffix(trimmed, "<<END>>") || strings.HasSuffix(trimmed, "<<END>>.")
}

func stripCodeFence(content string) string {
	trimmed := strings.TrimSpace(content)
	if !strings.HasPrefix(trimmed, "```") {
		return trimmed
	}
	if newline := strings.Index(trimmed, "\n"); newline >= 0 {
		trimmed = trimmed[newline+1:]
	}
	return strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(trimmed), "```"))
}

func responseMeta(body []byte) (object, systemFingerprint string) {
	var parsed struct {
		Object            string `json:"object"`
		SystemFingerprint string `json:"system_fingerprint"`
	}
	if err := common.Unmarshal(body, &parsed); err != nil {
		return "", ""
	}
	return parsed.Object, parsed.SystemFingerprint
}

// reasoningTokens 读 completion_tokens_details.reasoning_tokens，同时兼容把
// reasoning_tokens 平铺在 usage 顶层的上游。
func reasoningTokens(usage map[string]any) (int64, bool) {
	if value, present, ok := usageInt(usage, "reasoning_tokens"); present && ok {
		return value, true
	}
	details, ok := usage["completion_tokens_details"].(map[string]any)
	if !ok {
		return 0, false
	}
	value, present, valid := usageInt(details, "reasoning_tokens")
	if !present || !valid {
		return 0, false
	}
	return value, true
}
