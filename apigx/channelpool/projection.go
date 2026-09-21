// Package channelpool 把分享者的 Key 池投影到底座渠道的多 Key 模式。
//
// # 为什么是「投影」而不是「自己选」
//
// 多 Key 的选择、轮询游标、逐 Key 故障隔离、以及「全部 Key 失效才停渠道」全部由底座实现
// （`model.Channel.GetNextEnabledKey`、`handlerMultiKeyUpdate`、`UpdateChannelStatus`），
// 且底座在 `SetupContextForSelectedChannel` 里已完成**唯一的选路决定**（计划书 §9.2）。
// 因此 APIGX 不实现选择逻辑，只负责把「我们这边的 Key 池」同步成底座渠道的 Key 列表：
//
//	我们的领域表（稳定 ID / 归属 / 容量策略 / 加密存储 / 统计）
//	        ↓ ProjectKeyPool（解密 + 按确定顺序拼接 + 调底座 Update()）
//	底座 channels.key + channel_info.is_multi_key（选择与健康由底座负责）
//
// # 已知的残留风险（有意接受，必须记录）
//
// 底座渠道的 `key` 列是**明文**（底座既有设计）。投影后我们的凭据会以明文出现在底座表里。
// 我们自己的存储（`apigx_upstream_keys.secret_ref`）是加密信封，主密钥与库分离；
// 要消除这份明文投影，必须改底座的 relay 取键路径，超出阶段 1 范围。
package channelpool

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	apigxmodel "github.com/QuantumNous/new-api/apigx/model"
	"github.com/QuantumNous/new-api/apigx/secretbox"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	basemodel "github.com/QuantumNous/new-api/model"
	"gorm.io/gorm"
)

// ErrNoUsableKey 表示池内没有可用凭据——此时渠道不能接收新请求（计划书 §9.2）。
var ErrNoUsableKey = errors.New("channelpool: no usable upstream key")

// ManagedBaseChannelTag marks base channels provisioned exclusively for one
// APIGX share channel. They can be physically removed when the share channel
// is deleted; administrator-owned channels that were only linked to APIGX do
// not carry this marker and must stay in the base channel list.
const ManagedBaseChannelTag = "apigx-managed"

// QuiesceBaseChannel 先把底座渠道和能力置为不可选，作为删除 saga 的失败关闭边界。
// 它单独提交；后续清理失败时也不会重新接收新请求。返回 false 表示底座渠道已不存在。
func QuiesceBaseChannel(db *gorm.DB, baseChannelID int64) (bool, error) {
	if db == nil {
		return false, errors.New("channelpool: nil db")
	}
	if baseChannelID <= 0 {
		return false, fmt.Errorf("channelpool: invalid base channel id %d", baseChannelID)
	}
	exists := false
	err := db.Transaction(func(tx *gorm.DB) error {
		var baseChannel basemodel.Channel
		if err := tx.Select("id").Where("id = ?", baseChannelID).First(&baseChannel).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		exists = true
		if err := tx.Model(&basemodel.Channel{}).Where("id = ?", baseChannelID).
			Update("status", common.ChannelStatusManuallyDisabled).Error; err != nil {
			return err
		}
		return tx.Model(&basemodel.Ability{}).Where("channel_id = ?", baseChannelID).
			Update("enabled", false).Error
	})
	return exists, err
}

// ClearBaseChannelProjection 撤销 APIGX 写入底座的凭据、模型、能力和多 Key 状态。
// 调用方必须先 QuiesceBaseChannel；该函数可重复执行，供失败后的重试收尾。
func ClearBaseChannelProjection(db *gorm.DB, baseChannelID int64) error {
	if db == nil {
		return errors.New("channelpool: nil db")
	}
	if baseChannelID <= 0 {
		return fmt.Errorf("channelpool: invalid base channel id %d", baseChannelID)
	}
	return db.Transaction(func(tx *gorm.DB) error {
		var baseChannel basemodel.Channel
		if err := tx.Select("id").Where("id = ?", baseChannelID).First(&baseChannel).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		updates := map[string]any{
			"status":       common.ChannelStatusManuallyDisabled,
			"key":          "",
			"models":       "",
			"channel_info": basemodel.ChannelInfo{},
		}
		if err := tx.Model(&basemodel.Channel{}).Where("id = ?", baseChannelID).Updates(updates).Error; err != nil {
			return err
		}
		return tx.Where("channel_id = ?", baseChannelID).Delete(&basemodel.Ability{}).Error
	})
}

// DeleteManagedBaseChannel removes a base channel that APIGX provisioned for
// the given share channel. The exact legacy name check upgrades channels
// created before ManagedBaseChannelTag was introduced without mistaking an
// arbitrary administrator-bound channel for an APIGX-owned projection.
//
// Missing rows and administrator-owned rows are successful no-ops so the
// surrounding delete saga remains safe to retry.
func DeleteManagedBaseChannel(db *gorm.DB, baseChannelID int64, shareChannelName string) (bool, error) {
	if db == nil {
		return false, errors.New("channelpool: nil db")
	}
	if baseChannelID <= 0 {
		return false, fmt.Errorf("channelpool: invalid base channel id %d", baseChannelID)
	}
	var baseChannel basemodel.Channel
	if err := db.Where("id = ?", baseChannelID).First(&baseChannel).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, err
	}
	managed := baseChannel.Tag != nil && *baseChannel.Tag == ManagedBaseChannelTag
	legacyManaged := baseChannel.Name == "APIGX-"+strings.TrimSpace(shareChannelName) &&
		baseChannel.Group == "default"
	if !managed && !legacyManaged {
		return false, nil
	}
	if err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("channel_id = ?", baseChannelID).Delete(&basemodel.Ability{}).Error; err != nil {
			return err
		}
		return tx.Where("id = ?", baseChannelID).Delete(&basemodel.Channel{}).Error
	}); err != nil {
		return false, err
	}
	return true, nil
}

// RetireShareChannel removes one APIGX share channel from active discovery and
// routing. It is deliberately retryable: a previous attempt may already have
// disabled, cleared or deleted the base projection before the APIGX tombstone
// was written.
func RetireShareChannel(db *gorm.DB, ownerUserID, shareChannelID int64) error {
	shareChannel, err := apigxmodel.GetChannelIncludingDeleted(db, ownerUserID, shareChannelID)
	if err != nil {
		return err
	}
	baseExists, err := QuiesceBaseChannel(db, shareChannel.BaseChannelID)
	if err != nil {
		return err
	}
	if baseExists {
		basemodel.CacheUpdateChannelStatus(int(shareChannel.BaseChannelID), common.ChannelStatusManuallyDisabled)
	}
	if err := ClearBaseChannelProjection(db, shareChannel.BaseChannelID); err != nil {
		return err
	}
	if _, err := DeleteManagedBaseChannel(db, shareChannel.BaseChannelID, shareChannel.Name); err != nil {
		return err
	}
	if shareChannel.AliveFlag == apigxmodel.AliveFlagAlive {
		if err := apigxmodel.DeleteChannel(db, ownerUserID, shareChannelID); err != nil {
			return err
		}
	}
	if basemodel.DB != nil {
		basemodel.InitChannelCache()
	}
	return nil
}

// ReconcileDeletedShareResources finishes cleanup left by older versions:
// live channels below a deleted group are retired, and managed base-channel
// rows left behind by already-deleted share channels are removed. Historical
// APIGX rows and usage snapshots remain untouched.
func ReconcileDeletedShareResources(db *gorm.DB) (int, error) {
	if db == nil {
		return 0, errors.New("channelpool: nil db")
	}
	channelTable := apigxmodel.Channel{}.TableName()
	groupTable := apigxmodel.ShareGroup{}.TableName()
	var orphaned []apigxmodel.Channel
	if err := db.Unscoped().Table(channelTable+" AS c").Select("c.*").
		Joins("LEFT JOIN "+groupTable+" AS g ON g.id = c.share_group_id").
		Where("c.deleted_at IS NULL AND c.alive_flag = ? AND (g.id IS NULL OR g.deleted_at IS NOT NULL OR g.alive_flag <> ?)",
			apigxmodel.AliveFlagAlive, apigxmodel.AliveFlagAlive).
		Scan(&orphaned).Error; err != nil {
		return 0, err
	}
	cleaned := 0
	for _, shareChannel := range orphaned {
		if err := RetireShareChannel(db, shareChannel.OwnerUserID, shareChannel.ID); err != nil {
			return cleaned, err
		}
		cleaned++
	}

	var tombstones []apigxmodel.Channel
	if err := db.Unscoped().Where("deleted_at IS NOT NULL OR alive_flag <> ?", apigxmodel.AliveFlagAlive).
		Find(&tombstones).Error; err != nil {
		return cleaned, err
	}
	for _, shareChannel := range tombstones {
		removed, err := DeleteManagedBaseChannel(db, shareChannel.BaseChannelID, shareChannel.Name)
		if err != nil {
			return cleaned, err
		}
		if removed {
			cleaned++
		}
	}
	if cleaned > 0 && basemodel.DB != nil {
		basemodel.InitChannelCache()
	}
	return cleaned, nil
}

// Projection 是一次投影的结果。
type Projection struct {
	// IndexToStableKey 把底座渠道的 Key 索引映射回我们稳定的凭据 ID。
	// 底座的运行期统计只带索引（上下文 `ContextKeyChannelMultiKeyIndex`），
	// 需要靠它把统计落回正确的 StableKeyID。
	IndexToStableKey map[int]string
	// IndexToKeyValue 是投影时写入底座的**凭据明文**（与该索引一一对应）。
	//
	// 存在的唯一理由：底座的 Key 级状态更新函数 `model.UpdateChannelStatus(channelId, usingKey, ...)`
	// 是按**凭据字符串**定位索引的（它内部再换算成下标）。要让底座真正跳过一把失效的 Key，
	// 就必须把当时投影用的那串凭据原样交给它。
	//
	// **只允许在进程内传递，不得写入日志、审计或任何持久化字段。**
	IndexToKeyValue map[int]string
	// KeyCount 是投影到基座的 Key 数量。
	KeyCount int
}

// ProjectKeyPool 把分享渠道的可用 Key 池投影到底座渠道。
//
// 顺序**必须确定**：按 (weight DESC, id ASC) 排列（与 `ListUpstreamKeysByChannel` 一致），
// 这样索引到 StableKeyID 的映射在池未变化时可以稳定重算。
//
// 只投影**当前可用**的 Key（启用 + 未到期 + 不在冷却）：分享者显式停用或已失效的凭据
// 不应继续被发往上游。运行期故障（上游拒绝）仍由底座按索引隔离。
func ProjectKeyPool(db *gorm.DB, shareChannelID int64) (*Projection, error) {
	built, err := buildPool(db, shareChannelID)
	if err != nil {
		return nil, err
	}
	if err := writePoolToBaseChannel(db, built); err != nil {
		return nil, err
	}
	return built.Projection, nil
}

// MapBaseKeyIndexes 依据**底座渠道实际的 Key 列表**，把索引映射回我们的稳定凭据 ID。
//
// # 为什么不能按「当前可用的 Key」重算
//
// 早期实现用「重新构建一次投影」来翻译索引，于是**一旦有 Key 被排除，索引空间就分歧**：
// 底座隔离索引 0 之后继续用索引 1，而重建时会过滤掉已失效的那把，使另一把变成索引 0——
// 于是底座的索引 1 在我们这边根本不存在。行为上是安全地报错（不猜索引），
// 但后果是统计漏记；更危险的是两套索引**碰巧对齐**时会把统计静默记到错误的凭据上。
//
// 正确做法：以底座渠道**当前的 Key 列表**为唯一事实来源逐位匹配。这样无论底座
// 隔离、删除还是重排过哪一把，任何索引都能被正确翻译。
//
// 新投影把稳定 ID 与真实 Key 槽位一同写入 ChannelInfo，可直接恢复映射；
// 旧投影没有该元数据时才回退到按凭据明文比对。
func MapBaseKeyIndexes(db *gorm.DB, shareChannelID int64) (*Projection, error) {
	if db == nil {
		return nil, errors.New("channelpool: nil db")
	}
	var shareChannel apigxmodel.Channel
	if err := db.Where("id = ?", shareChannelID).First(&shareChannel).Error; err != nil {
		return nil, err
	}
	if shareChannel.BaseChannelID <= 0 {
		return nil, fmt.Errorf("channelpool: share channel %d has no base channel", shareChannelID)
	}

	var baseChannel basemodel.Channel
	if err := db.Where("id = ?", shareChannel.BaseChannelID).First(&baseChannel).Error; err != nil {
		return nil, err
	}

	baseKeys := baseChannel.GetKeys()
	indexToStable := make(map[int]string, len(baseKeys))
	indexToKeyValue := make(map[int]string, len(baseKeys))
	if len(baseChannel.ChannelInfo.MultiKeyStableIDs) == len(baseKeys) {
		complete := true
		for i, stableID := range baseChannel.ChannelInfo.MultiKeyStableIDs {
			stableID = strings.TrimSpace(stableID)
			if stableID == "" {
				complete = false
				break
			}
			indexToStable[i] = stableID
			indexToKeyValue[i] = baseKeys[i]
		}
		if complete {
			return &Projection{IndexToStableKey: indexToStable, IndexToKeyValue: indexToKeyValue, KeyCount: len(baseKeys)}, nil
		}
		clear(indexToStable)
		clear(indexToKeyValue)
	}

	// 兼容阶段 C 之前的旧投影：只有缺少稳定元数据时，才解密当前存活 Key 并按明文匹配。
	var ours []apigxmodel.UpstreamKey
	if err := db.Where("channel_id = ?", shareChannelID).Find(&ours).Error; err != nil {
		return nil, err
	}
	secretToStable := make(map[string]string, len(ours))
	for i := range ours {
		plain, err := secretbox.Decrypt(ours[i].SecretRef)
		if err != nil {
			common.SysError(fmt.Sprintf("channelpool: cannot decrypt key id=%d stable=%s: %v",
				ours[i].ID, ours[i].StableKeyID, err))
			continue
		}
		secretToStable[plain] = ours[i].StableKeyID
	}
	for i, secret := range baseKeys {
		stable, ok := secretToStable[secret]
		if !ok {
			// 底座列表里有我们认不出来的凭据（例如人工改过渠道）：不猜，跳过该索引
			continue
		}
		indexToStable[i] = stable
		indexToKeyValue[i] = secret
	}
	return &Projection{
		IndexToStableKey: indexToStable,
		IndexToKeyValue:  indexToKeyValue,
		KeyCount:         len(baseKeys),
	}, nil
}

// builtPool 是一次构建的中间结果（含仅进程内可用的凭据明文）。
type builtPool struct {
	*Projection
	shareChannel apigxmodel.Channel
	secrets      []string
	stableIDs    []string
	weights      []int64
}

func buildPool(db *gorm.DB, shareChannelID int64) (*builtPool, error) {
	if db == nil {
		return nil, errors.New("channelpool: nil db")
	}
	var shareChannel apigxmodel.Channel
	if err := db.Where("id = ?", shareChannelID).First(&shareChannel).Error; err != nil {
		return nil, err
	}
	if shareChannel.BaseChannelID <= 0 {
		return nil, fmt.Errorf("channelpool: share channel %d has no base channel", shareChannelID)
	}

	keys, err := apigxmodel.AvailableUpstreamKeys(db, shareChannel.OwnerUserID, shareChannelID, common.GetTimestamp())
	if err != nil {
		return nil, err
	}
	if len(keys) == 0 {
		return nil, ErrNoUsableKey
	}

	secrets := make([]string, 0, len(keys))
	indexToStable := make(map[int]string, len(keys))
	indexToKeyValue := make(map[int]string, len(keys))

	// 一把领域 Key 只投影为一个真实槽位。稳定 ID 和归一化权重与槽位一一对齐，
	// `GetNextEnabledKey` 直接执行加权轮询/随机；不再复制明文 Key 来表达份额。
	stableIDs := make([]string, 0, len(keys))
	weights := make([]int64, 0, len(keys))
	for i := range keys {
		plain, err := secretbox.Decrypt(keys[i].SecretRef)
		if err != nil {
			// 单把凭据解不开（密钥轮换/信封损坏）：跳过它并记录，不能让整池投影失败
			common.SysError(fmt.Sprintf("channelpool: cannot decrypt key id=%d stable=%s: %v",
				keys[i].ID, keys[i].StableKeyID, err))
			continue
		}
		index := len(secrets)
		indexToStable[index] = keys[i].StableKeyID
		indexToKeyValue[index] = plain
		secrets = append(secrets, plain)
		stableIDs = append(stableIDs, keys[i].StableKeyID)
		weights = append(weights, effectiveWeight(keys[i].Weight))
	}
	if len(secrets) == 0 {
		return nil, ErrNoUsableKey
	}

	return &builtPool{
		Projection:   &Projection{IndexToStableKey: indexToStable, IndexToKeyValue: indexToKeyValue, KeyCount: len(secrets)},
		shareChannel: shareChannel,
		secrets:      secrets,
		stableIDs:    stableIDs,
		weights:      weights,
	}, nil
}

// writePoolToBaseChannel 把池子写进底座渠道。**这是唯一会写底座的地方。**
func writePoolToBaseChannel(db *gorm.DB, built *builtPool) error {
	var baseChannel basemodel.Channel
	if err := db.Where("id = ?", built.shareChannel.BaseChannelID).First(&baseChannel).Error; err != nil {
		return err
	}

	// 索引即将改变，**必须清空按索引存放的状态**，否则旧状态会套到别的 Key 上。
	baseChannel.Key = strings.Join(built.secrets, "\n")
	baseChannel.ChannelInfo.IsMultiKey = true
	baseChannel.ChannelInfo.MultiKeySize = len(built.secrets)
	baseChannel.ChannelInfo.MultiKeyMode = baseMultiKeyMode(built.shareChannel.KeyPoolMode)
	baseChannel.ChannelInfo.MultiKeyStableIDs = append([]string(nil), built.stableIDs...)
	baseChannel.ChannelInfo.MultiKeyWeights = append([]int64(nil), built.weights...)
	baseChannel.ChannelInfo.MultiKeyStatusList = nil
	baseChannel.ChannelInfo.MultiKeyDisabledReason = nil
	baseChannel.ChannelInfo.MultiKeyDisabledTime = nil
	baseChannel.ChannelInfo.MultiKeyStatusByStableID = nil
	baseChannel.ChannelInfo.MultiKeyDisabledReasonByStableID = nil
	baseChannel.ChannelInfo.MultiKeyDisabledTimeByStableID = nil
	baseChannel.ChannelInfo.MultiKeyStatusSources = nil
	baseChannel.ChannelInfo.MultiKeyStatusSourcesByStableID = nil
	baseChannel.ChannelInfo.MultiKeyPollingIndex = 0

	// 池子重新有可用凭据时，把**因全池失效而被自动停用**的渠道恢复过来。
	//
	// 底座的语义是「全部 Key 都不可用才停掉整个渠道」（handlerMultiKeyUpdate），
	// 因此当分享者恢复或新增一把 Key 并 apply 之后，渠道本身也必须跟着恢复——
	// 否则会出现「池里有可用 Key、渠道却仍然全池停摆」的矛盾状态（端到端验证时即如此）。
	// 只恢复**自动禁用**的渠道：人工禁用的是管理员的决定，不能被我们悄悄改回来。
	restoredAutoDisabled := baseChannel.Status == common.ChannelStatusAutoDisabled
	if restoredAutoDisabled {
		baseChannel.Status = common.ChannelStatusEnabled
	}

	// 复用底座的更新路径：它会重算 MultiKeySize 并按当前 Key 列表裁剪越界的索引状态。
	if err := baseChannel.Update(); err != nil {
		return err
	}

	// **必须同步刷新底座的内存渠道缓存**（复用底座自己的缓存函数）。
	//
	// 底座运行时读的是内存缓存、每 SYNC_FREQUENCY（默认 60s）才从库里同步一次。
	// 只写库不刷缓存的话，投影在最长一个同步周期内**对运行中的网关不生效**：
	// 请求仍按旧渠道（单 Key、is_multi_key=false）发出，于是
	//   - 分享者点「应用」后的「已生效」是假的；
	//   - 逐 Key 统计与隔离针对的索引也不是真正被使用的那把凭据。
	// 这个问题正是由端到端验证发现的（单测里没有缓存，永远暴露不出来）。
	basemodel.CacheUpdateChannel(&baseChannel)

	// 恢复自动禁用的渠道时，**必须重建底座的「模型→渠道」路由索引**。
	//
	// 原因（已核实底座源码）：`CacheUpdateChannelStatus` 在渠道被置为非启用时会把该渠道
	// 从 `group2model2channels` 里删掉，而**重新启用时并不会加回来**。
	// 因此只改状态 + `CacheUpdateChannel` 会出现「库里一切正常、请求却报
	// no available channel for model」的现象（端到端验证时正是如此：库内渠道启用、
	// 有可用 Key、abilities 也齐全，但请求 503 且不达上游）。
	// `InitChannelCache` 是底座提供的重建入口，这里复用它而不是自己去维护那张索引表。
	if restoredAutoDisabled {
		basemodel.InitChannelCache()
	}

	// 池子与渠道状态都变了：映射缓存必须失效，否则后续统计会把索引翻译到变化前的凭据上。
	InvalidateMappingCache(built.shareChannel.ID)
	return nil
}

// baseMultiKeyMode 把分享渠道的池策略映射为底座的选择方式。
//
// 阶段 1 只支持底座已有的两种（用户裁决：不为「加权/剩余容量」扩展底座）。
// 未知取值一律回落到轮询——**不报错**，因为这是运行期选择方式而非准入判据，
// 让一个写错的配置把渠道打瘫是不划算的。
func baseMultiKeyMode(poolMode int) constant.MultiKeyMode {
	switch poolMode {
	case apigxmodel.KeyPoolModeRandom: // = 1
		return constant.MultiKeyModeRandom
	default:
		return constant.MultiKeyModePolling
	}
}

// StableKeyIDForIndex 把底座上下文里的 Key 索引翻译成我们的稳定凭据 ID。
//
// 底座的日志与上下文只携带索引，落库统计必须先翻译，否则轮换/增删之后统计会张冠李戴。
func (p *Projection) StableKeyIDForIndex(index int) (string, bool) {
	if p == nil {
		return "", false
	}
	stable, ok := p.IndexToStableKey[index]
	return stable, ok
}

// IndexOfStableKeyID 反向查找（例如按稳定 ID 主动禁用某把 Key 时定位其索引）。
func (p *Projection) IndexOfStableKeyID(stableKeyID string) (int, bool) {
	if p == nil {
		return 0, false
	}
	for idx, stable := range p.IndexToStableKey {
		if stable == stableKeyID {
			return idx, true
		}
	}
	return 0, false
}

// MustIndexString 仅用于日志/审计拼接，避免调用方在各处重复 strconv。
func (p *Projection) MustIndexString(index int) string {
	if stable, ok := p.StableKeyIDForIndex(index); ok {
		return stable
	}
	return "unknown:" + strconv.Itoa(index)
}
