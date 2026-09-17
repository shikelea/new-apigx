package price

import (
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"

	apigxmodel "github.com/QuantumNous/new-api/apigx/model"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
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

// ---- 单渠道同步的作用域与隔离（T2.2） ----

// 同步只能触达「这个渠道」与「这个渠道自己的供给」，绝不写全局，也不碰别的分享者。
func TestSyncChannelPricesDoesNotPolluteOthers(t *testing.T) {
	db := newPriceTestDB(t)

	const ownerA, ownerB int64 = 7101, 7102
	channelA := seedShareChannel(t, db, ownerA, "A渠道")
	channelB := seedShareChannel(t, db, ownerB, "B渠道")
	offeringA := seedOffering(t, db, ownerA, channelA, "gpt-x")

	// A 同步自己的价格
	report, err := SyncChannelPrices(db, ownerA, channelA, []Reading{
		{UpstreamModelID: "gpt-x", InputUSDPerMillion: 2, OutputUSDPerMillion: 8,
			Source: apigxmodel.PriceRuleSourceModelsDev, SourceRef: "models.dev/gpt-x", RawRecord: `{"in":2}`},
	})
	require.NoError(t, err)
	assert.Equal(t, 1, report.Applied)
	assert.Empty(t, report.Skipped)

	// 价格落在**供给**作用域（而不是全局，也不是渠道）
	rule, items, err := apigxmodel.GetEffectivePriceRule(db, apigxmodel.PriceScopeOffering, offeringA, 0)
	require.NoError(t, err)
	require.Len(t, items, 2)
	assert.EqualValues(t, 2_000_000, items[0].UnitPriceMicro)
	assert.EqualValues(t, 8_000_000, items[1].UnitPriceMicro)
	assert.Equal(t, apigxmodel.PriceRuleSourceModelsDev, rule.Source)
	assert.Equal(t, "models.dev/gpt-x", rule.SourceRef, "来源标识要能追溯价格出处")
	assert.NotEmpty(t, rule.RawRecord)

	// B 的渠道与供给**不受影响**
	_, _, err = apigxmodel.GetEffectivePriceRule(db, apigxmodel.PriceScopeChannel, channelB, 0)
	assert.ErrorIs(t, err, apigxmodel.ErrNoPriceRule, "别人的渠道不得被写入价格")

	// 全局作用域从未被写过：用一个不存在的"全局"标识反查，确认没有任何 scope 为空的快照
	var globalCount int64
	require.NoError(t, db.Model(&apigxmodel.PriceRule{}).Where("scope_type = ? OR scope_id = 0", -1).Count(&globalCount).Error)
	assert.Zero(t, globalCount)

	// 越权同步被拒（与"渠道不存在"同义）
	_, err = SyncChannelPrices(db, ownerB, channelA, []Reading{{UpstreamModelID: "gpt-x", InputUSDPerMillion: 99}})
	assert.ErrorIs(t, err, apigxmodel.ErrNotFound)
}

// 人工覆盖后，同步放弃但**不是失败**（旧价继续用，只报告跳过）。
func TestSyncChannelPricesRespectsManualOverride(t *testing.T) {
	db := newPriceTestDB(t)
	const owner int64 = 7201
	channel := seedShareChannel(t, db, owner, "C渠道")
	offering := seedOffering(t, db, owner, channel, "gpt-y")

	// 人工价（供给作用域）
	manual := &apigxmodel.PriceRule{
		ScopeType: apigxmodel.PriceScopeOffering, ScopeID: offering,
		Source: apigxmodel.PriceRuleSourceManual, Currency: "USD",
		ValidationState: apigxmodel.PriceValidationAccepted,
	}
	require.NoError(t, apigxmodel.CreatePriceRuleSnapshot(db, manual, []apigxmodel.PriceRuleItem{
		{Dimension: DimensionInputTokens, UnitScale: UnitScalePerMillionTokens, UnitPriceMicro: 9_999_999, Currency: "USD"},
	}))

	report, err := SyncChannelPrices(db, owner, channel, []Reading{
		{UpstreamModelID: "gpt-y", InputUSDPerMillion: 2, OutputUSDPerMillion: 8, Source: apigxmodel.PriceRuleSourceModelsDev},
	})
	require.NoError(t, err, "人工覆盖应导致跳过，而不是报错")
	assert.Zero(t, report.Applied)
	assert.Contains(t, report.Skipped, "gpt-y")
	assert.Equal(t, "manual_override", report.Notes["gpt-y"])

	// 人工价仍然是生效价
	_, items, err := apigxmodel.GetEffectivePriceRule(db, apigxmodel.PriceScopeOffering, offering, 0)
	require.NoError(t, err)
	assert.EqualValues(t, 9_999_999, items[0].UnitPriceMicro)
}

// 口径离谱的读数不写任何版本，只报告 —— 不让错价进入计费。
func TestSyncChannelPricesSkipsImplausibleReadings(t *testing.T) {
	db := newPriceTestDB(t)
	const owner int64 = 7301
	channel := seedShareChannel(t, db, owner, "D渠道")
	seedOffering(t, db, owner, channel, "gpt-z")

	report, err := SyncChannelPrices(db, owner, channel, []Reading{
		{UpstreamModelID: "gpt-z", InputUSDPerMillion: 5_000_000, Source: apigxmodel.PriceRuleSourceOpenRouter},
	})
	require.NoError(t, err)
	assert.Zero(t, report.Applied)
	assert.Equal(t, "implausible_price", report.Notes["gpt-z"])

	_, _, err = apigxmodel.GetEffectivePriceRule(db, apigxmodel.PriceScopeChannel, channel, 0)
	assert.ErrorIs(t, err, apigxmodel.ErrNoPriceRule, "离谱读数不得写下任何价格")
}

// 没有对应供给时退化到渠道作用域（渠道级默认价），仍然不写全局。
func TestSyncChannelPricesFallsBackToChannelScope(t *testing.T) {
	db := newPriceTestDB(t)
	const owner int64 = 7401
	channel := seedShareChannel(t, db, owner, "E渠道")

	report, err := SyncChannelPrices(db, owner, channel, []Reading{
		{UpstreamModelID: "no-offering-yet", InputUSDPerMillion: 1, OutputUSDPerMillion: 4,
			Source: apigxmodel.PriceRuleSourceUpstreamQuote},
	})
	require.NoError(t, err)
	assert.Equal(t, 1, report.Applied)

	rule, items, err := apigxmodel.GetEffectivePriceRule(db, apigxmodel.PriceScopeChannel, channel, 0)
	require.NoError(t, err)
	assert.EqualValues(t, 1_000_000, items[0].UnitPriceMicro)
	assert.Equal(t, "USD", rule.Currency)
}

// ---- fixture ----

// newPriceTestDB 建一个独立的 SQLite 库并跑迁移（与 apigx/model 的测试同一套结构）。
func newPriceTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "apigx_price_test.db")
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, apigxmodel.Migrate(db))
	return db
}

var priceSeq atomic.Int64

// seedShareChannel 建一个属于 owner 的分享渠道。
func seedShareChannel(t *testing.T, db *gorm.DB, ownerUserID int64, name string) int64 {
	t.Helper()
	publicID := "ch" + strconv.FormatInt(priceSeq.Add(1), 10) + name
	group := &apigxmodel.ShareGroup{OwnerUserID: ownerUserID, Name: name + "-组", PublicID: "g" + publicID}
	require.NoError(t, apigxmodel.CreateShareGroup(db, group))
	channel := &apigxmodel.Channel{
		PublicID: publicID, OwnerUserID: ownerUserID, ShareGroupID: group.ID,
		Name: name, BaseChannelID: priceSeq.Add(1) + 100000,
	}
	require.NoError(t, apigxmodel.CreateChannel(db, channel))
	return channel.ID
}

// seedOffering 为渠道建一条供给（价格优先落到供给作用域）。
func seedOffering(t *testing.T, db *gorm.DB, ownerUserID, channelID int64, upstreamModelID string) int64 {
	t.Helper()
	offering := &apigxmodel.ModelOffering{ChannelID: channelID, UpstreamModelID: upstreamModelID}
	require.NoError(t, apigxmodel.CreateModelOffering(db, ownerUserID, offering))
	return offering.ID
}

// 按次计价（底座 model_price）**不能被静默丢掉**：丢了等于把按次收费的模型当免费。
func TestSyncChannelPricesHandlesPerRequestPricing(t *testing.T) {
	db := newPriceTestDB(t)
	const owner int64 = 7501
	channel := seedShareChannel(t, db, owner, "F渠道")

	report, err := SyncChannelPrices(db, owner, channel, []Reading{
		{UpstreamModelID: "per-request-model", PerRequestUSD: 0.5,
			Source: apigxmodel.PriceRuleSourceBuiltinPreset, SourceRef: "new-api/model_price/per-request-model"},
	})
	require.NoError(t, err)
	assert.Equal(t, 1, report.Applied)

	rule, items, err := apigxmodel.GetEffectivePriceRule(db, apigxmodel.PriceScopeChannel, channel, 0)
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, DimensionRequestCount, items[0].Dimension, "按次价必须是「请求次数」维度")
	assert.Equal(t, UnitScalePerRequest, items[0].UnitScale)
	assert.EqualValues(t, 500_000, items[0].UnitPriceMicro, "0.5 USD/次 = 500000 微美元/次")
	assert.Equal(t, apigxmodel.PriceModePerRequest, rule.PricingMode, "计价方式要如实记成「按次」")
	assert.Equal(t, apigxmodel.PriceRuleSourceBuiltinPreset, rule.Source)
}

// 按次价离谱时同样拒绝写入。
func TestPerRequestPriceRejectsImplausibleAndZero(t *testing.T) {
	_, err := perRequestItems(0)
	assert.ErrorIs(t, err, apigxmodel.ErrInvalidArgument, "0 不是「免费」，是没配")

	_, err = perRequestItems(-1)
	assert.ErrorIs(t, err, apigxmodel.ErrInvalidArgument)

	_, err = perRequestItems(5_000_000)
	assert.ErrorIs(t, err, ErrPriceOutOfRange)
}

// ---- 多单位估算（T2.3） ----

// 同族单位可换算：token 的 1K/1M、时间的 秒/分钟。
func TestConvertUnitPriceMicroWithinFamily(t *testing.T) {
	// 0.002 USD /1K tokens = 2 USD /1M tokens
	perMillion, err := ConvertUnitPriceMicro(2000, UnitScalePerThousandTokens, UnitScalePerMillionTokens)
	require.NoError(t, err)
	assert.EqualValues(t, 2_000_000, perMillion)

	// 反向
	perThousand, err := ConvertUnitPriceMicro(2_000_000, UnitScalePerMillionTokens, UnitScalePerThousandTokens)
	require.NoError(t, err)
	assert.EqualValues(t, 2000, perThousand)

	// 0.01 USD /秒 = 0.6 USD /分钟
	perMinute, err := ConvertUnitPriceMicro(10_000, UnitScalePerSecond, UnitScalePerMinute)
	require.NoError(t, err)
	assert.EqualValues(t, 600_000, perMinute)

	// 同单位原样返回
	same, err := ConvertUnitPriceMicro(1234, UnitScalePerRequest, UnitScalePerRequest)
	require.NoError(t, err)
	assert.EqualValues(t, 1234, same)
}

// 跨族不可换算：必须报错，**不能猜一个系数**（猜错就是不会报错的错价）。
func TestConvertUnitPriceMicroRejectsCrossFamily(t *testing.T) {
	_, err := ConvertUnitPriceMicro(1000, UnitScalePerImage, UnitScalePerMillionTokens)
	assert.ErrorIs(t, err, ErrUnitNotConvertible)

	_, err = ConvertUnitPriceMicro(1000, UnitScalePerRequest, UnitScalePerSecond)
	assert.ErrorIs(t, err, ErrUnitNotConvertible)

	// 未知单位
	_, err = ConvertUnitPriceMicro(1000, 99, UnitScalePerMillionTokens)
	assert.ErrorIs(t, err, apigxmodel.ErrInvalidArgument)

	// 负价
	_, err = ConvertUnitPriceMicro(-1, UnitScalePerSecond, UnitScalePerMinute)
	assert.ErrorIs(t, err, apigxmodel.ErrInvalidArgument)
}

// 归一化到基准单位后可直接比较：不同来源用不同单位报价时能放在一起比。
func TestNormalizeToCanonicalUnit(t *testing.T) {
	// 0.002 USD/1K → 基准（/1M）
	normalized, err := NormalizeToCanonicalUnit(2000, UnitScalePerThousandTokens)
	require.NoError(t, err)
	assert.EqualValues(t, 2_000_000, normalized)

	// 已经是基准单位：不变
	normalized, err = NormalizeToCanonicalUnit(2_000_000, UnitScalePerMillionTokens)
	require.NoError(t, err)
	assert.EqualValues(t, 2_000_000, normalized)

	// 时间族基准是 /秒
	normalized, err = NormalizeToCanonicalUnit(600_000, UnitScalePerMinute)
	require.NoError(t, err)
	assert.EqualValues(t, 10_000, normalized)
}
