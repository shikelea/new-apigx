package routing

import (
	"context"
	"fmt"

	"github.com/QuantumNous/new-api/apigx/capacity"
	apigxmodel "github.com/QuantumNous/new-api/apigx/model"
	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// 候选装配（T2.9 的输入端）：把「某模型的所有已发布供给」组装成可决策的候选集。
//
// 这一层的职责边界很明确：
//   - **只读**：不写库、不改状态、不投影；
//   - **只碰自己的数据**：价格、质量、容量都从 APIGX 的表与 Redis 读；
//   - **把"未知"原样带出去**：价格没有快照就是 `PriceKnown=false`，质量没样本就是 `SampleCount=0`，
//     绝不在这里补默认值（那会把"未知"变成"零"或"中等"，是可解释性最怕的事）。

// AssembleOptions 控制装配范围。
type AssembleOptions struct {
	// WindowKind 质量窗口（默认 6 小时，与计划书一致）。
	WindowKind int
	// Now 注入"当前时间"，便于测试；0 表示取当前。
	Now int64
	// Breaker 是熔断阈值（T2.8）；零值取 `DefaultBreakerConfig`。
	Breaker BreakerConfig
	// AllowedOfferingIDs 是调用主体已通过 ACL 与平台 Key 范围求交的供给集合。
	// nil 表示调用方没有施加权限过滤；非 nil 的空 map 表示明确没有任何授权供给。
	AllowedOfferingIDs map[int64]struct{}
}

// AssembleCandidates 为某模型装配候选集。
//
// 返回值里的 public map 是**对外标识**，供解释层输出；内部 ID 只留在 Candidate 里。
func AssembleCandidates(db *gorm.DB, ctrl *capacity.Controller, modelName string,
	opts AssembleOptions) ([]Candidate, map[int64]CandidatePublicInfo, error) {
	if db == nil || modelName == "" {
		return nil, map[int64]CandidatePublicInfo{}, nil
	}
	if opts.WindowKind == 0 {
		opts.WindowKind = apigxmodel.QualityWindowSixHour
	}
	if opts.Now == 0 {
		opts.Now = common.GetTimestamp()
	}

	offerings, err := apigxmodel.ListPublishedOfferingsByModel(db, modelName)
	if err != nil {
		return nil, nil, err
	}

	candidates := make([]Candidate, 0, len(offerings))
	public := make(map[int64]CandidatePublicInfo, len(offerings))

	for i := range offerings {
		offering := &offerings[i]
		// 授权必须先于价格、质量、容量与熔断读取；无权资源不进入评分，也不进入解释输出。
		if opts.AllowedOfferingIDs != nil {
			if _, allowed := opts.AllowedOfferingIDs[offering.ID]; !allowed {
				continue
			}
		}
		var shareChannel apigxmodel.Channel
		if err := db.Where("id = ?", offering.ChannelID).First(&shareChannel).Error; err != nil {
			continue // 渠道已不可用：不进入候选（也不会出现在解释里）
		}
		public[offering.ID] = CandidatePublicInfo{
			OfferingPublicID: offering.PublicID,
			ChannelPublicID:  shareChannel.PublicID,
			OwnerUserID:      shareChannel.OwnerUserID,
			ModelName:        offering.UpstreamModelID,
		}

		candidate := Candidate{
			OfferingID:     offering.ID,
			ShareChannelID: offering.ChannelID,
			OwnerUserID:    shareChannel.OwnerUserID,
			Allowed:        true,
		}

		// 价格：先看**供给**作用域，没有再看**渠道**作用域；都未知就保持 PriceKnown=false
		if rule, items, priceErr := apigxmodel.GetEffectivePriceRule(db, apigxmodel.PriceScopeOffering, offering.ID, opts.Now); priceErr == nil {
			fillPriceFromRule(&candidate, rule, items)
		} else if rule, items, channelErr := apigxmodel.GetEffectivePriceRule(db, apigxmodel.PriceScopeChannel, offering.ChannelID, opts.Now); channelErr == nil {
			fillPriceFromRule(&candidate, rule, items)
		}

		// 质量：窗口内无样本时视图是"空的"（SampleCount=0 且 Insufficient）
		view, viewErr := apigxmodel.ResolveQualityView(db, offering.ID, opts.WindowKind, opts.Now)
		if viewErr == nil {
			candidate.Quality = view
		}

		// 熔断（T2.8）：失败率过高且冷却未过 → **从候选中摘除**（原因写清，便于解释）；
		// 冷却已过则为半开（放行试探），不永久封禁。
		breaker := EvaluateBreaker(candidate.Quality, opts.Now, opts.Breaker)
		if breaker.State == BreakerOpen {
			candidate.Allowed = false
			candidate.ExcludeReason = ExcludeBreakerOpen
			// 阶段 4「告警」：熔断打开时发一条**限频**运维告警（同一供给 10 分钟最多一次），
			// 否则熔断期内每个请求都会走到这里，日志会被刷爆反而掩盖问题。
			if claimBreakerAlertSlot(offering.ID, opts.Now) {
				common.SysError(fmt.Sprintf(
					"apigx alert: offering %s breaker OPEN — 候选已摘除，冷却结束前不会承接流量",
					public[offering.ID].OfferingPublicID,
				))
			}
		}

		// 容量：读渠道策略给出的上限与当前占用（未配置上限则不施加约束）
		fillCapacity(candidate.OfferingID, &candidate, shareChannel, ctrl, db)
		candidates = append(candidates, candidate)
	}
	return candidates, public, nil
}

// fillPriceFromRule 用价格快照填充候选的价格字段。
//
// **只填"可用"的价格**：未知分项留 0 且不置 PriceKnown，由解释层如实标注。
func fillPriceFromRule(candidate *Candidate, rule *apigxmodel.PriceRule, items []apigxmodel.PriceRuleItem) {
	known := false
	for i := range items {
		item := &items[i]
		price, ok := apigxmodel.EffectiveUSDUnitPriceMicro(rule, item)
		if !ok {
			continue
		}
		switch item.Dimension {
		case 0: // 输入 token
			candidate.InputPriceMicro = price
			known = true
		case 1: // 输出 token
			candidate.OutputPriceMicro = price
			known = true
		}
	}
	candidate.PriceKnown = known
}

// fillCapacity 填候选的容量上限与当前占用。
//
// 上限来自渠道的容量策略（四态解析）；占用从 Redis 计数读。
// 读不到占用时**不假设为空閒**（那会让满载渠道看起来可用），而是保持 InUse=0 并在
// 解释里由"无样本/无数据"体现；这里选择保守：只有读到占用才填。
func fillCapacity(offeringID int64, candidate *Candidate, shareChannel apigxmodel.Channel,
	ctrl *capacity.Controller, db *gorm.DB) {
	if shareChannel.CapacityPolicyID <= 0 {
		return
	}
	policy, err := apigxmodel.GetCapacityPolicy(db, shareChannel.CapacityPolicyID)
	if err != nil {
		return
	}
	mode, value := policy.Concurrency()
	resolved, err := capacity.Resolve(capacity.Policy{Mode: mode, Value: value})
	if err != nil || resolved.Decision != capacity.DecisionLimited || resolved.Value <= 0 {
		return
	}
	candidate.CapacityLimit = resolved.Value
	if ctrl == nil {
		return
	}
	used, usageErr := ctrl.Usage(context.Background(),
		capacity.ChannelConcurrency(shareChannel.BaseChannelID))
	if usageErr == nil {
		candidate.CapacityInUse = used
	}
}
