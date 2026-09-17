package channelpool

import (
	"errors"

	"github.com/QuantumNous/new-api/apigx/capacity"
	apigxmodel "github.com/QuantumNous/new-api/apigx/model"
	"gorm.io/gorm"
)

// KeyCapacityLimits 是上游 Key 与账户池两个维度的并发上限。
//
// 语义遵循四态：**0 表示未配置**（不施加约束），不会被解释成「不限」。
type KeyCapacityLimits struct {
	// UpstreamKeyLimit 单把上游 Key 的并发上限。
	UpstreamKeyLimit int64
	// UpstreamAccountLimit 同一上游账户池（可能被多渠道、多 Key 共用）的并发上限。
	//
	// 这一维是计划书 §9.2 的重点：**不能因为切换模型或 Key 就叠加容量**。
	UpstreamAccountLimit int64
}

// CurrentDB 返回当前数据库句柄（由 apigx 层在迁移时捕获）。
func CurrentDB() *gorm.DB { return dbProvider() }

// KeyIdentity 是「本次请求实际使用了哪把上游凭据」的解析结果。
type KeyIdentity struct {
	// UpstreamKeyID 是我们在 `apigx_upstream_keys` 里的行 ID（容量维度的稳定标识）。
	UpstreamKeyID int64
	// StableKeyID 是跨轮换稳定的凭据 ID（统计与审计用）。
	StableKeyID string
	// UpstreamAccountID > 0 时表示该凭据属于某个共享账户池。
	UpstreamAccountID int64
}

// ResolveKeyIdentity 依据底座给出的 Key 索引，解析出我们这边的凭据身份。
//
// 索引 → 稳定 ID 走映射缓存（`MapBaseKeyIndexesCached`），因此热路径不会反复解密全部凭据。
// 解析失败时返回 false —— 调用方应当**跳过该维度**而不是阻塞请求：拿不到身份说明池子刚变化过，
// 此时让请求按其余维度准入比直接拒绝更合理（拒绝会把一次内部状态抖动放大成用户可见的错误）。
func ResolveKeyIdentity(db *gorm.DB, baseChannelID int, keyIndex int) (KeyIdentity, bool) {
	if db == nil || baseChannelID <= 0 || keyIndex < 0 {
		return KeyIdentity{}, false
	}
	var shareChannel apigxmodel.Channel
	if err := db.Where("base_channel_id = ?", baseChannelID).First(&shareChannel).Error; err != nil {
		return KeyIdentity{}, false // 不是我们的分享渠道（例如管理员自建）
	}
	projection, err := MapBaseKeyIndexesCached(db, shareChannel.ID)
	if err != nil {
		return KeyIdentity{}, false
	}
	stableKeyID, ok := projection.StableKeyIDForIndex(keyIndex)
	if !ok {
		return KeyIdentity{}, false
	}
	var key apigxmodel.UpstreamKey
	if err := db.Where("channel_id = ? AND stable_key_id = ?", shareChannel.ID, stableKeyID).First(&key).Error; err != nil {
		return KeyIdentity{}, false
	}
	return KeyIdentity{
		UpstreamKeyID:     key.ID,
		StableKeyID:       key.StableKeyID,
		UpstreamAccountID: key.UpstreamAccountID,
	}, true
}

// BuildKeyCapacityDimensions 构造上游 Key 与账户池两个容量维度。
//
// 未配置上限（0）的维度会被跳过；解析不出身份时整体返回空——见 `ResolveKeyIdentity` 的说明。
func BuildKeyCapacityDimensions(db *gorm.DB, baseChannelID int, keyIndex int, limits KeyCapacityLimits) []capacity.Dimension {
	if limits.UpstreamKeyLimit <= 0 && limits.UpstreamAccountLimit <= 0 {
		return nil
	}
	identity, ok := ResolveKeyIdentity(db, baseChannelID, keyIndex)
	if !ok {
		return nil
	}
	dims := make([]capacity.Dimension, 0, 2)
	if limits.UpstreamKeyLimit > 0 {
		dims = append(dims, capacity.Dimension{
			Key:    capacity.UpstreamKeyConcurrency(identity.UpstreamKeyID),
			Limit:  limits.UpstreamKeyLimit,
			Amount: 1,
		})
	}
	// 账户池维度只在凭据确实归属某个池时才施加：共享账户是分享者显式声明的，
	// 平台不替它推断（计划书 §9.2）。
	if limits.UpstreamAccountLimit > 0 && identity.UpstreamAccountID > 0 {
		dims = append(dims, capacity.Dimension{
			Key:    capacity.UpstreamAccountConcurrency(identity.UpstreamAccountID),
			Limit:  limits.UpstreamAccountLimit,
			Amount: 1,
		})
	}
	return dims
}

// ErrKeyIdentityUnresolved 供需要区分「解析不到身份」的调用方使用。
var ErrKeyIdentityUnresolved = errors.New("channelpool: cannot resolve upstream key identity")
