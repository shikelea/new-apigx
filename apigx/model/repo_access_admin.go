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
	"errors"
	"fmt"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// 访问控制（分享者侧）：列出/撤销邀请、对指定用户授权与撤销。
//
// 安全前提：这些动作**只能由资源所有者**执行。校验失败一律返回 ErrNotFound 而非 403 ——
// 不向调用者确认"这个资源存在但不属于你"，否则接口就变成了他人资源 ID 的探测器。

// ResourceOwnerUserID 返回资源（分组/供给）的所有者；资源不存在时返回 ErrNotFound。
func ResourceOwnerUserID(db *gorm.DB, resourceType int, resourceID int64) (int64, error) {
	if db == nil {
		return 0, errors.New("apigxmodel: nil db")
	}
	switch resourceType {
	case GrantResourceGroup:
		var group ShareGroup
		if err := db.Where("id = ? AND deleted_at IS NULL", resourceID).First(&group).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return 0, ErrNotFound
			}
			return 0, err
		}
		return group.OwnerUserID, nil
	case GrantResourceOffering:
		var offering ModelOffering
		if err := db.Where("id = ? AND deleted_at IS NULL", resourceID).First(&offering).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return 0, ErrNotFound
			}
			return 0, err
		}
		var channel Channel
		if err := db.Where("id = ? AND deleted_at IS NULL", offering.ChannelID).First(&channel).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return 0, ErrNotFound
			}
			return 0, err
		}
		return channel.OwnerUserID, nil
	default:
		return 0, fmt.Errorf("%w: unsupported resource type", ErrInvalidArgument)
	}
}

// assertResourceOwnedBy 校验资源属于该用户；不是则返回 ErrNotFound（理由见文件头注释）。
func assertResourceOwnedBy(db *gorm.DB, userID int64, resourceType int, resourceID int64) error {
	ownerID, err := ResourceOwnerUserID(db, resourceType, resourceID)
	if err != nil {
		return err
	}
	if ownerID != userID {
		return ErrNotFound
	}
	return nil
}

// InviteView 是邀请列表对外的一行（**不含 TokenHash** —— 即便不可反推也不外泄）。
type InviteView struct {
	ID              int64
	InviteType      int
	TokenPrefix     string
	TargetScopeType int
	TargetScopeID   int64
	MaxUses         int64
	UsedCount       int64
	Status          int
	ExpiresAt       int64
	RevokeGrants    bool
	CreatedByUserID int64
	CreatedAt       int64
}

// ListAccessInvitesByCreator 列出该分享者创建的**资源访问邀请**。
func ListAccessInvitesByCreator(db *gorm.DB, creatorUserID int64, limit int) ([]InviteView, error) {
	if db == nil {
		return nil, errors.New("apigxmodel: nil db")
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var rows []Invite
	if err := db.Where("created_by_user_id = ? AND invite_type = ?", creatorUserID, InviteTypeAccess).
		Order("id DESC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]InviteView, 0, len(rows))
	for _, row := range rows {
		out = append(out, InviteView{
			ID: row.ID, InviteType: row.InviteType, TokenPrefix: row.TokenPrefix,
			TargetScopeType: row.TargetScopeType, TargetScopeID: row.TargetScopeID,
			MaxUses: row.MaxUses, UsedCount: row.UsedCount, Status: row.Status,
			ExpiresAt: row.ExpiresAt, RevokeGrants: row.RevokeGrants,
			CreatedByUserID: row.CreatedByUserID, CreatedAt: row.CreatedAt,
		})
	}
	return out, nil
}

// ListRegistrationInvites lists platform registration invites for an
// administrator. The caller performs the role check; the repository keeps the
// type boundary explicit so resource access invites never leak into this list.
func ListRegistrationInvites(db *gorm.DB, limit int) ([]InviteView, error) {
	if db == nil {
		return nil, errors.New("apigxmodel: nil db")
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var rows []Invite
	if err := db.Where("invite_type = ?", InviteTypeRegistration).
		Order("id DESC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]InviteView, 0, len(rows))
	for _, row := range rows {
		out = append(out, InviteView{
			ID: row.ID, InviteType: row.InviteType, TokenPrefix: row.TokenPrefix,
			TargetScopeType: row.TargetScopeType, TargetScopeID: row.TargetScopeID,
			MaxUses: row.MaxUses, UsedCount: row.UsedCount, Status: row.Status,
			ExpiresAt: row.ExpiresAt, CreatedByUserID: row.CreatedByUserID, CreatedAt: row.CreatedAt,
		})
	}
	return out, nil
}

// RevokeRegistrationInvite stops future registrations with one platform
// invite. It does not change users who already registered with the code.
func RevokeRegistrationInvite(db *gorm.DB, inviteID, revokedByUserID int64) error {
	if db == nil {
		return errors.New("apigxmodel: nil db")
	}
	if inviteID <= 0 || revokedByUserID <= 0 {
		return fmt.Errorf("%w: invite and revoker are required", ErrInvalidArgument)
	}
	now := common.GetTimestamp()
	res := db.Model(&Invite{}).
		Where("id = ? AND invite_type = ? AND status IN ?", inviteID, InviteTypeRegistration,
			[]int{InviteStatusActive, InviteStatusExhaust}).
		Updates(map[string]any{
			"status": InviteStatusRevoked, "revoked_at": now,
			"revoked_by_user_id": revokedByUserID, "updated_at": now,
		})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// RevokeAccessInvite 撤销邀请；revokeGrants 为真时**连带撤销由它产生的授权**（§7 要求的可选项）。
//
// 只能撤销自己创建的、且仍处"有效"的邀请：重复撤销返回 ErrNotFound（状态单向，不制造"撤销了两次"的错觉）。
func RevokeAccessInvite(db *gorm.DB, creatorUserID, inviteID int64, revokeGrants bool) error {
	if db == nil {
		return errors.New("apigxmodel: nil db")
	}
	now := common.GetTimestamp()
	return db.Transaction(func(tx *gorm.DB) error {
		// 允许从"有效"**或"已用尽"**撤销：单次邀请被兑换完就变成"已用尽"，但所有者
		// 仍然必须能撤销**它产生的授权**（§4「访问撤销」）。只有"已撤销"不再重复处理。
		res := tx.Model(&Invite{}).
			Where("id = ? AND created_by_user_id = ? AND invite_type = ? AND status IN ?",
				inviteID, creatorUserID, InviteTypeAccess, []int{InviteStatusActive, InviteStatusExhaust}).
			Updates(map[string]any{
				"status": InviteStatusRevoked, "revoked_at": now, "revoked_by_user_id": creatorUserID,
				"updated_at": now,
			})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return ErrNotFound
		}
		if revokeGrants {
			// 连带撤销由它产生的授权（无论该邀请当前是"有效"还是"已用尽"）
			return tx.Where("source_invite_id = ?", inviteID).Delete(&Grant{}).Error
		}
		return nil
	})
}

// CreateGrantForUser 把某资源授权给指定用户（§4「指定用户」）。只能由**资源所有者**执行。
//
// expiresAt = 0 表示不过期；重复授予同一 (主体, 资源) 按唯一索引幂等（不产生重复行）。
func CreateGrantForUser(db *gorm.DB, ownerUserID int64, resourceType int, resourceID int64, targetUserID int64, expiresAt int64) (*Grant, error) {
	if db == nil {
		return nil, errors.New("apigxmodel: nil db")
	}
	if targetUserID <= 0 {
		return nil, fmt.Errorf("%w: target user is required", ErrInvalidArgument)
	}
	if err := assertResourceOwnedBy(db, ownerUserID, resourceType, resourceID); err != nil {
		return nil, err
	}
	now := common.GetTimestamp()
	grant := Grant{
		SubjectType:     GrantSubjectUser,
		SubjectID:       targetUserID,
		ResourceType:    resourceType,
		ResourceID:      resourceID,
		ScopeKind:       GrantScopeAssigned,
		GrantedByUserID: ownerUserID,
		ExpiresAt:       expiresAt,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := db.Clauses(clause.OnConflict{DoNothing: true}).Create(&grant).Error; err != nil {
		return nil, err
	}
	var created Grant
	if err := db.Where("subject_type = ? AND subject_id = ? AND resource_type = ? AND resource_id = ? AND source_invite_id = 0",
		GrantSubjectUser, targetUserID, resourceType, resourceID).First(&created).Error; err != nil {
		return nil, err
	}
	return &created, nil
}

// ListGrantsForResource 列出某资源上的授权（只能由**资源所有者**查看）。
func ListGrantsForResource(db *gorm.DB, ownerUserID int64, resourceType int, resourceID int64, limit int) ([]Grant, error) {
	if db == nil {
		return nil, errors.New("apigxmodel: nil db")
	}
	if err := assertResourceOwnedBy(db, ownerUserID, resourceType, resourceID); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var rows []Grant
	err := db.Where("resource_type = ? AND resource_id = ?", resourceType, resourceID).
		Order("id DESC").Limit(limit).Find(&rows).Error
	return rows, err
}

// DeleteGrant 撤销一条授权；只能撤销**自己资源上**的授权（§4「访问撤销」）。
func DeleteGrant(db *gorm.DB, ownerUserID, grantID int64) error {
	if db == nil {
		return errors.New("apigxmodel: nil db")
	}
	var grant Grant
	if err := db.Where("id = ?", grantID).First(&grant).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrNotFound
		}
		return err
	}
	if err := assertResourceOwnedBy(db, ownerUserID, grant.ResourceType, grant.ResourceID); err != nil {
		return err
	}
	res := db.Where("id = ?", grantID).Delete(&Grant{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}
