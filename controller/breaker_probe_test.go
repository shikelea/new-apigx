package controller

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	apigxmodel "github.com/QuantumNous/new-api/apigx/model"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBreakerProbeRequiresTwoSuccessesAndRecordsProbeOnly(t *testing.T) {
	var requests atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl-probe","object":"chat.completion","created":1,"model":"g2-failover-model","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	t.Cleanup(upstream.Close)

	db := setupRelayMultiKeyDatabase(t)
	require.NoError(t, apigxmodel.Migrate(db))
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", 1).Update("role", common.RoleRootUser).Error)

	base := relayMultiKeyChannel(88_101, "breaker-probe", upstream.URL, "probe-key", []string{"probe-stable"})
	base.ChannelInfo.ReplaceMultiKeyStatusMarks(apigxmodel.StatusBreakerOpen, []int{0})
	base.Status = common.ChannelStatusAutoDisabled
	require.NoError(t, db.Create(base).Error)
	ability := relayMultiKeyAbility(base.Id)
	ability.Enabled = false
	require.NoError(t, db.Create(ability).Error)
	model.InitChannelCache()

	group := &apigxmodel.ShareGroup{OwnerUserID: 1, Name: "probe", PublicID: "probe-controller-group"}
	require.NoError(t, apigxmodel.CreateShareGroup(db, group))
	shareChannel := &apigxmodel.Channel{
		OwnerUserID: 1, ShareGroupID: group.ID, Name: "probe", PublicID: "probe-controller-channel",
		BaseChannelID: int64(base.Id),
	}
	require.NoError(t, apigxmodel.CreateChannel(db, shareChannel))
	canonical := &apigxmodel.CanonicalModel{
		APIModelID: "g2-failover-model", ModalityType: apigxmodel.ModalityChat, Status: 1,
	}
	require.NoError(t, db.Create(canonical).Error)
	offering := &apigxmodel.ModelOffering{
		ChannelID: shareChannel.ID, CanonicalModelID: canonical.ID, UpstreamModelID: "g2-failover-model",
	}
	require.NoError(t, apigxmodel.CreateModelOffering(db, 1, offering))

	now := common.GetTimestamp()
	require.NoError(t, apigxmodel.OpenBreakerProbe(db, offering.ID, int64(base.Id), now, 60))
	require.NoError(t, db.Model(&apigxmodel.BreakerProbeState{}).
		Where("offering_id = ?", offering.ID).Update("next_probe_at", now).Error)

	first, err := runBreakerProbeTask(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, first.Succeeded)
	assert.Zero(t, first.Recovered)
	assert.EqualValues(t, 1, requests.Load())
	stored, err := model.GetChannelById(base.Id, true)
	require.NoError(t, err)
	assert.Equal(t, common.ChannelStatusAutoDisabled, stored.Status)

	require.NoError(t, db.Model(&apigxmodel.BreakerProbeState{}).
		Where("offering_id = ?", offering.ID).Update("next_probe_at", common.GetTimestamp()).Error)
	second, err := runBreakerProbeTask(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, second.Succeeded)
	assert.Equal(t, 1, second.Recovered)
	assert.EqualValues(t, 2, requests.Load())

	require.NoError(t, db.Where("offering_id = ?", offering.ID).First(&apigxmodel.BreakerProbeState{}).Error)
	stored, err = model.GetChannelById(base.Id, true)
	require.NoError(t, err)
	assert.Equal(t, common.ChannelStatusEnabled, stored.Status)
	assert.False(t, stored.ChannelInfo.HasMultiKeyStatusMark(apigxmodel.StatusBreakerOpen))

	var bucket apigxmodel.QualityBucket
	require.NoError(t, db.Where("offering_id = ?", offering.ID).First(&bucket).Error)
	assert.EqualValues(t, 2, bucket.ProbeCount)
	assert.Zero(t, bucket.NaturalRequestCount)
	assert.Zero(t, bucket.CompleteSuccessCount)
}
