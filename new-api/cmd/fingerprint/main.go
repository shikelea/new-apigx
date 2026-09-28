package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/apigx/modelfp"
	"github.com/QuantumNous/new-api/apigx/relayprobe"
	"github.com/QuantumNous/new-api/service"
)

func main() {
	var (
		baseURL = flag.String("url", "", "API base URL (required)")
		apiKey  = flag.String("key", "", "API key (required)")
		model   = flag.String("model", "", "Model name (required)")
		samples = flag.Int("samples", 30, "Samples per probe")
		output  = flag.String("output", "", "Output JSON file (default: stdout)")
	)
	flag.Parse()

	if *baseURL == "" || *apiKey == "" || *model == "" {
		flag.Usage()
		os.Exit(1)
	}

	// Initialize HTTP client
	service.InitHttpClient()

	base, err := relayprobe.ValidateBaseURL(*baseURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Invalid base URL: %v\n", err)
		os.Exit(1)
	}

	client := service.GetSSRFProtectedHTTPClient()
	if client == nil {
		fmt.Fprintf(os.Stderr, "Failed to initialize HTTP client\n")
		os.Exit(1)
	}

	target := struct {
		client *http.Client
		base   string
		key    string
		model  string
	}{
		client: client,
		base:   base,
		key:    strings.TrimSpace(*apiKey),
		model:  strings.TrimSpace(*model),
	}

	ctx := context.Background()
	observed := make(map[string]modelfp.ProbeCounts)

	fmt.Fprintf(os.Stderr, "开始采样 %d 个探测题，每题 %d 次\n", len(modelfp.Probes), *samples)
	fmt.Fprintf(os.Stderr, "目标: %s\n", target.base)
	fmt.Fprintf(os.Stderr, "模型: %s\n", target.model)
	fmt.Fprintf(os.Stderr, "\n")

	for _, probe := range modelfp.Probes {
		counts := make(modelfp.ProbeCounts)
		fmt.Fprintf(os.Stderr, "[%s] ", probe.ID)

		for i := 0; i < *samples; i++ {
			answer, err := sampleOnce(ctx, target, probe)
			if err != nil {
				fmt.Fprintf(os.Stderr, "✗[%v]", err)
				continue
			}
			counts[answer]++
			fmt.Fprintf(os.Stderr, ".")
		}
		fmt.Fprintf(os.Stderr, " 完成\n")
		observed[probe.ID] = counts
	}

	result := map[string]interface{}{
		"model":    target.model,
		"samples":  *samples,
		"observed": observed,
	}

	var out *os.File
	if *output == "" {
		out = os.Stdout
	} else {
		out, err = os.Create(*output)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to create output file: %v\n", err)
			os.Exit(1)
		}
		defer out.Close()
	}

	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	if err := enc.Encode(result); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to encode JSON: %v\n", err)
		os.Exit(1)
	}

	if *output != "" {
		fmt.Fprintf(os.Stderr, "\n结果已写入: %s\n", *output)
	}
}

func sampleOnce(ctx context.Context, target struct {
	client *http.Client
	base   string
	key    string
	model  string
}, probe modelfp.Probe) (string, error) {
	reqBody := map[string]interface{}{
		"model": target.model,
		"messages": []map[string]string{
			{"role": "system", "content": probe.System},
			{"role": "user", "content": probe.Prompt},
		},
		"max_tokens": probe.MaxTokens,
		"stream":     false,
	}

	payload, _ := json.Marshal(reqBody)
	req, err := http.NewRequestWithContext(ctx, "POST", target.base+"/v1/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+target.key)

	resp, err := target.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		scanner := bufio.NewScanner(resp.Body)
		scanner.Scan()
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, scanner.Text())
	}

	var apiResp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return "", err
	}

	if len(apiResp.Choices) == 0 {
		return "", fmt.Errorf("empty response")
	}

	raw := strings.TrimSpace(apiResp.Choices[0].Message.Content)
	answer := modelfp.NormalizeAnswer(raw)

	time.Sleep(2100 * time.Millisecond)
	return answer, nil
}
