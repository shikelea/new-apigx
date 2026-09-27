package relayprobe

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"

	apigxmodel "github.com/QuantumNous/new-api/apigx/model"
)

type modelFamily struct {
	name string
	// hints identify the family from a model id; short hints only match as a prefix.
	hints []string
	claim *regexp.Regexp
}

var modelFamilies = []modelFamily{
	{"Anthropic Claude", []string{"claude"}, regexp.MustCompile(`(?i)claude|anthropic`)},
	{"OpenAI GPT", []string{"gpt", "chatgpt", "o1", "o3", "o4"}, regexp.MustCompile(`(?i)\bopenai\b|chatgpt|\bgpt-?\d`)},
	{"Google Gemini", []string{"gemini", "gemma"}, regexp.MustCompile(`(?i)gemini|gemma|\bgoogle\b|deepmind`)},
	{"DeepSeek", []string{"deepseek"}, regexp.MustCompile(`(?i)deepseek|深度求索`)},
	{"通义千问", []string{"qwen", "qwq"}, regexp.MustCompile(`(?i)qwen|通义|千问|alibaba|阿里`)},
	{"智谱 GLM", []string{"glm", "chatglm"}, regexp.MustCompile(`(?i)\bglm|chatglm|智谱|zhipu`)},
	{"Moonshot Kimi", []string{"kimi", "moonshot"}, regexp.MustCompile(`(?i)\bkimi\b|moonshot|月之暗面`)},
	{"字节豆包", []string{"doubao"}, regexp.MustCompile(`(?i)doubao|豆包|bytedance|字节跳动`)},
	{"Meta Llama", []string{"llama"}, regexp.MustCompile(`(?i)llama|\bmeta\b`)},
	{"Mistral", []string{"mistral", "mixtral"}, regexp.MustCompile(`(?i)mistral|mixtral`)},
	{"xAI Grok", []string{"grok"}, regexp.MustCompile(`(?i)\bgrok\b|\bxai\b`)},
	{"百度文心", []string{"ernie"}, regexp.MustCompile(`(?i)ernie|文心|baidu|百度`)},
	{"MiniMax", []string{"minimax", "abab"}, regexp.MustCompile(`(?i)minimax`)},
}

// negatedClaim strips "I am not GPT"-style fragments so a denial is not
// counted as a claim.
var negatedClaim = regexp.MustCompile(`(?i)(\bnot\b|n't|\bnever\b|不是|并非|而非|不属于)[^.,;!?。，；！？\n]{0,40}`)

const identityPrompt = "Who are you exactly? What is your model name and version? Which company developed you? Answer concisely in one sentence."

func expectedFamily(model string) string {
	id := strings.ToLower(strings.TrimSpace(model))
	if slash := strings.LastIndex(id, "/"); slash >= 0 {
		id = id[slash+1:]
	}
	for _, family := range modelFamilies {
		for _, hint := range family.hints {
			if strings.HasPrefix(id, hint) || (len(hint) >= 4 && strings.Contains(id, hint)) {
				return family.name
			}
		}
	}
	return ""
}

func claimedFamilies(reply string) []string {
	cleaned := negatedClaim.ReplaceAllString(reply, " ")
	claimed := make([]string, 0, 2)
	for _, family := range modelFamilies {
		if family.claim.MatchString(cleaned) {
			claimed = append(claimed, family.name)
		}
	}
	sort.Strings(claimed)
	return claimed
}

// routeHints reads routing fingerprints that some relays pass through.
func routeHints(id string, header http.Header) []string {
	hints := make([]string, 0, 2)
	switch {
	case strings.HasPrefix(id, "msg_bdrk_") || header.Get("x-amzn-bedrock-invocation-latency") != "":
		hints = append(hints, "AWS Bedrock")
	case strings.HasPrefix(id, "msg_vrtx_"):
		hints = append(hints, "Google Vertex")
	case strings.HasPrefix(id, "gen-") || header.Get("x-generation-id") != "":
		hints = append(hints, "OpenRouter")
	}
	for name := range header {
		lower := strings.ToLower(name)
		switch {
		case strings.HasPrefix(lower, "cf-aig-"):
			hints = append(hints, "Cloudflare AI Gateway")
		case strings.HasPrefix(lower, "anthropic-ratelimit-"):
			hints = append(hints, "Anthropic 官方限流头")
		}
	}
	return hints
}

func runIdentityCheck(ctx context.Context, target auditTarget) CheckResult {
	resp, err := target.chat(ctx, map[string]any{"messages": userMessages("", identityPrompt), "max_tokens": 80, "temperature": 0})
	if err != nil {
		return CheckResult{Status: apigxmodel.AuditCheckInconclusive, Evidence: "模型请求未成功：" + err.Error(), Confidence: "low", SampleCount: 1}
	}
	result := CheckResult{SampleCount: 1, LatencyMs: resp.LatencyMs, Confidence: "low"}
	expected := expectedFamily(target.model)
	claimed := claimedFamilies(resp.Content)
	notes := []string{"自报：" + truncateRunes(strings.TrimSpace(resp.Content), 80)}
	if resp.Model != "" && !strings.EqualFold(resp.Model, target.model) {
		notes = append(notes, "响应 model 字段为 "+truncateRunes(resp.Model, 60))
	}
	if hints := routeHints(resp.ID, resp.Header); len(hints) > 0 {
		notes = append(notes, "路由迹象："+strings.Join(hints, "、"))
	}
	responseFamily := expectedFamily(resp.Model)
	modelFieldMismatch := expected != "" && responseFamily != "" && responseFamily != expected
	switch {
	case expected == "":
		result.Status = apigxmodel.AuditCheckInconclusive
		notes = append([]string{"无法从模型名推断预期厂商"}, notes...)
	case modelFieldMismatch:
		result.Status, result.Confidence = apigxmodel.AuditCheckWarning, "medium"
		notes = append([]string{fmt.Sprintf("响应 model 字段属于 %s，与请求的 %s 不一致", responseFamily, expected)}, notes...)
	case len(claimed) == 0:
		result.Status = apigxmodel.AuditCheckInconclusive
		notes = append([]string{"回复未给出明确身份"}, notes...)
	case !containsString(claimed, expected):
		result.Status, result.Confidence = apigxmodel.AuditCheckWarning, "medium"
		notes = append([]string{fmt.Sprintf("强信号：自报为 %s，与请求的 %s 不一致", strings.Join(claimed, "/"), expected)}, notes...)
	case len(claimed) > 1:
		result.Status = apigxmodel.AuditCheckWarning
		notes = append([]string{"自报中同时出现多个厂商：" + strings.Join(claimed, "/")}, notes...)
	default:
		result.Status = apigxmodel.AuditCheckPassed
		notes = append([]string{"自报身份与 " + expected + " 一致"}, notes...)
	}
	result.Evidence = strings.Join(notes, "；") + "。自报身份可被系统提示伪造，仅作参考"
	return result
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
