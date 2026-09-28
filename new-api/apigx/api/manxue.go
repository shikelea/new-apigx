package apigxapi

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
)

// manxue.ai 智力测试（糖果 / 鹈鹕）接入。
//
// 安全边界：
//   - 上游地址固定；仅测试可通过 APIGX_MANXUE_BASE_URL 覆盖（生产不设置）。
//   - 公开代理只放行两条精确路径 + 白名单查询参数，并做短缓存，避免被当作匿名开放代理放大请求。
//   - 测试 ID 与发起人绑定，他人不能查询或取消（404，不区分不存在与无权）。
//   - 用户的上游 Key 只随本次请求转发给 manxue，不落库、不写日志，错误信息中脱敏。
const (
	manxueDefaultBaseURL  = "https://manxue.ai"
	manxueMaxRequestBody  = 3 << 20 // HTML 模式上限 2 MiB，另留 JSON 包装余量
	manxueMaxHTMLBytes    = 2 << 20
	manxueMaxResponseBody = 4 << 20
	manxueOwnerTTL        = 24 * time.Hour
	manxuePublicCacheTTL  = 30 * time.Second
	manxuePublicCacheMax  = 64
)

var (
	manxueHTTPClient = &http.Client{Timeout: 20 * time.Second}
	manxueTestIDRe   = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
	manxueEffortRe   = regexp.MustCompile(`^[a-z]{0,16}$`)
	manxueProtocols  = map[string]bool{"": true, "openai": true, "anthropic": true, "gemini": true}
	// 与前端实际使用的公开资源保持一致；其余一律 404。
	manxuePublicPaths = map[string]bool{"/api/public/monitors": true, "/api/public/community": true}

	manxueOwners   = map[string]manxueOwner{}
	manxueOwnersMu sync.Mutex

	manxueCache   = map[string]manxueCacheEntry{}
	manxueCacheMu sync.Mutex
)

type manxueOwner struct {
	userID    int
	expiresAt time.Time
}

type manxueCacheEntry struct {
	status    int
	body      []byte
	expiresAt time.Time
}

func manxueBaseURL() string {
	return strings.TrimRight(common.GetEnvOrDefaultString("APIGX_MANXUE_BASE_URL", manxueDefaultBaseURL), "/")
}

// RegisterManxuePublic 挂载只读公开资源。调用方负责模块访问策略（HeaderNavModuleAuth("tools")）。
func RegisterManxuePublic(r *gin.RouterGroup) {
	h := &handler{}
	r.GET("/manxue/public/*path", h.proxyManxueGET)
}

type manxueTestRequest struct {
	Benchmark       string `json:"benchmark"`
	BaseURL         string `json:"base_url,omitempty"`
	APIKey          string `json:"api_key,omitempty"`
	Model           string `json:"model,omitempty"`
	Protocol        string `json:"protocol,omitempty"`
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
	HTML            string `json:"html,omitempty"`
}

// validate 规范化并校验请求；返回面向用户的错误信息。
func (req *manxueTestRequest) validate() string {
	req.Benchmark = strings.ToLower(strings.TrimSpace(req.Benchmark))
	req.BaseURL = strings.TrimSpace(req.BaseURL)
	req.APIKey = strings.TrimSpace(req.APIKey)
	req.Model = strings.TrimSpace(req.Model)
	req.Protocol = strings.ToLower(strings.TrimSpace(req.Protocol))
	req.ReasoningEffort = strings.ToLower(strings.TrimSpace(req.ReasoningEffort))
	if req.Benchmark != "candy" && req.Benchmark != "pelican" {
		return "benchmark must be candy or pelican"
	}
	if strings.TrimSpace(req.HTML) != "" {
		if req.Benchmark != "pelican" || req.BaseURL != "" || req.APIKey != "" || req.Model != "" || req.Protocol != "" || req.ReasoningEffort != "" {
			return "HTML mode requires pelican and cannot include API fields"
		}
		if len(req.HTML) > manxueMaxHTMLBytes {
			return "html exceeds 2 MiB"
		}
		return ""
	}
	req.HTML = ""
	if req.BaseURL == "" || req.APIKey == "" {
		return "base_url and api_key are required in API mode"
	}
	if len(req.BaseURL) > 512 {
		return "base_url is too long"
	}
	parsed, err := url.Parse(req.BaseURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil {
		return "base_url must be an http(s) URL"
	}
	if len(req.APIKey) > 512 || strings.ContainsAny(req.APIKey, "\r\n") {
		return "api_key is invalid"
	}
	if len(req.Model) > 191 {
		return "model is too long"
	}
	if !manxueProtocols[req.Protocol] {
		return "protocol is not supported"
	}
	if !manxueEffortRe.MatchString(req.ReasoningEffort) {
		return "reasoning_effort is invalid"
	}
	return ""
}

func (h *handler) createManxueTest(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, manxueMaxRequestBody)
	var req manxueTestRequest
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		fail(c, http.StatusBadRequest, "invalid_body", "invalid request body")
		return
	}
	if msg := req.validate(); msg != "" {
		fail(c, http.StatusBadRequest, "invalid_argument", msg)
		return
	}
	body, status, err := manxueRequest(c.Request.Context(), http.MethodPost, "/api/v1/tests", "", req)
	if err != nil {
		writeManxueError(c, status, body, err, req.APIKey)
		return
	}
	var created struct {
		ID string `json:"id"`
	}
	if common.Unmarshal(body, &created) != nil || !manxueTestIDRe.MatchString(created.ID) {
		fail(c, http.StatusBadGateway, "manxue_invalid_response", "intelligence test service returned an unexpected response")
		return
	}
	rememberManxueOwner(created.ID, c.GetInt("id"))
	writeManxueJSON(c, status, body)
}

func (h *handler) getManxueTest(c *gin.Context) {
	h.forwardOwnedManxueTest(c, http.MethodGet)
}

func (h *handler) cancelManxueTest(c *gin.Context) {
	h.forwardOwnedManxueTest(c, http.MethodDelete)
}

func (h *handler) forwardOwnedManxueTest(c *gin.Context, method string) {
	id := strings.TrimSpace(c.Param("id"))
	if !manxueTestIDRe.MatchString(id) {
		fail(c, http.StatusBadRequest, "invalid_argument", "invalid test id")
		return
	}
	if !ownsManxueTest(id, c.GetInt("id")) {
		fail(c, http.StatusNotFound, "not_found", "resource not found")
		return
	}
	body, status, err := manxueRequest(c.Request.Context(), method, "/api/v1/tests/"+id, "", nil)
	if err != nil {
		writeManxueError(c, status, body, err, "")
		return
	}
	writeManxueJSON(c, status, body)
}

func (h *handler) proxyManxueGET(c *gin.Context) {
	path := c.Param("path")
	if !manxuePublicPaths[path] {
		fail(c, http.StatusNotFound, "not_found", "unknown public resource")
		return
	}
	query, ok := manxuePublicQuery(c.Request.URL.Query())
	if !ok {
		fail(c, http.StatusBadRequest, "invalid_argument", "unsupported query parameters")
		return
	}
	cacheKey := path + "?" + query
	if entry, hit := manxueCacheGet(cacheKey); hit {
		writeManxueJSON(c, entry.status, entry.body)
		return
	}
	body, status, err := manxueRequest(c.Request.Context(), http.MethodGet, path, query, nil)
	if err != nil {
		writeManxueError(c, status, body, err, "")
		return
	}
	manxueCachePut(cacheKey, status, body)
	writeManxueJSON(c, status, body)
}

// manxuePublicQuery 只保留已知参数并重新编码，防止任意参数透传上游。
func manxuePublicQuery(values url.Values) (string, bool) {
	out := url.Values{}
	for key, list := range values {
		if len(list) != 1 {
			return "", false
		}
		value := strings.TrimSpace(list[0])
		switch key {
		case "benchmark":
			if value != "candy" && value != "pelican" {
				return "", false
			}
		case "limit":
			n, err := strconv.Atoi(value)
			if err != nil || n < 1 || n > 50 {
				return "", false
			}
		default:
			return "", false
		}
		out.Set(key, value)
	}
	return out.Encode(), true
}

var errManxueUpstream = errors.New("manxue upstream error")

func manxueRequest(ctx context.Context, method, path, query string, payload any) ([]byte, int, error) {
	var body io.Reader
	if payload != nil {
		encoded, err := common.Marshal(payload)
		if err != nil {
			return nil, 0, err
		}
		body = bytes.NewReader(encoded)
	}
	target := manxueBaseURL() + path
	if query != "" {
		target += "?" + query
	}
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := manxueHTTPClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, manxueMaxResponseBody))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	if resp.StatusCode >= 400 {
		return data, resp.StatusCode, errManxueUpstream
	}
	// 只透传 JSON；上游若返回 HTML 错误页等，按异常处理。
	var probe any
	if common.Unmarshal(data, &probe) != nil {
		return nil, http.StatusBadGateway, errManxueUpstream
	}
	return data, resp.StatusCode, nil
}

// writeManxueError 把上游/网络错误映射为统一信封；不回显内部地址，并脱敏用户 Key。
func writeManxueError(c *gin.Context, status int, body []byte, err error, apiKey string) {
	if !errors.Is(err, errManxueUpstream) {
		common.SysError("apigx manxue: request failed: " + maskManxueText(err.Error(), apiKey))
		fail(c, http.StatusBadGateway, "manxue_unavailable", "intelligence test service is unavailable")
		return
	}
	switch {
	case status == http.StatusNotFound:
		fail(c, http.StatusNotFound, "not_found", "resource not found")
	case status == http.StatusTooManyRequests:
		fail(c, http.StatusTooManyRequests, "manxue_rate_limited", "intelligence test service is busy, try again later")
	case status >= 400 && status < 500:
		fail(c, http.StatusBadRequest, "manxue_rejected", maskManxueText(manxueErrorMessage(body), apiKey))
	default:
		fail(c, http.StatusBadGateway, "manxue_unavailable", "intelligence test service is unavailable")
	}
}

func manxueErrorMessage(body []byte) string {
	var payload struct {
		Error   any    `json:"error"`
		Message string `json:"message"`
		Detail  any    `json:"detail"`
	}
	msg := ""
	if common.Unmarshal(body, &payload) == nil {
		switch {
		case payload.Message != "":
			msg = payload.Message
		case payload.Error != nil:
			if s, ok := payload.Error.(string); ok {
				msg = s
			}
		case payload.Detail != nil:
			if s, ok := payload.Detail.(string); ok {
				msg = s
			}
		}
	}
	msg = strings.TrimSpace(msg)
	if msg == "" {
		return "intelligence test service rejected the request"
	}
	if len(msg) > 200 {
		msg = msg[:200]
	}
	return msg
}

func maskManxueText(text, apiKey string) string {
	if apiKey != "" {
		text = strings.ReplaceAll(text, apiKey, "***")
	}
	return text
}

func writeManxueJSON(c *gin.Context, status int, body []byte) {
	c.Data(status, "application/json; charset=utf-8", body)
}

func rememberManxueOwner(id string, userID int) {
	now := time.Now()
	manxueOwnersMu.Lock()
	defer manxueOwnersMu.Unlock()
	for key, owner := range manxueOwners {
		if now.After(owner.expiresAt) {
			delete(manxueOwners, key)
		}
	}
	manxueOwners[id] = manxueOwner{userID: userID, expiresAt: now.Add(manxueOwnerTTL)}
}

func ownsManxueTest(id string, userID int) bool {
	manxueOwnersMu.Lock()
	defer manxueOwnersMu.Unlock()
	owner, ok := manxueOwners[id]
	return ok && userID != 0 && owner.userID == userID && time.Now().Before(owner.expiresAt)
}

func manxueCacheGet(key string) (manxueCacheEntry, bool) {
	manxueCacheMu.Lock()
	defer manxueCacheMu.Unlock()
	entry, ok := manxueCache[key]
	if !ok || time.Now().After(entry.expiresAt) {
		return manxueCacheEntry{}, false
	}
	return entry, true
}

func manxueCachePut(key string, status int, body []byte) {
	now := time.Now()
	manxueCacheMu.Lock()
	defer manxueCacheMu.Unlock()
	if len(manxueCache) >= manxuePublicCacheMax {
		for k, entry := range manxueCache {
			if now.After(entry.expiresAt) {
				delete(manxueCache, k)
			}
		}
		if len(manxueCache) >= manxuePublicCacheMax {
			manxueCache = map[string]manxueCacheEntry{}
		}
	}
	manxueCache[key] = manxueCacheEntry{status: status, body: body, expiresAt: now.Add(manxuePublicCacheTTL)}
}
