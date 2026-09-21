package relayhook

import (
	"strings"

	"github.com/QuantumNous/new-api/apigx/capacity"
	"github.com/QuantumNous/new-api/apigx/channelpool"
	apigxmodel "github.com/QuantumNous/new-api/apigx/model"
	"github.com/QuantumNous/new-api/apigx/routing"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// 用户按请求选择路由策略（T2.7 用户侧，阶段 2 的最后一环）。
//
// # 做法：只算"合格集合"，不改底座选择器
//
// 底座的选渠道发生在 `CacheGetRandomSatisfiedChannel` 内部，我们**不改写它**；
// 但底座已经提供了**请求级候选过滤器**（`dto.ChannelFilter`，经
// `service.GetChannelConstraints(c)` 收集、在选择时统一应用）。因此这里只做一件事：
// 按调用者（平台 Key）的默认路由策略算出**本次该用哪些渠道**，由分发器加成过滤器。
//
// 这与计划书的措辞一致：「在满足硬限制的授权候选中，按调用者所选策略分别使用延迟 P50、
// 吞吐中位数或可靠性置信下界**排序**」「只选符合请求要求的渠道」。
//
// # 为什么返回"集合"而不是"一个渠道"
//
// 只保留**同分（同一 Rank）**的候选：策略决定"谁合格"，而**权重**继续负责在合格者之间
// 分散流量（计划书：渠道权重用于相近评分的流量分散）。这样策略不会退化成"永远只用一条"。
//
// 本函数**不引入对 service 的依赖**（那会形成 import 循环）：只返回渠道 ID，加过滤器由 middleware 完成。

// routeStrategyFilterEnabled 是"按 Key 的默认路由策略限定候选"的开关（默认关闭）。
func routeStrategyFilterEnabled() bool {
	return common.GetEnvOrDefaultBool("APIGX_ROUTE_STRATEGY_ENABLED", false)
}

// EligibleBaseChannelsForStrategy 按调用者平台 Key 的默认路由策略算出本次的合格渠道集合。
//
// 返回 ok=false 只表示功能未开启、没有平台 Key 偏好或当前不是 APIGX 请求。
// 一旦平台 Key 选择了策略（空值也表示默认 auto），即使没有合格候选也返回 ok=true
// 和空列表，让请求明确失败；不能静默退回底座随机而伪装成策略已经生效。
func EligibleBaseChannelsForStrategy(c *gin.Context, modelName string) ([]int, bool, error) {
	if !routeStrategyFilterEnabled() || c == nil || strings.TrimSpace(modelName) == "" {
		return nil, false, nil
	}
	tokenID := common.GetContextKeyInt(c, constant.ContextKeyTokenId)
	userID := common.GetContextKeyInt(c, constant.ContextKeyUserId)
	if tokenID <= 0 || userID <= 0 {
		return nil, false, nil
	}
	db := channelpool.CurrentDB()
	if db == nil {
		return nil, false, nil
	}
	key, found, err := loadPreferences(db, int64(userID), int64(tokenID))
	if err != nil {
		_, err = handleSourceAccessDBError(err)
		return nil, false, err
	}
	if !found || key == nil {
		return nil, false, nil
	}
	strategy, ok := parseRouteStrategy(key.DefaultRouteMode)
	if !ok {
		return nil, true, nil // 数据库中出现未知旧值时失败关闭，避免静默随机
	}
	allowedOfferingIDs, accessActive, err := AllowedOfferingIDsForRequest(c, modelName)
	if err != nil {
		return nil, false, err
	}
	if !accessActive {
		allowedOfferingIDs = nil
	}

	// 候选包含 Redis 中的当前并发占用，不能缓存最终优胜集合；否则渠道在缓存期内满载后，
	// 请求仍会不断命中它并在原子预留处 429，而不会改选仍有余量的渠道。
	channelIDs := computeEligibleBaseChannels(db, modelName, strategy, allowedOfferingIDs)
	if len(channelIDs) == 0 {
		return []int{}, true, nil
	}
	return channelIDs, true, nil
}

// computeEligibleBaseChannels 算出"最优名次那一档"对应的底座渠道 ID。
//
// 与解释接口、应用接口**共用同一套候选装配与熔断口径**，避免出现
// "解释里它在候选里、实际选路却不考虑它"这类不一致。
func computeEligibleBaseChannels(db *gorm.DB, modelName string, strategy routing.Strategy, allowedOfferingIDs map[int64]struct{}) []int {
	candidates, _, err := routing.AssembleCandidates(db, capacity.New(common.RDB), modelName, routing.AssembleOptions{
		AllowedOfferingIDs: allowedOfferingIDs,
	})
	if err != nil || len(candidates) == 0 {
		return nil
	}
	now := common.GetTimestamp()
	for i := range candidates {
		if routing.EvaluateBreaker(candidates[i].Quality, now, routing.DefaultBreakerConfig()).State == routing.BreakerOpen {
			candidates[i].Allowed = false
			candidates[i].ExcludeReason = routing.ExcludeBreakerOpen
			// The strategy filter runs before New API chooses a base channel. Once
			// this quality verdict excludes the source, CheckBreakerAllows will no
			// longer see that source and cannot establish durable recovery ownership.
			// Persist and project the open breaker at the decision boundary instead.
			if breakerEnabled() {
				blocked, blockErr := apigxmodel.BreakerProbeBlocksTraffic(db, candidates[i].OfferingID)
				if blockErr != nil || !blocked {
					baseChannelID := baseChannelIDForShareChannel(db, candidates[i].ShareChannelID)
					if baseChannelID > 0 {
						syncBreakerMarks(db, baseChannelID, candidates[i].OfferingID, true)
					}
				}
			}
		}
	}
	decision, err := routing.Decide(strategy, candidates, routing.DefaultOptions())
	if err != nil || len(decision.Ranked) == 0 {
		return nil
	}
	topRank := decision.Ranked[0].Rank
	channelIDs := make([]int, 0, len(decision.Ranked))
	seen := map[int]struct{}{}
	for i := range decision.Ranked {
		ranked := decision.Ranked[i]
		if ranked.Rank != topRank {
			continue
		}
		baseChannelID := baseChannelIDForShareChannel(db, ranked.Candidate.ShareChannelID)
		if baseChannelID <= 0 {
			continue
		}
		if _, dup := seen[baseChannelID]; dup {
			continue
		}
		seen[baseChannelID] = struct{}{}
		channelIDs = append(channelIDs, baseChannelID)
	}
	return channelIDs
}

// baseChannelIDForShareChannel 把我们的分享渠道映射为它绑定的底座渠道 ID。
func baseChannelIDForShareChannel(db *gorm.DB, shareChannelID int64) int {
	if db == nil || shareChannelID <= 0 {
		return 0
	}
	var channel apigxmodel.Channel
	if err := db.Where("id = ?", shareChannelID).First(&channel).Error; err != nil {
		return 0
	}
	if channel.BaseChannelID <= 0 {
		return 0
	}
	return int(channel.BaseChannelID)
}

// parseRouteStrategy 把平台 Key 上的默认路由模式解析为策略。
//
// 「空」与「auto」都表示综合自动；空值是历史平台 Key 的默认存储形式。
func parseRouteStrategy(mode string) (routing.Strategy, bool) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", "auto":
		return routing.StrategyAuto, true
	case "latency":
		return routing.StrategyLatency, true
	case "throughput":
		return routing.StrategyThroughput, true
	case "reliability":
		return routing.StrategyReliability, true
	default:
		return 0, false
	}
}
