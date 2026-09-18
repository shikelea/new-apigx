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
	// 对外**标准模型 ID** 与介绍：只有在"上游名 -> 标准模型"的映射**已确认**时才有值
	// （§8.2：客户端从可用目录/调用示例获取标准 ID）。未命中时留空，界面显示上游名。
	CanonicalAPIModelID string
	ModelDescription    string
	// 质量摘要（最近 6 小时窗口）：样本不足时 Insufficient 为 true，界面必须显示"样本不足"而不是编一个数字。
	SampleCount              int64
	ReliabilityLowerBoundPpm int64
	LatencyMsP50             int64
	Insufficient             bool
	Stale                    bool
	// GroupPublicID/GroupName 用于广场按"分享分组"组织（我们的模式）。
	GroupPublicID string
	GroupName     string
	// GroupDescription 供"分享分组页（访客视角）"显示介绍（不可信文本，渲染前转义）
	GroupDescription string
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

// plazaVisibilityPredicate 返回"某浏览者能否看见该广场条目"的 SQL 条件（**列表与详情共用一处**，
// 计划书明确要求两者判据一致；两处各写一份迟早会分叉）。
//
// 可见 = 以下任一：
//  1. 分组**公开**（§4「访问控制：公开」）；
//  2. 该分组或**该供给**上存在**未过期**的授权（§4「指定用户 / 邀请兑换」，§7）。
//
// 授权到期即失效（expires_at = 0 表示不过期）——对应"访问撤销与到期"。
//
// **刻意不含"本人所有"**：广场是**发现面**；分享者看自己的东西走「我的分享」页。
// 把所有者本人的私有分组也塞进广场，既与"广场只暴露公开/受邀"的既有约定冲突
// （既有用例 TestPlazaOnlyExposesPublishedAndPublic 正是如此断言的），也让"私有"名不副实。
func plazaVisibilityPredicate(viewerUserID int64) (string, []any) {
	grantTable := Grant{}.TableName()
	now := common.GetTimestamp()
	condition := "(g.default_visibility = ?" +
		" OR EXISTS (SELECT 1 FROM " + grantTable + " AS gr" +
		" WHERE gr.subject_type = ? AND gr.subject_id = ?" +
		" AND (gr.expires_at = 0 OR gr.expires_at > ?)" +
		" AND ((gr.resource_type = ? AND gr.resource_id = g.id) OR (gr.resource_type = ? AND gr.resource_id = o.id))))" +
		// 闸门（U-1）：公开/受邀**之外**，分组本身还必须处于「已发布」。
		// 放在括号外 ⇒ 公开与受邀两条分支都受它约束（草稿/待审核/已暂停一律不可见）。
		" AND g.publish_status = ?"
	args := []any{
		ShareGroupVisibilityPublic,
		GrantSubjectUser, viewerUserID, now,
		GrantResourceGroup, GrantResourceOffering,
		ShareGroupPublishPublished,
	}
	return condition, args
}

// ListPlazaItems 列出广场条目（已发布 + 分组公开），按更新时间倒序。
func ListPlazaItems(db *gorm.DB, viewerUserID int64, limit, offset int) ([]PlazaItem, int64, error) {
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
		Where("o.deleted_at IS NULL AND o.status = ?", OfferingStatusPublished)

	visibility, visibilityArgs := plazaVisibilityPredicate(viewerUserID)
	query = query.Where(visibility, visibilityArgs...)

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	type row struct {
		ID               int64
		GroupPublicID    string
		GroupName        string
		GroupDescription string
		PublicID         string
		UpstreamModelID  string
		ChannelPublicID  string
		OwnerUserID      int64
		Capabilities     string
		UpdatedAt        int64
	}
	var rows []row
	err := query.Select("o.id AS id, o.public_id AS public_id, o.upstream_model_id AS upstream_model_id, " +
		"c.public_id AS channel_public_id, c.owner_user_id AS owner_user_id, " +
		"g.public_id AS group_public_id, g.name AS group_name, g.description AS group_description, " +
		"o.capabilities AS capabilities, o.updated_at AS updated_at").
		Order("o.updated_at DESC, o.id DESC").Limit(limit).Offset(offset).Scan(&rows).Error
	if err != nil {
		return nil, 0, err
	}

	// 一次性解析本页涉及的所有上游模型名（避免逐个查库）
	upstreamIDs := make([]string, 0, len(rows))
	for _, r := range rows {
		upstreamIDs = append(upstreamIDs, r.UpstreamModelID)
	}
	canonicalByUpstream, resolveErr := ResolveCanonicalModels(db, upstreamIDs)
	if resolveErr != nil {
		// 目录是辅助能力：解析失败不该让广场整体 500，退化为"只显示上游名"
		canonicalByUpstream = map[string]CanonicalModel{}
	}

	items := make([]PlazaItem, 0, len(rows))
	for _, r := range rows {
		declared := Capability{}
		if r.Capabilities != "" {
			_ = common.UnmarshalJsonStr(r.Capabilities, &declared)
		}
		effective, unverified := projectCapabilities(declared)
		// 质量摘要：与路由/解释用的是同一个窗口与同一种口径（Wilson 下界），避免两处结论不一致
		quality, qErr := ResolveQualityView(db, r.ID, QualityWindowSixHour, common.GetTimestamp())
		if qErr != nil {
			quality = QualityView{Insufficient: true}
		}
		items = append(items, PlazaItem{
			OfferingPublicID:         r.PublicID,
			SampleCount:              quality.SampleCount,
			ReliabilityLowerBoundPpm: quality.ReliabilityLowerBoundPpm,
			LatencyMsP50:             quality.LatencyMsP50,
			Insufficient:             quality.Insufficient,
			Stale:                    quality.Stale,
			UpstreamModelID:          r.UpstreamModelID,
			CanonicalAPIModelID:      canonicalByUpstream[r.UpstreamModelID].APIModelID,
			ModelDescription:         canonicalByUpstream[r.UpstreamModelID].Description,
			ChannelPublicID:          r.ChannelPublicID,
			GroupPublicID:            r.GroupPublicID,
			GroupName:                r.GroupName,
			GroupDescription:         r.GroupDescription,
			OwnerUserID:              r.OwnerUserID,
			Capabilities:             effective,
			UnverifiedCapabilities:   unverified,
			UpdatedAt:                r.UpdatedAt,
		})
	}
	return items, total, nil
}

// GetPlazaItem 按供给的对外 public_id 取详情（判据与列表一致：已发布 + 分组公开）。
func GetPlazaItem(db *gorm.DB, viewerUserID int64, offeringPublicID string) (*PlazaItem, error) {
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
	// 可见性与**列表同源**：公开，或该分组/该供给上有未过期授权（**不含本人所有**，理由见列表判据注释）。
	if group.DefaultVisibility != ShareGroupVisibilityPublic {
		allowedGroup, err := GrantExistsForResource(db, GrantSubjectUser, viewerUserID, GrantResourceGroup, group.ID, 0)
		if err != nil {
			return nil, err
		}
		allowedOffering, err := GrantExistsForResource(db, GrantSubjectUser, viewerUserID, GrantResourceOffering, offering.ID, 0)
		if err != nil {
			return nil, err
		}
		if !allowedGroup && !allowedOffering {
			return nil, ErrNotFound
		}
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
