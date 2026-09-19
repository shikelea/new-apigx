package apigxmodel

import (
	"errors"
	"fmt"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// 价格快照（`apigx_price_rules` / `apigx_price_rule_items`，T2.1）。
//
// # 三条必须守住的不变量（计划书 §阶段 2 验收）
//
//  1. **价格未知 ≠ 零**：没取到价格时必须有显式状态，绝不能当成 0 参与估算。
//     本层用两个既有字段表达：`ValidationState != 已接受`（价格尚未被接受）与
//     分项的 `UsdState = 待汇率`（USD 值尚不可用）。对外统一由
//     `IsPriceUnknown` / `EffectiveUSDUnitPriceMicro` 回答"能不能用这个价"，而不是让调用方各自判断。
//  2. **人工覆盖不被同步改写**：`ManualOverride` 或 `Locked` 的快照，同步只能跳过。
//  3. **同步不污染其它作用域**：快照按 (ScopeType, ScopeID) 版本化，同步只写自己那一格。
//
// 另外，快照是**只追加**的：每次同步/修改都新建一个版本，历史版本不被改写——
// 这也是"历史记录不随汇率更新改写"（T2.3）的实现基础。

// 与 `models_metering.go` 中字段注释一一对应的枚举。
// 注释里已定义取值，这里补上 Go 常量以免各处写魔数。
const (
	// PriceRuleSourceManual 人工覆盖。
	PriceRuleSourceManual int = 0
	// PriceRuleSourceUpstreamQuote 上游渠道报价。
	PriceRuleSourceUpstreamQuote int = 1
	// PriceRuleSourceMoyan 模眼。
	PriceRuleSourceMoyan int = 2
	// PriceRuleSourceModelRadar ModelRadar。
	PriceRuleSourceModelRadar int = 3
	// PriceRuleSourceBuiltinPreset New API 内置预设。
	PriceRuleSourceBuiltinPreset int = 4
	// PriceRuleSourceModelsDev models.dev。
	PriceRuleSourceModelsDev int = 5
	// PriceRuleSourceOpenRouter OpenRouter。
	PriceRuleSourceOpenRouter int = 6
)

// 计价方式（`PricingMode`）。
const (
	PriceModePerDimension int = 0 // 分项绝对单价
	PriceModePerRequest   int = 1 // 按次（model_price）
	PriceModeLegacyRatio  int = 2 // 旧倍率兼容导入
)

// 校验状态（`ValidationState`）。
const (
	PriceValidationPending  int = 0 // 待校验
	PriceValidationAccepted int = 1 // 已接受
	PriceValidationRejected int = 2 // 已拒绝
	PriceValidationConflict int = 3 // 冲突
)

// USD 换算状态（`UsdState`）。
const (
	UsdStateConverted int = 0 // 已换算
	UsdStatePendingFx int = 1 // 待汇率 —— 即"USD 价格未知"
	UsdStateNotNeeded int = 2 // 原币种即 USD
)

// 价格快照的作用域类型（`apigx_price_rules.scope_type`）。
//
// ⚠️ 这套取值与**容量策略**的作用域（`CapacityScope*`）**不是同一套**：
// 价格是 0=供给 / 1=上游 Key / 2=渠道，容量是 0=平台 / 1=渠道 / …。
// 两者混用会把"渠道价格"写成"上游 Key 价格"这类静默错误，因此各自定义、不要互相借用。
const (
	// PriceScopeOffering 供给（`apigx_model_offerings.id`）。
	PriceScopeOffering int = 0
	// PriceScopeUpstreamKey 上游 Key（`apigx_upstream_keys.id`）。
	PriceScopeUpstreamKey int = 1
	// PriceScopeChannel 分享渠道（`apigx_channels.id`）。
	PriceScopeChannel int = 2
)

// ErrNoPriceRule 表示该作用域**没有**可用的价格快照。
//
// 调用方必须把它当成「价格未知」处理，**不得**退化成"价格为零"
// （计划书 §阶段 2 验收：价格未知不算零）。
var ErrNoPriceRule = errors.New("apigxmodel: no price rule for scope")

// ErrPriceLocked 表示目标快照被人工覆盖或锁定，同步不得改写。
var ErrPriceLocked = errors.New("apigxmodel: price rule is manual/locked")

// CreatePriceRuleSnapshot 追加一个新版本的价格快照。
//
// 版本号在 (ScopeType, ScopeID) 内自增；传入 rule.Version 会被忽略，
// 以免调用方（尤其是同步任务）误覆盖历史版本。
func CreatePriceRuleSnapshot(db *gorm.DB, rule *PriceRule, items []PriceRuleItem) error {
	if db == nil || rule == nil {
		return fmt.Errorf("%w: rule is required", ErrInvalidArgument)
	}
	if rule.ScopeID <= 0 {
		return fmt.Errorf("%w: scope_id is required", ErrInvalidArgument)
	}
	if rule.Currency == "" {
		return fmt.Errorf("%w: currency is required", ErrInvalidArgument)
	}
	if rule.Source == PriceRuleSourceManual {
		rule.ManualOverride = true
	}

	// 同维度不得重复：重复会让"该维度的单价"变得不确定
	seen := make(map[int]struct{}, len(items))
	for i := range items {
		if _, dup := seen[items[i].Dimension]; dup {
			return fmt.Errorf("%w: duplicate price dimension %d", ErrInvalidArgument, items[i].Dimension)
		}
		seen[items[i].Dimension] = struct{}{}
		if items[i].UnitPriceMicro < 0 || items[i].UsdUnitPriceMicro < 0 {
			return fmt.Errorf("%w: negative price is not allowed", ErrInvalidArgument)
		}
		if !usageUnitMatchesDimension(items[i].Dimension, items[i].UnitScale) {
			return fmt.Errorf("%w: incompatible price dimension %d and unit %d", ErrInvalidArgument, items[i].Dimension, items[i].UnitScale)
		}
	}

	var maxVersion int64
	if err := db.Model(&PriceRule{}).
		Where("scope_type = ? AND scope_id = ?", rule.ScopeType, rule.ScopeID).
		Select("COALESCE(MAX(version), 0)").Scan(&maxVersion).Error; err != nil {
		return err
	}

	now := common.GetTimestamp()
	rule.Version = maxVersion + 1
	rule.CreatedAt = now
	if rule.EffectiveFrom == 0 {
		rule.EffectiveFrom = now
	}

	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(rule).Error; err != nil {
			return err
		}
		for i := range items {
			items[i].ID = 0
			items[i].PriceRuleID = rule.ID
			if items[i].CreatedAt == 0 {
				items[i].CreatedAt = now
			}
			if err := tx.Create(&items[i]).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// GetEffectivePriceRule 取出某作用域在给定时刻**生效**的价格快照。
//
// 生效判据：`EffectiveFrom <= at` 且（`EffectiveTo == 0` 或 `at < EffectiveTo`），
// 多个候选取版本号最大者。**没有可用快照时返回 ErrNoPriceRule**——调用方必须按"价格未知"处理。
func GetEffectivePriceRule(db *gorm.DB, scopeType int, scopeID int64, at int64) (*PriceRule, []PriceRuleItem, error) {
	if db == nil || scopeID <= 0 {
		return nil, nil, fmt.Errorf("%w: scope_id is required", ErrInvalidArgument)
	}
	if at == 0 {
		at = common.GetTimestamp()
	}
	var rule PriceRule
	err := db.Where("scope_type = ? AND scope_id = ? AND effective_from <= ?", scopeType, scopeID, at).
		Where("effective_to = 0 OR effective_to > ?", at).
		// **只有已接受的版本才生效**：待校验/已拒绝/冲突的版本会被留存待确认，
		// 但不会顶掉正在使用的旧价格——这是计划书「价格同步失败、单位冲突或模型未知 →
		// 旧版本继续使用或待确认」的实现方式。
		Where("validation_state = ?", PriceValidationAccepted).
		Order("version DESC").First(&rule).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil, ErrNoPriceRule
	}
	if err != nil {
		return nil, nil, err
	}

	var items []PriceRuleItem
	if err := db.Where("price_rule_id = ?", rule.ID).Order("dimension ASC").Find(&items).Error; err != nil {
		return nil, nil, err
	}
	return &rule, items, nil
}

// ListPriceRuleSnapshots 列出某作用域的全部版本（新到旧），供**待确认队列**与审计使用。
//
// 与 GetEffectivePriceRule 的区别：这里**不过滤校验状态**——正因如此，
// 被挡下的可疑版本仍能被人看到并处理（否则它们会变成看不见的垃圾数据）。
func ListPriceRuleSnapshots(db *gorm.DB, scopeType int, scopeID int64, limit int) ([]PriceRule, error) {
	if db == nil || scopeID <= 0 {
		return nil, fmt.Errorf("%w: scope_id is required", ErrInvalidArgument)
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var rules []PriceRule
	err := db.Where("scope_type = ? AND scope_id = ?", scopeType, scopeID).
		Order("version DESC").Limit(limit).Find(&rules).Error
	return rules, err
}

// ApplyPriceSync 把一次同步结果写入快照，并守住「人工覆盖不被改写」。
//
// 返回 ErrPriceLocked 表示当前生效快照是人工覆盖/已锁定，**同步必须放弃**
// （而不是覆盖它）。调用方可据此记录一条"被人工价挡住"的同步日志。
func ApplyPriceSync(db *gorm.DB, rule *PriceRule, items []PriceRuleItem) error {
	if db == nil || rule == nil {
		return fmt.Errorf("%w: rule is required", ErrInvalidArgument)
	}
	current, _, err := GetEffectivePriceRule(db, rule.ScopeType, rule.ScopeID, 0)
	switch {
	case err == nil:
		if current.ManualOverride || current.Locked {
			return ErrPriceLocked
		}
	case errors.Is(err, ErrNoPriceRule):
		// 尚无快照：允许写入
	default:
		return err
	}
	return CreatePriceRuleSnapshot(db, rule, items)
}

// SetPriceRuleLock 人工锁定/解锁某作用域的价格（锁定后同步不再改写）。
func SetPriceRuleLock(db *gorm.DB, scopeType int, scopeID int64, locked bool) error {
	if db == nil || scopeID <= 0 {
		return fmt.Errorf("%w: scope_id is required", ErrInvalidArgument)
	}
	res := db.Model(&PriceRule{}).
		Where("scope_type = ? AND scope_id = ?", scopeType, scopeID).
		Updates(map[string]any{"locked": locked, "manual_override": locked})
	return res.Error
}

// IsPriceUnknown 报告某快照的价格是否**不可用**（未知）。
//
// 三种情形都算未知：没有快照、快照未被接受、或分项正在等待汇率换算。
// 调用方据此拒绝"把未知当零"的估算。
func IsPriceUnknown(rule *PriceRule, item *PriceRuleItem) bool {
	if rule == nil {
		return true
	}
	if rule.ValidationState != PriceValidationAccepted {
		return true
	}
	if item == nil {
		return true
	}
	if rule.Currency != "USD" && item.UsdState == UsdStatePendingFx {
		return true
	}
	return false
}

// EffectiveUSDUnitPriceMicro 返回分项的 USD 单价（微美元），并显式回答"是否可用"。
//
// 返回值 ok=false 时，`price` **没有意义**，调用方必须走"价格未知"分支——
// 这是防止"价格未知被当成零"的唯一入口。
func EffectiveUSDUnitPriceMicro(rule *PriceRule, item *PriceRuleItem) (price int64, ok bool) {
	if IsPriceUnknown(rule, item) {
		return 0, false
	}
	if rule.Currency == "USD" {
		return item.UnitPriceMicro, true
	}
	return item.UsdUnitPriceMicro, true
}

// SourcePriceIngest 是一次「来源价格」的摄入请求（同步任务或人工录入都走这里）。
type SourcePriceIngest struct {
	ScopeType   int
	ScopeID     int64
	Source      int
	SourceRef   string
	Currency    string
	PricingMode int
	Items       []PriceRuleItem
	// RawRecord 原始记录（留存以便人工复核可疑版本）。
	RawRecord string
	// ModelKnown 由调用方判定：该价格对应的模型能否映射到我们的目录。
	ModelKnown bool
	// FxRateID/FxRateMicro 是**保存的汇率**（非 USD 币种必须有；0 表示尚无可用汇率）。
	FxRateID    int64
	FxRateMicro int64
	// CreatedByUserID 人工录入者（同步任务为 0）。
	CreatedByUserID int64
}

// IngestResult 报告摄入结果。
type IngestResult struct {
	// Applied 为 true 表示新价格**已生效**；false 表示被留存为待确认（或未写入）。
	Applied bool
	RuleID  int64
	Version int64
	// Pending 为 true 表示写入了待确认版本（不生效）。
	Pending bool
	// Note 是待确认原因（机器可读的短标识），随快照的 ValidationNote 一并保存。
	Note string
}

// 待确认原因（写入 `ValidationNote`）。
const (
	PricePendingCurrencyMissing = "currency_missing"
	PricePendingModelUnknown    = "model_unknown"
	PricePendingUnitConflict    = "unit_conflict"
	PricePendingEmptyRecord     = "empty_record"
)

// IngestSourcePrice 摄入一条来源价格，并按计划书的处置规则决定「生效」还是「待确认」。
//
// 处置规则：
//   - 币种缺失 / 模型未知 / 单位冲突 / 记录里没有任何分项 → **待确认**（写入版本但 `ValidationState = 待校验`，
//     因此 `GetEffectivePriceRule` 不会取到它，**旧价格继续生效**，且该版本可在待确认队列里被人工处理）；
//   - 非 USD 且尚无可用汇率 → **可以生效**，但每个分项标记 `UsdState = 待汇率`：
//     原币种价格是可信的，缺的只是 USD 折算；这样"价格已知但 USD 未知"不会被误当成"没有价格"，
//     也不会被误算成 0（`EffectiveUSDUnitPriceMicro` 会返回 ok=false）。
//   - 当前生效快照是人工覆盖/已锁定 → `ErrPriceLocked`，同步放弃（人工价不被改写）。
func IngestSourcePrice(db *gorm.DB, in SourcePriceIngest) (IngestResult, error) {
	if db == nil || in.ScopeID <= 0 {
		return IngestResult{}, fmt.Errorf("%w: scope_id is required", ErrInvalidArgument)
	}

	rule := &PriceRule{
		ScopeType: in.ScopeType, ScopeID: in.ScopeID, Source: in.Source, SourceRef: in.SourceRef,
		Currency: in.Currency, PricingMode: in.PricingMode,
		FxRateID: in.FxRateID, FxRateMicro: in.FxRateMicro,
		CreatedByUserID: in.CreatedByUserID,
		RawRecord:       in.RawRecord, RawRecordBytes: len(in.RawRecord),
		ValidationState: PriceValidationAccepted,
	}

	// 判定待确认原因（按"越基础的问题越优先"排序，便于人工一眼看到根因）
	note := ""
	switch {
	case len(in.Currency) != 3:
		note = PricePendingCurrencyMissing
	case !in.ModelKnown:
		note = PricePendingModelUnknown
	case len(in.Items) == 0:
		note = PricePendingEmptyRecord
	default:
		for i := range in.Items {
			if in.Items[i].Currency != "" && in.Items[i].Currency != in.Currency {
				note = PricePendingUnitConflict
				break
			}
		}
	}

	items := make([]PriceRuleItem, len(in.Items))
	copy(items, in.Items)
	for i := range items {
		if items[i].Currency == "" {
			items[i].Currency = in.Currency
		}
		if in.Currency == "USD" {
			items[i].UsdState = UsdStateNotNeeded
			items[i].UsdUnitPriceMicro = items[i].UnitPriceMicro
		} else if in.FxRateMicro > 0 {
			// 用**保存的汇率**换算（历史快照因此不随汇率更新而改写）
			items[i].UsdState = UsdStateConverted
			items[i].UsdUnitPriceMicro = items[i].UnitPriceMicro * 1_000_000 / in.FxRateMicro
			rule.UsdConverted = true
		} else {
			items[i].UsdState = UsdStatePendingFx
		}
	}

	if note != "" {
		rule.ValidationState = PriceValidationPending
		rule.ValidationNote = note
		if len(rule.Currency) != 3 {
			// 币种缺失时仍要能**留存这条待确认记录**（否则人工无从看到"同步拿到了什么"）。
			// 用 ISO 4217 的 XXX（表示"无币种"）占位，真实原因保留在 ValidationNote 里——
			// 既不违反「币种必填」的库约束，也不假装它有币种。
			rule.Currency = "XXX"
		}
		// 待确认版本同样只追加：它不生效（GetEffectivePriceRule 过滤校验状态），
		// 因此**旧价格继续使用**，同时这条记录留给人处理。
		if err := CreatePriceRuleSnapshot(db, rule, items); err != nil {
			return IngestResult{}, err
		}
		return IngestResult{Applied: false, Pending: true, RuleID: rule.ID, Version: rule.Version, Note: note}, nil
	}

	if err := ApplyPriceSync(db, rule, items); err != nil {
		return IngestResult{}, err
	}
	return IngestResult{Applied: true, RuleID: rule.ID, Version: rule.Version}, nil
}
