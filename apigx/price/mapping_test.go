package price

import (
	"testing"

	apigxmodel "github.com/QuantumNous/new-api/apigx/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 底座内置价格口径：USD per 1M tokens → 微美元/1M tokens。
func TestItemsFromUSDPerMillion(t *testing.T) {
	items, err := ItemsFromUSDPerMillion(2, 8, 0.5, 0)
	require.NoError(t, err)
	require.Len(t, items, 3, "缓存写为 0 表示不单独计价，不应生成分项")

	byDimension := map[int]apigxmodel.PriceRuleItem{}
	for _, item := range items {
		byDimension[item.Dimension] = item
	}
	assert.EqualValues(t, 2_000_000, byDimension[DimensionInputTokens].UnitPriceMicro)
	assert.EqualValues(t, 8_000_000, byDimension[DimensionOutputTokens].UnitPriceMicro)
	assert.EqualValues(t, 500_000, byDimension[DimensionCacheReadTokens].UnitPriceMicro)
	for _, item := range items {
		assert.Equal(t, UnitScalePerMillionTokens, item.UnitScale)
		assert.Equal(t, "USD", item.Currency)
	}

	// **不适用不等于免费**：价格为 0 的维度不生成分项，而不是生成 0 元分项
	_, hasCacheWrite := byDimension[DimensionCacheWriteTokens]
	assert.False(t, hasCacheWrite, "未计价的维度不得写成 0 元分项（那会在估算里表现为免费）")
}

// 旧倍率口径：换算**必须**用运行时的 QuotaPerUnit，不能写死经典的 "×2"。
func TestItemsFromRatiosUsesRuntimeQuotaPerUnit(t *testing.T) {
	// QuotaPerUnit = 500000（底座默认）→ 倍率 1 = 2 USD/1M tokens
	items, err := ItemsFromRatios(1, 4, 0.25, 500_000)
	require.NoError(t, err)
	byDimension := map[int]apigxmodel.PriceRuleItem{}
	for _, item := range items {
		byDimension[item.Dimension] = item
	}
	assert.EqualValues(t, 2_000_000, byDimension[DimensionInputTokens].UnitPriceMicro)
	assert.EqualValues(t, 8_000_000, byDimension[DimensionOutputTokens].UnitPriceMicro, "输出 = 输入 × completionRatio")
	assert.EqualValues(t, 500_000, byDimension[DimensionCacheReadTokens].UnitPriceMicro)

	// 换一个 QuotaPerUnit（同样是合法配置）→ 结果必须随之变化，证明用的是运行时值
	items, err = ItemsFromRatios(1, 4, 0, 1_000_000)
	require.NoError(t, err)
	for _, item := range items {
		if item.Dimension == DimensionInputTokens {
			assert.EqualValues(t, 1_000_000, item.UnitPriceMicro,
				"QuotaPerUnit 变化后价格必须跟着变：写死 ×2 会在这里静默算错")
		}
	}
}

// 离谱的价格要在写入前被拒：否则一个口径错误会直接进计费。
func TestItemsRejectImplausiblePrices(t *testing.T) {
	_, err := ItemsFromUSDPerMillion(1_000_000, 0, 0, 0) // 100 万美元/1M tokens
	assert.ErrorIs(t, err, ErrPriceOutOfRange)

	_, err = ItemsFromUSDPerMillion(-1, 0, 0, 0)
	assert.ErrorIs(t, err, apigxmodel.ErrInvalidArgument)

	_, err = ItemsFromRatios(1, 1, 0, 0)
	assert.ErrorIs(t, err, apigxmodel.ErrInvalidArgument, "QuotaPerUnit 非法时必须拒绝而不是猜")

	_, err = ItemsFromRatios(-1, 1, 0, 500_000)
	assert.ErrorIs(t, err, apigxmodel.ErrInvalidArgument)
}

// 作用域用**价格自己的**枚举（0=供给/1=上游 Key/2=渠道），与容量那套不同。
func TestScopeHelpersUsePriceEnums(t *testing.T) {
	scopeType, scopeID := ChannelScope(42)
	assert.Equal(t, apigxmodel.PriceScopeChannel, scopeType)
	assert.EqualValues(t, 42, scopeID)

	scopeType, scopeID = UpstreamKeyScope(7)
	assert.Equal(t, apigxmodel.PriceScopeUpstreamKey, scopeType)
	assert.EqualValues(t, 7, scopeID)

	// 回归保护：这套取值不得与容量作用域混同（容量里 1 = 渠道，价格里 1 = 上游 Key）
	assert.NotEqual(t, apigxmodel.PriceScopeChannel, apigxmodel.CapacityScopeChannel,
		"价格与容量的作用域枚举是两套，混用会把渠道价写成 Key 价")
	assert.Equal(t, apigxmodel.PriceScopeOffering, 0)
}
