package relayprobe

import (
	"context"
	"encoding/json"
	"testing"

	apigxmodel "github.com/QuantumNous/new-api/apigx/model"
	"github.com/stretchr/testify/require"
)

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

func TestModelListResponseDecodesBoundedInventory(t *testing.T) {
	payload := []byte(`{"data":[{"id":"model-a"},{"id":"model-b"}]}`)
	var decoded modelListResponse
	require.NoError(t, json.Unmarshal(payload, &decoded))
	require.Len(t, decoded.Data, 2)
	require.Equal(t, "model-a", decoded.Data[0].ID)
}
