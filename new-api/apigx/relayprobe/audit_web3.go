package relayprobe

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	apigxmodel "github.com/QuantumNous/new-api/apigx/model"
)

// Web3 注入：中转处在明文链路上，可以在返回内容里把钱包地址换成自己的，或者往代码
// 里塞上传私钥、助记词的逻辑。这类改写对使用 AI 写链上代码的人是直接的资金风险。
//
// 手法：请求里给出一个已知的检验地址，要求原样回显；同时要求写一段简单的转账代码。
// 然后核对回显是否一致、是否出现了别的地址，以及代码里有没有外传密钥的迹象。
const (
	web3Address = "0x5FbDB2315678afecb367f032d93F642f64180aa3"
	web3Prompt  = `Task 1: echo this address exactly once, unchanged: ` + web3Address + `
Task 2: write a minimal ethers.js v6 snippet that sends 0.01 ETH from a wallet to that same address. Keep it under 15 lines and do not add extra services.`
)

var (
	web3AddressPattern = regexp.MustCompile(`0x[a-fA-F0-9]{40}`)
	// 助记词/私钥外传的典型迹象：把敏感值发到网络，或读取本地钱包文件。
	web3ExfilPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)(?:fetch|axios|request|https?\.(?:post|get)|urllib|requests\.post)\s*\(`),
		regexp.MustCompile(`(?i)(?:mnemonic|privateKey|private_key|seed\s*phrase)[^\n]{0,60}(?:post|send|upload|fetch|http)`),
		regexp.MustCompile(`(?i)(?:eval|exec|child_process|os\.system)\s*\(`),
		regexp.MustCompile(`(?i)(?:base64|atob|fromCharCode)\s*\(`),
	}
)

func runWeb3InjectionCheck(ctx context.Context, target auditTarget) CheckResult {
	resp, err := target.chat(ctx, map[string]any{"messages": userMessages("", web3Prompt), "max_tokens": 400, "temperature": 0})
	if err != nil {
		return CheckResult{Status: apigxmodel.AuditCheckInconclusive, Evidence: "模型请求未成功：" + err.Error(), Confidence: "low", SampleCount: 1}
	}
	reply := resp.Content
	result := CheckResult{SampleCount: 1, LatencyMs: resp.LatencyMs, Confidence: "medium"}
	if strings.TrimSpace(reply) == "" {
		result.Status, result.Confidence = apigxmodel.AuditCheckInconclusive, "low"
		result.Evidence = "模型没有返回内容，无法判断"
		return result
	}
	var problems, notes []string
	addresses := web3AddressPattern.FindAllString(reply, -1)
	foreign := make([]string, 0, 2)
	for _, address := range addresses {
		if !strings.EqualFold(address, web3Address) {
			foreign = append(foreign, maskAddress(address))
		}
	}
	switch {
	case len(addresses) == 0:
		notes = append(notes, "回复中没有出现任何地址，未能核对回显")
	case len(foreign) > 0:
		problems = append(problems, "回复中出现了本次未提供的钱包地址："+strings.Join(uniqueStrings(foreign), "、")+"，存在地址替换风险")
	default:
		notes = append(notes, fmt.Sprintf("检验地址被原样回显 %d 次，未出现其他地址", len(addresses)))
	}
	for _, pattern := range web3ExfilPatterns {
		if found := pattern.FindString(reply); found != "" {
			problems = append(problems, "生成代码含可疑外传/执行片段："+truncateRunes(strings.TrimSpace(found), 40))
		}
	}
	switch {
	case len(problems) > 0:
		result.Status, result.Confidence = apigxmodel.AuditCheckFailed, "high"
		result.Evidence = strings.Join(append(problems, notes...), "；") + "。链上代码请勿直接使用，务必人工核对地址"
	case len(notes) > 0 && len(addresses) == 0:
		result.Status = apigxmodel.AuditCheckInconclusive
		result.Evidence = strings.Join(notes, "；")
	default:
		result.Status = apigxmodel.AuditCheckPassed
		result.Evidence = strings.Join(notes, "；") + "，生成代码中未见外传密钥的迹象"
	}
	return result
}

// maskAddress 只保留地址首尾，避免把可疑收款地址完整写进公开证据里。
func maskAddress(address string) string {
	if len(address) <= 14 {
		return address
	}
	return address[:8] + "…" + address[len(address)-6:]
}

func uniqueStrings(items []string) []string {
	seen := make(map[string]bool, len(items))
	out := make([]string, 0, len(items))
	for _, item := range items {
		if seen[item] {
			continue
		}
		seen[item] = true
		out = append(out, item)
	}
	return out
}
