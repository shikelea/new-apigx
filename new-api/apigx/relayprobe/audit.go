package relayprobe

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	apigxmodel "github.com/QuantumNous/new-api/apigx/model"
	"github.com/QuantumNous/new-api/service"
)

// Supplier audit.
//
// The techniques and thresholds follow public relay-audit research
// (toby-bridges/api-relay-audit, canarybyte/veridrop, unclecode/modelprint,
// vlmrun streaming audit). They are re-implemented here; no code is copied.
// Every result is a signal that needs human review, never a verdict: upstream
// versions, routing and sampling all move these numbers.
const (
	CheckProtocol            = "protocol"
	CheckTokenUsage          = "token_usage"
	CheckTokenInjection      = "token_injection"
	CheckPromptExtraction    = "prompt_leakage"
	CheckInstructionConflict = "instruction_conflict"
	CheckIdentity            = "identity"
	CheckContextWindow       = "context_window"
	CheckToolRewrite         = "tool_rewrite"
	CheckSSEIntegrity        = "sse_integrity"
	CheckErrorLeakage        = "error_leakage"
	CheckWeb3Injection       = "web3_injection"
	CheckLatencyVariance     = "latency_variance"

	auditTotalTimeout = 150 * time.Second
	maxResponseBytes  = 256 << 10
	maxStreamBytes    = 1 << 20
	maxEvidenceRunes  = 480
	// Ten model-facing checks against one upstream would trip per-key rate
	// limits, so they run a few at a time instead of all at once.
	auditConcurrency = 4
)

// AuditDisclaimer is shown with every report.
const AuditDisclaimer = "检测结果是信号而非判决；会受上游版本、路由与采样影响，请结合人工复核。"

type CheckResult struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Category    string `json:"category"`
	Status      string `json:"status"`
	Evidence    string `json:"evidence"`
	Confidence  string `json:"confidence"`
	SampleCount int    `json:"sample_count"`
	LatencyMs   int64  `json:"latency_ms"`
}

type AuditReport struct {
	Checks     []CheckResult `json:"checks"`
	ModelCount int           `json:"model_count"`
	RiskLevel  string        `json:"risk_level"`
}

type auditTarget struct {
	client *http.Client
	base   string
	key    string
	model  string
}

type auditSpec struct {
	id, name, category string
	run                func(context.Context, auditTarget) CheckResult
}

// auditSpecs covers the ten supplier-audit items as one combined run. Order is
// the display order; the IDs are persisted, so they must stay stable.
var auditSpecs = []auditSpec{
	{CheckTokenUsage, "Token 用量一致性", "计量", runTokenUsageCheck},
	{CheckTokenInjection, "Token 注入", "计量", runTokenInjectionCheck},
	{CheckPromptExtraction, "Prompt 提取", "安全", runPromptExtractionCheck},
	{CheckInstructionConflict, "指令冲突", "安全", runInstructionConflictCheck},
	{CheckIdentity, "越狱与身份", "身份", runIdentityCheck},
	{CheckContextWindow, "上下文窗口", "能力", runContextWindowCheck},
	{CheckToolRewrite, "工具改写", "安全", runToolRewriteCheck},
	{CheckErrorLeakage, "错误泄漏", "安全", runErrorLeakageCheck},
	{CheckSSEIntegrity, "流式完整性", "传输", runSSEIntegrityCheck},
	{CheckWeb3Injection, "Web3 注入", "安全", runWeb3InjectionCheck},
	{CheckLatencyVariance, "延迟方差", "性能", runLatencyVarianceCheck},
}

// RunAudit validates the target against the SSRF policy and runs every check
// through the protected client. The key is used only for outbound requests
// and is masked out of all evidence.
func RunAudit(ctx context.Context, baseURL, protocol, key, model string) (AuditReport, error) {
	base, err := ValidateBaseURL(baseURL)
	if err != nil {
		return AuditReport{}, fmt.Errorf("目标地址未通过安全校验")
	}
	if !supportedProtocol(protocol) {
		return AuditReport{}, fmt.Errorf("暂不支持该协议的自动检测")
	}
	return runAudit(ctx, auditTarget{client: service.GetSSRFProtectedHTTPClient(), base: base, key: strings.TrimSpace(key), model: strings.TrimSpace(model)}), nil
}

func runAudit(ctx context.Context, target auditTarget) AuditReport {
	ctx, cancel := withProbeTimeout(ctx, auditTotalTimeout)
	defer cancel()
	inventory := probeInventory(ctx, target.client, target.base, target.key, time.Now())
	protocolCheck := CheckResult{ID: CheckProtocol, Name: "协议与模型列表", Category: "基础设施", SampleCount: 1, LatencyMs: inventory.LatencyMs, Confidence: "high"}
	report := AuditReport{ModelCount: inventory.ModelCount}
	switch {
	case inventory.Status == apigxmodel.RelayCheckFailed:
		protocolCheck.Status, protocolCheck.Evidence = apigxmodel.AuditCheckFailed, inventory.Reason
	case !containsModel(inventory.Models, target.model):
		protocolCheck.Status = apigxmodel.AuditCheckWarning
		protocolCheck.Evidence = fmt.Sprintf("模型列表可达（%d 个），但未列出待测模型；后续检查仍按该模型发起", inventory.ModelCount)
	default:
		protocolCheck.Status = apigxmodel.AuditCheckPassed
		protocolCheck.Evidence = fmt.Sprintf("模型列表可达（%d 个），包含待测模型", inventory.ModelCount)
	}
	report.Checks = append(report.Checks, protocolCheck)
	if inventory.Status == apigxmodel.RelayCheckFailed {
		for _, spec := range auditSpecs {
			report.Checks = append(report.Checks, CheckResult{ID: spec.id, Name: spec.name, Category: spec.category, Status: apigxmodel.AuditCheckSkipped, Evidence: "基础连通性未通过，未发送模型请求", Confidence: "low"})
		}
		return finalizeReport(report, target.key)
	}

	results := make([]CheckResult, len(auditSpecs))
	slots := make(chan struct{}, auditConcurrency)
	var wg sync.WaitGroup
	for i, spec := range auditSpecs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			slots <- struct{}{}
			defer func() { <-slots }()
			defer func() {
				if recovered := recover(); recovered != nil {
					results[i] = CheckResult{Status: apigxmodel.AuditCheckInconclusive, Evidence: "检查过程异常，已中止该项", Confidence: "low"}
				}
				results[i].ID, results[i].Name, results[i].Category = spec.id, spec.name, spec.category
			}()
			started := time.Now()
			results[i] = spec.run(ctx, target)
			if results[i].LatencyMs == 0 {
				results[i].LatencyMs = time.Since(started).Milliseconds()
			}
		}()
	}
	wg.Wait()
	report.Checks = append(report.Checks, results...)
	return finalizeReport(report, target.key)
}

func finalizeReport(report AuditReport, key string) AuditReport {
	worst, passed := "", 0
	for i := range report.Checks {
		report.Checks[i].Evidence = truncateRunes(maskSecrets(report.Checks[i].Evidence, key), maxEvidenceRunes)
		if report.Checks[i].ID != CheckProtocol && report.Checks[i].Status == apigxmodel.AuditCheckPassed {
			passed++
		}
		switch report.Checks[i].Status {
		case apigxmodel.AuditCheckFailed:
			worst = apigxmodel.AuditCheckFailed
		case apigxmodel.AuditCheckWarning:
			if worst != apigxmodel.AuditCheckFailed {
				worst = apigxmodel.AuditCheckWarning
			}
		}
	}
	switch worst {
	case apigxmodel.AuditCheckFailed:
		report.RiskLevel = "高风险信号"
	case apigxmodel.AuditCheckWarning:
		report.RiskLevel = "需复核"
	case "":
		if passed == 0 {
			report.RiskLevel = "证据不足"
			break
		}
		report.RiskLevel = "未发现明显异常"
	default:
		report.RiskLevel = "未发现明显异常"
	}
	return report
}

func containsModel(models []string, model string) bool {
	for _, item := range models {
		if strings.EqualFold(item, model) {
			return true
		}
	}
	return false
}
