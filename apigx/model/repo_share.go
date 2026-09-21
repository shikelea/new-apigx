package apigxmodel

import (
	"errors"
	"fmt"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// 领域仓储：分享分组与渠道。
//
// 三条不变量（docs/stage0/schema-draft.md §3.5、§7、§10.3）：
//
//  1. **软删除 = `deleted_at` + `alive_flag`**：删除时把 `alive_flag` 置为本行 ID（墓碑值唯一），
//     这样参与唯一索引的「存活期间唯一」约束既能阻止重复，又不会让墓碑互相冲突。
//  2. **归属由服务端强制**：所有分享管理操作都必须带 `ownerUserID` 并进入 WHERE 条件，
//     不能只靠调用方自律。查不到与不属于自己**返回同一个错误**，避免枚举他人资源。
//  3. **时间戳一律 UTC epoch 秒**（`common.GetTimestamp()`），与底座一致。
var (
	// ErrNotFound 表示对象不存在**或不属于该所有者**。对外一律等价（计划书 §9.1：404 不泄露存在性）。
	ErrNotFound = errors.New("apigx: object not found")
	// ErrInvalidArgument 表示入参不满足领域约束。
	ErrInvalidArgument = errors.New("apigx: invalid argument")
)

// PublicIDLength 是对外可见稳定 ID 的长度（share_groups.public_id / channels.public_id）。
const PublicIDLength = 24

func newPublicID() string { return common.GetRandomString(PublicIDLength) }

func touchTimestamps(created bool, createdAt, updatedAt *int64) {
	now := common.GetTimestamp()
	if created || *createdAt == 0 {
		*createdAt = now
	}
	*updatedAt = now
}

// softDeleteByOwner 用一条语句完成软删除（墓碑），并强制归属校验。
//
// 必须是一条语句：`WHERE id = ? AND owner_user_id = ? AND alive_flag = 0` 保证
// 重复删除不会改变墓碑值（第二次 RowsAffected = 0 → ErrNotFound）。
//
// **注意 `deleted_at` 的类型**：表里其它 `*_at` 列都是 `int64`（UTC epoch 秒），
// 但 `deleted_at` 是 `gorm.DeletedAt` → PostgreSQL 的 `timestamptz`、MySQL 的 `datetime`。
// 往它写整数会在 PG 上直接报 `SQLSTATE 22008 日期/时间值超出范围`，
// 而 SQLite 因为类型宽松会静默接受——**只有真实 PG 才能暴露**（首次端到端验收即被它拦下）。
func softDeleteByOwner(db *gorm.DB, model any, ownerUserID, id int64) error {
	if ownerUserID <= 0 || id <= 0 {
		return fmt.Errorf("%w: owner and id are required", ErrInvalidArgument)
	}
	now := common.GetTimestamp()
	res := db.Model(model).
		Where("id = ? AND owner_user_id = ? AND alive_flag = ?", id, ownerUserID, AliveFlagAlive).
		Updates(map[string]any{
			"deleted_at": softDeletedAt(now),
			"alive_flag": id,
			"updated_at": now,
		})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// softDeletedAt 把 epoch 秒转成 gorm.DeletedAt 能写入的**时间值**。
func softDeletedAt(epochSeconds int64) gorm.DeletedAt {
	return gorm.DeletedAt{Time: time.Unix(epochSeconds, 0).UTC(), Valid: true}
}

// ---------------------------------------------------------------------------
// ShareGroup
// ---------------------------------------------------------------------------

// CreateShareGroup 创建分享分组。OwnerUserID 必须由**服务端**从登录态填入。
func CreateShareGroup(db *gorm.DB, group *ShareGroup) error {
	if group == nil {
		return fmt.Errorf("%w: nil group", ErrInvalidArgument)
	}
	if group.OwnerUserID <= 0 {
		return fmt.Errorf("%w: owner_user_id is required", ErrInvalidArgument)
	}
	if group.Name == "" {
		return fmt.Errorf("%w: name is required", ErrInvalidArgument)
	}
	if group.PublicID == "" {
		group.PublicID = newPublicID()
	}
	group.AliveFlag = AliveFlagAlive
	touchTimestamps(true, &group.CreatedAt, &group.UpdatedAt)
	return db.Create(group).Error
}

// GetShareGroup 按所有者读取分组；不属于该所有者时返回 ErrNotFound。
func GetShareGroup(db *gorm.DB, ownerUserID, id int64) (*ShareGroup, error) {
	var group ShareGroup
	err := db.Where("id = ? AND owner_user_id = ?", id, ownerUserID).First(&group).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &group, nil
}

// ListShareGroups 列出某所有者的存活分组（按更新时间倒序）。
func ListShareGroups(db *gorm.DB, ownerUserID int64, limit int) ([]ShareGroup, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var groups []ShareGroup
	err := db.Where("owner_user_id = ?", ownerUserID).
		Order("updated_at DESC, id DESC").Limit(limit).Find(&groups).Error
	return groups, err
}

// DeleteShareGroup 软删除分组（墓碑语义）。
// ShareGroup 发布状态（字段注释为准：0=草稿 / 1=待审核 / 2=已发布 / 3=已暂停 / 4=已退役）。
const (
	// ShareGroupPublishDraft 草稿：分享者还在准备，不对外可见。
	ShareGroupPublishDraft int = 0
	// ShareGroupPublishPending 待审核：已提交，等待平台审核（公开分组的新建初始状态）。
	ShareGroupPublishPending int = 1
	// ShareGroupPublishPublished 已发布：对外可见、可被路由。
	ShareGroupPublishPublished int = 2
	// ShareGroupPublishSuspended 已暂停：曾发布，现停止对外服务。
	ShareGroupPublishSuspended int = 3
	// ShareGroupPublishRetired 已退役：不再使用。
	ShareGroupPublishRetired int = 4
)

// SetShareGroupPublishStatus 设置分组的发布状态（平台审核动作）。
//
// 只改状态一个字段：不触碰分组名、可见性、成员等其他信息，避免审核动作顺带改写分享者的内容。
func SetShareGroupPublishStatus(db *gorm.DB, groupID int64, status int) error {
	if groupID <= 0 {
		return fmt.Errorf("%w: group_id is required", ErrInvalidArgument)
	}
	res := db.Model(&ShareGroup{}).
		Where("id = ? AND deleted_at IS NULL", groupID).
		Update("publish_status", status)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func DeleteShareGroup(db *gorm.DB, ownerUserID, id int64) error {
	return softDeleteByOwner(db, &ShareGroup{}, ownerUserID, id)
}

// ---------------------------------------------------------------------------
// Channel（分享渠道）
// ---------------------------------------------------------------------------

// CreateChannel 创建分享渠道。
//
// **必须绑定一条底座渠道**（`BaseChannelID > 0`）：
//  1. 该列上有唯一索引（一个底座渠道只能被一个分享渠道引用），因此**未绑定（0）的行之间会互相冲突**——
//     允许 0 就等于只允许存在一条未绑定渠道，那不是有效状态；
//  2. 转发链路需要它来定位真实的底座渠道与凭据。
//
// 这是首次实现时由测试发现的缺陷：测试里连建两条未绑定渠道，第二条直接撞唯一索引。
func CreateChannel(db *gorm.DB, channel *Channel) error {
	if channel == nil {
		return fmt.Errorf("%w: nil channel", ErrInvalidArgument)
	}
	if channel.OwnerUserID <= 0 || channel.ShareGroupID <= 0 {
		return fmt.Errorf("%w: owner_user_id and share_group_id are required", ErrInvalidArgument)
	}
	if channel.Name == "" {
		return fmt.Errorf("%w: name is required", ErrInvalidArgument)
	}
	if channel.BaseChannelID <= 0 {
		return fmt.Errorf("%w: base_channel_id is required（每个分享渠道必须绑定一条底座渠道；该列有唯一索引，0 会互相冲突）", ErrInvalidArgument)
	}
	if channel.KeyPoolMode != KeyPoolModePolling && channel.KeyPoolMode != KeyPoolModeRandom {
		return fmt.Errorf("%w: unsupported key_pool_mode %d", ErrInvalidArgument, channel.KeyPoolMode)
	}
	// **必须校验分组归属**：ShareGroupID 来自请求体，若不校验，
	// 只要知道别人的分组 ID 就能把自己的渠道挂进去（接口层测试发现的越权）。
	// 先验归属再看其它字段，避免通过错误信息差异探测他人分组是否存在。
	if _, err := GetShareGroup(db, channel.OwnerUserID, channel.ShareGroupID); err != nil {
		return err
	}
	if channel.PublicID == "" {
		channel.PublicID = newPublicID()
	}
	channel.AliveFlag = AliveFlagAlive
	touchTimestamps(true, &channel.CreatedAt, &channel.UpdatedAt)
	return db.Create(channel).Error
}

// GetChannel 按所有者读取渠道。
func GetChannel(db *gorm.DB, ownerUserID, id int64) (*Channel, error) {
	var channel Channel
	err := db.Where("id = ? AND owner_user_id = ?", id, ownerUserID).First(&channel).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &channel, nil
}

// GetChannelIncludingDeleted 按所有者读取渠道及其墓碑，用于可重试的生命周期收尾。
// owner 条件仍在查询里：已经删除的资源也不能被其他用户枚举。
func GetChannelIncludingDeleted(db *gorm.DB, ownerUserID, id int64) (*Channel, error) {
	if db == nil || ownerUserID <= 0 || id <= 0 {
		return nil, fmt.Errorf("%w: owner and id are required", ErrInvalidArgument)
	}
	var channel Channel
	err := db.Unscoped().Where("id = ? AND owner_user_id = ?", id, ownerUserID).First(&channel).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &channel, nil
}

// ListChannelsByGroup 列出某分组下的存活渠道（已校验分组归属）。
func ListChannelsByGroup(db *gorm.DB, ownerUserID, shareGroupID int64, limit int) ([]Channel, error) {
	if _, err := GetShareGroup(db, ownerUserID, shareGroupID); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var channels []Channel
	err := db.Where("owner_user_id = ? AND share_group_id = ?", ownerUserID, shareGroupID).
		Order("updated_at DESC, id DESC").Limit(limit).Find(&channels).Error
	return channels, err
}

// ListChannelsByOwner 列出某用户的**全部**渠道（不限分组）。
//
// 与 ListChannelsByGroup 只差"是否按分组过滤"：分组维度需要先校验分组归属，
// 这里不需要，因此直接按 owner 查；排序与 limit 语义保持一致，避免两处行为分叉。
func ListChannelsByOwner(db *gorm.DB, ownerUserID int64, limit int) ([]Channel, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var channels []Channel
	err := db.Where("owner_user_id = ?", ownerUserID).
		Order("updated_at DESC, id DESC").Limit(limit).Find(&channels).Error
	return channels, err
}

// ManagedBaseChannelIDs returns the subset of New API channel IDs that are
// currently owned by live APIGX share channels. Generic channel maintenance
// must not delete these rows because APIGX retains the owner, encrypted Key
// pool, ACL and offering lifecycle around them.
func ManagedBaseChannelIDs(db *gorm.DB, candidateIDs []int) ([]int, error) {
	if db == nil || len(candidateIDs) == 0 {
		return nil, nil
	}
	// A few isolated New API controller tests intentionally migrate only the
	// base tables. In a real APIGX process this table is always created before
	// routes are served; when it is absent there cannot be an APIGX-managed
	// channel to protect.
	if !db.Migrator().HasTable(&Channel{}) {
		return nil, nil
	}
	ids64 := make([]int64, 0, len(candidateIDs))
	for _, id := range candidateIDs {
		if id > 0 {
			ids64 = append(ids64, int64(id))
		}
	}
	if len(ids64) == 0 {
		return nil, nil
	}
	var managed64 []int64
	if err := db.Model(&Channel{}).
		Where("alive_flag = ? AND base_channel_id IN ?", AliveFlagAlive, ids64).
		Distinct("base_channel_id").Pluck("base_channel_id", &managed64).Error; err != nil {
		return nil, err
	}
	managed := make([]int, 0, len(managed64))
	for _, id := range managed64 {
		managed = append(managed, int(id))
	}
	return managed, nil
}

// UpdateShareGroup 更新分组的展示名与简介（计划书 §4"分享名称、介绍"）。
//
// 只动展示字段：更名与改简介**不影响任何授权**（Group 结构体注释已写明这一点），
// 因此这里不做授权相关处理，只校验归属并写库。
func UpdateShareGroup(db *gorm.DB, ownerUserID, id int64, name, description string) error {
	res := db.Model(&ShareGroup{}).
		Where("id = ? AND owner_user_id = ? AND deleted_at IS NULL", id, ownerUserID).
		Updates(map[string]any{"name": name, "description": description, "updated_at": common.GetTimestamp()})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteChannel 软删除渠道（墓碑语义）。
func DeleteChannel(db *gorm.DB, ownerUserID, id int64) error {
	return softDeleteByOwner(db, &Channel{}, ownerUserID, id)
}

// UpdateChannelHealth 更新渠道健康状态与原因（调度实时读取，不做缓存）。
func UpdateChannelHealth(db *gorm.DB, ownerUserID, id int64, state int, reason string) error {
	now := common.GetTimestamp()
	res := db.Model(&Channel{}).
		Where("id = ? AND owner_user_id = ? AND alive_flag = ?", id, ownerUserID, AliveFlagAlive).
		Updates(map[string]any{
			"health_state":      state,
			"health_reason":     reason,
			"health_updated_at": now,
			"updated_at":        now,
		})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}
