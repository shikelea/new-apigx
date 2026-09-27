package relayprobe

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	apigxmodel "github.com/QuantumNous/new-api/apigx/model"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testRelayKey = "sk-audit-test-0123456789abcdefghij"

func TestValidateBaseURLRejectsUnsupportedAndLocalTargets(t *testing.T) {
	for _, raw := range []string{"", "ftp://example.com", "http://127.0.0.1:3000", "http://localhost:3000"} {
		_, err := ValidateBaseURL(raw)
		require.Error(t, err, raw)
	}
}

func TestProbeRejectsLocalTargetWithoutLeakingCredential(t *testing.T) {
	result := Probe(context.Background(), "http://127.0.0.1:3000", "new-api", "test-secret")
	require.Equal(t, apigxmodel.RelayCheckFailed, result.Status)
	require.NotContains(t, result.Reason, "test-secret")
}

func TestRunAuditRejectsLocalTargetBeforeSendingRequests(t *testing.T) {
	_, err := RunAudit(context.Background(), "http://127.0.0.1:3000", "new-api", testRelayKey, "claude-sonnet-4")
	require.Error(t, err)
	require.NotContains(t, err.Error(), testRelayKey)
}

func TestModelListResponseDecodesBoundedInventory(t *testing.T) {
	payload := []byte(`{"data":[{"id":"model-a"},{"id":"model-b"}]}`)
	var decoded modelListResponse
	require.NoError(t, common.Unmarshal(payload, &decoded))
	require.Len(t, decoded.Data, 2)
	require.Equal(t, "model-a", decoded.Data[0].ID)
}

// fakeRelay simulates a relay. honest=false injects the misbehaviour each
// audit check is supposed to flag.
type fakeRelay struct {
	honest bool
	model  string
}

func (f fakeRelay) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	writeJSON := func(status int, value any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		body, _ := common.Marshal(value)
		_, _ = w.Write(body)
	}
	if !f.honest && r.URL.Path != "/v1/models" && r.Header.Get("Authorization") != "Bearer "+testRelayKey {
		writeJSON(http.StatusInternalServerError, map[string]any{"error": "Traceback (most recent call last): upstream https://api.anthropic.com rejected sk-upstreamSECRET0123456789abcdef"})
		return
	}
	if r.Header.Get("Authorization") != "Bearer "+testRelayKey {
		writeJSON(http.StatusUnauthorized, map[string]any{"error": map[string]any{"message": "invalid token"}})
		return
	}
	switch r.URL.Path {
	case "/v1/models":
		writeJSON(http.StatusOK, map[string]any{"data": []map[string]any{{"id": f.model}, {"id": "other-model"}}})
		return
	case "/v1/chat/completions":
	default:
		writeJSON(http.StatusNotFound, map[string]any{"error": map[string]any{"message": "route not found"}})
		return
	}
	var req struct {
		Model    string `json:"model"`
		Stream   bool   `json:"stream"`
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") || common.DecodeJson(r.Body, &req) != nil {
		writeJSON(http.StatusBadRequest, map[string]any{"error": map[string]any{"message": "invalid request body"}})
		return
	}
	if req.Model != f.model {
		writeJSON(http.StatusNotFound, map[string]any{"error": map[string]any{"message": "model not found"}})
		return
	}
	if len(req.Messages) == 0 {
		writeJSON(http.StatusBadRequest, map[string]any{"error": map[string]any{"message": "messages is required"}})
		return
	}
	if req.Stream {
		f.serveStream(w)
		return
	}
	words, system, user := 0, "", ""
	for _, message := range req.Messages {
		words += len(strings.Fields(message.Content))
		if message.Role == "system" {
			system = message.Content
		} else {
			user = message.Content
		}
	}
	reply := "ok"
	switch {
	case strings.Contains(user, "Who are you") && f.honest:
		reply = "I am Claude, an AI assistant made by Anthropic, not GPT."
	case strings.Contains(user, "Who are you"):
		reply = "I am GPT-4, a model developed by OpenAI."
	case system != "" && f.honest:
		reply = "meow"
	case system != "":
		reply = "1+1 equals 2"
	}
	prompt, completion := int64(8+words), int64(len(strings.Fields(reply)))
	total := prompt + completion
	if !f.honest {
		prompt, total = 500, 500+completion+7
	}
	writeJSON(http.StatusOK, map[string]any{
		"id": "chatcmpl-test", "object": "chat.completion", "model": f.model,
		"choices": []map[string]any{{"index": 0, "message": map[string]any{"role": "assistant", "content": reply}}},
		"usage":   map[string]any{"prompt_tokens": prompt, "completion_tokens": completion, "total_tokens": total},
	})
}

func (f fakeRelay) serveStream(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	flusher, _ := w.(http.Flusher)
	send := func(value any) {
		body, _ := common.Marshal(value)
		_, _ = fmt.Fprintf(w, "data: %s\n\n", body)
		if flusher != nil {
			flusher.Flush()
		}
	}
	chunk := func(text string) map[string]any {
		return map[string]any{"object": "chat.completion.chunk", "choices": []map[string]any{{"index": 0, "delta": map[string]any{"content": text}}}}
	}
	if !f.honest {
		// Everything in one chunk, no usage and no [DONE] terminator.
		numbers := make([]string, 0, streamCountTo)
		for i := 1; i <= streamCountTo; i++ {
			numbers = append(numbers, fmt.Sprint(i))
		}
		send(chunk(strings.Join(numbers, " ")))
		return
	}
	for start := 1; start <= streamCountTo; start += 4 {
		parts := make([]string, 0, 4)
		for i := start; i < start+4 && i <= streamCountTo; i++ {
			parts = append(parts, fmt.Sprint(i))
		}
		send(chunk(strings.Join(parts, " ") + " "))
		time.Sleep(6 * time.Millisecond)
	}
	send(map[string]any{"object": "chat.completion.chunk", "choices": []any{}, "usage": map[string]any{"prompt_tokens": 20, "completion_tokens": 40, "total_tokens": 60}})
	_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
}

func runFakeAudit(t *testing.T, honest bool) map[string]CheckResult {
	t.Helper()
	server := httptest.NewServer(fakeRelay{honest: honest, model: "claude-sonnet-4"})
	t.Cleanup(server.Close)
	report := runAudit(context.Background(), auditTarget{client: server.Client(), base: server.URL, key: testRelayKey, model: "claude-sonnet-4"})
	byID := make(map[string]CheckResult, len(report.Checks))
	for _, check := range report.Checks {
		byID[check.ID] = check
		assert.NotContains(t, check.Evidence, testRelayKey, check.ID)
		assert.NotContains(t, check.Evidence, "sk-upstreamSECRET", check.ID)
		assert.NotEmpty(t, check.Name, check.ID)
	}
	require.Len(t, byID, len(auditSpecs)+1)
	return byID
}

func TestRunAuditPassesHonestRelay(t *testing.T) {
	checks := runFakeAudit(t, true)
	for _, id := range []string{CheckProtocol, CheckTokenUsage, CheckPromptIntegrity, CheckIdentity, CheckSSEIntegrity, CheckErrorLeakage} {
		assert.Equal(t, apigxmodel.AuditCheckPassed, checks[id].Status, "%s: %s", id, checks[id].Evidence)
	}
}

func TestRunAuditFlagsDishonestRelay(t *testing.T) {
	checks := runFakeAudit(t, false)
	assert.Equal(t, apigxmodel.AuditCheckFailed, checks[CheckTokenUsage].Status, checks[CheckTokenUsage].Evidence)
	assert.Contains(t, checks[CheckTokenUsage].Evidence, "total_tokens")
	assert.Equal(t, apigxmodel.AuditCheckWarning, checks[CheckPromptIntegrity].Status, checks[CheckPromptIntegrity].Evidence)
	assert.Contains(t, checks[CheckPromptIntegrity].Evidence, "隐藏提示词")
	assert.Equal(t, apigxmodel.AuditCheckWarning, checks[CheckIdentity].Status, checks[CheckIdentity].Evidence)
	assert.Contains(t, checks[CheckIdentity].Evidence, "OpenAI GPT")
	assert.Equal(t, apigxmodel.AuditCheckFailed, checks[CheckSSEIntegrity].Status, checks[CheckSSEIntegrity].Evidence)
	assert.Contains(t, checks[CheckSSEIntegrity].Evidence, "[DONE]")
	assert.Equal(t, apigxmodel.AuditCheckFailed, checks[CheckErrorLeakage].Status, checks[CheckErrorLeakage].Evidence)
	assert.Contains(t, checks[CheckErrorLeakage].Evidence, "api.anthropic.com")
}

func TestRunAuditSkipsModelChecksWhenInventoryFails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(server.Close)
	report := runAudit(context.Background(), auditTarget{client: server.Client(), base: server.URL, key: testRelayKey, model: "gpt-4o"})
	require.Len(t, report.Checks, len(auditSpecs)+1)
	assert.Equal(t, apigxmodel.AuditCheckFailed, report.Checks[0].Status)
	for _, check := range report.Checks[1:] {
		assert.Equal(t, apigxmodel.AuditCheckSkipped, check.Status, check.ID)
	}
	assert.Equal(t, "高风险信号", report.RiskLevel)
}

func TestClaimedFamiliesIgnoresNegations(t *testing.T) {
	assert.Equal(t, []string{"Anthropic Claude"}, claimedFamilies("I am Claude by Anthropic, not GPT-4 from OpenAI."))
	assert.Equal(t, []string{"通义千问"}, claimedFamilies("我是通义千问，不是 ChatGPT。"))
	assert.Empty(t, claimedFamilies("I am a helpful assistant."))
	assert.Equal(t, "Anthropic Claude", expectedFamily("anthropic/claude-3-5-sonnet"))
	assert.Equal(t, "OpenAI GPT", expectedFamily("gpt-4o-mini"))
	assert.Equal(t, "", expectedFamily("my-custom-model"))
}

func TestMaskSecretsRemovesKeysAndCredentials(t *testing.T) {
	masked := maskSecrets("key "+testRelayKey+" Bearer abcdefghijklmnopqrstuvwxyz0123 AKIAABCDEFGHIJKLMNOP https://user:pass@host", testRelayKey)
	assert.NotContains(t, masked, testRelayKey)
	assert.NotContains(t, masked, "abcdefghijklmnopqrstuvwxyz0123")
	assert.NotContains(t, masked, "AKIAABCDEFGHIJKLMNOP")
	assert.NotContains(t, masked, "user:pass")
}

func TestRefreshSessionCookieReturnsRotatedCookieAndSendsOrigin(t *testing.T) {
	var gotOrigin, gotCookie string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotOrigin, gotCookie = r.Header.Get("Origin"), r.Header.Get("Cookie")
		http.SetCookie(w, &http.Cookie{Name: service.RefreshCookieName, Value: "sid.next-secret", Path: "/"})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{"access_token":"access-123"}}`))
	}))
	t.Cleanup(server.Close)
	access, rotated, err := refreshSessionCookie(context.Background(), server.Client(), server.URL, "sid.old-secret")
	require.NoError(t, err)
	assert.Equal(t, "access-123", access)
	assert.Equal(t, service.RefreshCookieName+"=sid.next-secret", rotated)
	assert.Equal(t, server.URL, gotOrigin)
	assert.Equal(t, service.RefreshCookieName+"=sid.old-secret", gotCookie)
}

func TestRefreshSessionCookieKeepsRotationOnFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: service.RefreshCookieName, Value: "sid.rotated", Path: "/"})
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"success":false}`))
	}))
	t.Cleanup(server.Close)
	_, rotated, err := refreshSessionCookie(context.Background(), server.Client(), server.URL, service.RefreshCookieName+"=sid.old")
	require.Error(t, err)
	assert.Equal(t, service.RefreshCookieName+"=sid.rotated", rotated, "rotated cookie must be persisted even when the refresh payload is unusable")
}
