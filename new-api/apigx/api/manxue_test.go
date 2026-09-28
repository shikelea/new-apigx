package apigxapi

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const manxueTestKey = "sk-manxue-test-0123456789abcdef"

// fakeManxue 模拟上游：记录收到的路径/查询，按需返回错误。
type fakeManxue struct {
	publicHits atomic.Int32
	lastQuery  atomic.Value
}

func (f *fakeManxue) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/tests":
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "reject.example") {
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = w.Write([]byte(`{"message":"upstream said no for ` + manxueTestKey + `"}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"t_abc123","benchmark":"candy","status":"queued"}`))
	case r.URL.Path == "/api/v1/tests/t_abc123":
		_, _ = w.Write([]byte(`{"id":"t_abc123","benchmark":"candy","status":"running"}`))
	case r.URL.Path == "/api/public/monitors" || r.URL.Path == "/api/public/community":
		f.publicHits.Add(1)
		f.lastQuery.Store(r.URL.RawQuery)
		_, _ = w.Write([]byte(`{"items":[]}`))
	default:
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`<html>internal ` + r.URL.Path + `</html>`))
	}
}

func newManxueTestEngine(t *testing.T) (*gin.Engine, *fakeManxue) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	fake := &fakeManxue{}
	upstream := httptest.NewServer(fake)
	t.Cleanup(upstream.Close)
	t.Setenv("APIGX_MANXUE_BASE_URL", upstream.URL)

	manxueOwnersMu.Lock()
	manxueOwners = map[string]manxueOwner{}
	manxueOwnersMu.Unlock()
	manxueCacheMu.Lock()
	manxueCache = map[string]manxueCacheEntry{}
	manxueCacheMu.Unlock()

	engine := gin.New()
	api := engine.Group("/api")
	RegisterManxuePublic(api)
	authed := api.Group("/apigx/intelligence", func(c *gin.Context) {
		if id, err := strconv.Atoi(c.GetHeader("X-Test-User")); err == nil {
			c.Set("id", id)
		}
	})
	h := &handler{}
	authed.POST("/tests", h.createManxueTest)
	authed.GET("/tests/:id", h.getManxueTest)
	authed.DELETE("/tests/:id", h.cancelManxueTest)
	return engine, fake
}

func manxueDo(engine *gin.Engine, method, target, user, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if user != "" {
		req.Header.Set("X-Test-User", user)
	}
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	return rec
}

func TestManxueTestIsBoundToCreator(t *testing.T) {
	engine, _ := newManxueTestEngine(t)
	body := `{"benchmark":"candy","base_url":"https://relay.example/v1","api_key":"` + manxueTestKey + `"}`

	created := manxueDo(engine, http.MethodPost, "/api/apigx/intelligence/tests", "7", body)
	require.Equal(t, http.StatusOK, created.Code, created.Body.String())
	assert.Contains(t, created.Body.String(), `"t_abc123"`)

	assert.Equal(t, http.StatusOK, manxueDo(engine, http.MethodGet, "/api/apigx/intelligence/tests/t_abc123", "7", "").Code)
	// 其他用户既不能查询也不能取消，且与“不存在”不可区分。
	assert.Equal(t, http.StatusNotFound, manxueDo(engine, http.MethodGet, "/api/apigx/intelligence/tests/t_abc123", "8", "").Code)
	assert.Equal(t, http.StatusNotFound, manxueDo(engine, http.MethodDelete, "/api/apigx/intelligence/tests/t_abc123", "8", "").Code)
	// 编码后的 “/” 被路由层拒绝（404）；能进处理器的非法 ID 则被格式校验拒绝。
	assert.Equal(t, http.StatusNotFound, manxueDo(engine, http.MethodGet, "/api/apigx/intelligence/tests/..%2Fadmin", "7", "").Code)
	assert.Equal(t, http.StatusBadRequest, manxueDo(engine, http.MethodGet, "/api/apigx/intelligence/tests/bad.id", "7", "").Code)
}

func TestManxueCreateValidatesInput(t *testing.T) {
	engine, _ := newManxueTestEngine(t)
	cases := map[string]string{
		"unknown benchmark": `{"benchmark":"iq","base_url":"https://a.example","api_key":"k"}`,
		"missing key":       `{"benchmark":"candy","base_url":"https://a.example"}`,
		"non-http url":      `{"benchmark":"candy","base_url":"file:///etc/passwd","api_key":"k"}`,
		"userinfo url":      `{"benchmark":"candy","base_url":"https://u:p@a.example","api_key":"k"}`,
		"html with api":     `{"benchmark":"pelican","html":"<p>x</p>","api_key":"k"}`,
		"html on candy":     `{"benchmark":"candy","html":"<p>x</p>"}`,
		"bad protocol":      `{"benchmark":"candy","base_url":"https://a.example","api_key":"k","protocol":"ftp"}`,
		"html too large":    `{"benchmark":"pelican","html":"` + strings.Repeat("a", manxueMaxHTMLBytes+1) + `"}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			rec := manxueDo(engine, http.MethodPost, "/api/apigx/intelligence/tests", "7", body)
			assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
		})
	}
}

func TestManxueUpstreamErrorsAreMaskedAndMapped(t *testing.T) {
	engine, _ := newManxueTestEngine(t)
	body := `{"benchmark":"candy","base_url":"https://reject.example/v1","api_key":"` + manxueTestKey + `"}`

	rec := manxueDo(engine, http.MethodPost, "/api/apigx/intelligence/tests", "7", body)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "upstream said no")
	assert.NotContains(t, rec.Body.String(), manxueTestKey)
}

func TestManxuePublicProxyOnlyAllowsWhitelistedResources(t *testing.T) {
	engine, fake := newManxueTestEngine(t)

	rec := manxueDo(engine, http.MethodGet, "/api/manxue/public/api/public/community?limit=20&benchmark=candy", "", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "benchmark=candy&limit=20", fake.lastQuery.Load())

	// 同一资源 30s 内命中缓存，不重复打上游。
	require.Equal(t, http.StatusOK, manxueDo(engine, http.MethodGet, "/api/manxue/public/api/public/community?benchmark=candy&limit=20", "", "").Code)
	assert.Equal(t, int32(1), fake.publicHits.Load())

	blocked := map[string]int{
		"/api/manxue/public/api/v1/tests":                                 http.StatusNotFound,
		"/api/manxue/public/api/public/community/../../api/v1/tests":      http.StatusNotFound,
		"/api/manxue/public/api/public/community/%2e%2e/%2e%2e/api/v1/x":  http.StatusNotFound,
		"/api/manxue/public/api/public/community?admin=1":                 http.StatusBadRequest,
		"/api/manxue/public/api/public/community?limit=1000":              http.StatusBadRequest,
		"/api/manxue/public/api/public/community?benchmark=a&benchmark=b": http.StatusBadRequest,
	}
	for target, want := range blocked {
		assert.Equal(t, want, manxueDo(engine, http.MethodGet, target, "", "").Code, target)
	}
	assert.Equal(t, int32(1), fake.publicHits.Load())
}
