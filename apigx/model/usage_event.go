package apigxmodel

import (
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

	// UsageDimensionInputTokens 计量维度：输入 token。
	UsageDimensionInputTokens int = 0
	// UsageDimensionOutputTokens 计量维度：输出 token。
	UsageDimensionOutputTokens int = 1

	// UsageUnitScalePerMillionTokens 单价计量单位：/1M tokens。
	UsageUnitScalePerMillionTokens int = 0

	// UsageTrustUpstreamReported 可信度：上游上报（非本地估算）。
	UsageTrustUpstreamReported int = 0

	// UsageCostStateUnpriced 成本状态：未定价（此状态下 cost_usd_micro 必须为 0）。
	UsageCostStateUnpriced int = 2
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
