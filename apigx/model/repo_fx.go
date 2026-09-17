package apigxmodel

import (
	"errors"
	"fmt"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// 汇率快照（`apigx_fx_rates`，T2.3）。
//
// # 为什么必须"存下来"而不是每次现查
//
// 计划书验收要求：**非美元价格按保存的汇率归一**、且**历史记录不随汇率更新改写**。
// 价格规则在写入时就带上 `FxRateID` + `FxRateMicro`，因此：
//   - 汇率后来怎么变，都不会改变已落库的历史价格；
//   - 事后能回答"当时用的是哪一次汇率"（凭 `FxRateID` 回溯）。
//
// # 方向只有一个，避免歧义
//
// `ConversionDirection = 0` 固定含义：**USD = quote / rate**
// （`RateMicro` 表示 1 USD 等于多少原币：7.123456 CNY → 7123456）。
// 只有一种方向，就不存在"这次是乘还是除"的口径分歧。

// FxDirectionUSDEqualsQuoteDivRate 是唯一支持的换算方向。
const FxDirectionUSDEqualsQuoteDivRate int = 0

// ErrNoFxRate 表示没有可用的汇率。
//
// 调用方**必须**把它当成"USD 价格未知"处理——绝不能退回 1:1，
// 那会把 7.2 CNY 当成 7.2 USD，属于静默的严重错价。
var ErrNoFxRate = errors.New("apigxmodel: no fx rate for currency")

// CreateFxRateSnapshot 追加一条汇率快照，并**封版**上一条（单向，只允许一次）。
//
// 封版不是可选的：同一币种若同时存在两条生效汇率，"用哪一条"就变成未定义行为。
func CreateFxRateSnapshot(db *gorm.DB, rate *FxRate) error {
	if db == nil || rate == nil {
		return fmt.Errorf("%w: rate is required", ErrInvalidArgument)
	}
	if len(rate.QuoteCurrency) != 3 {
		return fmt.Errorf("%w: quote currency must be a 3-letter code", ErrInvalidArgument)
	}
	if rate.QuoteCurrency == "USD" {
		// USD 对自己不需要汇率；允许写入会让"1 USD = ? USD"出现两种答案
		return fmt.Errorf("%w: USD does not need an fx rate", ErrInvalidArgument)
	}
	if rate.RateMicro <= 0 {
		return fmt.Errorf("%w: rate must be positive", ErrInvalidArgument)
	}
	if rate.BaseCurrency == "" {
		rate.BaseCurrency = "USD"
	}
	if rate.BaseCurrency != "USD" {
		return fmt.Errorf("%w: base currency must be USD", ErrInvalidArgument)
	}
	if rate.ConversionDirection != FxDirectionUSDEqualsQuoteDivRate {
		return fmt.Errorf("%w: unsupported conversion direction %d", ErrInvalidArgument, rate.ConversionDirection)
	}
	if rate.EffectiveFrom == 0 {
		rate.EffectiveFrom = common.GetTimestamp()
	}
	rate.CreatedAt = common.GetTimestamp()

	return db.Transaction(func(tx *gorm.DB) error {
		// 封版上一条（EffectiveTo == 0 的那条）
		sealErr := tx.Model(&FxRate{}).
			Where("quote_currency = ? AND effective_to = 0", rate.QuoteCurrency).
			Update("effective_to", rate.EffectiveFrom).Error
		if sealErr != nil {
			return sealErr
		}
		return tx.Create(rate).Error
	})
}

// GetEffectiveFxRate 取某币种在给定时刻生效的汇率。没有则返回 `ErrNoFxRate`。
func GetEffectiveFxRate(db *gorm.DB, quoteCurrency string, at int64) (*FxRate, error) {
	if db == nil || len(quoteCurrency) != 3 {
		return nil, fmt.Errorf("%w: quote currency is required", ErrInvalidArgument)
	}
	if at == 0 {
		at = common.GetTimestamp()
	}
	var rate FxRate
	err := db.Where("quote_currency = ? AND base_currency = ? AND effective_from <= ?",
		quoteCurrency, "USD", at).
		Where("effective_to = 0 OR effective_to > ?", at).
		Order("effective_from DESC").First(&rate).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNoFxRate
	}
	if err != nil {
		return nil, err
	}
	return &rate, nil
}

// QuoteToUSDMicro 按给定汇率把原币金额（微单位）换算成 USD（微单位）。
//
// 方向依据模型注释：`USD = quote / rate`。汇率缺失/非法时返回 `ok=false`——
// **绝不退回 1:1**。
func QuoteToUSDMicro(rate *FxRate, quoteMicro int64) (usdMicro int64, ok bool) {
	if rate == nil || rate.RateMicro <= 0 {
		return 0, false
	}
	if rate.ConversionDirection != FxDirectionUSDEqualsQuoteDivRate {
		return 0, false
	}
	return quoteMicro * 1_000_000 / rate.RateMicro, true
}

// USDToQuoteMicro 是 `QuoteToUSDMicro` 的反向换算（USD = quote / rate ⇒ quote = USD × rate）。
func USDToQuoteMicro(rate *FxRate, usdMicro int64) (quoteMicro int64, ok bool) {
	if rate == nil || rate.RateMicro <= 0 {
		return 0, false
	}
	if rate.ConversionDirection != FxDirectionUSDEqualsQuoteDivRate {
		return 0, false
	}
	return usdMicro * rate.RateMicro / 1_000_000, true
}
