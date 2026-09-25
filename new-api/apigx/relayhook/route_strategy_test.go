package relayhook

import (
	"net/http/httptest"
	"testing"

	apigxmodel "github.com/QuantumNous/new-api/apigx/model"
	"github.com/QuantumNous/new-api/apigx/routing"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	basemodel "github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseRouteStrategyTreatsEmptyAndAutoAsCompositeAuto(t *testing.T) {
	for _, mode := range []string{"", "auto", " AUTO "} {
		strategy, ok := parseRouteStrategy(mode)
		assert.True(t, ok, "mode=%q", mode)
		assert.Equal(t, routing.StrategyAuto, strategy, "mode=%q", mode)
	}
	_, ok := parseRouteStrategy("typo")
	assert.False(t, ok)
}

func TestComputeEligibleBaseChannelsUsesAutoQualityRanking(t *testing.T) {
	env := newBreakerTestEnv(t)
	require.NoError(t, env.db.Model(&apigxmodel.ModelOffering{}).Where("id = ?", env.offeringID).
		Update("status", apigxmodel.OfferingStatusPublished).Error)
	require.NoError(t, env.db.Model(&apigxmodel.ShareGroup{}).Where("id = ?", env.shareChannel.ShareGroupID).
		Update("publish_status", apigxmodel.ShareGroupPublishPublished).Error)

	secondBaseID := env.baseChannelID + 10000
	priority := int64(0)
	weight := uint(0)
	require.NoError(t, env.db.Create(&basemodel.Channel{
		Id: secondBaseID, Name: "strategy-base-2", Key: "sk-y", Status: common.ChannelStatusEnabled,
		Priority: &priority, Weight: &weight,
	}).Error)
	secondGroup := &apigxmodel.ShareGroup{
		OwnerUserID: 991002, Name: "strategy-group-2", PublicID: "strategy-group-2",
		PublishStatus: apigxmodel.ShareGroupPublishPublished,
	}
	require.NoError(t, apigxmodel.CreateShareGroup(env.db, secondGroup))
	secondChannel := &apigxmodel.Channel{
		PublicID: "strategy-channel-2", OwnerUserID: 991002, ShareGroupID: secondGroup.ID,
		Name: "strategy-share-2", BaseChannelID: int64(secondBaseID),
	}
	require.NoError(t, apigxmodel.CreateChannel(env.db, secondChannel))
	secondOffering := &apigxmodel.ModelOffering{ChannelID: secondChannel.ID, UpstreamModelID: "breaker-model"}
	require.NoError(t, apigxmodel.CreateModelOffering(env.db, 991002, secondOffering))
	require.NoError(t, env.db.Model(&apigxmodel.ModelOffering{}).Where("id = ?", secondOffering.ID).
		Update("status", apigxmodel.OfferingStatusPublished).Error)

	now := common.GetTimestamp()
	bucketTs := now - now%60
	for range apigxmodel.MinSamplesForQuality {
		require.NoError(t, apigxmodel.RecordQualitySample(env.db, apigxmodel.QualitySample{
			OfferingID: env.offeringID, BucketTs: bucketTs, Outcome: apigxmodel.QualityOutcomeSuccess,
			LatencyMs: 100, OutputTokens: 120, GenerationMs: 1000,
		}))
		require.NoError(t, apigxmodel.RecordQualitySample(env.db, apigxmodel.QualitySample{
			OfferingID: secondOffering.ID, BucketTs: bucketTs, Outcome: apigxmodel.QualityOutcomeSuccess,
			LatencyMs: 900, OutputTokens: 20, GenerationMs: 1000,
		}))
	}

	allowed := map[int64]struct{}{env.offeringID: {}, secondOffering.ID: {}}
	assert.Equal(t, []int{env.baseChannelID},
		computeEligibleBaseChannels(env.db, "breaker-model", routing.StrategyAuto, allowed))
	assert.Equal(t, []int{env.baseChannelID},
		computeEligibleBaseChannels(env.db, "breaker-model", routing.StrategyThroughput, allowed))
	primary, fallback := computeEligibleBaseChannelsWithFallback(env.db, "breaker-model", routing.StrategyAuto, allowed)
	assert.Equal(t, []int{env.baseChannelID}, primary)
	assert.Equal(t, []int{int(secondBaseID)}, fallback,
		"lower-ranked mature channels remain available as fallback after the top channel is exhausted")
	assert.Empty(t, computeEligibleBaseChannels(env.db, "breaker-model", routing.StrategyAuto, map[int64]struct{}{}),
		"没有授权供给时必须保持空集合，不能回退到底座随机")
}

func TestComputeEligibleBaseChannelsPersistsOpenBreakerBeforeNativeSelection(t *testing.T) {
	env := newBreakerTestEnv(t)
	t.Setenv("APIGX_BREAKER_ENABLED", "true")
	require.NoError(t, env.db.Model(&apigxmodel.ModelOffering{}).Where("id = ?", env.offeringID).
		Update("status", apigxmodel.OfferingStatusPublished).Error)
	require.NoError(t, env.db.Model(&apigxmodel.ShareGroup{}).Where("id = ?", env.shareChannel.ShareGroupID).
		Update("publish_status", apigxmodel.ShareGroupPublishPublished).Error)
	env.recordFailures(t, 20)

	allowed := map[int64]struct{}{env.offeringID: {}}
	assert.Empty(t, computeEligibleBaseChannels(
		env.db, "breaker-model", routing.StrategyAuto, allowed,
	))

	var state apigxmodel.BreakerProbeState
	require.NoError(t, env.db.Where("offering_id = ?", env.offeringID).First(&state).Error)
	assert.Equal(t, apigxmodel.BreakerProbeStateOpen, state.State)
	assert.EqualValues(t, env.baseChannelID, state.BaseChannelID)

	var base basemodel.Channel
	require.NoError(t, env.db.Where("id = ?", env.baseChannelID).First(&base).Error)
	assert.Equal(t, common.ChannelStatusAutoDisabled, base.Status)
}

func TestExpandRouteStrategyFallbackOnlyAfterPrimaryExhausted(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	state := &RouteStrategyState{PrimaryChannelIDs: []int{35}, FallbackChannelIDs: []int{41}}
	common.SetContextKey(c, constant.ContextKeyRouteStrategyState, state)
	constraints := &dto.ChannelConstraints{Filters: []dto.ChannelFilter{{
		Kind: dto.FilterAllowedChannelIds, AllowedChannelIds: []int{35},
	}}}
	common.SetContextKey(c, constant.ContextKeyChannelConstraints, constraints)
	assert.False(t, ExpandRouteStrategyFallback(c), "a healthy primary must remain preferred")
	constraints.AddFilter(dto.ChannelFilter{Kind: dto.FilterDeniedChannelIds, DeniedChannelIds: []int{35}})
	assert.True(t, ExpandRouteStrategyFallback(c), "all primary channels exhausted should activate fallback")
	assert.Equal(t, []int{35, 41}, constraints.Filters[0].AllowedChannelIds)
	assert.False(t, ExpandRouteStrategyFallback(c), "fallback expansion is idempotent")
}
