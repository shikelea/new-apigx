package relayhook

import (
	"strings"
	"sync"
	"time"

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

// routeStrategyCacheTTL 是"合格集合"的缓存时长：算一次要读候选+质量+价格，
// 属每请求路径，故按 (模型, 策略) 缓存。
const routeStrategyCacheTTL = 30 * time.Second

type cachedStrategyChannels struct {
	channelIDs []int
	expiresAt  time.Time
}

var (
	routeStrategyCacheMu sync.RWMutex
	routeStrategyCache   = map[string]cachedStrategyChannels{}
)

// InvalidateRouteStrategyCache 清空策略合格集合缓存（策略变化或测试时用）。
func InvalidateRouteStrategyCache() {
	routeStrategyCacheMu.Lock()
	routeStrategyCache = map[string]cachedStrategyChannels{}
	routeStrategyCacheMu.Unlock()
}

// routeStrategyFilterEnabled 是"按 Key 的默认路由策略限定候选"的开关（默认关闭）。
func routeStrategyFilterEnabled() bool {
	return common.GetEnvOrDefaultBool("APIGX_ROUTE_STRATEGY_ENABLED", false)
}

// EligibleBaseChannelsForStrategy 按调用者平台 Key 的默认路由策略算出本次的合格渠道集合。
//
// 返回 ok=false 表示**不要加限定**（未开启、未配置策略、策略为 auto、或算不出候选）——
// 宁可退回底座默认选路，也不要因为算不出来就把请求打成"没有合格渠道"。
func EligibleBaseChannelsForStrategy(c *gin.Context, modelName string) ([]int, bool) {
	if !routeStrategyFilterEnabled() || c == nil || strings.TrimSpace(modelName) == "" {
		return nil, false
	}
	tokenID := common.GetContextKeyInt(c, constant.ContextKeyTokenId)
	userID := common.GetContextKeyInt(c, constant.ContextKeyUserId)
	if tokenID <= 0 || userID <= 0 {
		return nil, false
	}
	db := channelpool.CurrentDB()
	if db == nil {
		return nil, false
	}
	key, found := loadPreferences(db, int64(userID), int64(tokenID))
	if !found || key == nil {
		return nil, false
	}
	strategy, ok := parseRouteStrategy(key.DefaultRouteMode)
	if !ok {
		return nil, false // 空/auto/无法识别：不加限定
	}

	cacheKey := modelName + "|" + key.DefaultRouteMode
	now := time.Now()
	routeStrategyCacheMu.RLock()
	cached, exists := routeStrategyCache[cacheKey]
	routeStrategyCacheMu.RUnlock()
	if exists && now.Before(cached.expiresAt) {
		if len(cached.channelIDs) == 0 {
			return nil, false
		}
		return cached.channelIDs, true
	}

	channelIDs := computeEligibleBaseChannels(db, modelName, strategy)
	routeStrategyCacheMu.Lock()
	routeStrategyCache[cacheKey] = cachedStrategyChannels{channelIDs: channelIDs, expiresAt: now.Add(routeStrategyCacheTTL)}
	routeStrategyCacheMu.Unlock()
	if len(channelIDs) == 0 {
		return nil, false
	}
	return channelIDs, true
}

// computeEligibleBaseChannels 算出"最优名次那一档"对应的底座渠道 ID。
//
// 与解释接口、应用接口**共用同一套候选装配与熔断口径**，避免出现
// "解释里它在候选里、实际选路却不考虑它"这类不一致。
func computeEligibleBaseChannels(db *gorm.DB, modelName string, strategy routing.Strategy) []int {
	candidates, _, err := routing.AssembleCandidates(db, capacity.New(common.RDB), modelName, routing.AssembleOptions{})
	if err != nil || len(candidates) == 0 {
		return nil
	}
	now := common.GetTimestamp()
	for i := range candidates {
		if routing.EvaluateBreaker(candidates[i].Quality, now, routing.DefaultBreakerConfig()).State == routing.BreakerOpen {
			candidates[i].Allowed = false
			candidates[i].ExcludeReason = routing.ExcludeBreakerOpen
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
// 「空」与「auto」都表示**不加限定**（计划书：最后使用系统默认 auto）。
func parseRouteStrategy(mode string) (routing.Strategy, bool) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
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
