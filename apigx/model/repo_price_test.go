package apigxmodel

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// T2.1 价格快照的三条不变量（计划书 §阶段 2 验收）。

// ① 价格未知 ≠ 零：没有快照、快照未被接受、或等待汇率换算，都必须判为「未知」，
// 且取价接口必须显式返回 ok=false，而不是给出 0 让调用方当成免费。
func TestPriceUnknownIsNotZero(t *testing.T) {
	db := newRepoDB(t)

	// 没有任何快照 → ErrNoPriceRule
	_, _, err := GetEffectivePriceRule(db, 1, 9501, 0)
	assert.ErrorIs(t, err, ErrNoPriceRule, "无快照必须显式报「没有价格」")

	// 快照存在但**未被接受** → 仍算未知
	pending := &PriceRule{ScopeType: 1, ScopeID: 9501, Source: PriceRuleSourceModelsDev, Currency: "USD",
		ValidationState: PriceValidationPending}
	require.NoError(t, CreatePriceRuleSnapshot(db, pending, []PriceRuleItem{
		{Dimension: 1, UnitPriceMicro: 2500, Currency: "USD", UsdState: UsdStateNotNeeded},
	}))
	// **未被接受的快照不生效**：取用时根本看不到它（旧价格继续使用 / 视为未知）
	_, _, err = GetEffectivePriceRule(db, 1, 9501, 0)
	assert.ErrorIs(t, err, ErrNoPriceRule, "待校验的快照不得生效")

	// 接受之后才可用
	require.NoError(t, db.Model(&PriceRule{}).Where("scope_type = 1 AND scope_id = 9501").
		Update("validation_state", PriceValidationAccepted).Error)
	rule, items, err := GetEffectivePriceRule(db, 1, 9501, 0)
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.False(t, IsPriceUnknown(rule, &items[0]))
	_, ok := EffectiveUSDUnitPriceMicro(rule, &items[0])
	require.True(t, ok)
	rule, items, err = GetEffectivePriceRule(db, 1, 9501, 0)
	require.NoError(t, err)
	price, ok := EffectiveUSDUnitPriceMicro(rule, &items[0])
	require.True(t, ok)
	assert.EqualValues(t, 2500, price)

	// 非 USD 币种在**待汇率**时同样算未知
	cny := &PriceRule{ScopeType: 1, ScopeID: 9502, Source: PriceRuleSourceUpstreamQuote, Currency: "CNY",
		ValidationState: PriceValidationAccepted}
	require.NoError(t, CreatePriceRuleSnapshot(db, cny, []PriceRuleItem{
		{Dimension: 1, UnitPriceMicro: 18000, Currency: "CNY", UsdState: UsdStatePendingFx},
	}))
	rule, items, err = GetEffectivePriceRule(db, 1, 9502, 0)
	require.NoError(t, err)
	assert.True(t, IsPriceUnknown(rule, &items[0]), "非 USD 且待换算时必须算未知")
	_, ok = EffectiveUSDUnitPriceMicro(rule, &items[0])
	assert.False(t, ok)

	// 换算完成后可用（用保存的 USD 单价）
	require.NoError(t, db.Model(&PriceRuleItem{}).Where("id = ?", items[0].ID).
		Updates(map[string]any{"usd_state": UsdStateConverted, "usd_unit_price_micro": 2500}).Error)
	rule, items, err = GetEffectivePriceRule(db, 1, 9502, 0)
	require.NoError(t, err)
	price, ok = EffectiveUSDUnitPriceMicro(rule, &items[0])
	require.True(t, ok)
	assert.EqualValues(t, 2500, price)
}

// ② 人工覆盖不被同步改写：人工/锁定的快照之后，同步必须放弃。
func TestPriceSyncCannotOverwriteManualOverride(t *testing.T) {
	db := newRepoDB(t)

	manual := &PriceRule{ScopeType: 1, ScopeID: 9601, Source: PriceRuleSourceManual, Currency: "USD",
		ValidationState: PriceValidationAccepted}
	require.NoError(t, CreatePriceRuleSnapshot(db, manual, []PriceRuleItem{
		{Dimension: 1, UnitPriceMicro: 9999, Currency: "USD", UsdState: UsdStateNotNeeded},
	}))
	assert.True(t, manual.ManualOverride, "人工来源应自动标记 ManualOverride")

	// 同步想改写 → 必须被挡住
	sync := &PriceRule{ScopeType: 1, ScopeID: 9601, Source: PriceRuleSourceModelsDev, Currency: "USD",
		ValidationState: PriceValidationAccepted}
	err := ApplyPriceSync(db, sync, []PriceRuleItem{
		{Dimension: 1, UnitPriceMicro: 1, Currency: "USD", UsdState: UsdStateNotNeeded},
	})
	assert.ErrorIs(t, err, ErrPriceLocked, "人工覆盖的价格不得被同步改写")

	// 人工价保持原值，且没有产生新版本
	rule, items, err := GetEffectivePriceRule(db, 1, 9601, 0)
	require.NoError(t, err)
	assert.EqualValues(t, 9999, items[0].UnitPriceMicro)
	assert.EqualValues(t, 1, rule.Version, "被挡下的同步不得产生新版本")

	// 未人工覆盖的作用域可以被同步
	other := &PriceRule{ScopeType: 1, ScopeID: 9602, Source: PriceRuleSourceModelsDev, Currency: "USD",
		ValidationState: PriceValidationAccepted}
	require.NoError(t, ApplyPriceSync(db, other, []PriceRuleItem{
		{Dimension: 1, UnitPriceMicro: 100, Currency: "USD", UsdState: UsdStateNotNeeded},
	}))
	// 再次同步 → 追加新版本（历史保留）
	second := &PriceRule{ScopeType: 1, ScopeID: 9602, Source: PriceRuleSourceModelsDev, Currency: "USD",
		ValidationState: PriceValidationAccepted}
	require.NoError(t, ApplyPriceSync(db, second, []PriceRuleItem{
		{Dimension: 1, UnitPriceMicro: 200, Currency: "USD", UsdState: UsdStateNotNeeded},
	}))
	rule, items, err = GetEffectivePriceRule(db, 1, 9602, 0)
	require.NoError(t, err)
	assert.EqualValues(t, 2, rule.Version)
	assert.EqualValues(t, 200, items[0].UnitPriceMicro)

	// 锁定之后同步被挡
	require.NoError(t, SetPriceRuleLock(db, 1, 9602, true))
	third := &PriceRule{ScopeType: 1, ScopeID: 9602, Source: PriceRuleSourceModelsDev, Currency: "USD",
		ValidationState: PriceValidationAccepted}
	assert.ErrorIs(t, ApplyPriceSync(db, third, nil), ErrPriceLocked, "锁定后同步必须被挡")
}

// ③ 单渠道同步不污染全局或其他分享者：快照按作用域版本化，互不可见。
func TestPriceSnapshotScopesAreIsolated(t *testing.T) {
	db := newRepoDB(t)

	channelA := &PriceRule{ScopeType: 1, ScopeID: 9701, Source: PriceRuleSourceUpstreamQuote, Currency: "USD",
		ValidationState: PriceValidationAccepted}
	require.NoError(t, CreatePriceRuleSnapshot(db, channelA, []PriceRuleItem{
		{Dimension: 1, UnitPriceMicro: 111, Currency: "USD", UsdState: UsdStateNotNeeded},
	}))
	channelB := &PriceRule{ScopeType: 1, ScopeID: 9702, Source: PriceRuleSourceUpstreamQuote, Currency: "USD",
		ValidationState: PriceValidationAccepted}
	require.NoError(t, CreatePriceRuleSnapshot(db, channelB, []PriceRuleItem{
		{Dimension: 1, UnitPriceMicro: 222, Currency: "USD", UsdState: UsdStateNotNeeded},
	}))

	// 同一个 scope_id 但不同 scope_type（例如"渠道"与"供给"）也互不影响
	offering := &PriceRule{ScopeType: 2, ScopeID: 9701, Source: PriceRuleSourceManual, Currency: "USD",
		ValidationState: PriceValidationAccepted}
	require.NoError(t, CreatePriceRuleSnapshot(db, offering, []PriceRuleItem{
		{Dimension: 1, UnitPriceMicro: 333, Currency: "USD", UsdState: UsdStateNotNeeded},
	}))

	// A 的同步不能改到 B
	syncA := &PriceRule{ScopeType: 1, ScopeID: 9701, Source: PriceRuleSourceUpstreamQuote, Currency: "USD",
		ValidationState: PriceValidationAccepted}
	require.NoError(t, ApplyPriceSync(db, syncA, []PriceRuleItem{
		{Dimension: 1, UnitPriceMicro: 999, Currency: "USD", UsdState: UsdStateNotNeeded},
	}))

	_, itemsB, err := GetEffectivePriceRule(db, 1, 9702, 0)
	require.NoError(t, err)
	assert.EqualValues(t, 222, itemsB[0].UnitPriceMicro, "另一渠道的价格不得被同步污染")

	_, itemsOffering, err := GetEffectivePriceRule(db, 2, 9701, 0)
	require.NoError(t, err)
	assert.EqualValues(t, 333, itemsOffering[0].UnitPriceMicro, "不同作用域类型之间同样不得串价")

	// 归零检查：谁都没有被写成 0
	for _, scope := range []struct {
		scopeType int
		scopeID   int64
	}{{1, 9701}, {1, 9702}, {2, 9701}} {
		rule, items, err := GetEffectivePriceRule(db, scope.scopeType, scope.scopeID, 0)
		require.NoError(t, err)
		price, ok := EffectiveUSDUnitPriceMicro(rule, &items[0])
		require.True(t, ok)
		assert.NotZero(t, price, "任何作用域的价格都不得被写成 0")
	}
}

// 生效区间与校验：历史版本可回看，非法输入被拒。
func TestPriceRuleVersioningAndValidation(t *testing.T) {
	db := newRepoDB(t)
	base := &PriceRule{ScopeType: 1, ScopeID: 9801, Source: PriceRuleSourceModelsDev, Currency: "USD",
		ValidationState: PriceValidationAccepted, EffectiveFrom: 1000, EffectiveTo: 2000}
	require.NoError(t, CreatePriceRuleSnapshot(db, base, []PriceRuleItem{
		{Dimension: 1, UnitPriceMicro: 10, Currency: "USD", UsdState: UsdStateNotNeeded},
	}))
	// 区间内可取到
	rule, _, err := GetEffectivePriceRule(db, 1, 9801, 1500)
	require.NoError(t, err)
	assert.EqualValues(t, 1, rule.Version)
	// 区间外 → 视为没有价格（而不是"取到旧价"）
	_, _, err = GetEffectivePriceRule(db, 1, 9801, 2500)
	assert.ErrorIs(t, err, ErrNoPriceRule)

	// 非法输入
	assert.ErrorIs(t, CreatePriceRuleSnapshot(db, &PriceRule{ScopeType: 1, Currency: "USD"}, nil), ErrInvalidArgument)
	assert.ErrorIs(t, CreatePriceRuleSnapshot(db, &PriceRule{ScopeType: 1, ScopeID: 9, Currency: ""}, nil), ErrInvalidArgument)
	assert.ErrorIs(t, CreatePriceRuleSnapshot(db, &PriceRule{ScopeType: 1, ScopeID: 9, Currency: "USD"}, []PriceRuleItem{
		{Dimension: 1, UnitPriceMicro: 1, Currency: "USD"}, {Dimension: 1, UnitPriceMicro: 2, Currency: "USD"},
	}), ErrInvalidArgument, "同维度重复必须被拒")
	assert.ErrorIs(t, CreatePriceRuleSnapshot(db, &PriceRule{ScopeType: 1, ScopeID: 9, Currency: "USD"}, []PriceRuleItem{
		{Dimension: 1, UnitPriceMicro: -1, Currency: "USD"},
	}), ErrInvalidArgument, "负价格必须被拒")
}

// T2.1 来源价格摄入：可疑数据**留存待确认且不顶掉旧价**（计划书 §阶段 2 验收）。
func TestIngestSourcePriceKeepsOldPriceOnSuspiciousData(t *testing.T) {
	db := newRepoDB(t)
	const scopeType = 1
	const scopeID int64 = 9901

	// 先有一条已生效的正常价格（line 1 = 2500 微美元 / 单位）
	ok := IngestSourcePrice
	res, err := ok(db, SourcePriceIngest{
		ScopeType: scopeType, ScopeID: scopeID, Source: PriceRuleSourceModelsDev,
		SourceRef: "models.dev/openai/gpt-x", Currency: "USD", ModelKnown: true,
		Items:     []PriceRuleItem{{Dimension: 1, UnitPriceMicro: 2500}},
		RawRecord: `{"input":2.5}`,
	})
	require.NoError(t, err)
	assert.True(t, res.Applied)
	require.NotZero(t, res.RuleID)

	rule, items, err := GetEffectivePriceRule(db, scopeType, scopeID, 0)
	require.NoError(t, err)
	price, usable := EffectiveUSDUnitPriceMicro(rule, &items[0])
	require.True(t, usable)
	assert.EqualValues(t, 2500, price)

	// ① 模型未知 → 待确认，**旧价继续生效**
	res, err = ok(db, SourcePriceIngest{
		ScopeType: scopeType, ScopeID: scopeID, Source: PriceRuleSourceMoyan,
		SourceRef: "moyan/unknown-model", Currency: "USD", ModelKnown: false,
		Items: []PriceRuleItem{{Dimension: 1, UnitPriceMicro: 1}},
	})
	require.NoError(t, err)
	assert.False(t, res.Applied)
	assert.True(t, res.Pending)
	assert.Equal(t, PricePendingModelUnknown, res.Note)

	rule, items, err = GetEffectivePriceRule(db, scopeType, scopeID, 0)
	require.NoError(t, err)
	price, usable = EffectiveUSDUnitPriceMicro(rule, &items[0])
	require.True(t, usable)
	assert.EqualValues(t, 2500, price, "可疑的新价格不得顶掉旧价格")
	assert.EqualValues(t, 1, rule.Version, "生效版本应仍是 v1")

	// 待确认版本可被人工看到（不过滤校验状态）
	all, err := ListPriceRuleSnapshots(db, scopeType, scopeID, 10)
	require.NoError(t, err)
	require.Len(t, all, 2)
	assert.Equal(t, PriceValidationPending, all[0].ValidationState)
	assert.Equal(t, PricePendingModelUnknown, all[0].ValidationNote)
	assert.Equal(t, PriceValidationAccepted, all[1].ValidationState)

	// ② 单位冲突 → 待确认
	res, err = ok(db, SourcePriceIngest{
		ScopeType: scopeType, ScopeID: scopeID, Source: PriceRuleSourceUpstreamQuote,
		Currency: "USD", ModelKnown: true,
		Items: []PriceRuleItem{{Dimension: 1, UnitPriceMicro: 5, Currency: "CNY"}},
	})
	require.NoError(t, err)
	assert.Equal(t, PricePendingUnitConflict, res.Note)
	assert.False(t, res.Applied)

	// ③ 币种缺失 → 待确认
	res, err = ok(db, SourcePriceIngest{
		ScopeType: scopeType, ScopeID: scopeID, Source: PriceRuleSourceUpstreamQuote,
		Currency: "", ModelKnown: true, Items: []PriceRuleItem{{Dimension: 1, UnitPriceMicro: 5}},
	})
	require.NoError(t, err)
	assert.Equal(t, PricePendingCurrencyMissing, res.Note)

	// ④ 空记录 → 待确认
	res, err = ok(db, SourcePriceIngest{
		ScopeType: scopeType, ScopeID: scopeID, Source: PriceRuleSourceUpstreamQuote,
		Currency: "USD", ModelKnown: true,
	})
	require.NoError(t, err)
	assert.Equal(t, PricePendingEmptyRecord, res.Note)

	// 旧价始终没有被改写
	rule, items, err = GetEffectivePriceRule(db, scopeType, scopeID, 0)
	require.NoError(t, err)
	price, usable = EffectiveUSDUnitPriceMicro(rule, &items[0])
	require.True(t, usable)
	assert.EqualValues(t, 2500, price)

	// ⑤ 人工锁定后，正常数据也不能改写
	require.NoError(t, SetPriceRuleLock(db, scopeType, scopeID, true))
	_, err = ok(db, SourcePriceIngest{
		ScopeType: scopeType, ScopeID: scopeID, Source: PriceRuleSourceModelsDev,
		Currency: "USD", ModelKnown: true, Items: []PriceRuleItem{{Dimension: 1, UnitPriceMicro: 777}},
	})
	assert.ErrorIs(t, err, ErrPriceLocked, "人工锁定后同步必须放弃")
}

// 非 USD 价格：有保存的汇率则按该汇率归一；没有汇率则**可生效但 USD 未知**（不误算为 0）。
func TestIngestNonUSDPriceUsesSavedFxRate(t *testing.T) {
	db := newRepoDB(t)
	const scopeType = 1

	// 有汇率：1 USD = 7.2 CNY → FxRateMicro = 7_200_000
	res, err := IngestSourcePrice(db, SourcePriceIngest{
		ScopeType: scopeType, ScopeID: 9911, Source: PriceRuleSourceUpstreamQuote,
		Currency: "CNY", ModelKnown: true, FxRateID: 5, FxRateMicro: 7_200_000,
		Items: []PriceRuleItem{{Dimension: 1, UnitPriceMicro: 18_000}},
	})
	require.NoError(t, err)
	assert.True(t, res.Applied)
	rule, items, err := GetEffectivePriceRule(db, scopeType, 9911, 0)
	require.NoError(t, err)
	assert.True(t, rule.UsdConverted)
	price, usable := EffectiveUSDUnitPriceMicro(rule, &items[0])
	require.True(t, usable)
	assert.EqualValues(t, 2500, price, "18000 CNY / 7.2 = 2500 微美元")

	// 没有汇率：快照生效（原币价可信），但 USD 未知——**不得**返回 0
	res, err = IngestSourcePrice(db, SourcePriceIngest{
		ScopeType: scopeType, ScopeID: 9912, Source: PriceRuleSourceUpstreamQuote,
		Currency: "CNY", ModelKnown: true,
		Items: []PriceRuleItem{{Dimension: 1, UnitPriceMicro: 18_000}},
	})
	require.NoError(t, err)
	assert.True(t, res.Applied)
	rule, items, err = GetEffectivePriceRule(db, scopeType, 9912, 0)
	require.NoError(t, err)
	assert.Equal(t, UsdStatePendingFx, items[0].UsdState)
	_, usable = EffectiveUSDUnitPriceMicro(rule, &items[0])
	assert.False(t, usable, "缺汇率时 USD 价格必须显式未知，而不是 0")
}
