/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/

package apigxmodel

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// OfferingAccessResource 是出站授权所需的最小资源快照。
//
// 它刻意只包含权限判定需要的字段，不把上游地址、凭据或内部配置带进路由层。
// BaseChannelID 用于把 APIGX 分享渠道映射回 New API 的实际候选渠道。
type OfferingAccessResource struct {
	OfferingID      int64
	ShareChannelID  int64
	BaseChannelID   int64
	OwnerUserID     int64
	ShareGroupID    int64
	GroupVisibility int
}

// SourceAccessSnapshot 是某个调用主体针对一个模型的授权快照。
// ManagedBaseChannelIDs 包含 APIGX 曾创建过的全部底座渠道；Allowed* 只包含当前仍可路由且有权访问的资源。
// 两者做差即可在候选阶段排除草稿、暂停、删除或无权访问的分享渠道，同时不影响管理员自建渠道。
type SourceAccessSnapshot struct {
	ManagedBaseChannelIDs []int
	AllowedBaseChannelIDs []int
	AllowedOfferingIDs    map[int64]struct{}
	AllowedResources      []OfferingAccessResource
}

// 访问控制：邀请与授权（计划书 §4「访问控制：公开、邀请兑换、指定用户、仅自己；访问撤销与到期」、
// §5「邀请准入」、§10.1「注册邀请码与资源访问邀请码使用独立类型和校验逻辑」）。
//
// # 在此之前的状态
//
// `apigx_invites` / `apigx_grants` 两张表**只有 schema 与设计注释**，没有任何写入方或读取方
// —— 也就是说这项能力实际上没实现。本文件补上最要紧的三件事：
//
//  1. **发放邀请**：只存 **SHA-256 哈希**（明文只在返回时出现一次，永不入库、永不记日志）；
//  2. **原子核销**：一条 `UPDATE ... WHERE token_hash = ? AND status = 0 AND used_count < max_uses ...`
//     —— 并发兑换同一码时**只有一个**能成功（模型注释里已经写明这个形状）；
//  3. **访问判定**：主体（用户/平台 Key）是否有权访问某资源（分组/供给）—— 所有者本人或有效授权。

// 邀请类型（与模型的列注释一致）。
const (
	InviteTypeRegistration = 0 // 注册邀请
	InviteTypeAccess       = 1 // 资源访问邀请
)

// 邀请状态。
const (
	InviteStatusActive   = 0 // 有效
	InviteStatusRevoked  = 1 // 已撤销
	InviteStatusExhaust  = 2 // 已用尽
	InviteStatusExpired  = 3 // 已过期
	InviteTargetNone     = 0 // 无目标（注册邀请）
	InviteTargetGroup    = 1 // 分享分组
	InviteTargetOffering = 2 // 模型供给
)

// 授权主体与来源。
const (
	GrantSubjectUser      = 0
	GrantSubjectAPIKey    = 1
	GrantSubjectInvite    = 2
	GrantResourceGroup    = 0
	GrantResourceOffering = 1
	GrantScopeAssigned    = 0 // 指定用户
	GrantScopeRedeemed    = 1 // 邀请兑换
	GrantScopeOwner       = 2 // 所有者本人
)

// accessInviteTokenBytes 是邀请码的随机字节数（base64 后约 22 字符，够用且便于复制）。
const accessInviteTokenBytes = 16

// hashInviteToken 计算邀请码的存储哈希。
//
// **只存哈希**：数据库泄露时邀请码不可反推；明文只在创建响应里出现一次。
func hashInviteToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// CreateAccessInvite 发放一个**资源访问邀请**，返回一次性明文邀请码。
//
// `maxUses <= 0` 表示不限次数（与模型的四态限制语义一致：0=未配置/不限）；
// `expiresAt` 为 0 表示不过期。
func CreateAccessInvite(db *gorm.DB, createdByUserID int64, targetScopeType int, targetScopeID int64, maxUses int64, expiresAt int64, revokeGrants bool) (string, *Invite, error) {
	if db == nil {
		return "", nil, errors.New("apigxmodel: nil db")
	}
	if createdByUserID <= 0 {
		return "", nil, fmt.Errorf("%w: creator is required", ErrInvalidArgument)
	}
	if targetScopeType != InviteTargetGroup && targetScopeType != InviteTargetOffering {
		return "", nil, fmt.Errorf("%w: unsupported invite target scope", ErrInvalidArgument)
	}
	if targetScopeID <= 0 {
		return "", nil, fmt.Errorf("%w: target scope id is required", ErrInvalidArgument)
	}

	token, err := randomInviteToken()
	if err != nil {
		return "", nil, err
	}

	now := common.GetTimestamp()
	invite := &Invite{
		InviteType:      InviteTypeAccess,
		TokenHash:       hashInviteToken(token),
		TokenPrefix:     token[:8],
		TargetScopeType: targetScopeType,
		TargetScopeID:   targetScopeID,
		MaxUses:         maxUses,
		RevokeGrants:    revokeGrants,
		Status:          InviteStatusActive,
		ExpiresAt:       expiresAt,
		CreatedByUserID: createdByUserID,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := db.Create(invite).Error; err != nil {
		return "", nil, err
	}
	return token, invite, nil
}

// RedeemAccessInvite 核销邀请码并据此授予访问权（**原子、单次**）。
//
// 关键点：
//   - 用一条带条件的 `UPDATE` 抢占名额 —— 并发兑换同一码时只有一条能成功，
//     不会出现"两个人都拿到了授权但只扣了一次次数"或反之；
//   - 未命中（不存在/已撤销/已用尽/已过期）一律返回 `ErrNotFound`，**不区分原因** ✓
//     （避免把"这个码存在但过期了"这种信息透给攻击者，用于枚举有效码）；
//   - 授权写入用 `ON CONFLICT DO NOTHING`（唯一索引 `idx_apigx_grant_uniq`）⇒ 幂等，
//     同一主体重复兑换同一资源不会产生重复授权。
func RedeemAccessInvite(db *gorm.DB, token string, redeemerUserID int64) (*Grant, error) {
	if db == nil {
		return nil, errors.New("apigxmodel: nil db")
	}
	token = strings.TrimSpace(token)
	if token == "" || redeemerUserID <= 0 {
		return nil, ErrNotFound
	}
	now := common.GetTimestamp()

	var invite Invite
	err := db.Transaction(func(tx *gorm.DB) error {
		// 先按条件原子抢占（次数与状态一起判定）
		// 必须**限定邀请类型**：§10.1 要求两类邀请校验逻辑独立。
		// 不限定的话，注册类邀请（target_scope_type=0）会被当成资源邀请核销，
		// 生成 resource_id=0 的垃圾授权 —— 这是跨类型漏洞。
		res := tx.Model(&Invite{}).
			Where("token_hash = ? AND invite_type = ? AND status = ? AND (expires_at = 0 OR expires_at > ?) AND (max_uses = 0 OR used_count < max_uses)",
				hashInviteToken(token), InviteTypeAccess, InviteStatusActive, now).
			Updates(map[string]any{
				"used_count": gorm.Expr("used_count + 1"),
				"updated_at": now,
			})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			// 不存在 / 已撤销 / 已过期 / 已用尽 —— 一律同一结果
			return ErrNotFound
		}
		if err := tx.Where("token_hash = ? AND invite_type = ?", hashInviteToken(token), InviteTypeAccess).First(&invite).Error; err != nil {
			return err
		}

		// 次数用尽则置状态（不影响本次已获得的授权）
		if invite.MaxUses > 0 && invite.UsedCount >= invite.MaxUses {
			if err := tx.Model(&Invite{}).Where("id = ? AND status = ?", invite.ID, InviteStatusActive).
				Updates(map[string]any{"status": InviteStatusExhaust, "updated_at": now}).Error; err != nil {
				return err
			}
		}

		// 目标范围 -> 授权资源
		resourceType := GrantResourceGroup
		if invite.TargetScopeType == InviteTargetOffering {
			resourceType = GrantResourceOffering
		}
		grant := Grant{
			SubjectType:     GrantSubjectUser,
			SubjectID:       redeemerUserID,
			ResourceType:    resourceType,
			ResourceID:      invite.TargetScopeID,
			ScopeKind:       GrantScopeRedeemed,
			SourceInviteID:  invite.ID,
			GrantedByUserID: invite.CreatedByUserID,
			CreatedAt:       now,
			UpdatedAt:       now,
		}
		// 幂等：同一 (主体, 资源, 来源邀请) 已存在则不重复插入
		return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&grant).Error
	})
	if err != nil {
		return nil, err
	}

	var created Grant
	if err := db.Where("subject_type = ? AND subject_id = ? AND resource_type = ? AND resource_id = ? AND source_invite_id = ?",
		GrantSubjectUser, redeemerUserID, grantResourceTypeForInvite(&invite), invite.TargetScopeID, invite.ID).
		First(&created).Error; err != nil {
		return nil, err
	}
	return &created, nil
}

func grantResourceTypeForInvite(invite *Invite) int {
	if invite.TargetScopeType == InviteTargetOffering {
		return GrantResourceOffering
	}
	return GrantResourceGroup
}

// SubjectCanAccessResource 判断主体是否可访问某资源：**所有者本人**，或存在**未过期**的授权。
//
// 未过期 = `expires_at = 0`（不过期）或 `expires_at > now` —— 与"访问撤销与到期"的要求一致。
func SubjectCanAccessResource(db *gorm.DB, subjectType int, subjectID int64, resourceType int, resourceID int64, now int64) (bool, error) {
	if db == nil {
		return false, errors.New("apigxmodel: nil db")
	}
	if subjectID <= 0 || resourceID <= 0 {
		return false, nil
	}
	if now <= 0 {
		now = common.GetTimestamp()
	}

	var ownerID int64
	switch resourceType {
	case GrantResourceGroup:
		var group ShareGroup
		if err := db.Where("id = ? AND deleted_at IS NULL", resourceID).First(&group).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return false, nil
			}
			return false, err
		}
		ownerID = group.OwnerUserID
	case GrantResourceOffering:
		var offering ModelOffering
		if err := db.Where("id = ? AND deleted_at IS NULL", resourceID).First(&offering).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return false, nil
			}
			return false, err
		}
		var channel Channel
		if err := db.Where("id = ? AND deleted_at IS NULL", offering.ChannelID).First(&channel).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return false, nil
			}
			return false, err
		}
		ownerID = channel.OwnerUserID
	default:
		return false, fmt.Errorf("%w: unsupported resource type", ErrInvalidArgument)
	}

	if subjectType == GrantSubjectUser && subjectID == ownerID {
		return true, nil
	}

	var count int64
	if err := db.Model(&Grant{}).
		Where("subject_type = ? AND subject_id = ? AND resource_type = ? AND resource_id = ? AND revoked_at = 0 AND (expires_at = 0 OR expires_at > ?)",
			subjectType, subjectID, resourceType, resourceID, now).
		Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

// GrantExistsForResource 只判断"该主体在该资源上是否有未过期授权"，**不含所有者放行**。
//
// 与 SubjectCanAccessResource 的分工（刻意不合并）：
//   - 前者用于**广场可见性**：广场是发现面，"公开 or 受邀"才是它的口径，
//     分享者本人的私有资源不该出现在广场（他有「我的分享」页）；
//   - 后者用于**访问判定**：所有者本人当然有权访问自己的资源。
//
// 若合并成一个"owner-aware"的函数，广场会顺手把所有者私有资源也显示出来 —— 口径就被悄悄改掉了。
func GrantExistsForResource(db *gorm.DB, subjectType int, subjectID int64, resourceType int, resourceID int64, now int64) (bool, error) {
	if db == nil {
		return false, errors.New("apigxmodel: nil db")
	}
	if subjectID <= 0 || resourceID <= 0 {
		return false, nil
	}
	if now <= 0 {
		now = common.GetTimestamp()
	}
	var count int64
	if err := db.Model(&Grant{}).
		Where("subject_type = ? AND subject_id = ? AND resource_type = ? AND resource_id = ? AND revoked_at = 0 AND (expires_at = 0 OR expires_at > ?)",
			subjectType, subjectID, resourceType, resourceID, now).
		Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

// ResolveSourceAccessSnapshot 统一计算一个用户/平台 Key 对某模型的分享来源访问范围。
//
// 判定顺序固定为：资源存活且已发布 → owner / 公开 / 分组 Grant / Offering Grant。
// 平台 Key 的固定来源、允许分享者与允许模型属于调用凭证约束，由 relayhook 在本结果上继续求交。
// 这里一次性读取 Grant，避免对每条供给做 N+1 查询。
func ResolveSourceAccessSnapshot(db *gorm.DB, viewerUserID, platformKeyID int64, upstreamModelID string, now int64) (SourceAccessSnapshot, error) {
	snapshot := SourceAccessSnapshot{AllowedOfferingIDs: map[int64]struct{}{}}
	if db == nil {
		return snapshot, errors.New("apigxmodel: nil db")
	}
	if viewerUserID <= 0 {
		return snapshot, nil
	}
	if now <= 0 {
		now = common.GetTimestamp()
	}

	// 用 Unscoped 读取历史映射：只要某个底座渠道曾由 APIGX 创建，它就不能在分享资源
	// 被暂停或软删后伪装成普通管理员渠道绕过 ACL。
	var managedBaseIDs64 []int64
	if err := db.Unscoped().Model(&Channel{}).
		Where("base_channel_id > 0").Distinct("base_channel_id").
		Pluck("base_channel_id", &managedBaseIDs64).Error; err != nil {
		return snapshot, err
	}
	managedSeen := make(map[int]struct{}, len(managedBaseIDs64))
	for _, id := range managedBaseIDs64 {
		if id <= 0 {
			continue
		}
		baseID := int(id)
		if _, exists := managedSeen[baseID]; exists {
			continue
		}
		managedSeen[baseID] = struct{}{}
		snapshot.ManagedBaseChannelIDs = append(snapshot.ManagedBaseChannelIDs, baseID)
	}
	if strings.TrimSpace(upstreamModelID) == "" {
		return snapshot, nil
	}

	offeringTable := ModelOffering{}.TableName()
	canonicalTable := CanonicalModel{}.TableName()
	channelTable := Channel{}.TableName()
	groupTable := ShareGroup{}.TableName()

	// A request normally carries the public canonical model ID, while each
	// offering keeps the provider-specific upstream ID that must be sent to
	// its channel. Resolve the request to canonical identities first so an
	// exhausted/blocked provider alias does not hide other published sources
	// of the same model. The upstream-ID match is retained for clients that
	// still call an alias directly.
	var canonicalIDs []int64
	if err := db.Table(offeringTable+" AS o").
		Joins("JOIN "+canonicalTable+" AS cm ON cm.id = o.canonical_model_id").
		Where("(cm.api_model_id = ? OR o.upstream_model_id = ?) AND o.status = ? AND o.deleted_at IS NULL AND o.alive_flag = ?",
			upstreamModelID, upstreamModelID, OfferingStatusPublished, AliveFlagAlive).
		Distinct("o.canonical_model_id").Pluck("o.canonical_model_id", &canonicalIDs).Error; err != nil {
		return snapshot, err
	}

	var resources []OfferingAccessResource
	resourceQuery := db.Table(offeringTable+" AS o").
		Select("o.id AS offering_id, o.channel_id AS share_channel_id, c.base_channel_id AS base_channel_id, "+
			"c.owner_user_id AS owner_user_id, g.id AS share_group_id, g.default_visibility AS group_visibility").
		Joins("JOIN "+channelTable+" AS c ON c.id = o.channel_id AND c.deleted_at IS NULL AND c.alive_flag = ?", AliveFlagAlive).
		Joins("JOIN "+groupTable+" AS g ON g.id = c.share_group_id AND g.deleted_at IS NULL AND g.alive_flag = ?", AliveFlagAlive).
		Where("o.status = ? AND o.deleted_at IS NULL AND o.alive_flag = ? AND g.publish_status = ?",
			OfferingStatusPublished, AliveFlagAlive, ShareGroupPublishPublished)
	if len(canonicalIDs) > 0 {
		resourceQuery = resourceQuery.Where("o.canonical_model_id IN ?", canonicalIDs)
	} else {
		// Legacy rows may predate canonical identity assignment. Preserve the
		// exact upstream lookup when no canonical identity can be resolved.
		resourceQuery = resourceQuery.Where("o.upstream_model_id = ?", upstreamModelID)
	}
	err := resourceQuery.Order("o.id ASC").Scan(&resources).Error
	if err != nil {
		return snapshot, err
	}
	if len(resources) == 0 {
		return snapshot, nil
	}

	groupIDs := make([]int64, 0, len(resources))
	offeringIDs := make([]int64, 0, len(resources))
	for _, resource := range resources {
		groupIDs = append(groupIDs, resource.ShareGroupID)
		offeringIDs = append(offeringIDs, resource.OfferingID)
	}

	granted := map[string]struct{}{}
	subjectSQL := "(subject_type = ? AND subject_id = ?)"
	subjectArgs := []any{GrantSubjectUser, viewerUserID}
	if platformKeyID > 0 {
		subjectSQL = "(" + subjectSQL + " OR (subject_type = ? AND subject_id = ?))"
		subjectArgs = append(subjectArgs, GrantSubjectAPIKey, platformKeyID)
	}
	var grants []Grant
	grantQuery := db.Model(&Grant{}).
		Where(subjectSQL, subjectArgs...).
		Where("revoked_at = 0 AND (expires_at = 0 OR expires_at > ?)", now).
		Where("(resource_type = ? AND resource_id IN ?) OR (resource_type = ? AND resource_id IN ?)",
			GrantResourceGroup, groupIDs, GrantResourceOffering, offeringIDs)
	if err := grantQuery.Find(&grants).Error; err != nil {
		return snapshot, err
	}
	for _, grant := range grants {
		granted[fmt.Sprintf("%d:%d", grant.ResourceType, grant.ResourceID)] = struct{}{}
	}

	allowedBaseSeen := map[int]struct{}{}
	for _, resource := range resources {
		allowed := resource.OwnerUserID == viewerUserID || resource.GroupVisibility == ShareGroupVisibilityPublic
		if !allowed {
			_, groupGranted := granted[fmt.Sprintf("%d:%d", GrantResourceGroup, resource.ShareGroupID)]
			_, offeringGranted := granted[fmt.Sprintf("%d:%d", GrantResourceOffering, resource.OfferingID)]
			allowed = groupGranted || offeringGranted
		}
		if !allowed {
			continue
		}
		snapshot.AllowedOfferingIDs[resource.OfferingID] = struct{}{}
		snapshot.AllowedResources = append(snapshot.AllowedResources, resource)
		baseID := int(resource.BaseChannelID)
		if baseID <= 0 {
			continue
		}
		if _, exists := allowedBaseSeen[baseID]; exists {
			continue
		}
		allowedBaseSeen[baseID] = struct{}{}
		snapshot.AllowedBaseChannelIDs = append(snapshot.AllowedBaseChannelIDs, baseID)
	}
	return snapshot, nil
}

// randomInviteToken 生成一次性邀请码明文（base64url，无填充）。
//
// 两个调用方：资源访问邀请（CreateAccessInvite）与注册邀请（CreateRegistrationInvite）——
// 属于稳定的领域概念，而不是"为缩短调用点"抽出来的机械步骤。
func randomInviteToken() (string, error) {
	raw := make([]byte, accessInviteTokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}
