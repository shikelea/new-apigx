package apigxmodel

import (
	"errors"
	"fmt"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// 领域仓储：上游账户池与上游 Key。
//
// 归属传递：`UpstreamKey` 与 `UpstreamAccount` 里，账户带 owner，Key **没有** owner 列——
// Key 的归属经其 `ChannelID` 传递。因此所有 Key 操作都必须先校验渠道归属，
// 不能只按 channel_id 查（否则知道别人 channel id 就能操作别人的 Key）。

// UpstreamKey.Status 的取值。
//
// 「启停」「冷却」「到期」是三件不同的事，分别由 Status / CooldownUntil / ExpiresAt 表达：
// 只有 Status 是分享者的显式表态；冷却与到期是系统/时间推导出来的，不能互相覆盖。
const (
	// UpstreamKeyStatusDisabled 分享者显式停用。
	UpstreamKeyStatusDisabled int = 0
	// UpstreamKeyStatusEnabled 启用。
	UpstreamKeyStatusEnabled int = 1
	// UpstreamKeyStatusInvalid 上游鉴权失效（Key 级故障隔离，计划书 §9.2）。
	UpstreamKeyStatusInvalid int = 2
)

// UpstreamAccount.CircuitState 的取值。
const (
	CircuitStateClosed   int = 0
	CircuitStateOpen     int = 1
	CircuitStateHalfOpen int = 2
)

// IsUpstreamKeyUsable 报告某把 Key 在给定时刻是否可用于新请求。
//
// 三条判据都必须成立：启用、未到期、不在冷却期。**任一条不成立都不是「不可用」的近似**，
// 而是确定的「跳过该 Key」（计划书 §9.2：Key 满载、到期、失效或冷却时跳过）。
func IsUpstreamKeyUsable(key *UpstreamKey, now int64) bool {
	if key == nil {
		return false
	}
	if key.Status != UpstreamKeyStatusEnabled {
		return false
	}
	if key.ExpiresAt > 0 && key.ExpiresAt <= now {
		return false
	}
	if key.CooldownUntil > 0 && key.CooldownUntil > now {
		return false
	}
	return true
}

// ---------------------------------------------------------------------------
// UpstreamAccount
// ---------------------------------------------------------------------------

// UpstreamAccountUniqueHash 用所有者与账户标签生成稳定去重哈希的输入串。
//
// 计划书 §9.2 允许分享者声明「这些渠道共用同一上游账户」，平台据此把它们放进同一限额池。
// 账户标识由分享者提供，平台不尝试推断；`AccountKeyHash` 只用于防止同一所有者重复登记。
func UpstreamAccountUniqueHash(ownerUserID int64, provider, label string) string {
	return fmt.Sprintf("owner=%d|provider=%s|label=%s", ownerUserID, provider, label)
}

// CreateUpstreamAccount 创建上游账户池记录。
func CreateUpstreamAccount(db *gorm.DB, account *UpstreamAccount) error {
	if account == nil || account.OwnerUserID <= 0 {
		return fmt.Errorf("%w: owner_user_id is required", ErrInvalidArgument)
	}
	if account.Provider == "" || account.AccountLabel == "" {
		return fmt.Errorf("%w: provider and account_label are required", ErrInvalidArgument)
	}
	if account.AccountKeyHash == "" {
		return fmt.Errorf("%w: account_key_hash is required (防止同一账户被重复登记)", ErrInvalidArgument)
	}
	account.CircuitState = CircuitStateClosed
	touchTimestamps(true, &account.CreatedAt, &account.UpdatedAt)
	return db.Create(account).Error
}

// GetUpstreamAccount 按所有者读取账户池。
func GetUpstreamAccount(db *gorm.DB, ownerUserID, id int64) (*UpstreamAccount, error) {
	var account UpstreamAccount
	err := db.Where("id = ? AND owner_user_id = ?", id, ownerUserID).First(&account).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &account, nil
}

// ListUpstreamAccounts 列出某所有者的账户池。
func ListUpstreamAccounts(db *gorm.DB, ownerUserID int64, limit int) ([]UpstreamAccount, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var accounts []UpstreamAccount
	err := db.Where("owner_user_id = ?", ownerUserID).
		Order("updated_at DESC, id DESC").Limit(limit).Find(&accounts).Error
	return accounts, err
}

// ---------------------------------------------------------------------------
// UpstreamKey
// ---------------------------------------------------------------------------

// CreateUpstreamKey 为某渠道新增一把上游 Key（已校验渠道归属）。
//
// `(channel_id, stable_key_id, alive_flag)` 上有唯一索引：同一渠道内重复的稳定 Key ID 会被拒绝；
// 而软删除后 `alive_flag` 变为本行 ID，因此**同一个 StableKeyID 可以被重新添加**
// （轮换/重新导入的场景，见 schema-draft §3.5）。
func CreateUpstreamKey(db *gorm.DB, ownerUserID int64, key *UpstreamKey) error {
	if key == nil || key.ChannelID <= 0 {
		return fmt.Errorf("%w: channel_id is required", ErrInvalidArgument)
	}
	if _, err := GetChannel(db, ownerUserID, key.ChannelID); err != nil {
		return err
	}
	if key.StableKeyID == "" {
		return fmt.Errorf("%w: stable_key_id is required（轮询索引与历史统计都依赖它，不能用行号）", ErrInvalidArgument)
	}
	if key.SecretRef == "" {
		return fmt.Errorf("%w: secret_ref is required（凭据只存引用，不存明文）", ErrInvalidArgument)
	}
	if key.SecretFingerprint == "" {
		return fmt.Errorf("%w: secret_fingerprint is required（密文不可比对，重复导入只能靠指纹发现）", ErrInvalidArgument)
	}
	if key.CredentialVersion <= 0 {
		key.CredentialVersion = 1
	}
	if key.CredentialStartedAt == 0 {
		key.CredentialStartedAt = common.GetTimestamp()
	}
	if key.Weights() == 0 {
		// 权重 0 表示未设置；默认 1，保证加权顺序确定（权重不能突破硬限额，只影响挑选偏好）
		key.Weight = 1
	}
	key.AliveFlag = AliveFlagAlive
	touchTimestamps(true, &key.CreatedAt, &key.UpdatedAt)
	return db.Create(key).Error
}

// Weights 返回用于加权挑选的权重（0 视为未设置）。
func (k *UpstreamKey) Weights() int64 { return k.Weight }

// ListUpstreamKeysByChannel 列出渠道内全部存活 Key（已校验渠道归属）。
func ListUpstreamKeysByChannel(db *gorm.DB, ownerUserID, channelID int64) ([]UpstreamKey, error) {
	if _, err := GetChannel(db, ownerUserID, channelID); err != nil {
		return nil, err
	}
	var keys []UpstreamKey
	err := db.Where("channel_id = ?", channelID).
		Order("weight DESC, id ASC").Find(&keys).Error
	return keys, err
}

// AvailableUpstreamKeys 返回渠道内**可用于新请求**的 Key（已校验渠道归属）。
//
// 这是「整个池没有合格 Key 时渠道不能接收新请求」这句规则的数据来源（计划书 §9.2）。
// 注意：本函数只做**状态筛选**，不做容量预留——容量由 apigx/capacity 的原子预留负责。
func AvailableUpstreamKeys(db *gorm.DB, ownerUserID, channelID int64, now int64) ([]UpstreamKey, error) {
	keys, err := ListUpstreamKeysByChannel(db, ownerUserID, channelID)
	if err != nil {
		return nil, err
	}
	available := make([]UpstreamKey, 0, len(keys))
	for i := range keys {
		if IsUpstreamKeyUsable(&keys[i], now) {
			available = append(available, keys[i])
		}
	}
	return available, nil
}

// UpdateUpstreamKeyStatus 设置 Key 的启停/失效状态（已校验渠道归属）。
func UpdateUpstreamKeyStatus(db *gorm.DB, ownerUserID, keyID int64, status int) error {
	key, err := getUpstreamKeyWithOwnership(db, ownerUserID, keyID)
	if err != nil {
		return err
	}
	now := common.GetTimestamp()
	return db.Model(&UpstreamKey{}).Where("id = ? AND alive_flag = ?", key.ID, AliveFlagAlive).
		Updates(map[string]any{"status": status, "updated_at": now}).Error
}

// SetUpstreamKeyCooldown 设置冷却截止时间。
//
// 计划书 §9.2：Key 级限流只让**该 Key** 冷却；确认是账户级或渠道级故障时才作用于共享范围。
// 因此本函数只动单把 Key，不触碰账户池或渠道状态。
func SetUpstreamKeyCooldown(db *gorm.DB, ownerUserID, keyID int64, until int64) error {
	key, err := getUpstreamKeyWithOwnership(db, ownerUserID, keyID)
	if err != nil {
		return err
	}
	now := common.GetTimestamp()
	return db.Model(&UpstreamKey{}).Where("id = ? AND alive_flag = ?", key.ID, AliveFlagAlive).
		Updates(map[string]any{"cooldown_until": until, "updated_at": now}).Error
}

// DeleteUpstreamKey 软删除 Key（墓碑语义；StableKeyID 可被重新添加）。
func DeleteUpstreamKey(db *gorm.DB, ownerUserID, keyID int64) error {
	key, err := getUpstreamKeyWithOwnership(db, ownerUserID, keyID)
	if err != nil {
		return err
	}
	now := common.GetTimestamp()
	res := db.Model(&UpstreamKey{}).
		Where("id = ? AND alive_flag = ?", key.ID, AliveFlagAlive).
		Updates(map[string]any{"deleted_at": softDeletedAt(now), "alive_flag": key.ID, "updated_at": now})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// getUpstreamKeyWithOwnership 取出 Key 并校验其所属渠道属于该所有者。
func getUpstreamKeyWithOwnership(db *gorm.DB, ownerUserID, keyID int64) (*UpstreamKey, error) {
	var key UpstreamKey
	if err := db.Where("id = ?", keyID).First(&key).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	// 归属经渠道传递：不匹配时对外与「不存在」等价
	if _, err := GetChannel(db, ownerUserID, key.ChannelID); err != nil {
		return nil, ErrNotFound
	}
	return &key, nil
}
