package relayprobe

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"

	apigxmodel "github.com/QuantumNous/new-api/apigx/model"
	"github.com/QuantumNous/new-api/common"
)

type errorProbe struct {
	name, method, path, contentType, body string
	auth                                  string // "key", "fake" or ""
}

var errorProbes = []errorProbe{
	{"畸形 JSON", http.MethodPost, "/v1/chat/completions", "application/json", `{not json`, "key"},
	{"不存在的模型", http.MethodPost, "/v1/chat/completions", "application/json", `{"model":"apigx-audit-nonexistent-999","messages":[{"role":"user","content":"hi"}],"max_tokens":1}`, "key"},
	{"缺少 messages", http.MethodPost, "/v1/chat/completions", "application/json", `{"model":"%MODEL%","max_tokens":1}`, "key"},
	{"未知路由", http.MethodPost, "/v1/apigx-audit-unknown-route", "application/json", `{}`, "key"},
	{"错误 Content-Type", http.MethodPost, "/v1/chat/completions", "text/plain", `hello`, "key"},
	{"伪造凭据", http.MethodPost, "/v1/chat/completions", "application/json", `{"model":"%MODEL%","messages":[{"role":"user","content":"hi"}],"max_tokens":1}`, "fake"},
}

type leakRule struct {
	label   string
	pattern *regexp.Regexp
}

var (
	upstreamHosts = regexp.MustCompile(`(?i)api\.anthropic\.com|api\.openai\.com|generativelanguage\.googleapis\.com|openrouter\.ai|api\.deepseek\.com|api\.groq\.com|api\.together\.xyz|api\.mistral\.ai|dashscope\.aliyuncs\.com|open\.bigmodel\.cn|api\.moonshot\.cn|[a-z0-9-]+\.openai\.azure\.com|bedrock-runtime\.[a-z0-9-]+\.amazonaws\.com`)
	warnLeakRules = []leakRule{
		{"上游地址", upstreamHosts},
		{"调用栈", regexp.MustCompile(`Traceback \(most recent call last\)|goroutine \d+ \[|panic: |\bat async |\.go:\d+|\.py", line \d+`)},
		{"服务器路径", regexp.MustCompile(`(?:/home/|/root/|/opt/|/app/|/usr/src/)[\w.\-/]+|[A-Za-z]:\\Users\\`)},
		{"内网地址", regexp.MustCompile(`\b(?:10\.\d{1,3}|127\.\d{1,3}|192\.168|172\.(?:1[6-9]|2\d|3[01]))\.\d{1,3}\.\d{1,3}\b`)},
		{"环境变量名", regexp.MustCompile(`\b[A-Z][A-Z0-9_]*_API_KEY\b|SECRET_KEY=`)},
		{"网关内部字段", regexp.MustCompile(`litellm_params|UserAPIKeyAuth`)},
	}
	gatewayHeaders = []string{"x-powered-by", "server", "x-litellm-version", "x-new-api-version", "x-oneapi-request-id"}
)

const fakeProbeKey = "apigx-audit-invalid-credential"

func runErrorLeakageCheck(ctx context.Context, target auditTarget) CheckResult {
	type probeOutcome struct {
		status int
		text   string
		err    error
	}
	outcomes := make([]probeOutcome, len(errorProbes))
	var wg sync.WaitGroup
	for i, probe := range errorProbes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			status, text, err := target.rawProbe(ctx, probe)
			outcomes[i] = probeOutcome{status, text, err}
		}()
	}
	wg.Wait()

	var secrets, echoes []string
	signals := map[string]bool{}
	fingerprints := map[string]bool{}
	responded, errored := 0, 0
	for i, outcome := range outcomes {
		if outcome.err != nil {
			continue
		}
		responded++
		if outcome.status >= 400 {
			errored++
		}
		text := outcome.text
		if key := target.key; len(key) >= 12 && (strings.Contains(text, key) || strings.Contains(text, key[:8])) {
			echoes = append(echoes, errorProbes[i].name)
			text = strings.ReplaceAll(text, key, "")
		}
		for _, pattern := range secretPatterns {
			if pattern.MatchString(text) {
				secrets = append(secrets, errorProbes[i].name)
				break
			}
		}
		for _, rule := range warnLeakRules {
			if match := rule.pattern.FindString(text); match != "" {
				sample := match
				if rule.label != "上游地址" {
					sample = maskToken(match)
				}
				signals[rule.label+"（"+truncateRunes(sample, 40)+"）"] = true
			}
		}
		for _, line := range strings.Split(text, "\n") {
			for _, header := range gatewayHeaders {
				if value, ok := strings.CutPrefix(strings.ToLower(line), header+": "); ok && value != "" {
					fingerprints[header+"="+truncateRunes(value, 30)] = true
				}
			}
		}
	}
	result := CheckResult{SampleCount: len(errorProbes), Confidence: "medium"}
	notes := make([]string, 0, 4)
	if len(fingerprints) > 0 {
		notes = append(notes, "网关指纹："+strings.Join(sortedKeys(fingerprints), "，"))
	}
	switch {
	case responded == 0:
		result.Status, result.Confidence = apigxmodel.AuditCheckInconclusive, "low"
		result.Evidence = "所有错误探针都未得到响应"
		return result
	case len(secrets) > 0:
		result.Status, result.Confidence = apigxmodel.AuditCheckFailed, "high"
		lead := []string{"错误响应中出现疑似凭据（" + strings.Join(secrets, "、") + "），已脱敏"}
		if len(signals) > 0 {
			lead = append(lead, "同时暴露了 "+strings.Join(sortedKeys(signals), "、"))
		}
		notes = append(lead, notes...)
	case errored == 0:
		result.Status, result.Confidence = apigxmodel.AuditCheckInconclusive, "low"
		notes = append([]string{"错误探针全部返回 2xx，无法判断错误处理"}, notes...)
	case len(signals) > 0 || len(echoes) > 0:
		result.Status = apigxmodel.AuditCheckWarning
		if len(signals) > 0 {
			notes = append([]string{"错误响应暴露了 " + strings.Join(sortedKeys(signals), "、")}, notes...)
		}
		if len(echoes) > 0 {
			notes = append(notes, "错误响应回显了本次检测 Key（"+strings.Join(echoes, "、")+"），存在日志泄漏风险")
		}
	default:
		result.Status = apigxmodel.AuditCheckPassed
		notes = append([]string{fmt.Sprintf("%d 个错误探针的响应中未发现凭据、上游地址、调用栈或内部路径", errored)}, notes...)
	}
	result.Evidence = strings.Join(notes, "；")
	return result
}

// rawProbe returns the status plus response headers and a bounded body as
// text. Header names are lower-cased so fingerprints can be matched.
func (t auditTarget) rawProbe(ctx context.Context, probe errorProbe) (int, string, error) {
	ctx, cancel := withProbeTimeout(ctx, probeRequestTimeout)
	defer cancel()
	// The model id is user input: JSON-escape it before splicing into the body.
	quoted, err := common.Marshal(t.model)
	if err != nil {
		return 0, "", err
	}
	body := strings.ReplaceAll(probe.body, `"%MODEL%"`, string(quoted))
	req, err := http.NewRequestWithContext(ctx, probe.method, t.base+probe.path, strings.NewReader(body))
	if err != nil {
		return 0, "", err
	}
	req.Header.Set("Content-Type", probe.contentType)
	req.Header.Set("Accept", "application/json")
	switch probe.auth {
	case "key":
		req.Header.Set("Authorization", "Bearer "+t.key)
	case "fake":
		req.Header.Set("Authorization", "Bearer "+fakeProbeKey)
		req.Header.Set("x-api-key", fakeProbeKey)
	}
	resp, err := t.client.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	var builder strings.Builder
	for name, values := range resp.Header {
		builder.WriteString(strings.ToLower(name) + ": " + strings.Join(values, ",") + "\n")
	}
	builder.Write(data)
	return resp.StatusCode, builder.String(), nil
}

func sortedKeys(set map[string]bool) []string {
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
