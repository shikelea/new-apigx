package relayprobe

import (
	"context"
	"crypto/rand"
	"encoding/xml"
	"fmt"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"time"

	apigxmodel "github.com/QuantumNous/new-api/apigx/model"
	"github.com/QuantumNous/new-api/service"
)

// 降智/众测题库。
//
// 两道题的取向不同，刻意都保留：
//
//   - 糖果测试（candy）：多步整数推理，答案唯一且由服务端算出。题面参数每次随机，
//     所以答不上来不能用「背过题」解释，只能是推理能力本身下滑。降智持续检测固定用它，
//     因为它可机判、成本低、可比。
//   - 鹈鹕测试（pelican）：让模型画「骑自行车的鹈鹕」SVG，按结构规则打分。它对生成
//     长度与空间规划更敏感，但评分必然更粗，所以只在众测里让用户自选。
//
// 两者都只作为信号：同一模型不同采样也会有波动。
const (
	candyRounds           = 3
	candyMaxTok           = 400
	pelicanMaxTok         = 1600
	benchmarkTotalTimeout = 5 * time.Minute
)

// candyProblem 是一道参数化的糖果题。答案由服务端计算，不依赖模型「记得」标准答案。
type candyProblem struct {
	Prompt string
	Answer int
}

// newCandyProblem 生成一道三步整数题：先按份分掉，再拿走固定数量，最后按倍数补回。
// 参数经过约束，保证每一步都整除、且结果为正，答案唯一。
func newCandyProblem() candyProblem {
	// jars ∈ [3,6]，perJar ∈ [12,24] 且能被 share 整除，share ∈ [2,4]，
	// taken ∈ [5,15]，refill ∈ [2,4]。
	share := randInt(2, 4)
	perJar := share * randInt(4, 8)
	jars := randInt(3, 6)
	taken := randInt(5, 15)
	refill := randInt(2, 4)

	total := jars * perJar
	afterShare := total - total/share
	afterTaken := afterShare - taken
	answer := afterTaken + refill*taken

	prompt := fmt.Sprintf(
		"A shop has %d jars, each holding %d candies. First, exactly one %s of all the candies is given away. "+
			"Then %d candies are eaten from what remains. Finally, the shop restocks %d candies for every candy that was eaten. "+
			"How many candies does the shop have at the end? Think step by step, then end your reply with the final number on its own line.",
		jars, perJar, ordinalShare(share), taken, refill)
	return candyProblem{Prompt: prompt, Answer: answer}
}

func ordinalShare(share int) string {
	switch share {
	case 2:
		return "half"
	case 3:
		return "third"
	default:
		return "quarter"
	}
}

// randInt 返回 [low, high] 内的整数。用 crypto/rand 是为了避免同一进程内多个中转
// 拿到同一串题目——那会让「背题」重新变得可能。
func randInt(low, high int) int {
	if high <= low {
		return low
	}
	n, err := rand.Int(rand.Reader, big.NewInt(int64(high-low+1)))
	if err != nil {
		return low
	}
	return low + int(n.Int64())
}

var trailingNumber = regexp.MustCompile(`-?\d[\d,]*`)

// candyAnswer 取回复里最后一个整数：题面要求把答案单独放在最后一行，取最后一个数
// 比取第一个稳得多（推理过程里全是中间结果）。
func candyAnswer(reply string) (int, bool) {
	matches := trailingNumber.FindAllString(reply, -1)
	if len(matches) == 0 {
		return 0, false
	}
	last := strings.ReplaceAll(matches[len(matches)-1], ",", "")
	value, err := strconv.Atoi(last)
	if err != nil {
		return 0, false
	}
	return value, true
}

// BenchmarkResult 是一次糖果或鹈鹕测试的结果。
type BenchmarkResult struct {
	Benchmark string `json:"benchmark"`
	Model     string `json:"model"`
	// Score 0–100。糖果是答对轮次占比，鹈鹕是结构规则加权得分。
	Score    int    `json:"score"`
	Status   string `json:"status"`
	Evidence string `json:"evidence"`
	// Artifact 仅鹈鹕测试有值：模型产出的 SVG 原文，供前端以图片形式展示。
	Artifact  string `json:"artifact,omitempty"`
	LatencyMs int64  `json:"latency_ms"`
	Rounds    int    `json:"rounds"`
}

// runCandy 连续答 candyRounds 道随机糖果题，按答对比例给分。
func (t auditTarget) runCandy(ctx context.Context) (BenchmarkResult, error) {
	result := BenchmarkResult{Benchmark: apigxmodel.BenchmarkCandy, Model: t.model, Rounds: candyRounds}
	correct, answered, truncated := 0, 0, 0
	details := make([]string, 0, candyRounds)
	var lastErr error
	for range candyRounds {
		problem := newCandyProblem()
		resp, err := t.chat(ctx, map[string]any{"messages": userMessages("", problem.Prompt), "max_tokens": candyMaxTok, "temperature": 0})
		if err != nil {
			lastErr = err
			continue
		}
		result.LatencyMs += resp.LatencyMs
		answered++
		got, parsed := candyAnswer(resp.Content)
		switch {
		case !parsed && resp.FinishReason == "length":
			// 思维链吃掉了整个预算，正文没来。这不是答错，不计入已答题数。
			truncated++
			answered--
			details = append(details, "回复在 max_tokens 处被截断，未给出答案")
		case !parsed:
			details = append(details, "未给出可解析的数字")
		case got == problem.Answer:
			correct++
		default:
			details = append(details, fmt.Sprintf("答 %d，应为 %d", got, problem.Answer))
		}
	}
	if answered == 0 {
		if truncated > 0 {
			// 全部被截断：只能说明这个模型在 400 token 内答不完，不能说它答错了。
			result.Rounds = truncated
			result.Status = apigxmodel.RelayCheckUnknown
			result.Score = 0
			result.Evidence = fmt.Sprintf("%d 道题的回复都在 max_tokens(%d) 处被截断，未给出答案；推理模型的思维链可能已用掉全部预算，无法判定是否降智", truncated, candyMaxTok)
			return result, nil
		}
		if lastErr != nil {
			return result, lastErr
		}
		return result, fmt.Errorf("糖果测试未取得有效回答")
	}
	result.Rounds = answered
	result.Score = correct * 100 / answered
	switch {
	case correct == answered:
		result.Status = apigxmodel.RelayCheckHealthy
		result.Evidence = fmt.Sprintf("%d/%d 道多步整数题全部答对", correct, answered)
	case correct == 0:
		result.Status = apigxmodel.RelayCheckFailed
		result.Evidence = fmt.Sprintf("%d 道题全部答错：%s。题目参数每次随机，答错无法用背题解释", answered, strings.Join(details, "；"))
	default:
		result.Status = apigxmodel.RelayCheckDegraded
		result.Evidence = fmt.Sprintf("%d/%d 道答对：%s", correct, answered, strings.Join(details, "；"))
	}
	return result, nil
}

const pelicanPrompt = "Draw a pelican riding a bicycle as a single standalone SVG. Output only the SVG markup, starting with <svg and ending with </svg>. Include a viewBox, both bicycle wheels as circles, a frame, and a recognisable pelican with a long beak. Do not include any script, event handler, or external reference."

// pelicanRule 是一条结构规则。全部规则都只看 SVG 结构，不做主观审美判断。
type pelicanRule struct {
	name   string
	weight int
	check  func(svgDoc) bool
}

type svgDoc struct {
	raw      string
	wellForm bool
	elements map[string]int
	hasView  bool
	colors   int
}

var pelicanRules = []pelicanRule{
	{"是合法的 SVG 文档", 25, func(d svgDoc) bool { return d.wellForm }},
	{"包含 viewBox", 10, func(d svgDoc) bool { return d.hasView }},
	{"两个车轮（≥2 个 circle/ellipse）", 20, func(d svgDoc) bool {
		return d.elements["circle"]+d.elements["ellipse"] >= 2
	}},
	{"有车架线条（line/path/polyline）", 15, func(d svgDoc) bool {
		return d.elements["line"]+d.elements["path"]+d.elements["polyline"] >= 2
	}},
	{"结构足够复杂（≥10 个绘图元素）", 15, func(d svgDoc) bool { return d.drawables() >= 10 }},
	{"使用了多种颜色（≥3）", 15, func(d svgDoc) bool { return d.colors >= 3 }},
}

func (d svgDoc) drawables() int {
	total := 0
	for _, name := range []string{"circle", "ellipse", "rect", "path", "line", "polyline", "polygon", "g", "text"} {
		total += d.elements[name]
	}
	return total
}

var (
	svgTagPattern   = regexp.MustCompile(`(?i)<\s*([a-z][a-z0-9]*)`)
	svgColorPattern = regexp.MustCompile(`(?i)(#[0-9a-f]{3,8}|rgb\([^)]*\)|\b(?:fill|stroke)\s*[:=]\s*"?([a-z]+))`)
	// 不允许出现的危险构造：脚本、事件处理器、外部引用。
	svgUnsafePattern = regexp.MustCompile(`(?i)<\s*(script|foreignObject|iframe|use\b)|\son[a-z]+\s*=|javascript:|xlink:href\s*=\s*"(?:https?:)?//|href\s*=\s*"(?:https?:)?//`)
)

// parseSVG 抽出结构统计。用 xml.Decoder 判断是否良构，再用正则统计元素与颜色，
// 因为模型经常输出带自闭合小错的 SVG，正则统计比严格解析更鲁棒。
func parseSVG(markup string) svgDoc {
	doc := svgDoc{raw: markup, elements: map[string]int{}}
	decoder := xml.NewDecoder(strings.NewReader(markup))
	decoder.Strict = false
	root := ""
	for {
		token, err := decoder.Token()
		if err != nil {
			break
		}
		if start, isStart := token.(xml.StartElement); isStart && root == "" {
			root = strings.ToLower(start.Name.Local)
		}
	}
	doc.wellForm = root == "svg"
	for _, match := range svgTagPattern.FindAllStringSubmatch(markup, -1) {
		doc.elements[strings.ToLower(match[1])]++
	}
	doc.hasView = strings.Contains(strings.ToLower(markup), "viewbox")
	colors := map[string]bool{}
	for _, match := range svgColorPattern.FindAllStringSubmatch(markup, -1) {
		colors[strings.ToLower(match[0])] = true
	}
	doc.colors = len(colors)
	return doc
}

// extractSVG 从回复里截出 SVG 片段，顺手丢掉代码围栏与前后解说。
func extractSVG(reply string) string {
	lower := strings.ToLower(reply)
	start := strings.Index(lower, "<svg")
	end := strings.LastIndex(lower, "</svg>")
	if start < 0 || end < 0 || end < start {
		return ""
	}
	return strings.TrimSpace(reply[start : end+len("</svg>")])
}

func (t auditTarget) runPelican(ctx context.Context) (BenchmarkResult, error) {
	result := BenchmarkResult{Benchmark: apigxmodel.BenchmarkPelican, Model: t.model, Rounds: 1}
	resp, err := t.chat(ctx, map[string]any{"messages": userMessages("", pelicanPrompt), "max_tokens": pelicanMaxTok, "temperature": 0})
	if err != nil {
		return result, err
	}
	result.LatencyMs = resp.LatencyMs
	markup := extractSVG(resp.Content)
	if markup == "" {
		if resp.FinishReason == "length" {
			// 同上：预算用尽而没画完，判 unknown 而不是判它画不出来。
			result.Status, result.Score = apigxmodel.RelayCheckUnknown, 0
			result.Evidence = fmt.Sprintf("回复在 max_tokens(%d) 处被截断，没有完整的 SVG；推理模型的思维链可能已用掉全部预算", pelicanMaxTok)
			return result, nil
		}
		result.Status, result.Score = apigxmodel.RelayCheckFailed, 0
		result.Evidence = "回复中没有可识别的 SVG"
		return result, nil
	}
	// 不安全构造直接判 0 分且不保留原文：这段 SVG 会在众测页面展示给其他人。
	if svgUnsafePattern.MatchString(markup) {
		result.Status, result.Score = apigxmodel.RelayCheckFailed, 0
		result.Evidence = "SVG 含脚本、事件处理器或外部引用，已丢弃，不予展示"
		return result, nil
	}
	if len(markup) > maxArtifactBytes {
		result.Status, result.Score = apigxmodel.RelayCheckDegraded, 0
		result.Evidence = fmt.Sprintf("SVG 超过 %d KiB 上限，已丢弃", maxArtifactBytes>>10)
		return result, nil
	}
	doc := parseSVG(markup)
	score, passed, failed := 0, make([]string, 0, len(pelicanRules)), make([]string, 0, len(pelicanRules))
	for _, rule := range pelicanRules {
		if rule.check(doc) {
			score += rule.weight
			passed = append(passed, rule.name)
			continue
		}
		failed = append(failed, rule.name)
	}
	result.Score, result.Artifact = score, markup
	switch {
	case score >= 85:
		result.Status = apigxmodel.RelayCheckHealthy
		result.Evidence = fmt.Sprintf("结构规则 %d/100，满足：%s", score, strings.Join(passed, "、"))
	case score >= 50:
		result.Status = apigxmodel.RelayCheckDegraded
		result.Evidence = fmt.Sprintf("结构规则 %d/100，未满足：%s", score, strings.Join(failed, "、"))
	default:
		result.Status = apigxmodel.RelayCheckFailed
		result.Evidence = fmt.Sprintf("结构规则仅 %d/100，未满足：%s", score, strings.Join(failed, "、"))
	}
	return result, nil
}

// maxArtifactBytes 限制入库的 SVG 大小，避免一次众测塞进一个超大 payload。
const maxArtifactBytes = 64 << 10

// BenchmarkDisclaimer 与每一份众测结果一起展示。
const BenchmarkDisclaimer = "同一模型在不同时刻的采样本身就有波动，单次得分只是信号；糖果题看多步推理是否稳定，鹈鹕题按结构规则打分，都不代表模型整体水平。"

// RunBenchmark 跑一次糖果或鹈鹕测试。
func RunBenchmark(ctx context.Context, benchmark, baseURL, protocol, key, model string) (BenchmarkResult, error) {
	if !apigxmodel.ValidBenchmark(benchmark) {
		return BenchmarkResult{}, fmt.Errorf("不支持的测试类型")
	}
	base, err := ValidateBaseURL(baseURL)
	if err != nil {
		return BenchmarkResult{}, fmt.Errorf("目标地址未通过安全校验")
	}
	if !supportedProtocol(protocol) {
		return BenchmarkResult{}, fmt.Errorf("暂不支持该协议的自动检测")
	}
	target := auditTarget{client: service.GetSSRFProtectedHTTPClient(), base: base, key: strings.TrimSpace(key), model: strings.TrimSpace(model)}
	return target.runBenchmark(ctx, benchmark)
}

func (t auditTarget) runBenchmark(ctx context.Context, benchmark string) (BenchmarkResult, error) {
	// 鹈鹕题只发一次请求但要 1600 token，糖果题发三次短请求，两者都可能超过审计用的
	// 150 秒总预算，所以这里单独给一个上限。
	ctx, cancel := withProbeTimeout(ctx, benchmarkTotalTimeout)
	defer cancel()
	if benchmark == apigxmodel.BenchmarkPelican {
		return t.runPelican(ctx)
	}
	return t.runCandy(ctx)
}
