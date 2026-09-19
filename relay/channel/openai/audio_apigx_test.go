package openai

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestOpenaiTTSHandlerPreservesMeasuredDurationForAPIGX(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/audio/speech", nil)

	// OpenAI PCM is 24 kHz, signed 16-bit mono: 48,000 bytes is one second.
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"audio/pcm"}},
		Body:       io.NopCloser(bytes.NewReader(make([]byte, 48_000))),
	}
	info := &relaycommon.RelayInfo{Request: &dto.AudioRequest{ResponseFormat: "pcm"}}
	usage := OpenaiTTSHandler(c, resp, info)

	assert.EqualValues(t, 1_000, info.APIGXOutputAudioDurationMilli)
	assert.Positive(t, usage.CompletionTokenDetails.AudioTokens)
}
