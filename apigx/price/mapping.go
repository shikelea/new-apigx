// Package price 负责把「来源价格」映射成 APIGX 的分项单价快照（T2.2）。
//
// # 口径（与底座对齐，不另立一套）
//
// 底座内部有两种价格表达：
//   - **内置计价表达式**：直接就是 **USD per 1M tokens**（见 `setting/billing_setting/builtin_billing.go`
//     与 `relay/helper/price.go` 里 `rawCost / 1_000_000 * common.QuotaPerUnit` 的换算）；
//   - **旧倍率**（`model_ratio` / `completion_ratio` / 缓存倍率）：相对量，
//     与底座自己的 `common.QuotaPerUnit` 绑定。
//
// 两者都能映射到我们的分项单价 `UnitPriceMicro` + `UnitScale = /1M tokens`：
//
//	USD/1M  →  UnitPriceMicro = USD/1M × 1_000_000（微美元/1M tokens）
//	倍率 r  →  USD/1M = r × 1_000_000 / QuotaPerUnit
//
// 倍率那条**刻意用运行时的 `common.QuotaPerUnit`**，而不是写死经典的 "×2"：
// 写死会在 QuotaPerUnit 被改动时静默算错价格。
package price

import (
	"fmt"
	"math"

	apigxmodel "github.com/QuantumNous/new-api/apigx/model"
)

// 计量维度（与 `apigx_price_rule_items.dimension` 注释一致）。
const (
	DimensionInputTokens      = 0
	DimensionOutputTokens     = 1
	DimensionCacheReadTokens  = 2
	DimensionCacheWriteTokens = 3
	DimensionReasoningTokens  = 4
	DimensionRequestCount     = 9
)

// 单价单位（与 `apigx_price_rule_items.unit_scale` 注释一致）。
const (
	UnitScalePerMillionTokens = 0
	UnitScalePerRequest       = 5
)

// maxUnitPriceMicro 是单个分项单价的合理上限（微美元 / 1M tokens ≈ 1000 美元）。
//
// 设它是为了**尽早暴露解析错误**：同步来源把"每 1K tokens"当成"每 1M tokens"、
// 或者把人民币数值当美元塞进来时，会算出天文数字；宁可在写入前拒绝，
// 也不要让一个错价进入计费。
const maxUnitPriceMicro int64 = 1_000_000_000

// ErrPriceOutOfRange 表示映射出的单价超出合理范围（通常是来源口径弄错）。
var ErrPriceOutOfRange = fmt.Errorf("price: unit price out of range")

// usdPerMillionToMicro 把 USD/1M tokens 转成微美元/1M tokens。
func usdPerMillionToMicro(usdPerMillion float64) (int64, error) {
	if math.IsNaN(usdPerMillion) || math.IsInf(usdPerMillion, 0) || usdPerMillion < 0 {
		return 0, fmt.Errorf("%w: invalid usd value %v", apigxmodel.ErrInvalidArgument, usdPerMillion)
	}
	micro := usdPerMillion * 1_000_000
	if micro > float64(maxUnitPriceMicro) {
		return 0, fmt.Errorf("%w: %v USD per million is implausible", ErrPriceOutOfRange, usdPerMillion)
	}
	return int64(math.Round(micro)), nil
}

// ItemsFromUSDPerMillion 按「USD per 1M tokens」构造分项（底座内置价格的口径）。
//
// 传 0 表示该维度不单独计价，会被跳过（**不会**写成 0 元/1M 的分项，
// 因为"不适用"与"免费"是两件事：写成 0 会让它在估算里表现为免费）。
func ItemsFromUSDPerMillion(input, output, cacheRead, cacheWrite float64) ([]apigxmodel.PriceRuleItem, error) {
	type entry struct {
		dimension int
		usd       float64
	}
	all := []entry{
		{DimensionInputTokens, input},
		{DimensionOutputTokens, output},
		{DimensionCacheReadTokens, cacheRead},
		{DimensionCacheWriteTokens, cacheWrite},
	}
	// 先整体校验再取用：**0 表示"该维度不单独计价"，负数/NaN 则是数据错误**，
	// 两者必须区分——否则一个负价会被静默跳过，表现为"这个维度免费"。
	for _, e := range all {
		if math.IsNaN(e.usd) || math.IsInf(e.usd, 0) || e.usd < 0 {
			return nil, fmt.Errorf("%w: invalid usd value %v for dimension %d",
				apigxmodel.ErrInvalidArgument, e.usd, e.dimension)
		}
	}

	items := make([]apigxmodel.PriceRuleItem, 0, 4)
	for _, e := range all {
		if e.usd == 0 {
			continue
		}
		micro, err := usdPerMillionToMicro(e.usd)
		if err != nil {
			return nil, err
		}
		items = append(items, apigxmodel.PriceRuleItem{
			Dimension: e.dimension, UnitScale: UnitScalePerMillionTokens,
			UnitPriceMicro: micro, Currency: "USD",
		})
	}
	return items, nil
}

// ItemsFromRatios 按底座的**旧倍率**构造分项。
//
// `modelRatio` 是输入价的基础倍率；输出/缓存倍率沿用底座语义（相对 `modelRatio` 的倍数）。
// `quotaPerUnit` 必须传底座运行时的 `common.QuotaPerUnit`——倍率与美元的换算关系由它决定。
func ItemsFromRatios(modelRatio, completionRatio, cacheRatio float64, quotaPerUnit float64) ([]apigxmodel.PriceRuleItem, error) {
	if quotaPerUnit <= 0 {
		return nil, fmt.Errorf("%w: quota per unit must be positive", apigxmodel.ErrInvalidArgument)
	}
	if modelRatio < 0 || completionRatio < 0 || cacheRatio < 0 {
		return nil, fmt.Errorf("%w: negative ratio", apigxmodel.ErrInvalidArgument)
	}
	usdPerMillion := func(ratio float64) float64 { return ratio * 1_000_000 / quotaPerUnit }

	input := usdPerMillion(modelRatio)
	output := usdPerMillion(modelRatio * completionRatio)
	cacheRead := 0.0
	if cacheRatio > 0 {
		cacheRead = usdPerMillion(modelRatio * cacheRatio)
	}
	return ItemsFromUSDPerMillion(input, output, cacheRead, 0)
}

// ChannelScope 返回价格快照的「渠道」作用域标识。
//
// 价格按**渠道**（而非全局）作用域化，是计划书验收里
// 「单渠道同步不污染全局或其他分享者」的实现基础。
// 注意用的是**价格自己的**作用域常量（0=供给/1=上游 Key/2=渠道），不是容量那一套。
func ChannelScope(shareChannelID int64) (scopeType int, scopeID int64) {
	return apigxmodel.PriceScopeChannel, shareChannelID
}

// UpstreamKeyScope 返回价格快照的「上游 Key」作用域标识。
//
// 同一渠道池里各把 Key 的上游分组倍率可能不同，因此价格要能按 Key 作用域化
// （计划书验收：按实际执行 Key 的价格版本估算与预留，不借用池内最低价）。
func UpstreamKeyScope(upstreamKeyID int64) (scopeType int, scopeID int64) {
	return apigxmodel.PriceScopeUpstreamKey, upstreamKeyID
}
