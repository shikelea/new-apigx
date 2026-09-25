package service

import (
	"bytes"
	"encoding/binary"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	kittypes "github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func pcmWAVFixture(t *testing.T, sampleRate, seconds int) []byte {
	t.Helper()
	dataSize := sampleRate * seconds * 2 // mono, signed 16-bit PCM
	var out bytes.Buffer
	out.WriteString("RIFF")
	require.NoError(t, binary.Write(&out, binary.LittleEndian, uint32(36+dataSize)))
	out.WriteString("WAVEfmt ")
	require.NoError(t, binary.Write(&out, binary.LittleEndian, uint32(16)))
	require.NoError(t, binary.Write(&out, binary.LittleEndian, uint16(1)))
	require.NoError(t, binary.Write(&out, binary.LittleEndian, uint16(1)))
	require.NoError(t, binary.Write(&out, binary.LittleEndian, uint32(sampleRate)))
	require.NoError(t, binary.Write(&out, binary.LittleEndian, uint32(sampleRate*2)))
	require.NoError(t, binary.Write(&out, binary.LittleEndian, uint16(2)))
	require.NoError(t, binary.Write(&out, binary.LittleEndian, uint16(16)))
	out.WriteString("data")
	require.NoError(t, binary.Write(&out, binary.LittleEndian, uint32(dataSize)))
	out.Write(make([]byte, dataSize))
	return out.Bytes()
}

func TestCountRequestTokenPreservesSTTInputDurationForAPIGX(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "one-second.wav")
	require.NoError(t, err)
	_, err = part.Write(pcmWAVFixture(t, 8_000, 1))
	require.NoError(t, err)
	require.NoError(t, writer.WriteField("model", "whisper-1"))
	require.NoError(t, writer.Close())

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", bytes.NewReader(body.Bytes()))
	c.Request.Header.Set("Content-Type", writer.FormDataContentType())
	info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeAudioTranscription}

	tokens, err := CountRequestToken(c, &kittypes.TokenCountMeta{}, info)
	require.NoError(t, err)
	assert.Positive(t, tokens)
	assert.EqualValues(t, 1_000, info.APIGXInputAudioDurationMilli)
}
