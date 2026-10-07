package relayprobe

import (
	"context"
	"fmt"
	"regexp"
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
	dilutionMeowTimeout  = 540 * time.Second
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
	return RunDilutionTestWith(ctx, baseURL, protocol, key, model, modelfp.MeowDefaultTier, 2)
}

// RunDilutionTestWith 允许指定挡位和并发数对一个中转上声称的型号做掺水检测。
func RunDilutionTestWith(ctx context.Context, baseURL, protocol, key, model, tier string, concurrency int) (modelfp.Report, error) {
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
	target := auditTarget{client: service.GetSSRFProtectedHTTPClient(), base: base, key: strings.TrimSpace(key), model: strings.TrimSpace(model)}
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
	if plans := modelfp.PlanMeowAll(target.model, tier); len(plans) > 0 {
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
		return modelfp.Report{}, fmt.Errorf("所有检测请求均未成功")
	}
	return modelfp.Evaluate(target.model, obs), nil
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
