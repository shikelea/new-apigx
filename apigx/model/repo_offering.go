package apigxmodel

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// 模型供给条目（`apigx_model_offerings`）：
// **同一个上游模型名，在不同分享渠道上就是不同的供给**——价格、能力、限额都可以不同。
//
// 唯一性用 `OfferingKeyHash`（渠道 + 上游模型名）表达，并与 `AliveFlag` 组成唯一索引：
// 存活期间同一渠道内同一模型只有一条供给；软删除后可重新发布（与其它表同一套墓碑语义）。

// OfferingStatus 取值：供给的发布状态。
const (
	// OfferingStatusDraft 草稿（分享者在编辑，不对外可见）。
	OfferingStatusDraft int = 0
	// OfferingStatusPending 待审核（已提交，等待平台审核）。
	OfferingStatusPending int = 1
	// OfferingStatusPublished 已发布（对外可见、可被路由）。
	OfferingStatusPublished int = 2
	// OfferingStatusRejected 已驳回（审核未通过，分享者可修改后重新提交）。
	OfferingStatusRejected int = 3
)

// Capability 描述一个模型在**这条供给**上实际支持的能力。
//
// 底座的 `relaykit` 转换器是按**模型名前缀**硬编码能力的，没有"按供给声明能力"的位置；
// 因此 APIGX 必须自己承载它（见 docs/architecture/baseline.md 的 E4）。
type Capability struct {
	Streaming bool `json:"streaming"`
	Vision    bool `json:"vision"`
	Tools     bool `json:"tools"`
	Reasoning bool `json:"reasoning"`
}

// OfferingKeyHashFor 计算供给的业务键哈希（渠道 + 上游模型名）。
//
// 用哈希而不是把两列一起放进唯一索引，是为了让「墓碑」语义只依赖一个列：
// 软删除时把 AliveFlag 置为本行 ID 即可放行业务键。
func OfferingKeyHashFor(channelID int64, upstreamModelID string) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("apigx:offering:%d:%s", channelID, upstreamModelID)))
	return hex.EncodeToString(sum[:])
}

// CreateModelOffering 新增一条供给（已校验渠道归属）。
//
// 新供给一律从**草稿**开始：发布需要显式动作（提交审核 → 平台发布），
// 避免"加了模型就自动对外可见"。
func CreateModelOffering(db *gorm.DB, ownerUserID int64, offering *ModelOffering) error {
	if offering == nil || offering.ChannelID <= 0 {
		return fmt.Errorf("%w: channel_id is required", ErrInvalidArgument)
	}
	if _, err := GetChannel(db, ownerUserID, offering.ChannelID); err != nil {
		return err
	}
	if offering.UpstreamModelID == "" {
		return fmt.Errorf("%w: upstream_model_id is required", ErrInvalidArgument)
	}
	if offering.Capabilities == "" {
		encoded, err := common.Marshal(Capability{})
		if err != nil {
			return err
		}
		offering.Capabilities = string(encoded)
	}
	if offering.PublicID == "" {
		offering.PublicID = newPublicID()
	}
	offering.OfferingKeyHash = OfferingKeyHashFor(offering.ChannelID, offering.UpstreamModelID)
	offering.Status = OfferingStatusDraft
	offering.Revision = 1
	offering.AliveFlag = AliveFlagAlive
	touchTimestamps(true, &offering.CreatedAt, &offering.UpdatedAt)
	if err := db.Create(offering).Error; err != nil {
		return err
	}
	// 顺手把上游模型名登记为**候选**别名（幂等；候选不会当结论用，见 repo_catalog.go）。
	// 放在仓储层是为了让所有调用方（含"一键导入"的批量路径）都覆盖到；
	// 失败**不影响供给创建**（目录是辅助能力，不该挡住主流程），因此只记录不返回错误。
	if err := RecordAliasCandidate(db, AliasScopePlatform, 0, 0, offering.UpstreamModelID); err != nil {
		common.SysError("apigx: record alias candidate failed: " + err.Error())
	}
	return nil
}

// GetModelOffering 按所有者读取供给。
func GetModelOffering(db *gorm.DB, ownerUserID, id int64) (*ModelOffering, error) {
	var offering ModelOffering
	if err := db.Where("id = ?", id).First(&offering).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	// 归属经渠道传递（供给没有 owner 列）
	if _, err := GetChannel(db, ownerUserID, offering.ChannelID); err != nil {
		return nil, ErrNotFound
	}
	return &offering, nil
}

// ListModelOfferingsByChannel 列出某渠道下的全部存活供给（已校验渠道归属）。
func ListModelOfferingsByChannel(db *gorm.DB, ownerUserID, channelID int64) ([]ModelOffering, error) {
	if _, err := GetChannel(db, ownerUserID, channelID); err != nil {
		return nil, err
	}
	var offerings []ModelOffering
	err := db.Where("channel_id = ?", channelID).Order("id ASC").Find(&offerings).Error
	return offerings, err
}

// UpdateModelOfferingCapabilities 更新能力声明并递增修订号（能力是路由与展示的依据）。
func UpdateModelOfferingCapabilities(db *gorm.DB, ownerUserID, id int64, caps Capability) error {
	offering, err := GetModelOffering(db, ownerUserID, id)
	if err != nil {
		return err
	}
	encoded, err := common.Marshal(caps)
	if err != nil {
		return err
	}
	return db.Model(&ModelOffering{}).Where("id = ? AND alive_flag = ?", offering.ID, AliveFlagAlive).
		Updates(map[string]any{
			"capabilities": string(encoded),
			"revision":     offering.Revision + 1,
			"updated_at":   common.GetTimestamp(),
		}).Error
}

// SubmitModelOffering 把草稿（或已驳回）提交为待审核。
//
// 状态机刻意做窄：**已发布的供给不能靠提交动作改状态**（改价/改能力应当走修订流程），
// 否则一次误点就会让已对外可见的供给退回待审核、对使用者表现为"模型突然消失"。
func SubmitModelOffering(db *gorm.DB, ownerUserID, id int64) error {
	return transitionOffering(db, ownerUserID, id, []int{OfferingStatusDraft, OfferingStatusRejected}, OfferingStatusPending)
}

// PublishModelOffering 平台审核通过（待审核 → 已发布）。
func PublishModelOffering(db *gorm.DB, ownerUserID, id int64) error {
	return transitionOffering(db, ownerUserID, id, []int{OfferingStatusPending}, OfferingStatusPublished)
}

// RejectModelOffering 平台驳回（待审核 → 已驳回）。
func RejectModelOffering(db *gorm.DB, ownerUserID, id int64) error {
	return transitionOffering(db, ownerUserID, id, []int{OfferingStatusPending}, OfferingStatusRejected)
}

func transitionOffering(db *gorm.DB, ownerUserID, id int64, from []int, to int) error {
	offering, err := GetModelOffering(db, ownerUserID, id)
	if err != nil {
		return err
	}
	allowed := false
	for _, candidate := range from {
		if offering.Status == candidate {
			allowed = true
			break
		}
	}
	if !allowed {
		return fmt.Errorf("%w: offering %d cannot move from status %d to %d", ErrInvalidArgument, id, offering.Status, to)
	}
	now := common.GetTimestamp()
	updates := map[string]any{"status": to, "updated_at": now}
	if to == OfferingStatusPublished {
		updates["verified_at"] = now
	}
	return db.Model(&ModelOffering{}).Where("id = ? AND alive_flag = ?", offering.ID, AliveFlagAlive).
		Updates(updates).Error
}

// DeleteModelOffering 软删除供给（墓碑语义：同一模型可被重新添加）。
func DeleteModelOffering(db *gorm.DB, ownerUserID, id int64) error {
	offering, err := GetModelOffering(db, ownerUserID, id)
	if err != nil {
		return err
	}
	now := common.GetTimestamp()
	res := db.Model(&ModelOffering{}).
		Where("id = ? AND alive_flag = ?", offering.ID, AliveFlagAlive).
		Updates(map[string]any{"deleted_at": softDeletedAt(now), "alive_flag": offering.ID, "updated_at": now})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// CapabilityOf 解析供给的能力声明。
func (o *ModelOffering) CapabilityOf() Capability {
	var caps Capability
	if o == nil || o.Capabilities == "" {
		return caps
	}
	_ = common.UnmarshalJsonStr(o.Capabilities, &caps)
	return caps
}

// ImportModelOfferings 批量导入供给（一键获取上游模型列表后的落库动作）。
//
// 幂等：已存在（存活）的同名供给会被跳过而不是报错——上游模型列表反复拉取是常态，
// 报错会让分享者必须先手工比对。返回新建数量与被跳过的模型名，便于界面如实回显。
//
// 新导入的供给仍然是**草稿**：导入不等于发布（发布要显式提交审核）。
func ImportModelOfferings(db *gorm.DB, ownerUserID, channelID int64, upstreamModelIDs []string) (created int, skipped []string, err error) {
	if len(upstreamModelIDs) == 0 {
		return 0, nil, nil
	}
	if _, err := GetChannel(db, ownerUserID, channelID); err != nil {
		return 0, nil, err
	}

	existing := make(map[string]struct{})
	var alive []ModelOffering
	if err := db.Where("channel_id = ?", channelID).Find(&alive).Error; err != nil {
		return 0, nil, err
	}
	for i := range alive {
		existing[alive[i].UpstreamModelID] = struct{}{}
	}

	seen := make(map[string]struct{}, len(upstreamModelIDs))
	for _, modelID := range upstreamModelIDs {
		trimmed := strings.TrimSpace(modelID)
		if trimmed == "" {
			continue
		}
		if _, dup := seen[trimmed]; dup {
			continue
		}
		seen[trimmed] = struct{}{}
		if _, exists := existing[trimmed]; exists {
			skipped = append(skipped, trimmed)
			continue
		}
		offering := &ModelOffering{ChannelID: channelID, UpstreamModelID: trimmed}
		if err := CreateModelOffering(db, ownerUserID, offering); err != nil {
			return created, skipped, err
		}
		existing[trimmed] = struct{}{}
		created++
	}
	return created, skipped, nil
}

// FindOfferingByUpstreamModel 按「渠道 + 上游模型名」定位存活供给。
//
// 质量统计要按供给归档样本，而供给的身份就是这一对——用列表接口再筛会多一次全量查询。
func FindOfferingByUpstreamModel(db *gorm.DB, channelID int64, upstreamModelID string) (int64, bool, error) {
	if db == nil || channelID <= 0 || upstreamModelID == "" {
		return 0, false, nil
	}
	var offering ModelOffering
	err := db.Where("channel_id = ? AND upstream_model_id = ? AND alive_flag = ?",
		channelID, upstreamModelID, AliveFlagAlive).First(&offering).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return offering.ID, true, nil
}

// ListPublishedOfferingsByModel 列出某上游模型名下**已发布**的全部供给（跨分享者）。
//
// 用于可解释路由（T2.9）：候选集就是"这个模型当前所有已发布的供给"。
// 只返回已发布（草稿/待审核/已驳回不对外），与广场的可见性判据保持一致。
func ListPublishedOfferingsByModel(db *gorm.DB, upstreamModelID string) ([]ModelOffering, error) {
	if db == nil || upstreamModelID == "" {
		return nil, nil
	}
	var offerings []ModelOffering
	// 闸门（U-1）：供给"已发布"还不够 —— 它所属的**分享分组**也必须处于「已发布」。
	// 否则草稿/待审核/已暂停的分组会继续对外服务（这正是"新建渠道后直接就确定"的根因）。
	// 用 TableName() 取表名、只做普通列等值比较 ⇒ SQLite/MySQL/PostgreSQL 通用。
	channelTable := Channel{}.TableName()
	groupTable := ShareGroup{}.TableName()
	groupPublished := "channel_id IN (SELECT id FROM " + channelTable +
		" WHERE share_group_id IN (SELECT id FROM " + groupTable +
		" WHERE publish_status = ? AND deleted_at IS NULL) AND deleted_at IS NULL)"
	err := db.Where("upstream_model_id = ? AND status = ?", upstreamModelID, OfferingStatusPublished).
		Where(groupPublished, ShareGroupPublishPublished).
		Order("id ASC").Find(&offerings).Error
	return offerings, err
}
