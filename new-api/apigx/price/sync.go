package price

import (
	"errors"
	"fmt"

	apigxmodel "github.com/QuantumNous/new-api/apigx/model"
	"gorm.io/gorm"
)

// 单渠道价格同步（T2.2）。
//
// # 为什么作用域落到「供给」而不是全局
//
// 同一个上游模型名在不同分享渠道上就是**不同的供给**，价格本来就可以不同
// （计划书验收：价格变体不互相覆盖）。因此同步结果的落点是：
//
//	有对应供给 → 写到**供给**作用域（`PriceScopeOffering`）
//	没有供给   → 退化到**渠道**作用域（`PriceScopeChannel`）
//
// **任何情况下都不写全局作用域**——这正是验收条款
// 「单渠道同步不污染全局或其他分享者」在实现层的体现：
// 一次同步能触达的作用域只有"这个渠道"和"这个渠道自己的供给"。

// Reading 是一次同步中「某个上游模型」的价格读数。
type Reading struct {
	// UpstreamModelID 是**上游**模型名（与供给的 `upstream_model_id` 对齐）。
	UpstreamModelID string
	// 各维度价格，单位 USD per 1M tokens（底座内置口径）。0 表示该维度不单独计价。
	InputUSDPerMillion      float64
	OutputUSDPerMillion     float64
	CacheReadUSDPerMillion  float64
	CacheWriteUSDPerMillion float64
	// PerRequestUSD 按次价（USD/次，对应底座的 `model_price`）。
	// 与 token 价**互斥**：底座也是二选一表达。两者都给时以按次价为准（与底座一致）。
	PerRequestUSD float64
	// Source 价格来源（`PriceRuleSource*`）。
	Source int
	// SourceRef 来源标识（如 "models.dev/openai/gpt-4o"）。
	SourceRef string
	// RawRecord 原始记录（留存以便复核可疑版本）。
	RawRecord string
	// Currency 原币种；空视为 USD。
	Currency string
	// FxRateID/FxRateMicro 保存的汇率（非 USD 需要）。
	FxRateID    int64
	FxRateMicro int64
}

// SyncReport 汇总一次同步的结果。
type SyncReport struct {
	// Applied 已生效（新建或追加了生效版本）。
	Applied int
	// Pending 写入待确认（不生效，旧价继续用）。
	Pending int
	// Skipped 被跳过且没有写任何版本（例如人工锁定）。
	Skipped []string
	// Notes 记录每个被跳过/待确认模型的短原因。
	Notes map[string]string
}

// SyncChannelPrices 把一批价格读数同步到某个分享渠道（及其供给）的价格快照。
//
// 前置：调用方必须是该渠道的所有者（否则返回 `ErrNotFound`，与"渠道不存在"同义，
// 不泄露存在性）。
func SyncChannelPrices(db *gorm.DB, ownerUserID, shareChannelID int64, readings []Reading) (SyncReport, error) {
	report := SyncReport{Notes: map[string]string{}}
	if db == nil {
		return report, fmt.Errorf("%w: db is required", apigxmodel.ErrInvalidArgument)
	}
	if _, err := apigxmodel.GetChannel(db, ownerUserID, shareChannelID); err != nil {
		return report, err
	}

	// 该渠道自己的供给：建立"上游模型名 → 供给 ID"的映射，用于把价格落到供给作用域
	offerings, err := apigxmodel.ListModelOfferingsByChannel(db, ownerUserID, shareChannelID)
	if err != nil {
		return report, err
	}
	offeringByModel := make(map[string]int64, len(offerings))
	for i := range offerings {
		offeringByModel[offerings[i].UpstreamModelID] = offerings[i].ID
	}

	for _, reading := range readings {
		modelID := reading.UpstreamModelID
		if modelID == "" {
			continue
		}
		var items []apigxmodel.PriceRuleItem
		var mapErr error
		if reading.PerRequestUSD > 0 {
			// 按次计价：**不能静默丢掉**（丢了就等于把"按次收费"的模型当成免费）
			items, mapErr = perRequestItems(reading.PerRequestUSD)
		} else {
			items, mapErr = ItemsFromUSDPerMillion(
				reading.InputUSDPerMillion, reading.OutputUSDPerMillion,
				reading.CacheReadUSDPerMillion, reading.CacheWriteUSDPerMillion)
		}
		if mapErr != nil {
			// 口径错误（如把"每 1K"当"每 1M"）：**不写任何版本**，只报告
			report.Skipped = append(report.Skipped, modelID)
			report.Notes[modelID] = "implausible_price"
			continue
		}

		scopeType, scopeID := ChannelScope(shareChannelID)
		// 供给存在则落到供给作用域：价格天然是"某个渠道的某个模型"的属性
		if offeringID, ok := offeringByModel[modelID]; ok {
			scopeType, scopeID = apigxmodel.PriceScopeOffering, offeringID
		}

		currency := reading.Currency
		if currency == "" {
			currency = "USD"
		}
		pricingMode := apigxmodel.PriceModePerDimension
		if reading.PerRequestUSD > 0 {
			pricingMode = apigxmodel.PriceModePerRequest
		}
		// 有对应供给、或调用方给了价格读数的模型，视为"模型已知"；
		// 真正未知的模型（既无供给又无价格）根本不会出现在 readings 里。
		result, ingestErr := apigxmodel.IngestSourcePrice(db, apigxmodel.SourcePriceIngest{
			ScopeType: scopeType, ScopeID: scopeID,
			Source: reading.Source, SourceRef: reading.SourceRef,
			Currency: currency, PricingMode: pricingMode,
			Items: items, RawRecord: reading.RawRecord,
			ModelKnown: true,
			FxRateID:   reading.FxRateID, FxRateMicro: reading.FxRateMicro,
		})
		if ingestErr != nil {
			if errors.Is(ingestErr, apigxmodel.ErrPriceLocked) {
				// 人工覆盖：同步放弃，但**不是失败**——旧价继续用
				report.Skipped = append(report.Skipped, modelID)
				report.Notes[modelID] = "manual_override"
				continue
			}
			return report, ingestErr
		}
		if result.Applied {
			report.Applied++
			continue
		}
		report.Pending++
		report.Notes[modelID] = result.Note
	}
	return report, nil
}

// SourceRefFor 生成一条来源标识（来源名 + 上游模型名），便于事后追溯价格出处。
func SourceRefFor(sourceName, upstreamModelID string) string {
	if sourceName == "" {
		return upstreamModelID
	}
	return sourceName + "/" + upstreamModelID
}
