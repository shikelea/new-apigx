package apigxmodel

import (
	"errors"
	"fmt"

	"gorm.io/gorm"
)

// UsageEvent 枚举（取值依据 docs/stage0/schema-draft.md §5.16 / §5.21）。
//
// 此前这些取值只写在字段注释里，代码中没有任何常量 ⇒ 写入方无从下笔。
// 这里把**已在规范中定死**的那部分补成常量，避免各处写魔数。
const (
	// UsageEventKindUsage 用量事件（0=用量 / 1=调整 / 2=校正）。
	UsageEventKindUsage int = 0
	// UsageEventKindCorrection 校正事件：追加新行修订原事件，不覆盖历史证据。
	UsageEventKindCorrection int = 2

	// UsageDimensionInputTokens 计量维度：输入 token。
	UsageDimensionInputTokens int = 0
	// UsageDimensionOutputTokens 计量维度：输出 token。
	UsageDimensionOutputTokens int = 1

	// UsageUnitScalePerMillionTokens 单价计量单位：/1M tokens。
	UsageUnitScalePerMillionTokens int = 0

	// UsageTrustUpstreamReported 可信度：上游上报（非本地估算）。
	UsageTrustUpstreamReported int = 0

	// UsageCostStateReferenceEstimated 成本状态：参考成本已估（0）。
	UsageCostStateReferenceEstimated int = 0
	// UsageCostStateUnpriced 成本状态：未定价（此状态下 cost_usd_micro 必须为 0）。
	UsageCostStateUnpriced int = 2
	// UsageCostStatePendingFx 成本状态：待换算（缺汇率）。
	UsageCostStatePendingFx int = 3
	// UsageCostStateExplicitZero 成本状态：明确零价（有分项但单价为 0）。
	UsageCostStateExplicitZero int = 4

	// UsageUnitScalePerThousandTokens 单价计量单位：/1K tokens。
	UsageUnitScalePerThousandTokens int = 1
)

// UsageQuantityMilliFromTokens 把 token 数换算成 quantity_milli（千分之一最小单位）。
//
// 规范：1 token = 1000。负数按 0 处理（不制造负用量）。
func UsageQuantityMilliFromTokens(tokens int) int64 {
	if tokens <= 0 {
		return 0
	}
	return int64(tokens) * 1000
}

// RecordUsageEvent 追加一条用量事件。
//
// 表是**不可变快照**：整行一次写入、绝不回写；重复事件靠 event_uid 唯一约束去重
// （调用方负责生成稳定 UID）。未定价时 cost_usd_micro 必须为 0 —— 这里强制校验并拒绝违规写入，
// 避免把"价格未知"写成"成本为零"。
func RecordUsageEvent(db *gorm.DB, event *UsageEvent) error {
	if db == nil {
		return fmt.Errorf("%w: nil db", ErrInvalidArgument)
	}
	if event == nil {
		return fmt.Errorf("%w: nil event", ErrInvalidArgument)
	}
	if event.EventUID == "" {
		return fmt.Errorf("%w: event_uid is required", ErrInvalidArgument)
	}
	if event.CostState == UsageCostStateUnpriced && event.CostUsdMicro != 0 {
		return fmt.Errorf("%w: unpriced event must have zero usd cost", ErrInvalidArgument)
	}
	return db.Create(event).Error
}

// UsageCostSnapshot 一次用量事件的成本快照（与 §5.21 的成本列一一对应）。
type UsageCostSnapshot struct {
	PriceRuleID     int64
	PriceRuleItemID int64
	UnitPriceMicro  int64
	Currency        string
	FxRateMicro     int64
	CostMicro       int64
	CostUsdMicro    int64
	CostState       int
}

// ResolveUsageCost 按「实际执行 Key → 供给 → 渠道」的顺序取价，并算出该维度的成本快照。
//
// 顺序依据（阶段 4 验收表第 19 行）：Key 的价格代表"实际执行"那一刻的成本；
// 未配置时按 UpstreamKey.PriceRuleID 的注释语义（0=继承）逐级回退。
//
// 计费安全：全程整数运算；乘法前判界（溢出则不写成本）；
// **不认识的计算单位一律不猜** —— 记未定价，而不是拿错单位硬算一个数出来。
func ResolveUsageCost(db *gorm.DB, keyID, offeringID, channelID int64, dimension int, tokens int64, at int64) UsageCostSnapshot {
	snap := UsageCostSnapshot{CostState: UsageCostStateUnpriced}
	if db == nil || tokens <= 0 {
		return snap
	}

	var rule *PriceRule
	var items []PriceRuleItem

	// ① 实际执行 Key：**直接按 Key 作用域查**（不依赖 UpstreamKey.PriceRuleID 是否被回填 ——
	// 实测发现给 Key 配价不会自动回填该列，因此用"查得到就用、查不到就继承"的语义更可靠）
	if keyID > 0 {
		if r, is, ruleErr := GetEffectivePriceRule(db, PriceScopeUpstreamKey, keyID, at); ruleErr == nil {
			rule, items = r, is
		}
	}
	// ② 供给
	if rule == nil && offeringID > 0 {
		if r, is, ruleErr := GetEffectivePriceRule(db, PriceScopeOffering, offeringID, at); ruleErr == nil {
			rule, items = r, is
		}
	}
	// ③ 渠道
	if rule == nil && channelID > 0 {
		if r, is, ruleErr := GetEffectivePriceRule(db, PriceScopeChannel, channelID, at); ruleErr == nil {
			rule, items = r, is
		}
	}
	if rule == nil {
		return snap // 未定价：cost_usd_micro 保持 0
	}
	snap.PriceRuleID = rule.ID
	snap.Currency = rule.Currency
	snap.FxRateMicro = rule.FxRateMicro

	var item *PriceRuleItem
	for i := range items {
		if items[i].Dimension == dimension {
			item = &items[i]
			break
		}
	}
	if item == nil {
		return snap // 该维度没有分项 ⇒ 未定价
	}
	snap.PriceRuleItemID = item.ID
	snap.UnitPriceMicro = item.UnitPriceMicro

	// 明确零价与"未定价"是两件事
	if item.UnitPriceMicro == 0 {
		snap.CostState = UsageCostStateExplicitZero
		return snap
	}

	usdUnit, ok := EffectiveUSDUnitPriceMicro(rule, item)
	if !ok {
		if item.Currency != "USD" {
			snap.CostState = UsageCostStatePendingFx // 待换算，而不是未定价
		}
		return snap
	}

	// 单位换算：只支持规范里明确定义的两个单位
	var divisor int64
	switch item.UnitScale {
	case UsageUnitScalePerMillionTokens:
		divisor = 1_000_000
	case UsageUnitScalePerThousandTokens:
		divisor = 1_000
	default:
		return snap // 不认识的单位：不猜
	}

	const maxSafeTokens int64 = 1_000_000_000_000
	if tokens > maxSafeTokens {
		return snap
	}
	product := tokens * usdUnit
	if usdUnit != 0 && product/usdUnit != tokens {
		return snap // 乘法溢出 ⇒ 不写成本
	}

	snap.CostUsdMicro = product / divisor
	snap.CostMicro = snap.CostUsdMicro
	snap.CostState = UsageCostStateReferenceEstimated
	return snap
}

// AppendUsageCorrection 追加一条**校正事件**（kind=2 + correction_of_event_id），用于"补到准确 usage"之后修订估算。
//
// 为什么是追加而不是更新（§5.21）：本表是**不可变快照**，校正靠新增行形成修订轨迹，
// 原行的估算与价格证据必须原样保留；因此这里只写新行，**不触碰原行**。
//
// 唯一约束仍是 event_uid：重复补正不会产生第二条校正；
// "未定价 ⇒ cost_usd_micro 必须为 0"的硬约束由 RecordUsageEvent 统一把关。
func AppendUsageCorrection(db *gorm.DB, originalEventID int64, corrected *UsageEvent) error {
	if db == nil {
		return fmt.Errorf("%w: nil db", ErrInvalidArgument)
	}
	if originalEventID <= 0 {
		return fmt.Errorf("%w: original event id is required", ErrInvalidArgument)
	}
	if corrected == nil {
		return fmt.Errorf("%w: nil corrected event", ErrInvalidArgument)
	}
	// 原事件必须存在（否则"修订了一条不存在的记录"会让轨迹断裂）
	var original UsageEvent
	if err := db.Where("id = ?", originalEventID).First(&original).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrNotFound
		}
		return err
	}
	corrected.Kind = UsageEventKindCorrection
	corrected.CorrectionOfEventID = originalEventID
	return RecordUsageEvent(db, corrected)
}
