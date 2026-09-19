package relayhook

import (
	"path/filepath"
	"testing"

	apigxmodel "github.com/QuantumNous/new-api/apigx/model"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func factByDimension(t *testing.T, facts []UsageFact, dimension int) UsageFact {
	t.Helper()
	for _, fact := range facts {
		if fact.Dimension == dimension {
			return fact
		}
	}
	t.Fatalf("missing usage fact dimension %d", dimension)
	return UsageFact{}
}

func TestSettlementUsageFactsSeparateTokensAndExcludeImageFallback(t *testing.T) {
	image := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeImagesGenerations, ImageRequestCount: 2}
	facts := settlementUsageFacts(image, &dto.Usage{PromptTokens: 1, TotalTokens: 1}, apigxmodel.UsageTrustLocalEstimated)
	assert.Len(t, facts, 2, "legacy fallback token must not become an APIGX token event")
	assert.EqualValues(t, 2_000, factByDimension(t, facts, apigxmodel.UsageDimensionImageCount).QuantityMilli)
	assert.EqualValues(t, 1_000, factByDimension(t, facts, apigxmodel.UsageDimensionRequestCount).QuantityMilli)

	image.APIGXImageUsageReported = true
	image.UpdateImageCount(3)
	usage := &dto.Usage{
		PromptTokens: 100, CompletionTokens: 30,
		PromptTokensDetails:    dto.InputTokenDetails{CachedTokens: 20, CacheWriteTokens: 10},
		CompletionTokenDetails: dto.OutputTokenDetails{ReasoningTokens: 5},
	}
	facts = settlementUsageFacts(image, usage, apigxmodel.UsageTrustUpstreamReported)
	assert.EqualValues(t, 70_000, factByDimension(t, facts, apigxmodel.UsageDimensionInputTokens).QuantityMilli)
	assert.EqualValues(t, 25_000, factByDimension(t, facts, apigxmodel.UsageDimensionOutputTokens).QuantityMilli)
	assert.EqualValues(t, 20_000, factByDimension(t, facts, apigxmodel.UsageDimensionCacheReadTokens).QuantityMilli)
	assert.EqualValues(t, 10_000, factByDimension(t, facts, apigxmodel.UsageDimensionCacheWriteTokens).QuantityMilli)
	assert.EqualValues(t, 5_000, factByDimension(t, facts, apigxmodel.UsageDimensionReasoningTokens).QuantityMilli)
	imageFact := factByDimension(t, facts, apigxmodel.UsageDimensionImageCount)
	assert.EqualValues(t, 3_000, imageFact.QuantityMilli)
	assert.Equal(t, apigxmodel.UsageTrustUpstreamReported, imageFact.Trust)

	usage.UsageSemantic = dto.BillingUsageSemanticAnthropic
	facts = settlementUsageFacts(&relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeChatCompletions}, usage, apigxmodel.UsageTrustUpstreamReported)
	assert.EqualValues(t, 100_000, factByDimension(t, facts, apigxmodel.UsageDimensionInputTokens).QuantityMilli,
		"Anthropic input tokens already exclude cache read/write tokens")
}

func TestSettlementUsageFactsPreserveAudioDurationOrUnavailable(t *testing.T) {
	recognition := &relaycommon.RelayInfo{
		RelayMode: relayconstant.RelayModeAudioTranscription, APIGXInputAudioDurationMilli: 1_250,
	}
	facts := settlementUsageFacts(recognition, nil, apigxmodel.UsageTrustLocalEstimated)
	input := factByDimension(t, facts, apigxmodel.UsageDimensionInputAudioSeconds)
	assert.EqualValues(t, 1_250, input.QuantityMilli)
	assert.Equal(t, apigxmodel.UsageTrustLocalEstimated, input.Trust)

	generation := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeAudioSpeech}
	facts = settlementUsageFacts(generation, nil, apigxmodel.UsageTrustLocalEstimated)
	output := factByDimension(t, facts, apigxmodel.UsageDimensionOutputAudioSeconds)
	assert.Zero(t, output.QuantityMilli)
	assert.Equal(t, apigxmodel.UsageTrustUnavailable, output.Trust)
}

func TestUsageCapabilitySeparatesSupportedProtocolClasses(t *testing.T) {
	tests := []struct {
		mode int
		want int
	}{
		{relayconstant.RelayModeChatCompletions, apigxmodel.CapabilityChat},
		{relayconstant.RelayModeEmbeddings, apigxmodel.CapabilityEmbedding},
		{relayconstant.RelayModeImagesEdits, apigxmodel.CapabilityImageEdit},
		{relayconstant.RelayModeAudioTranscription, apigxmodel.CapabilityAudioRecognition},
		{relayconstant.RelayModeAudioSpeech, apigxmodel.CapabilityAudioGeneration},
		{relayconstant.RelayModeRerank, apigxmodel.CapabilityRerank},
		{relayconstant.RelayModeVideoSubmit, apigxmodel.CapabilityVideo},
	}
	for _, tc := range tests {
		assert.Equal(t, tc.want, usageCapability(&relaycommon.RelayInfo{RelayMode: tc.mode}))
	}
}

func TestSettlementUsageTrustDistinguishesUpstreamAndLocalEstimates(t *testing.T) {
	c, _ := gin.CreateTestContext(nil)
	usage := &dto.Usage{PromptTokens: 1}
	assert.Equal(t, apigxmodel.UsageTrustUpstreamReported, SettlementUsageTrust(c, usage))

	common.SetContextKey(c, constant.ContextKeyLocalCountTokens, true)
	assert.Equal(t, apigxmodel.UsageTrustLocalEstimated, SettlementUsageTrust(c, usage))

	c, _ = gin.CreateTestContext(nil)
	usage.BillingUsage = &dto.BillingUsage{Estimated: true}
	assert.Equal(t, apigxmodel.UsageTrustLocalEstimated, SettlementUsageTrust(c, usage))
}

func TestRecordAsyncTaskUsageUsesFrozenPriceAndIsIdempotent(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "async-usage.db")), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, db.AutoMigrate(apigxmodel.AllModels()...))
	previous := usageDBProvider
	usageDBProvider = func() *gorm.DB { return db }
	t.Cleanup(func() { usageDBProvider = previous })

	oldRule := &apigxmodel.PriceRule{
		ScopeType: apigxmodel.PriceScopeChannel, ScopeID: 44001, Source: apigxmodel.PriceRuleSourceManual,
		Currency: "USD", ValidationState: apigxmodel.PriceValidationAccepted,
	}
	require.NoError(t, apigxmodel.CreatePriceRuleSnapshot(db, oldRule, []apigxmodel.PriceRuleItem{
		{Dimension: apigxmodel.UsageDimensionOutputVideoSeconds, UnitScale: apigxmodel.UsageUnitScalePerSecond, UnitPriceMicro: 2_000_000, Currency: "USD", UsdState: apigxmodel.UsdStateNotNeeded},
		{Dimension: apigxmodel.UsageDimensionRequestCount, UnitScale: apigxmodel.UsageUnitScalePerRequest, UnitPriceMicro: 1_000, Currency: "USD", UsdState: apigxmodel.UsdStateNotNeeded},
	}))
	task := &apigxmodel.AsyncTask{
		PublicTaskID: "apigx_44002", BaseTaskID: 44002, Status: apigxmodel.AsyncTaskStatusSucceeded,
		Capability: apigxmodel.CapabilityVideo, ChannelID: 44001, PriceRuleID: oldRule.ID, FinishAt: 1_700_000_000,
		CanonicalModelID: 44003, ApiModelID: "video-model",
	}
	require.NoError(t, db.Create(task).Error)

	newRule := &apigxmodel.PriceRule{
		ScopeType: apigxmodel.PriceScopeChannel, ScopeID: 44001, Source: apigxmodel.PriceRuleSourceModelsDev,
		Currency: "USD", ValidationState: apigxmodel.PriceValidationAccepted,
	}
	require.NoError(t, apigxmodel.CreatePriceRuleSnapshot(db, newRule, []apigxmodel.PriceRuleItem{{
		Dimension: apigxmodel.UsageDimensionOutputVideoSeconds, UnitScale: apigxmodel.UsageUnitScalePerSecond,
		UnitPriceMicro: 9_000_000, Currency: "USD", UsdState: apigxmodel.UsdStateNotNeeded,
	}}))

	require.NoError(t, RecordAsyncTaskUsage(task.BaseTaskID, map[string]any{"seconds": float64(5)}))
	require.NoError(t, RecordAsyncTaskUsage(task.BaseTaskID, map[string]any{"seconds": float64(99)}))

	var events []apigxmodel.UsageEvent
	require.NoError(t, db.Where("async_task_id = ?", task.ID).Order("dimension ASC").Find(&events).Error)
	require.Len(t, events, 2)
	video := events[0]
	if video.Dimension != apigxmodel.UsageDimensionOutputVideoSeconds {
		video = events[1]
	}
	assert.EqualValues(t, 5_000, video.QuantityMilli)
	assert.EqualValues(t, 10_000_000, video.CostUsdMicro)
	assert.Equal(t, oldRule.ID, video.PriceRuleID)
	assert.EqualValues(t, task.CanonicalModelID, video.CanonicalModelID)
	assert.Equal(t, task.ApiModelID, video.ApiModelID)
	assert.EqualValues(t, task.FinishAt, video.OccurredAt)

	var reloaded apigxmodel.AsyncTask
	require.NoError(t, db.First(&reloaded, task.ID).Error)
	assert.NotZero(t, reloaded.SettledUsageEventID)
}
