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
	dilutionTotalTimeout = 240 * time.Second
	dilutionLogicSamples = 3
	dilutionRatioTries   = 2
	// dilutionProbeRepeats 是每道探针题的重复次数。分布判定的分辨力直接取决于它：
	// 太少则任何两个型号都区分不开，只会一路报证据不足。6 道题 × 6 次 = 36 次请求，
	// 是在线检测能接受的上限附近，所以总超时同步抬到 240 秒。
	dilutionProbeRepeats = 6
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
	dilutionNumberPattern = regexp.MustCompile(`\b(\d{1,3})\b`)
	dilutionYearPattern   = regexp.MustCompile(`\b(19|20)\d{2}\b`)
)

const DilutionDisclaimer = modelfp.Disclaimer

// RunDilutionTest 对一个中转上声称的型号做掺水检测。
func RunDilutionTest(ctx context.Context, baseURL, protocol, key, model string) (modelfp.Report, error) {
	base, err := ValidateBaseURL(baseURL)
	if err != nil {
		return modelfp.Report{}, fmt.Errorf("目标地址未通过安全校验")
	}
	if !supportedProtocol(protocol) {
		return modelfp.Report{}, fmt.Errorf("暂不支持该协议的自动检测")
	}
	target := auditTarget{client: service.GetSSRFProtectedHTTPClient(), base: base, key: strings.TrimSpace(key), model: strings.TrimSpace(model)}
	return runDilutionTest(ctx, target)
}

func runDilutionTest(ctx context.Context, target auditTarget) (modelfp.Report, error) {
	ctx, cancel := withProbeTimeout(ctx, dilutionTotalTimeout)
	defer cancel()
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
	if resp, err := target.chat(ctx, map[string]any{"messages": userMessages("", dilutionCutoffPrompt), "max_tokens": 16, "temperature": 0}); err == nil {
		record(resp)
		if year := dilutionYearPattern.FindString(resp.Content); year != "" {
			obs.ClaimedCutoffYear, _ = strconv.Atoi(year)
		}
	}
	for _, probe := range dilutionFutureProbes {
		resp, err := target.chat(ctx, map[string]any{"messages": userMessages("", probe), "max_tokens": 8, "temperature": 0})
		if err != nil {
			continue
		}
		record(resp)
		obs.FutureEventProbes++
		if strings.Contains(strings.ToUpper(resp.Content), "YES") {
			obs.KnewFutureEvents++
		}
	}

	// 型号指纹：每道探针题重复采样，统计答案分布。
	//
	// 这里必须用 temperature=1，不能用 0。我们要测的是型号自身的答案偏好分布，
	// 温度压到 0 只会拿到一个固定答案，分布信息全部丢失。
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
			if answer := modelfp.NormalizeAnswer(resp.Content); answer != "" {
				counts[answer]++
			}
		}
		if len(counts) > 0 {
			obs.ProbeAnswers[probe.ID] = counts
		}
	}

	// 逻辑稳定性。
	for range dilutionLogicSamples {
		resp, err := target.chat(ctx, map[string]any{"messages": userMessages("", dilutionLogicPrompt), "max_tokens": 16, "temperature": 0})
		if err != nil {
			continue
		}
		record(resp)
		if match := dilutionNumberPattern.FindStringSubmatch(resp.Content); match != nil {
			obs.LogicAnswers = append(obs.LogicAnswers, match[1])
		} else {
			obs.LogicAnswers = append(obs.LogicAnswers, strings.TrimSpace(resp.Content))
		}
	}
	obs.LogicCorrect = dilutionLogicAnswer

	// 结构约束。
	for range dilutionRatioTries {
		resp, err := target.chat(ctx, map[string]any{"messages": userMessages("", dilutionStructurePrompt), "max_tokens": 160, "temperature": 0})
		if err != nil {
			continue
		}
		record(resp)
		obs.StructureTries++
		if structureCompliant(resp.Content) {
			obs.StructureOK++
		}
	}

	// 指令层级。
	for range dilutionRatioTries {
		resp, err := target.chat(ctx, map[string]any{"messages": userMessages(dilutionHierarchySystem, dilutionHierarchyPrompt), "max_tokens": 160, "temperature": 0})
		if err != nil {
			continue
		}
		record(resp)
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

	if requests == 0 {
		return modelfp.Report{}, fmt.Errorf("所有检测请求均未成功")
	}
	return modelfp.Evaluate(target.model, obs), nil
}

// structureCompliant 严格按题面校验：字段集合、类型、枚举、数组长度全部对上才算达标。
func structureCompliant(content string) bool {
	payload := stripCodeFence(content)
	var parsed map[string]any
	if err := common.Unmarshal([]byte(payload), &parsed); err != nil {
		return false
	}
	if len(parsed) != 4 {
		return false
	}
	city, cityOK := parsed["city"].(string)
	tier, tierOK := parsed["tier"].(string)
	population, populationOK := parsed["population"].(float64)
	tags, tagsOK := parsed["tags"].([]any)
	if !cityOK || !tierOK || !populationOK || !tagsOK {
		return false
	}
	if !strings.EqualFold(city, "Lyon") || tier != "b" || population <= 0 || population != float64(int64(population)) || len(tags) != 2 {
		return false
	}
	for _, tag := range tags {
		if _, ok := tag.(string); !ok {
			return false
		}
	}
	return true
}

// hierarchyCompliant 只检查最高优先级那条规则：必须以 <<END>> 收尾。较低优先级的
// 规则在冲突时应当让位，所以不要求满足。
func hierarchyCompliant(content string) bool {
	return strings.HasSuffix(strings.TrimSpace(content), "<<END>>")
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
