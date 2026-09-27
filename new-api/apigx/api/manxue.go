package apigxapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

const manxueBaseURL = "https://manxue.ai"

var manxueHTTPClient = &http.Client{Timeout: 20 * time.Second}

type manxueTestRequest struct {
	Benchmark       string `json:"benchmark"`
	BaseURL         string `json:"base_url,omitempty"`
	APIKey          string `json:"api_key,omitempty"`
	Model           string `json:"model,omitempty"`
	Protocol        string `json:"protocol,omitempty"`
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
	HTML            string `json:"html,omitempty"`
}

func (h *handler) createManxueTest(c *gin.Context) {
	var req manxueTestRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "invalid_body", "invalid request body")
		return
	}
	req.Benchmark = strings.ToLower(strings.TrimSpace(req.Benchmark))
	req.BaseURL, req.APIKey, req.Model, req.Protocol, req.ReasoningEffort = strings.TrimSpace(req.BaseURL), strings.TrimSpace(req.APIKey), strings.TrimSpace(req.Model), strings.TrimSpace(req.Protocol), strings.TrimSpace(req.ReasoningEffort)
	if req.Benchmark != "candy" && req.Benchmark != "pelican" {
		fail(c, http.StatusBadRequest, "invalid_argument", "benchmark must be candy or pelican")
		return
	}
	if req.HTML != "" {
		if req.Benchmark != "pelican" || len(req.HTML) > 2<<20 || req.BaseURL != "" || req.APIKey != "" || req.Model != "" || req.Protocol != "" || req.ReasoningEffort != "" {
			fail(c, http.StatusBadRequest, "invalid_argument", "HTML mode requires pelican and cannot include API fields")
			return
		}
	} else if req.BaseURL == "" || req.APIKey == "" {
		fail(c, http.StatusBadRequest, "invalid_argument", "base_url and api_key are required in API mode")
		return
	}
	body, status, err := manxueRequest(c.Request.Context(), http.MethodPost, "/api/v1/tests", "", req)
	if err != nil {
		fail(c, http.StatusBadGateway, "manxue_unavailable", err.Error())
		return
	}
	writeManxueJSON(c, status, body)
}

func (h *handler) getManxueTest(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	if id == "" || strings.ContainsAny(id, "/\\?") {
		fail(c, http.StatusBadRequest, "invalid_argument", "invalid test id")
		return
	}
	body, status, err := manxueRequest(c.Request.Context(), http.MethodGet, "/api/v1/tests/"+url.PathEscape(id), "", nil)
	if err != nil {
		fail(c, http.StatusBadGateway, "manxue_unavailable", err.Error())
		return
	}
	writeManxueJSON(c, status, body)
}

func (h *handler) cancelManxueTest(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	if id == "" || strings.ContainsAny(id, "/\\?") {
		fail(c, http.StatusBadRequest, "invalid_argument", "invalid test id")
		return
	}
	body, status, err := manxueRequest(c.Request.Context(), http.MethodDelete, "/api/v1/tests/"+url.PathEscape(id), "", nil)
	if err != nil {
		fail(c, http.StatusBadGateway, "manxue_unavailable", err.Error())
		return
	}
	writeManxueJSON(c, status, body)
}

func (h *handler) proxyManxueGET(c *gin.Context) {
	path := c.Param("path")
	if path != "/api/public/monitors" && path != "/api/public/community" && !strings.HasPrefix(path, "/api/public/community/") && !strings.HasPrefix(path, "/api/public/monitors/pelican/") {
		fail(c, http.StatusNotFound, "not_found", "unknown public resource")
		return
	}
	body, status, err := manxueRequest(c.Request.Context(), http.MethodGet, path, c.Request.URL.RawQuery, nil)
	if err != nil {
		fail(c, http.StatusBadGateway, "manxue_unavailable", err.Error())
		return
	}
	writeManxueJSON(c, status, body)
}

func manxueRequest(ctx context.Context, method, path, query string, payload any) ([]byte, int, error) {
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return nil, 0, err
		}
		body = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, manxueBaseURL+path+func() string {
		if query != "" {
			return "?" + query
		}
		return ""
	}(), body)
	if err != nil {
		return nil, 0, err
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := manxueHTTPClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	if resp.StatusCode >= 400 {
		return data, resp.StatusCode, fmt.Errorf("manxue returned HTTP %s", strconv.Itoa(resp.StatusCode))
	}
	return data, resp.StatusCode, nil
}

func writeManxueJSON(c *gin.Context, status int, body []byte) {
	c.Data(status, "application/json; charset=utf-8", body)
}
