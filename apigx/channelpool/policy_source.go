package channelpool

import (
	"sync"
	"time"

	"github.com/QuantumNous/new-api/apigx/capacity"
	apigxmodel "github.com/QuantumNous/new-api/apigx/model"
	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// 容量策略的读取与缓存。
//
// # 为什么要有缓存
//
// 上限要在**每个请求**上解析（热路径），而策略存在数据库里。缓存按「分享渠道」维度存
// 已经解析好的**策略链**，TTL 取短值；策略变更（分享者改上限）最长一个 TTL 后生效——
// 这一点必须记录：容量上限不是「改了立刻全局生效」的配置，短暂延迟是可接受的，
// 因为它只影响准入的宽严，不会造成记账错误。

const policyCacheTTL = 30 * time.Second

type cachedPolicies struct {
	// concurrency 是并发维度的策略链（最具体在最前）。
	concurrency []capacity.Policy
	expiresAt   time.Time
}

var (
	policyCacheMu sync.RWMutex
	policyCache   = map[int64]cachedPolicies{} // key = 分享渠道 ID
)

// policyFromRaw 把「模式 + 值」的原始组合转成四态策略。
//
// 直接透传模式：**未配置（0）绝不被解释成「不限」**，解释权归 `capacity.Resolve`。
func policyFromRaw(mode int, value int64) capacity.Policy {
	return capacity.Policy{Mode: mode, Value: value}
}

// loadChannelConcurrencyChain 组装该分享渠道的并发上限策略链。
//
// 顺序是「最具体 → 最一般」：
//  1. 渠道自身的容量策略（`apigx_channels.capacity_policy_id` → `apigx_capacity_policies`）
//  2. 环境变量兜底（过渡期的部署级默认，值为 0 表示未配置）
//
// 两者都未配置时整条链仍未表态，`capacity.Resolve` 会返回 Unset —— 调用方据此
// **不施加约束**。这正是「未配置 ≠ 不限」在行为层的体现：它是待补的过渡状态，
// 而不是一个被解释成无限的默认值。
func loadChannelConcurrencyChain(db *gorm.DB, shareChannelID int64, envLimit int64) []capacity.Policy {
	chain := make([]capacity.Policy, 0, 2)

	if db != nil {
		var shareChannel apigxmodel.Channel
		if err := db.Where("id = ?", shareChannelID).First(&shareChannel).Error; err == nil && shareChannel.CapacityPolicyID > 0 {
			if policy, err := apigxmodel.GetCapacityPolicy(db, shareChannel.CapacityPolicyID); err == nil {
				mode, value := policy.Concurrency()
				chain = append(chain, policyFromRaw(mode, value))
			} else {
				common.SysError("channelpool: load capacity policy failed: " + err.Error())
			}
		}
	}

	if envLimit > 0 {
		chain = append(chain, capacity.Policy{Mode: capacity.LimitModeValue, Value: envLimit})
	} else {
		chain = append(chain, capacity.Policy{Mode: capacity.LimitModeUnset})
	}
	return chain
}

// ChannelConcurrencyChain 返回渠道并发维度的策略链（带缓存）。
func ChannelConcurrencyChain(db *gorm.DB, shareChannelID int64, envLimit int64) []capacity.Policy {
	now := time.Now()
	policyCacheMu.RLock()
	if cached, ok := policyCache[shareChannelID]; ok && now.Before(cached.expiresAt) {
		policyCacheMu.RUnlock()
		return cached.concurrency
	}
	policyCacheMu.RUnlock()

	chain := loadChannelConcurrencyChain(db, shareChannelID, envLimit)

	policyCacheMu.Lock()
	policyCache[shareChannelID] = cachedPolicies{concurrency: chain, expiresAt: now.Add(policyCacheTTL)}
	policyCacheMu.Unlock()
	return chain
}

// InvalidatePolicyCache 丢弃某渠道的策略缓存（策略变更、渠道变更后调用）。
func InvalidatePolicyCache(shareChannelID int64) {
	policyCacheMu.Lock()
	delete(policyCache, shareChannelID)
	policyCacheMu.Unlock()
}

// ResetPolicyCacheForTest 清空全部策略缓存（仅测试使用）。
func ResetPolicyCacheForTest() {
	policyCacheMu.Lock()
	policyCache = map[int64]cachedPolicies{}
	policyCacheMu.Unlock()
}

// ShareChannelIDByBase 按底座渠道 ID 找出我们的分享渠道 ID。
//
// 返回 false 表示这不是我们的分享渠道（例如管理员自建的普通渠道），
// 此时调用方应当只有环境变量兜底可用。
func ShareChannelIDByBase(db *gorm.DB, baseChannelID int) (int64, bool) {
	if db == nil || baseChannelID <= 0 {
		return 0, false
	}
	var shareChannel apigxmodel.Channel
	if err := db.Where("base_channel_id = ?", baseChannelID).First(&shareChannel).Error; err != nil {
		return 0, false
	}
	return shareChannel.ID, true
}
