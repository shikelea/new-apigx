package apigxmodel

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveUsageCostAcrossSupportedUnits(t *testing.T) {
	db := newRepoDB(t)
	rule := &PriceRule{
		ScopeType: PriceScopeChannel, ScopeID: 88001, Source: PriceRuleSourceManual,
		Currency: "USD", ValidationState: PriceValidationAccepted,
	}
	items := []PriceRuleItem{
		{Dimension: UsageDimensionInputTokens, UnitScale: UsageUnitScalePerMillionTokens, UnitPriceMicro: 2_000_000, Currency: "USD", UsdState: UsdStateNotNeeded},
		{Dimension: UsageDimensionOutputTokens, UnitScale: UsageUnitScalePerThousandTokens, UnitPriceMicro: 1_000, Currency: "USD", UsdState: UsdStateNotNeeded},
		{Dimension: UsageDimensionImageCount, UnitScale: UsageUnitScalePerImage, UnitPriceMicro: 40_000, Currency: "USD", UsdState: UsdStateNotNeeded},
		{Dimension: UsageDimensionInputAudioSeconds, UnitScale: UsageUnitScalePerSecond, UnitPriceMicro: 2_000, Currency: "USD", UsdState: UsdStateNotNeeded},
		{Dimension: UsageDimensionOutputAudioSeconds, UnitScale: UsageUnitScalePerMinute, UnitPriceMicro: 600_000, Currency: "USD", UsdState: UsdStateNotNeeded},
		{Dimension: UsageDimensionRequestCount, UnitScale: UsageUnitScalePerRequest, UnitPriceMicro: 5_000, Currency: "USD", UsdState: UsdStateNotNeeded},
		{Dimension: UsageDimensionInputCharacters, UnitScale: UsageUnitScalePerThousandCharacters, UnitPriceMicro: 1_000, Currency: "USD", UsdState: UsdStateNotNeeded},
		{Dimension: UsageDimensionDocumentCount, UnitScale: UsageUnitScalePerDocument, UnitPriceMicro: 3_000, Currency: "USD", UsdState: UsdStateNotNeeded},
	}
	require.NoError(t, CreatePriceRuleSnapshot(db, rule, items))

	tests := []struct {
		name          string
		dimension     int
		quantityMilli int64
		factUnit      int
		wantCost      int64
	}{
		{name: "per million tokens", dimension: UsageDimensionInputTokens, quantityMilli: UsageQuantityMilliFromCount(500_000), factUnit: UsageUnitScalePerMillionTokens, wantCost: 1_000_000},
		{name: "per thousand tokens", dimension: UsageDimensionOutputTokens, quantityMilli: UsageQuantityMilliFromCount(2_500), factUnit: UsageUnitScalePerMillionTokens, wantCost: 2_500},
		{name: "per image", dimension: UsageDimensionImageCount, quantityMilli: UsageQuantityMilliFromCount(3), factUnit: UsageUnitScalePerImage, wantCost: 120_000},
		{name: "per second", dimension: UsageDimensionInputAudioSeconds, quantityMilli: 1_500, factUnit: UsageUnitScalePerSecond, wantCost: 3_000},
		{name: "per minute", dimension: UsageDimensionOutputAudioSeconds, quantityMilli: 90_000, factUnit: UsageUnitScalePerSecond, wantCost: 900_000},
		{name: "per request", dimension: UsageDimensionRequestCount, quantityMilli: UsageQuantityMilliFromCount(2), factUnit: UsageUnitScalePerRequest, wantCost: 10_000},
		{name: "per thousand characters", dimension: UsageDimensionInputCharacters, quantityMilli: UsageQuantityMilliFromCount(1_500), factUnit: UsageUnitScalePerThousandCharacters, wantCost: 1_500},
		{name: "per document", dimension: UsageDimensionDocumentCount, quantityMilli: UsageQuantityMilliFromCount(4), factUnit: UsageUnitScalePerDocument, wantCost: 12_000},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ResolveUsageCostFromPriceRule(db, rule.ID, tc.dimension, tc.quantityMilli, tc.factUnit)
			assert.Equal(t, UsageCostStateReferenceEstimated, got.CostState)
			assert.Equal(t, tc.wantCost, got.CostMicro)
			assert.Equal(t, tc.wantCost, got.CostUsdMicro)
			assert.Equal(t, rule.ID, got.PriceRuleID)
		})
	}
}

func TestResolveUsageCostPreservesUnknownZeroAndOverflowStates(t *testing.T) {
	db := newRepoDB(t)

	pending := &PriceRule{
		ScopeType: PriceScopeChannel, ScopeID: 88002, Source: PriceRuleSourceManual,
		Currency: "CNY", ValidationState: PriceValidationAccepted,
	}
	require.NoError(t, CreatePriceRuleSnapshot(db, pending, []PriceRuleItem{{
		Dimension: UsageDimensionOutputAudioSeconds, UnitScale: UsageUnitScalePerMinute,
		UnitPriceMicro: 6_000_000, Currency: "CNY", UsdState: UsdStatePendingFx,
	}}))
	got := ResolveUsageCostFromPriceRule(db, pending.ID, UsageDimensionOutputAudioSeconds, 30_000, UsageUnitScalePerSecond)
	assert.Equal(t, UsageCostStatePendingFx, got.CostState)
	assert.EqualValues(t, 3_000_000, got.CostMicro)
	assert.Zero(t, got.CostUsdMicro)

	zero := &PriceRule{
		ScopeType: PriceScopeChannel, ScopeID: 88003, Source: PriceRuleSourceManual,
		Currency: "USD", ValidationState: PriceValidationAccepted,
	}
	require.NoError(t, CreatePriceRuleSnapshot(db, zero, []PriceRuleItem{{
		Dimension: UsageDimensionImageCount, UnitScale: UsageUnitScalePerImage,
		UnitPriceMicro: 0, Currency: "USD", UsdState: UsdStateNotNeeded,
	}}))
	got = ResolveUsageCostFromPriceRule(db, zero.ID, UsageDimensionImageCount, 1_000, UsageUnitScalePerImage)
	assert.Equal(t, UsageCostStateExplicitZero, got.CostState)
	assert.NotZero(t, got.PriceRuleItemID)
	assert.Zero(t, got.CostUsdMicro)

	incompatible := resolveUsageCostFromRule(
		&PriceRule{ID: 91, Currency: "USD", ValidationState: PriceValidationAccepted},
		[]PriceRuleItem{{ID: 92, PriceRuleID: 91, Dimension: UsageDimensionInputTokens, UnitScale: UsageUnitScalePerImage, UnitPriceMicro: 1}},
		UsageDimensionInputTokens, UsageQuantityMilliFromCount(10), UsageUnitScalePerMillionTokens,
	)
	assert.Equal(t, UsageCostStateUnpriced, incompatible.CostState)
	assert.Zero(t, incompatible.CostUsdMicro)
	assert.Error(t, CreatePriceRuleSnapshot(db, &PriceRule{
		ScopeType: PriceScopeChannel, ScopeID: 88004, Source: PriceRuleSourceManual,
		Currency: "USD", ValidationState: PriceValidationAccepted,
	}, []PriceRuleItem{{
		Dimension: UsageDimensionInputTokens, UnitScale: UsageUnitScalePerImage,
		UnitPriceMicro: 1, Currency: "USD", UsdState: UsdStateNotNeeded,
	}}))

	overflow := resolveUsageCostFromRule(
		&PriceRule{ID: 93, Currency: "USD", ValidationState: PriceValidationAccepted},
		[]PriceRuleItem{{ID: 94, PriceRuleID: 93, Dimension: UsageDimensionImageCount, UnitScale: UsageUnitScalePerImage, UnitPriceMicro: math.MaxInt64, Currency: "USD", UsdState: UsdStateNotNeeded}},
		UsageDimensionImageCount, math.MaxInt64, UsageUnitScalePerImage,
	)
	assert.Equal(t, UsageCostStateUnpriced, overflow.CostState)
	assert.Zero(t, overflow.CostMicro)
	assert.Zero(t, overflow.CostUsdMicro)
}
