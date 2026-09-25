package apigxmodel

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// 平台 Key 的 APIGX 偏好（`apigx_api_keys`）。
//
// 与底座的关系（TD-07）：**凭据本身仍由底座的 `tokens` 表持有**（明文 + 等值查找），
// 本表通过 `TokenID` 关联，只承载 APIGX 特有的偏好：
//   - 允许模型 / 允许来源 / 固定来源 / 是否允许回退
//   - 范围模式、速率与预算上限（四态）、生命周期（状态/到期/撤销）
//
// 这样既不改动底座的令牌体系，又能表达底座没有的偏好。

// ScopeMode 取值：平台 Key 可以用哪些（分享）来源。
const (
	// ScopeModeDefault 按分组可见性自动路由（默认）。
	ScopeModeDefault int = 0
	// ScopeModeAllowedOwners 仅限 `AllowedOwnerUserIDs` 列出的分享者所提供的渠道。
	ScopeModeAllowedOwners int = 1
	// ScopeModePinned 固定来源：只使用 `PinnedOfferingID`。
	ScopeModePinned int = 2
	// ScopeModeAccessKinds 仅使用公开、私有、共享中的所选范围。
	ScopeModeAccessKinds int = 3
)

// APIKeyStatus 取值。
const (
	APIKeyStatusActive  int = 1
	APIKeyStatusRevoked int = 2
)

// GetAPIKeyByToken 读取某个平台 Key（按底座令牌 ID + 使用者）的 APIGX 偏好。
//
// 不存在时返回 ErrNotFound —— 上层据此按「未配置偏好」处理（默认自动路由、允许回退）。
func GetAPIKeyByToken(db *gorm.DB, userID, tokenID int64) (*APIKey, error) {
	if db == nil || tokenID <= 0 || userID <= 0 {
		return nil, fmt.Errorf("%w: user and token are required", ErrInvalidArgument)
	}
	var key APIKey
	err := db.Where("user_id = ? AND token_id = ?", userID, tokenID).First(&key).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &key, nil
}

// UpsertAPIKeyPreferences 创建或更新平台 Key 的 APIGX 偏好。
//
// 校验尽量靠前：范围模式与允许列表必须自洽（固定来源模式要求给出 PinnedOfferingID；
// 允许来源模式要求列表非空），避免写入「看起来配了、实际不生效」的组合。
func UpsertAPIKeyPreferences(db *gorm.DB, userID, tokenID int64, mutate func(*APIKey) error) (*APIKey, error) {
	if db == nil || tokenID <= 0 || userID <= 0 {
		return nil, fmt.Errorf("%w: user and token are required", ErrInvalidArgument)
	}

	key, err := GetAPIKeyByToken(db, userID, tokenID)
	switch {
	case errors.Is(err, ErrNotFound):
		key = &APIKey{UserID: userID, TokenID: tokenID, ScopeMode: ScopeModeDefault, Status: APIKeyStatusActive}
	case err != nil:
		return nil, err
	}

	if mutate != nil {
		if err := mutate(key); err != nil {
			return nil, err
		}
	}
	if err := validateAPIKeyPreferences(key); err != nil {
		return nil, err
	}
	// `key_hash` 上有唯一索引，但偏好行**没有真正的 Key 哈希**：凭据由底座的 `tokens` 持有
	// （TD-07），本行只承载偏好。若留空，多行空字符串会互相冲突
	// （首次实现即如此：第二个使用者写偏好时撞 `UNIQUE constraint failed: apigx_api_keys.key_hash`）。
	// 因此写入由 (user_id, token_id) 推导的**稳定占位哈希**：既满足唯一索引，
	// 又保证「一个令牌一条偏好记录」的语义。
	if key.KeyHash == "" {
		key.KeyHash = placeholderKeyHash(userID, tokenID)
	}

	now := common.GetTimestamp()
	key.UpdatedAt = now
	if key.ID == 0 {
		key.CreatedAt = now
		if err := db.Create(key).Error; err != nil {
			return nil, err
		}
		return key, nil
	}
	// 全字段更新：偏好是整体语义，逐字段零值更新会漏掉「清空允许列表」这类意图
	if err := db.Save(key).Error; err != nil {
		return nil, err
	}
	return key, nil
}

func validateAPIKeyPreferences(key *APIKey) error {
	switch key.ScopeMode {
	case ScopeModeDefault:
	case ScopeModeAllowedOwners:
		if len(key.AllowedOwnerIDs()) == 0 {
			return fmt.Errorf("%w: scope_mode=allowed_owners requires a non-empty allowed owner list", ErrInvalidArgument)
		}
	case ScopeModePinned:
		if key.PinnedOfferingID <= 0 {
			return fmt.Errorf("%w: scope_mode=pinned requires pinned_offering_id", ErrInvalidArgument)
		}
	case ScopeModeAccessKinds:
		if len(key.AllowedAccessKindValues()) == 0 {
			return fmt.Errorf("%w: scope_mode=access_kinds requires a non-empty access kind list", ErrInvalidArgument)
		}
	default:
		return fmt.Errorf("%w: unknown scope mode %d", ErrInvalidArgument, key.ScopeMode)
	}

	// 允许模型列表：去重并剔除空项，避免同一模型重复存储
	models := key.AllowedModels()
	if len(models) > 0 {
		slices.Sort(models)
		models = slices.Compact(models)
		encoded, err := common.Marshal(models)
		if err != nil {
			return err
		}
		key.AllowedModelIDs = string(encoded)
	}
	return nil
}

// AllowedAccessKindValues 解析公开、私有、共享范围；空字符串表示未配置该限制。
func (k *APIKey) AllowedAccessKindValues() []string {
	if k == nil || k.AllowedAccessKinds == "" {
		return nil
	}
	var out []string
	if err := common.UnmarshalJsonStr(k.AllowedAccessKinds, &out); err != nil {
		return nil
	}
	return out
}

// SetAllowedAccessKinds 校验并保存用户可理解的资源范围。
func (k *APIKey) SetAllowedAccessKinds(kinds []string) error {
	if len(kinds) == 0 {
		k.AllowedAccessKinds = ""
		return nil
	}
	for _, kind := range kinds {
		switch kind {
		case PlazaAccessKindPublic, PlazaAccessKindPrivate, PlazaAccessKindShared:
		default:
			return fmt.Errorf("%w: unknown access kind %q", ErrInvalidArgument, kind)
		}
	}
	slices.Sort(kinds)
	kinds = slices.Compact(kinds)
	encoded, err := common.Marshal(kinds)
	if err != nil {
		return err
	}
	k.AllowedAccessKinds = string(encoded)
	return nil
}

// AllowsAccessKind 报告该 Key 的资源范围是否包含给定类别。
func (k *APIKey) AllowsAccessKind(kind string) bool {
	if k == nil || k.ScopeMode != ScopeModeAccessKinds {
		return true
	}
	return slices.Contains(k.AllowedAccessKindValues(), kind)
}

// AllowedModels 解析允许模型列表；空字符串表示未限制。
//
// 方法名刻意与字段名不同（Go 不允许同名字段与方法）。
func (k *APIKey) AllowedModels() []string {
	if k == nil || k.AllowedModelIDs == "" {
		return nil
	}
	var out []string
	if err := common.UnmarshalJsonStr(k.AllowedModelIDs, &out); err != nil {
		return nil
	}
	return out
}

// AllowedOwnerIDs 解析允许来源（分享者）列表；空表示未限制。
func (k *APIKey) AllowedOwnerIDs() []int64 {
	if k == nil || k.AllowedOwnerUserIDs == "" {
		return nil
	}
	var out []int64
	if err := common.UnmarshalJsonStr(k.AllowedOwnerUserIDs, &out); err != nil {
		return nil
	}
	return out
}

// SetAllowedOwnerIDs 写入允许来源列表（JSON 数组，去重排序）。
func (k *APIKey) SetAllowedOwnerIDs(ids []int64) error {
	if len(ids) == 0 {
		k.AllowedOwnerUserIDs = ""
		return nil
	}
	slices.Sort(ids)
	ids = slices.Compact(ids)
	encoded, err := common.Marshal(ids)
	if err != nil {
		return err
	}
	k.AllowedOwnerUserIDs = string(encoded)
	return nil
}

// AllowsOwner 报告该平台 Key 是否允许使用某分享者提供的渠道。
//
// **回退语义**：不在允许列表里时，返回值的第二个元素表示「是否允许回退到其它来源」——
// 调用方据此决定是让底座**重选**（可回退）还是直接拒绝（不可回退）。
func (k *APIKey) AllowsOwner(ownerUserID int64) (allowed bool, mayFallback bool) {
	if k == nil || k.ScopeMode == ScopeModeDefault {
		return true, false
	}
	if k.ScopeMode != ScopeModeAllowedOwners {
		return true, false
	}
	if slices.Contains(k.AllowedOwnerIDs(), ownerUserID) {
		return true, false
	}
	return false, k.AllowFallback
}

// placeholderKeyHash 为「只承载偏好」的行生成稳定且唯一的占位哈希。
//
// 仅用于满足 `key_hash` 的唯一索引；它**不是**任何真实凭据的哈希，也不用于鉴权。
func placeholderKeyHash(userID, tokenID int64) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("apigx:prefs:%d:%d", userID, tokenID)))
	return hex.EncodeToString(sum[:])
}
