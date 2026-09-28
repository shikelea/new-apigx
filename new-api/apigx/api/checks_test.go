package apigxapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	apigxmodel "github.com/QuantumNous/new-api/apigx/model"
	"github.com/QuantumNous/new-api/apigx/secretbox"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 持续检测设置与众测接口的测试。众测刻意不与中转绑定，所以这里重点验两件事：
// 响应里既没有被测地址也没有凭据，且他人无法改动或复跑别人的记录。

func newProbeContext(userID int64, body string) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	if userID > 0 {
		c.Set("id", int(userID))
	}
	return c, rec
}

func TestCheckSettingsApplyOnlyTouchesGivenFields(t *testing.T) {
	config := defaultCheckConfig(7, 9)
	require.Equal(t, apigxmodel.CheckVisibilityPublic, config.AvailabilityMode)
	// 降智与掺水默认关闭：它们要花上游额度，不能在主人没点头时自己跑起来。
	require.Equal(t, apigxmodel.CheckVisibilityOff, config.DegradeMode)
	require.Equal(t, apigxmodel.CheckVisibilityOff, config.DilutionMode)

	mode, interval := apigxmodel.CheckVisibilityOwner, apigxmodel.CheckInterval15Min
	req := checkSettingsRequest{DegradeMode: &mode, DegradeInterval: &interval, DegradeModels: []string{"gpt-4o", " gpt-4o ", ""}}
	require.NoError(t, req.applyTo(config))
	assert.Equal(t, apigxmodel.CheckVisibilityOwner, config.DegradeMode)
	assert.Equal(t, apigxmodel.CheckInterval15Min, config.DegradeInterval)
	assert.Equal(t, []string{"gpt-4o"}, config.Models(apigxmodel.ProbeKindDegrade), "model list must be trimmed and deduped")
	// 没给的字段保持原值，所以同一个结构能用于「只改一项」。
	assert.Equal(t, apigxmodel.CheckVisibilityPublic, config.AvailabilityMode)
	assert.Equal(t, apigxmodel.CheckInterval30Min, config.AvailabilityInterval)
}

func TestCheckSettingsRejectNonTierIntervalAndEmptyModelList(t *testing.T) {
	config := defaultCheckConfig(7, 9)
	bad := 777
	req := checkSettingsRequest{DegradeInterval: &bad}
	require.NoError(t, req.applyTo(config), "applyTo only assigns; validation happens on upsert")
	assert.ErrorIs(t, config.Validate(), apigxmodel.ErrInvalidArgument)

	config = defaultCheckConfig(7, 9)
	on := apigxmodel.CheckVisibilityPublic
	require.NoError(t, checkSettingsRequest{DilutionMode: &on}.applyTo(config))
	// 开着掺水检测却没选模型是无意义的配置，必须拦下来。
	assert.ErrorIs(t, config.Validate(), apigxmodel.ErrInvalidArgument)
}

func TestCheckSettingsRejectTooManyModels(t *testing.T) {
	config := defaultCheckConfig(7, 9)
	models := make([]string, 0, 12)
	for i := range 12 {
		models = append(models, string(rune('a'+i))+"-model")
	}
	assert.ErrorIs(t, checkSettingsRequest{DegradeModels: models}.applyTo(config), apigxmodel.ErrInvalidArgument)
}

func TestCrowdtestJSONNeverExposesAddressOrCredential(t *testing.T) {
	test := apigxmodel.Crowdtest{
		PublicID: "ct-1", SubmitterUserID: 7, Benchmark: apigxmodel.BenchmarkPelican, Model: "gpt-4o",
		Status: apigxmodel.CrowdtestStatusScored, Score: 90, Evidence: "结构规则 90/100",
		Artifact: `<svg viewBox="0 0 1 1"><circle r="1"/></svg>`, Visibility: apigxmodel.CrowdtestVisibilityPublic,
		PublishedAt: 1700000000,
	}
	visitor := crowdtestJSON(test, false)
	// 公开视图不带地址、不带凭据，也不带可见性——别人无从照着复跑。
	for _, key := range []string{"base_url", "key", "relay_share_id", "visibility", "protocol"} {
		assert.NotContains(t, visitor, key)
	}
	assert.Equal(t, true, visitor["published"])
	assert.Contains(t, visitor["artifact"], "<circle")

	owner := crowdtestJSON(test, true)
	assert.Equal(t, apigxmodel.CrowdtestVisibilityPublic, owner["visibility"])
}

func TestResolveProbeTargetRequiresModelAndCredential(t *testing.T) {
	env := newTestEnv(t, true)
	h := &handler{db: env.db}

	c, _ := newProbeContext(7, "{}")
	_, okTarget := h.resolveProbeTarget(c, 7, probeTargetRequest{BaseURL: "https://relay.example", Key: "sk-x"})
	assert.False(t, okTarget, "model is required")

	c, _ = newProbeContext(7, "{}")
	_, okTarget = h.resolveProbeTarget(c, 7, probeTargetRequest{Model: "gpt-4o"})
	assert.False(t, okTarget, "either a relay share or base URL with key is required")

	c, _ = newProbeContext(7, "{}")
	_, okTarget = h.resolveProbeTarget(c, 7, probeTargetRequest{Model: strings.Repeat("m", checkModelMaxRunes+1), BaseURL: "https://relay.example", Key: "sk-x"})
	assert.False(t, okTarget, "over-long model must be rejected before any request")

	c, _ = newProbeContext(7, "{}")
	target, okTarget := h.resolveProbeTarget(c, 7, probeTargetRequest{Model: " gpt-4o ", BaseURL: " https://relay.example ", Key: " sk-plain "})
	require.True(t, okTarget)
	assert.Equal(t, "gpt-4o", target.model)
	assert.Equal(t, "sk-plain", target.key)
	assert.Zero(t, target.shareID, "an ad-hoc target is not attached to any share")
}

func TestResolveProbeTargetRefusesAnotherOwnersRelay(t *testing.T) {
	env := newTestEnv(t, true)
	h := &handler{db: env.db}
	encrypted, err := secretbox.Encrypt("sk-owner-secret")
	require.NoError(t, err)
	fingerprint, err := secretbox.Fingerprint("sk-owner-secret")
	require.NoError(t, err)

	share := &apigxmodel.RelayShare{OwnerUserID: 7, Title: "mine", BaseURL: "https://relay.example", Kind: apigxmodel.RelayKindCharity, Protocol: "new-api", Status: apigxmodel.RelayStatusPublished}
	require.NoError(t, apigxmodel.CreateRelayShare(env.db, share))
	require.NoError(t, apigxmodel.CreateRelayCredential(env.db, &apigxmodel.RelayCredential{
		OwnerUserID: 7, RelayShareID: share.ID, SecretRef: encrypted, Fingerprint: fingerprint, KeySuffix: "cret",
	}))

	c, _ := newProbeContext(7, "{}")
	target, okTarget := h.resolveProbeTarget(c, 7, probeTargetRequest{RelaySharePublicID: share.PublicID, Model: "gpt-4o"})
	require.True(t, okTarget)
	assert.Equal(t, "sk-owner-secret", target.key, "the stored credential is decrypted in memory only")
	assert.Equal(t, share.ID, target.shareID)

	// 陌生人拿着别人的 publicID 来测，会烧掉中转主人的额度，必须 404。
	c, rec := newProbeContext(8, "{}")
	_, okTarget = h.resolveProbeTarget(c, 8, probeTargetRequest{RelaySharePublicID: share.PublicID, Model: "gpt-4o"})
	assert.False(t, okTarget)
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.NotContains(t, rec.Body.String(), "sk-owner-secret")
}

func TestTruncateRunesKeepsMultibyteCharactersIntact(t *testing.T) {
	trimmed := truncateRunes(strings.Repeat("降智", 400), 460)
	assert.Equal(t, 461, len([]rune(trimmed)))
	assert.True(t, strings.HasSuffix(trimmed, "…"))
	assert.Equal(t, "短", truncateRunes("  短  ", 460))
}
