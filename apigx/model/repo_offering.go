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
	return db.Create(offering).Error
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
