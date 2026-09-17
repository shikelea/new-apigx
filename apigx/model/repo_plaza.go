package apigxmodel

import (
	"errors"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// 广场（T1.9）：把**已发布且对外可见**的供给聚合出来。
//
// 两条硬性判据：
//  1. 供给必须是 `已发布`（草稿/待审核/已驳回都不对外）；
//  2. 所属分享分组的默认可见性必须是 `公开`（受限可见需要 Grant，阶段 1 尚未实现，
//     因此**不放行**——宁可少展示，也不要把没有授权机制的东西暴露出去）。
//
// 另有一条产品约束来自计划书：**只启用已完成能力**。因此能力对外只输出平台当前
// 真正具备的那部分（见 `PlatformSupportedCapabilities`），声明了但尚未具备的单独列出，
// 不混进 capabilities 里冒充可用。

// ShareGroupVisibility 取值（`apigx_share_groups.default_visibility`）。
const (
	ShareGroupVisibilityPrivate int = 0
	ShareGroupVisibilityLimited int = 1
	ShareGroupVisibilityPublic  int = 2
)

// PlazaItem 是广场列表/详情对外暴露的字段集合。
//
// 刻意只包含**与使用者决策相关且不敏感**的信息：模型名、来源的对外标识、
// 已具备的能力、以及不敏感的统计。不含上游地址、渠道内部 ID、凭据、价格明细的内部结构。
type PlazaItem struct {
	OfferingPublicID string
	UpstreamModelID  string
	ChannelPublicID  string
	OwnerUserID      int64
	// Capabilities 是**平台当前真正具备**的能力（见 PlatformSupportedCapabilities）。
	Capabilities Capability
	// UnverifiedCapabilities 是分享者声明、但平台当前尚不具备的能力：如实列出而不是隐藏，
	// 也不混进 Capabilities 冒充可用。
	UnverifiedCapabilities []string
	UpdatedAt              int64
}

// PlatformSupportedCapabilities 返回平台当前真正具备的能力。
//
// 只声明流式：它由底座的中继链路直接支持并已在阶段 0 验证（多格式流式请求均正常）。
// 视觉/工具调用/推理这些**尚未按供给验证**，因此不计入对外能力——这正是
// 「只启用已完成能力」的落地方式（计划书 §11 阶段 1）。
func PlatformSupportedCapabilities() Capability {
	return Capability{Streaming: true}
}

// capabilityFlagNames 把 Capability 的结构化字段映射为可枚举的名称（用于 Unverified 列表）。
func capabilityFlagNames(caps Capability) []string {
	names := make([]string, 0, 4)
	if caps.Streaming {
		names = append(names, "streaming")
	}
	if caps.Vision {
		names = append(names, "vision")
	}
	if caps.Tools {
		names = append(names, "tools")
	}
	if caps.Reasoning {
		names = append(names, "reasoning")
	}
	return names
}

// projectCapabilities 把「声明的能力」投影为「平台具备的能力 + 未验证列表」。
func projectCapabilities(declared Capability) (Capability, []string) {
	supported := PlatformSupportedCapabilities()
	effective := Capability{Streaming: declared.Streaming && supported.Streaming}

	unverified := make([]string, 0, 4)
	for _, name := range capabilityFlagNames(declared) {
		switch name {
		case "streaming":
			// 已被支持，不列入未验证
		case "vision", "tools", "reasoning":
			unverified = append(unverified, name)
		}
	}
	return effective, unverified
}

// ListPlazaItems 列出广场条目（已发布 + 分组公开），按更新时间倒序。
func ListPlazaItems(db *gorm.DB, limit, offset int) ([]PlazaItem, int64, error) {
	if db == nil {
		return nil, 0, errors.New("apigxmodel: nil db")
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}

	// 表名取自各模型自带的 TableName()，与迁移保持一致（不硬编码字符串）
	offeringTable := ModelOffering{}.TableName()
	channelTable := Channel{}.TableName()
	groupTable := ShareGroup{}.TableName()

	query := db.Table(offeringTable+" AS o").
		Joins("JOIN "+channelTable+" AS c ON c.id = o.channel_id AND c.deleted_at IS NULL").
		Joins("JOIN "+groupTable+" AS g ON g.id = c.share_group_id AND g.deleted_at IS NULL").
		Where("o.deleted_at IS NULL AND o.status = ?", OfferingStatusPublished).
		Where("g.default_visibility = ?", ShareGroupVisibilityPublic)

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	type row struct {
		PublicID        string
		UpstreamModelID string
		ChannelPublicID string
		OwnerUserID     int64
		Capabilities    string
		UpdatedAt       int64
	}
	var rows []row
	err := query.Select("o.public_id AS public_id, o.upstream_model_id AS upstream_model_id, " +
		"c.public_id AS channel_public_id, c.owner_user_id AS owner_user_id, " +
		"o.capabilities AS capabilities, o.updated_at AS updated_at").
		Order("o.updated_at DESC, o.id DESC").Limit(limit).Offset(offset).Scan(&rows).Error
	if err != nil {
		return nil, 0, err
	}

	items := make([]PlazaItem, 0, len(rows))
	for _, r := range rows {
		declared := Capability{}
		if r.Capabilities != "" {
			_ = common.UnmarshalJsonStr(r.Capabilities, &declared)
		}
		effective, unverified := projectCapabilities(declared)
		items = append(items, PlazaItem{
			OfferingPublicID:       r.PublicID,
			UpstreamModelID:        r.UpstreamModelID,
			ChannelPublicID:        r.ChannelPublicID,
			OwnerUserID:            r.OwnerUserID,
			Capabilities:           effective,
			UnverifiedCapabilities: unverified,
			UpdatedAt:              r.UpdatedAt,
		})
	}
	return items, total, nil
}

// GetPlazaItem 按供给的对外 public_id 取详情（判据与列表一致：已发布 + 分组公开）。
func GetPlazaItem(db *gorm.DB, offeringPublicID string) (*PlazaItem, error) {
	if db == nil || offeringPublicID == "" {
		return nil, errors.New("apigxmodel: offering public id is required")
	}
	var offering ModelOffering
	if err := db.Where("public_id = ?", offeringPublicID).First(&offering).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if offering.Status != OfferingStatusPublished {
		// 未发布的供给对广场**不存在**（与未找到同义，不泄露存在性）
		return nil, ErrNotFound
	}
	var channel Channel
	if err := db.Where("id = ?", offering.ChannelID).First(&channel).Error; err != nil {
		return nil, ErrNotFound
	}
	var group ShareGroup
	if err := db.Where("id = ?", channel.ShareGroupID).First(&group).Error; err != nil {
		return nil, ErrNotFound
	}
	if group.DefaultVisibility != ShareGroupVisibilityPublic {
		return nil, ErrNotFound
	}

	effective, unverified := projectCapabilities(offering.CapabilityOf())
	return &PlazaItem{
		OfferingPublicID:       offering.PublicID,
		UpstreamModelID:        offering.UpstreamModelID,
		ChannelPublicID:        channel.PublicID,
		OwnerUserID:            channel.OwnerUserID,
		Capabilities:           effective,
		UnverifiedCapabilities: unverified,
		UpdatedAt:              offering.UpdatedAt,
	}, nil
}
