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
