package price

import (
	"strconv"

	apigxmodel "github.com/QuantumNous/new-api/apigx/model"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
)

// 从**底座既有价格设置**读取价格读数（T2.2 收尾）。
//
// # 为什么不自己维护一份价格表
//
// 底座的 `setting/ratio_setting` 与 `setting/billing_setting` 已经承载了模型价格：
//   - `GetModelPrice`     按次价（USD/次，`model_price`）
//   - `GetModelRatio`     输入倍率；`GetCompletionRatio` 输出倍率
//   - `GetCacheRatio` / `GetCreateCacheRatio` 缓存读/写倍率
//   - `GetBillingExpr`    计价表达式（USD per 1M tokens）
//
// 管理员在底座后台维护的价目表、以及底座自身的同步任务写进去的结果，都会反映在这些入口上。
// 我们的职责是**把它们读出来、按我们的口径落成带来源与版本的快照**，
// 而不是再建一份价目表——那会立刻产生"两处价格不一致"的问题。
//
// 注意：这里**不使用 `effectiveQuota`/group ratio 等请求期放大系数**。
// 那些是"某个用户在某次请求上实际付多少"的倍率，属于计费链路，不是**模型单价**；
// 把它们混进单价会让"同一模型在同一渠道的价格"随请求而变，无法作为快照沉淀。

// ReadModelPriceFromBase 读取某模型在底座设置里的价格读数。
//
// 返回 false 表示底座**没有**这个模型的价格（此时调用方必须按"价格未知"处理，
// 绝不能当成 0 元）。
func ReadModelPriceFromBase(modelName string) (Reading, bool) {
	if modelName == "" {
		return Reading{}, false
	}

	// ① 按次价优先：底座把它表达为独立的 model_price，与 token 价互斥
	if perRequest, ok := ratio_setting.GetModelPrice(modelName, false); ok && perRequest > 0 {
		return Reading{
			UpstreamModelID: modelName,
			PerRequestUSD:   perRequest,
			Source:          apigxmodel.PriceRuleSourceBuiltinPreset,
			SourceRef:       SourceRefFor("new-api/model_price", modelName),
		}, true
	}

	// ② token 倍率
	modelRatio, ok, _ := ratio_setting.GetModelRatio(modelName)
	if !ok || modelRatio <= 0 {
		return Reading{}, false
	}
	completionRatio := ratio_setting.GetCompletionRatio(modelName)
	cacheRatio, _ := ratio_setting.GetCacheRatio(modelName)
	createCacheRatio, _ := ratio_setting.GetCreateCacheRatio(modelName)

	// 倍率 → USD/1M tokens（用**运行时** QuotaPerUnit，与底座口径一致）
	perMillion := func(ratio float64) float64 {
		if common.QuotaPerUnit <= 0 {
			return 0
		}
		return ratio * 1_000_000 / common.QuotaPerUnit
	}

	return Reading{
		UpstreamModelID:         modelName,
		InputUSDPerMillion:      perMillion(modelRatio),
		OutputUSDPerMillion:     perMillion(modelRatio * completionRatio),
		CacheReadUSDPerMillion:  perMillion(modelRatio * cacheRatio),
		CacheWriteUSDPerMillion: perMillion(modelRatio * createCacheRatio),
		Source:                  apigxmodel.PriceRuleSourceBuiltinPreset,
		SourceRef:               SourceRefFor("new-api/model_ratio", modelName),
		RawRecord: "model_ratio=" + strconv.FormatFloat(modelRatio, 'f', -1, 64) +
			";completion_ratio=" + strconv.FormatFloat(completionRatio, 'f', -1, 64),
	}, true
}

// ReadModelPriceFromBillingExpr 读取某模型**计价表达式**的存在性（USD per 1M tokens 口径）。
//
// 表达式本身由底座的计费引擎解释；这里只用来判定"底座是否已经为这个模型配了价格"，
// 从而区分「价格未知」与「价格为 0」。表达式内部的具体数值不在本阶段解析——
// 那需要复刻计费引擎的语法，属于"造轮子"，我们不做。
func ReadModelPriceFromBillingExpr(modelName string) (expr string, ok bool) {
	if modelName == "" {
		return "", false
	}
	if expression, found := billing_setting.GetBillingExpr(modelName); found {
		return expression, true
	}
	return billing_setting.GetBuiltinBillingExpr(modelName)
}
