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
	for i := range keys {
		plain, err := secretbox.Decrypt(keys[i].SecretRef)
		if err != nil {
			// 单把凭据解不开（密钥轮换/信封损坏）：跳过它并记录，不能让整池投影失败
			common.SysError(fmt.Sprintf("channelpool: cannot decrypt key id=%d stable=%s: %v",
				keys[i].ID, keys[i].StableKeyID, err))
			continue
		}
		indexToStable[len(secrets)] = keys[i].StableKeyID
		indexToKeyValue[len(secrets)] = plain
		secrets = append(secrets, plain)
	}
	if len(secrets) == 0 {
		return nil, ErrNoUsableKey
	}

	var baseChannel basemodel.Channel
	if err := db.Where("id = ?", shareChannel.BaseChannelID).First(&baseChannel).Error; err != nil {
		return nil, err
	}

	// 索引即将改变，**必须清空按索引存放的状态**，否则旧状态会套到别的 Key 上。
	baseChannel.Key = strings.Join(secrets, "\n")
	baseChannel.ChannelInfo.IsMultiKey = true
	baseChannel.ChannelInfo.MultiKeySize = len(secrets)
	baseChannel.ChannelInfo.MultiKeyMode = baseMultiKeyMode(shareChannel.KeyPoolMode)
	baseChannel.ChannelInfo.MultiKeyStatusList = nil
	baseChannel.ChannelInfo.MultiKeyDisabledReason = nil
	baseChannel.ChannelInfo.MultiKeyDisabledTime = nil
	baseChannel.ChannelInfo.MultiKeyPollingIndex = 0

	// 复用底座的更新路径：它会重算 MultiKeySize 并按当前 Key 列表裁剪越界的索引状态。
	if err := baseChannel.Update(); err != nil {
		return nil, err
	}
	return &Projection{IndexToStableKey: indexToStable, IndexToKeyValue: indexToKeyValue, KeyCount: len(secrets)}, nil
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
