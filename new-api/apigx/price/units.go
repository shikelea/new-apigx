package price

import (
	"fmt"

	apigxmodel "github.com/QuantumNous/new-api/apigx/model"
)

// 多单位估算（T2.3）：把不同「单价单位」的分项换算到同一口径，才能比较与合并。
//
// 单位的取值定义在 `apigx_price_rule_items.unit_scale` 的注释里。
// 这里只做**同族之间**的换算：
//
//	token 族：/1M tokens ↔ /1K tokens
//	时间族  ：/分钟 ↔ /秒
//
// 跨族（例如 /张 与 /1M tokens）**不可换算**，必须报错而不是猜一个系数——
// 猜错就是错价，而且不会有任何报错留下。

// 单价单位（与 `unit_scale` 注释一致）。
const (
	UnitScalePerThousandTokens = 1
	UnitScalePerImage          = 2
	UnitScalePerSecond         = 3
	UnitScalePerMinute         = 4
	UnitScalePerThousandChars  = 6
	UnitScalePerDocument       = 7
)

// ErrUnitNotConvertible 表示两个单位不在同一族，不能换算。
var ErrUnitNotConvertible = fmt.Errorf("price: unit scales are not convertible")

// ConvertUnitPriceMicro 把单价在单位之间换算。
//
// 例：`/1K tokens` 的 0.002 USD → `/1M tokens` 的 2 USD（×1000）；
// `/秒` 的 0.01 USD → `/分钟` 的 0.6 USD（×60）。
func ConvertUnitPriceMicro(priceMicro int64, from, to int) (int64, error) {
	if from == to {
		return priceMicro, nil
	}
	if priceMicro < 0 {
		return 0, fmt.Errorf("%w: negative unit price", apigxmodel.ErrInvalidArgument)
	}

	// 每单位对应的"基数"：换算系数 = fromBase / toBase
	base, ok := unitBase(from)
	if !ok {
		return 0, fmt.Errorf("%w: unknown unit scale %d", apigxmodel.ErrInvalidArgument, from)
	}
	toBase, ok := unitBase(to)
	if !ok {
		return 0, fmt.Errorf("%w: unknown unit scale %d", apigxmodel.ErrInvalidArgument, to)
	}
	if base.family != toBase.family {
		return 0, fmt.Errorf("%w: %d (%s) -> %d (%s)", ErrUnitNotConvertible, from, base.family, to, toBase.family)
	}

	// priceMicro 是"每个 from 单位"的价格；先化成"每个基准单位"，再乘回 to 的规模
	perBase := float64(priceMicro) * float64(toBase.size) / float64(base.size)
	return int64(perBase + 0.5), nil
}

// NormalizeToCanonicalUnit 把单价换算到该族的**基准单位**
// （token 族 → /1M tokens；时间族 → /秒），便于跨来源比较。
func NormalizeToCanonicalUnit(priceMicro int64, from int) (int64, error) {
	base, ok := unitBase(from)
	if !ok {
		return 0, fmt.Errorf("%w: unknown unit scale %d", apigxmodel.ErrInvalidArgument, from)
	}
	return ConvertUnitPriceMicro(priceMicro, from, base.canonical)
}

type unitInfo struct {
	// family 是同族标识：只有同族之间才能换算。
	family string
	// size 是该单位相对于同族基准的规模（基准单位 size = 1）。
	size int
	// canonical 是该族的基准单位（比较时统一换算到它）。
	canonical int
}

// unitBase 返回单位的分族与规模。
//
// 只有**确定无疑**的换算才写进来：token 的 1M/1K 与时间的 分钟/秒 是纯数量级关系；
// 张/次/文档之间没有任何可靠关系，因此各自独立成族（等于不可换算）。
func unitBase(unit int) (unitInfo, bool) {
	switch unit {
	case UnitScalePerMillionTokens:
		return unitInfo{family: "tokens", size: 1_000_000, canonical: UnitScalePerMillionTokens}, true
	case UnitScalePerThousandTokens:
		return unitInfo{family: "tokens", size: 1_000, canonical: UnitScalePerMillionTokens}, true
	case UnitScalePerSecond:
		return unitInfo{family: "time", size: 1, canonical: UnitScalePerSecond}, true
	case UnitScalePerMinute:
		return unitInfo{family: "time", size: 60, canonical: UnitScalePerSecond}, true
	case UnitScalePerImage:
		return unitInfo{family: "image", size: 1, canonical: UnitScalePerImage}, true
	case UnitScalePerThousandChars:
		return unitInfo{family: "chars", size: 1_000, canonical: UnitScalePerThousandChars}, true
	case UnitScalePerDocument:
		return unitInfo{family: "document", size: 1, canonical: UnitScalePerDocument}, true
	case UnitScalePerRequest:
		return unitInfo{family: "request", size: 1, canonical: UnitScalePerRequest}, true
	default:
		return unitInfo{}, false
	}
}
