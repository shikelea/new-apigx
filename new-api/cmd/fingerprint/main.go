package main

import (
	"bufio"
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/apigx/modelfp"
	"github.com/QuantumNous/new-api/apigx/relayprobe"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service"
)

// baselineTemperature 必须和实时采集口径一致（见 relayprobe.collectLegacyProbes）。
// 温度压到 0 只会拿到一个固定答案，分布信息全部丢失；不传则由上游默认值决定，基准与
// 观测就来自两个采样工况，不可比。所以这里钉死，不开放成命令行参数。
const baselineTemperature = 1

type endpoint struct {
	client *http.Client
	base   string
	key    string
	model  string
}

// probeRun 是单题的采样账目。有效数、空答案数、失败数都要落进产物，否则一份缺样本的
// 基准看起来和一份完整的没有任何区别。
type probeRun struct {
	Counts     modelfp.ProbeCounts `json:"-"`
	Valid      int                 `json:"valid"`
	Empty      int                 `json:"empty"`
	Failed     int                 `json:"failed"`
	FirstError string              `json:"first_error,omitempty"`
}

type baseline struct {
	// Model 是实测时实际使用的请求名。落库前必须核对它对应哪一个收录型号——基准挂错
	// 型号会让所有声称该型号的链路拿别人的偏好做参照。
	Model           string                         `json:"model"`
	Temperature     int                            `json:"temperature"`
	SamplesPerProbe int                            `json:"samples_per_probe"`
	MeasuredAt      string                         `json:"measured_at"`
	Runs            map[string]*probeRun           `json:"per_probe"`
	Observed        map[string]modelfp.ProbeCounts `json:"observed"`
}

func main() {
	var (
		baseURL      = flag.String("url", "", "API base URL (required)")
		apiKey       = flag.String("key", "", "API key, prefer the FINGERPRINT_API_KEY env var (required)")
		model        = flag.String("model", "", "实际请求名, e.g. deepseek-flash (required)")
		samples      = flag.Int("samples", 30, "每题请求次数")
		interval     = flag.Duration("interval", 2100*time.Millisecond, "两次请求的最小间隔, 成功失败都受限速")
		reqTimeout   = flag.Duration("request-timeout", 60*time.Second, "单次请求超时")
		output       = flag.String("output", "", "输出 JSON 文件（默认 stdout）")
		force        = flag.Bool("force", false, "允许覆盖已存在的输出文件")
		allowPartial = flag.Bool("allow-partial", false, "允许输出有效样本不足的基准")
	)
	flag.Parse()

	if *baseURL == "" || *model == "" || *samples < 1 {
		flag.Usage()
		os.Exit(2)
	}
	key := strings.TrimSpace(*apiKey)
	if key == "" {
		key = strings.TrimSpace(os.Getenv("FINGERPRINT_API_KEY"))
	}
	if key == "" {
		fmt.Fprintln(os.Stderr, "缺少 API Key：用 -key 传入，或设环境变量 FINGERPRINT_API_KEY（避免 Key 落进 shell 历史）")
		os.Exit(2)
	}
	if *output != "" && !*force {
		if _, err := os.Stat(*output); err == nil {
			fmt.Fprintf(os.Stderr, "%s 已存在，确认要覆盖请加 -force\n", *output)
			os.Exit(2)
		}
	}

	service.InitHttpClient()

	base, err := relayprobe.ValidateBaseURL(*baseURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Invalid base URL: %v\n", err)
		os.Exit(1)
	}
	client := service.GetSSRFProtectedHTTPClient()
	if client == nil {
		fmt.Fprintln(os.Stderr, "HTTP 客户端未初始化")
		os.Exit(1)
	}

	ep := endpoint{client: client, base: base, key: key, model: strings.TrimSpace(*model)}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	fmt.Fprintf(os.Stderr, "目标 %s，模型 %s，%d 个探测题 × 每题 %d 次，temperature=%d，间隔 %s\n",
		ep.base, ep.model, len(modelfp.Probes), *samples, baselineTemperature, *interval)
	fmt.Fprintf(os.Stderr, ". 有效  ○ 空答案  ✗ 失败\n\n")

	runs := make(map[string]*probeRun, len(modelfp.Probes))
	observed := make(map[string]modelfp.ProbeCounts, len(modelfp.Probes))
	var lastRequest time.Time
	interrupted := false

	for _, probe := range modelfp.Probes {
		run := &probeRun{Counts: modelfp.ProbeCounts{}}
		runs[probe.ID] = run
		fmt.Fprintf(os.Stderr, "[%s] ", probe.ID)

		for range *samples {
			if !lastRequest.IsZero() {
				if wait := *interval - time.Since(lastRequest); wait > 0 {
					select {
					case <-time.After(wait):
					case <-ctx.Done():
					}
				}
			}
			if ctx.Err() != nil {
				interrupted = true
				break
			}
			answer, err := sampleOnce(ctx, ep, probe, *reqTimeout)
			lastRequest = time.Now()
			switch {
			case err != nil:
				run.Failed++
				if run.FirstError == "" {
					run.FirstError = err.Error()
				}
				fmt.Fprint(os.Stderr, "✗")
			case answer == "":
				// 空答案不是偏好，是这一次没采到。实时采集同样丢弃它，基准里也必须丢，
				// 否则多出的是一个观测侧永远打不中的类目。
				run.Empty++
				fmt.Fprint(os.Stderr, "○")
			default:
				run.Counts[answer]++
				run.Valid++
				fmt.Fprint(os.Stderr, ".")
			}
		}

		// 不同答案数要打印：基准的分辨力来自分布的离散程度。全落在一个类目上往往不是
		// 「这个型号就偏好它」，而是采样参数没生效或型号退化成贪心解码——这种数据不能
		// 当分布基准，但只能由人来判断，工具不替人下结论。
		fmt.Fprintf(os.Stderr, "  有效 %d，空 %d，失败 %d，%d 个不同答案\n", run.Valid, run.Empty, run.Failed, len(run.Counts))
		if len(run.Counts) > 0 {
			observed[probe.ID] = run.Counts
		}
		if interrupted {
			break
		}
	}

	var short []string
	var firstShort *probeRun
	for _, probe := range modelfp.Probes {
		run := runs[probe.ID]
		if run == nil || run.Valid >= *samples {
			continue
		}
		short = append(short, fmt.Sprintf("%s %d/%d", probe.ID, run.Valid, *samples))
		if firstShort == nil {
			firstShort = run
		}
	}
	if len(short) > 0 && !*allowPartial {
		fmt.Fprintf(os.Stderr, "\n有效样本不足：%s\n", strings.Join(short, "；"))
		if firstShort != nil && firstShort.FirstError != "" {
			fmt.Fprintf(os.Stderr, "首个失败原因：%s\n", firstShort.FirstError)
		}
		fmt.Fprintln(os.Stderr, "确认要使用不完整基准时加 -allow-partial")
		os.Exit(1)
	}

	if len(observed) == 0 {
		fmt.Fprintln(os.Stderr, "\n一个有效答案都没采到，检查 -model 请求名与 Key 是否可用")
		os.Exit(1)
	}

	result := baseline{
		Model:           ep.model,
		Temperature:     baselineTemperature,
		SamplesPerProbe: *samples,
		MeasuredAt:      time.Now().Format(time.DateOnly),
		Runs:            runs,
		Observed:        observed,
	}
	encoded, err := common.Marshal(result)
	if err != nil {
		fmt.Fprintf(os.Stderr, "编码失败: %v\n", err)
		os.Exit(1)
	}
	if encoded, err = common.IndentJson(encoded); err != nil {
		fmt.Fprintf(os.Stderr, "格式化失败: %v\n", err)
		os.Exit(1)
	}

	if *output == "" {
		if _, err := os.Stdout.Write(append(encoded, '\n')); err != nil {
			fmt.Fprintf(os.Stderr, "写出失败: %v\n", err)
			os.Exit(1)
		}
	} else if err := os.WriteFile(*output, append(encoded, '\n'), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "写入 %s 失败: %v\n", *output, err)
		os.Exit(1)
	} else {
		fmt.Fprintf(os.Stderr, "\n结果已写入 %s（model=%s，落库前请核对它对应哪一个收录型号）\n", *output, result.Model)
	}

	if interrupted {
		fmt.Fprintln(os.Stderr, "采样被中断，产物是不完整的")
		os.Exit(1)
	}
}

func sampleOnce(ctx context.Context, ep endpoint, probe modelfp.Probe, timeout time.Duration) (string, error) {
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	messages := make([]map[string]any, 0, 2)
	if probe.System != "" {
		messages = append(messages, map[string]any{"role": "system", "content": probe.System})
	}
	messages = append(messages, map[string]any{"role": "user", "content": probe.Prompt})

	payload, err := common.Marshal(map[string]any{
		"model":       ep.model,
		"messages":    messages,
		"max_tokens":  probe.MaxTokens,
		"temperature": baselineTemperature,
		"stream":      false,
	})
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, ep.base+"/v1/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+ep.key)

	resp, err := ep.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		scanner := bufio.NewScanner(io.LimitReader(resp.Body, 4096))
		message := ""
		if scanner.Scan() {
			message = strings.TrimSpace(scanner.Text())
		}
		if message == "" {
			message = "(空响应体)"
		}
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, message)
	}

	var apiResp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := common.DecodeJson(resp.Body, &apiResp); err != nil {
		return "", err
	}
	if len(apiResp.Choices) == 0 {
		return "", fmt.Errorf("响应没有 choices")
	}
	return modelfp.NormalizeAnswer(apiResp.Choices[0].Message.Content), nil
}
