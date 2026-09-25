package apigxmodel

import (
	"errors"
	"fmt"
	"math/big"

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

	// CapabilityUnknown 表示当前协议无法安全归类，不能猜成某一种能力。
	CapabilityUnknown int = 0
	// CapabilityChat 对话、Completions、Responses 与 Gemini 文本能力。
	CapabilityChat int = 1
	// CapabilityEmbedding 向量能力。
	CapabilityEmbedding int = 2
	// CapabilityImageGeneration 图片生成能力。
	CapabilityImageGeneration int = 3
	// CapabilityImageEdit 图片编辑能力。
	CapabilityImageEdit int = 4
	// CapabilityAudioRecognition 语音识别/翻译能力。
	CapabilityAudioRecognition int = 5
	// CapabilityAudioGeneration 语音生成能力。
	CapabilityAudioGeneration int = 6
	// CapabilityRerank 重排能力。
	CapabilityRerank int = 7
	// CapabilityVideo 视频能力。
	CapabilityVideo int = 8

	// UsageDimensionInputTokens 计量维度：输入 token。
	UsageDimensionInputTokens int = 0
	// UsageDimensionOutputTokens 计量维度：输出 token。
	UsageDimensionOutputTokens int = 1
	// UsageDimensionCacheReadTokens 计量维度：缓存读取 token。
	UsageDimensionCacheReadTokens int = 2
	// UsageDimensionCacheWriteTokens 计量维度：缓存写入 token。
	UsageDimensionCacheWriteTokens int = 3
	// UsageDimensionReasoningTokens 计量维度：推理 token。
	UsageDimensionReasoningTokens int = 4
	// UsageDimensionImageCount 计量维度：图片张数。
	UsageDimensionImageCount int = 5
	// UsageDimensionInputAudioSeconds 计量维度：输入音频秒数。
	UsageDimensionInputAudioSeconds int = 6
	// UsageDimensionOutputAudioSeconds 计量维度：输出音频秒数。
	UsageDimensionOutputAudioSeconds int = 7
	// UsageDimensionOutputVideoSeconds 计量维度：输出视频秒数。
	UsageDimensionOutputVideoSeconds int = 8
	// UsageDimensionRequestCount 计量维度：请求次数。
	UsageDimensionRequestCount int = 9
	// UsageDimensionInputCharacters 计量维度：输入字符数。
	UsageDimensionInputCharacters int = 10
	// UsageDimensionDocumentCount 计量维度：文档数。
	UsageDimensionDocumentCount int = 11

	// UsageUnitScalePerMillionTokens 单价计量单位：/1M tokens。
	UsageUnitScalePerMillionTokens int = 0
	// UsageUnitScalePerThousandTokens 单价计量单位：/1K tokens。
	UsageUnitScalePerThousandTokens int = 1
	// UsageUnitScalePerImage 单价计量单位：/张。
	UsageUnitScalePerImage int = 2
	// UsageUnitScalePerSecond 单价计量单位：/秒。
	UsageUnitScalePerSecond int = 3
	// UsageUnitScalePerMinute 单价计量单位：/分钟。
	UsageUnitScalePerMinute int = 4
	// UsageUnitScalePerRequest 单价计量单位：/次。
	UsageUnitScalePerRequest int = 5
	// UsageUnitScalePerThousandCharacters 单价计量单位：/1K 字符。
	UsageUnitScalePerThousandCharacters int = 6
	// UsageUnitScalePerDocument 单价计量单位：/文档。
	UsageUnitScalePerDocument int = 7

	// UsageTrustUpstreamReported 可信度：上游上报（非本地估算）。
	UsageTrustUpstreamReported int = 0
	// UsageTrustLocalEstimated 可信度：本地测量或估算。
	UsageTrustLocalEstimated int = 1
	// UsageTrustUnknown 可信度：来源未知。
	UsageTrustUnknown int = 2
	// UsageTrustUnavailable 可信度：该数量不可得，不能反推伪造。
	UsageTrustUnavailable int = 3

	// UsageCostStateReferenceEstimated 成本状态：参考成本已估（0）。
	UsageCostStateReferenceEstimated int = 0
	// UsageCostStateChannelEstimated 成本状态：渠道成本已估（1）。
	UsageCostStateChannelEstimated int = 1
	// UsageCostStateUnpriced 成本状态：未定价（此状态下 cost_usd_micro 必须为 0）。
	UsageCostStateUnpriced int = 2
	// UsageCostStatePendingFx 成本状态：待换算（缺汇率）。
	UsageCostStatePendingFx int = 3
	// UsageCostStateExplicitZero 成本状态：明确零价（有分项但单价为 0）。
	UsageCostStateExplicitZero int = 4
)

// UsageQuantityMilliFromTokens 把 token 数换算成 quantity_milli（千分之一最小单位）。
//
// 规范：1 token = 1000。负数按 0 处理（不制造负用量）。
func UsageQuantityMilliFromTokens(tokens int) int64 {
	return UsageQuantityMilliFromCount(int64(tokens))
}

// UsageQuantityMilliFromCount 把整数数量换算成 quantity_milli。
func UsageQuantityMilliFromCount(count int64) int64 {
	if count <= 0 || count > int64(^uint64(0)>>1)/1000 {
		return 0
	}
	return count * 1000
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
	if event.QuantityMilli < 0 {
		return fmt.Errorf("%w: quantity_milli cannot be negative", ErrInvalidArgument)
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
	UnitScale       int
}

// ResolveUsagePriceRule 按实际执行 Key → 供给 → 渠道的顺序读取生效价格版本。
// 返回 nil 表示没有价格；一旦某一级存在价格版本，就由该版本完整定义所有分项，
// 缺失的维度保持未定价，不再混用下一级版本补洞。
func ResolveUsagePriceRule(db *gorm.DB, keyID, offeringID, channelID, at int64) (*PriceRule, []PriceRuleItem) {
	if db == nil {
		return nil, nil
	}
	for _, scope := range []struct {
		typeID int
		id     int64
	}{
		{PriceScopeUpstreamKey, keyID},
		{PriceScopeOffering, offeringID},
		{PriceScopeChannel, channelID},
	} {
		if scope.id <= 0 {
			continue
		}
		rule, items, err := GetEffectivePriceRule(db, scope.typeID, scope.id, at)
		if err == nil {
			return rule, items
		}
	}
	return nil, nil
}

// ResolveUsageCost 按「实际执行 Key → 供给 → 渠道」的顺序取价，并算出该维度的成本快照。
//
// 顺序依据（阶段 4 验收表第 19 行）：Key 的价格代表"实际执行"那一刻的成本；
// 未配置时按 UpstreamKey.PriceRuleID 的注释语义（0=继承）逐级回退。
//
// 计费安全：全程整数运算；乘法前判界（溢出则不写成本）；
// **不认识的计算单位一律不猜** —— 记未定价，而不是拿错单位硬算一个数出来。
func ResolveUsageCost(db *gorm.DB, keyID, offeringID, channelID int64, dimension int, quantityMilli int64, unitScale int, at int64) UsageCostSnapshot {
	snap := UsageCostSnapshot{CostState: UsageCostStateUnpriced, UnitScale: unitScale}
	if db == nil || quantityMilli <= 0 || !usageUnitMatchesDimension(dimension, unitScale) {
		return snap
	}
	rule, items := ResolveUsagePriceRule(db, keyID, offeringID, channelID, at)
	if rule == nil {
		return snap // 未定价：cost_usd_micro 保持 0
	}
	return resolveUsageCostFromRule(rule, items, dimension, quantityMilli, unitScale)
}

// ResolveUsageCostFromPriceRule 使用异步任务提交时冻结的价格版本计算成本。
// 历史任务不得在完成时改用新价格；快照不存在或分项不兼容时保持未定价。
func ResolveUsageCostFromPriceRule(db *gorm.DB, priceRuleID int64, dimension int, quantityMilli int64, unitScale int) UsageCostSnapshot {
	snap := UsageCostSnapshot{CostState: UsageCostStateUnpriced, UnitScale: unitScale}
	if db == nil || priceRuleID <= 0 || quantityMilli <= 0 || !usageUnitMatchesDimension(dimension, unitScale) {
		return snap
	}
	var rule PriceRule
	if err := db.Where("id = ?", priceRuleID).First(&rule).Error; err != nil {
		return snap
	}
	var items []PriceRuleItem
	if err := db.Where("price_rule_id = ?", priceRuleID).Find(&items).Error; err != nil {
		return snap
	}
	return resolveUsageCostFromRule(&rule, items, dimension, quantityMilli, unitScale)
}

func resolveUsageCostFromRule(rule *PriceRule, items []PriceRuleItem, dimension int, quantityMilli int64, unitScale int) UsageCostSnapshot {
	snap := UsageCostSnapshot{CostState: UsageCostStateUnpriced, UnitScale: unitScale}
	if rule == nil || quantityMilli <= 0 || !usageUnitMatchesDimension(dimension, unitScale) {
		return snap
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
		return snap
	}
	snap.PriceRuleItemID = item.ID
	snap.UnitPriceMicro = item.UnitPriceMicro
	snap.UnitScale = item.UnitScale
	divisorMilli, compatible := usagePriceDivisorMilli(dimension, item.UnitScale)
	if !compatible {
		return snap
	}
	if item.UnitPriceMicro == 0 {
		snap.CostState = UsageCostStateExplicitZero
		return snap
	}
	originalCost, ok := scaledUsageCost(quantityMilli, item.UnitPriceMicro, divisorMilli)
	if !ok {
		return snap
	}
	snap.CostMicro = originalCost
	usdUnit, ok := EffectiveUSDUnitPriceMicro(rule, item)
	if !ok {
		if item.Currency != "USD" {
			snap.CostState = UsageCostStatePendingFx
		}
		return snap
	}
	usdCost, ok := scaledUsageCost(quantityMilli, usdUnit, divisorMilli)
	if !ok {
		return UsageCostSnapshot{CostState: UsageCostStateUnpriced, UnitScale: unitScale}
	}
	snap.CostUsdMicro = usdCost
	snap.CostState = UsageCostStateReferenceEstimated
	return snap
}

func usageUnitMatchesDimension(dimension, unitScale int) bool {
	_, ok := usagePriceDivisorMilli(dimension, unitScale)
	return ok
}

func usagePriceDivisorMilli(dimension, unitScale int) (int64, bool) {
	switch dimension {
	case UsageDimensionInputTokens, UsageDimensionOutputTokens, UsageDimensionCacheReadTokens,
		UsageDimensionCacheWriteTokens, UsageDimensionReasoningTokens:
		switch unitScale {
		case UsageUnitScalePerMillionTokens:
			return 1_000_000 * 1000, true
		case UsageUnitScalePerThousandTokens:
			return 1_000 * 1000, true
		}
	case UsageDimensionImageCount:
		return 1000, unitScale == UsageUnitScalePerImage
	case UsageDimensionInputAudioSeconds, UsageDimensionOutputAudioSeconds, UsageDimensionOutputVideoSeconds:
		switch unitScale {
		case UsageUnitScalePerSecond:
			return 1000, true
		case UsageUnitScalePerMinute:
			return 60 * 1000, true
		}
	case UsageDimensionRequestCount:
		return 1000, unitScale == UsageUnitScalePerRequest
	case UsageDimensionInputCharacters:
		return 1_000 * 1000, unitScale == UsageUnitScalePerThousandCharacters
	case UsageDimensionDocumentCount:
		return 1000, unitScale == UsageUnitScalePerDocument
	}
	return 0, false
}

func scaledUsageCost(quantityMilli, unitPriceMicro, divisorMilli int64) (int64, bool) {
	if quantityMilli < 0 || unitPriceMicro < 0 || divisorMilli <= 0 {
		return 0, false
	}
	product := new(big.Int).Mul(big.NewInt(quantityMilli), big.NewInt(unitPriceMicro))
	product.Quo(product, big.NewInt(divisorMilli))
	if !product.IsInt64() {
		return 0, false
	}
	return product.Int64(), true
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
	// 校正是对同一条计量事实的替换值，不允许借校正改变归属、能力、维度或事件时间。
	// 若调用方传入的是既有校正行，仍统一指回根事件，确保多次校正形成同一组候选，
	// 汇总时只取该组最新一行，不会把校正链或并列校正重复相加。
	rootEventID := original.ID
	if original.Kind == UsageEventKindCorrection && original.CorrectionOfEventID > 0 {
		rootEventID = original.CorrectionOfEventID
	}
	corrected.Kind = UsageEventKindCorrection
	corrected.CorrectionOfEventID = rootEventID
	corrected.RequestID = original.RequestID
	corrected.AttemptID = original.AttemptID
	corrected.AsyncTaskID = original.AsyncTaskID
	corrected.UserID = original.UserID
	corrected.OwnerUserID = original.OwnerUserID
	corrected.ApiKeyID = original.ApiKeyID
	corrected.CanonicalModelID = original.CanonicalModelID
	corrected.ApiModelID = original.ApiModelID
	corrected.OfferingID = original.OfferingID
	corrected.ChannelID = original.ChannelID
	corrected.UpstreamKeyID = original.UpstreamKeyID
	corrected.UpstreamAccountID = original.UpstreamAccountID
	corrected.Capability = original.Capability
	corrected.Dimension = original.Dimension
	corrected.UnitScale = original.UnitScale
	corrected.CycleKey = original.CycleKey
	corrected.OccurredAt = original.OccurredAt
	return RecordUsageEvent(db, corrected)
}
