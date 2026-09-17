package channelpool

import (
	"sync"
	"time"

	"gorm.io/gorm"
)

// 索引映射的进程内缓存。
//
// # 为什么必须有
//
// `MapBaseKeyIndexes` 要读底座渠道并**解密该渠道的全部凭据**才能建立索引映射。
// 它被逐 Key 统计（每次请求结束）以及任何「按请求解析上游 Key 维度」的扩展调用，
// 每次都重算的话，热路径上会反复解密全部凭据——既慢，也让明文在内存里反复出现。
//
// # 安全权衡（必须记录）
//
// 缓存里包含 `IndexToKeyValue`（投影进底座的凭据明文）与 `IndexToStableKey`。
// 明文本来就要在投影/故障隔离时短暂存在，但缓存会**延长它的驻留时间**，
// 因此：TTL 取短值（30s），并保证**只在 `apply`（池子或渠道状态变化）时失效**；
// 绝不把该缓存写入日志、持久化或对外响应。
const mappingCacheTTL = 30 * time.Second

type cachedMapping struct {
	projection *Projection
	expiresAt  time.Time
}

var (
	mappingCacheMu sync.RWMutex
	mappingCache   = map[int64]cachedMapping{} // key = 分享渠道 ID
)

// MapBaseKeyIndexesCached 与 `MapBaseKeyIndexes` 等价，但带进程内缓存。
//
// 热路径（逐 Key 统计）应当用它；池子或渠道状态变化后由 `InvalidateMappingCache` 失效。
func MapBaseKeyIndexesCached(db *gorm.DB, shareChannelID int64) (*Projection, error) {
	now := time.Now()

	mappingCacheMu.RLock()
	if cached, ok := mappingCache[shareChannelID]; ok && now.Before(cached.expiresAt) {
		mappingCacheMu.RUnlock()
		return cached.projection, nil
	}
	mappingCacheMu.RUnlock()

	projection, err := MapBaseKeyIndexes(db, shareChannelID)
	if err != nil {
		return nil, err
	}

	mappingCacheMu.Lock()
	mappingCache[shareChannelID] = cachedMapping{projection: projection, expiresAt: now.Add(mappingCacheTTL)}
	mappingCacheMu.Unlock()
	return projection, nil
}

// InvalidateMappingCache 丢弃某渠道的映射缓存。
//
// **必须在池子或渠道状态变化后调用**（`apply` 之后、逐 Key 状态变更之后），
// 否则缓存会继续把索引翻译到变化前的凭据上。
func InvalidateMappingCache(shareChannelID int64) {
	mappingCacheMu.Lock()
	delete(mappingCache, shareChannelID)
	mappingCacheMu.Unlock()
}

// ResetMappingCacheForTest 清空全部映射缓存（仅测试使用）。
func ResetMappingCacheForTest() {
	mappingCacheMu.Lock()
	mappingCache = map[int64]cachedMapping{}
	mappingCacheMu.Unlock()
}
