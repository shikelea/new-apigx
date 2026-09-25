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
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// 注册邀请准入（计划书 §5「必须明确修改的底座行为」第 2 条）：
//
//	「原有 aff_code 用于推广归因，**不等于**必须提交有效邀请码才能注册。
//	  新增真正的注册邀请策略及原子核销，不复用其名称就宣称功能完成。」
//
// 因此这里**不复用** aff_code：注册邀请码是我们自己的 `apigx_invites`（invite_type=0），
// 与资源访问邀请码（invite_type=1）**共用表但校验逻辑独立**（§10.1）。
//
// 安全要点：
//   - **原子核销**：与资源邀请同一套"带条件 UPDATE 抢名额"的写法，并发注册时不会双花；
//   - **失败不区分原因**：不存在 / 已撤销 / 已过期 / 已用尽 ⇒ 同一个错误，避免枚举；
//   - **不隐式授予管理员权限**：核销只负责"这个码可用"，创建出来的用户一律用**系统默认**角色与分组，
//     邀请码本身不携带任何角色信息（§10.1 明确要求）。

// RedeemRegistrationInvite 原子核销一个**注册邀请码**。
//
// 返回被核销的邀请（调用方在"用户创建失败"时应当调用 RefundRegistrationInvite 回补）。
func RedeemRegistrationInvite(db *gorm.DB, token string, now int64) (*Invite, error) {
	if db == nil {
		return nil, errors.New("apigxmodel: nil db")
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, ErrNotFound
	}
	if now <= 0 {
		now = common.GetTimestamp()
	}
	hash := hashInviteToken(token)

	var invite Invite
	err := db.Transaction(func(tx *gorm.DB) error {
		res := tx.Model(&Invite{}).
			Where("token_hash = ? AND invite_type = ? AND status = ? AND (expires_at = 0 OR expires_at > ?) AND (max_uses = 0 OR used_count < max_uses)",
				hash, InviteTypeRegistration, InviteStatusActive, now).
			Updates(map[string]any{
				"used_count": gorm.Expr("used_count + 1"),
				"updated_at": now,
			})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return ErrNotFound
		}
		if err := tx.Where("token_hash = ? AND invite_type = ?", hash, InviteTypeRegistration).First(&invite).Error; err != nil {
			return err
		}
		if invite.MaxUses > 0 && invite.UsedCount >= invite.MaxUses {
			return tx.Model(&Invite{}).Where("id = ? AND status = ?", invite.ID, InviteStatusActive).
				Updates(map[string]any{"status": InviteStatusExhaust, "updated_at": now}).Error
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &invite, nil
}

// RefundRegistrationInvite 回补一次核销（**仅用于补偿**：用户创建失败时把名额还回去）。
//
// 只减次数、不复活已撤销的邀请：若该邀请在核销后被撤销，则保持撤销状态（撤销优先）。
func RefundRegistrationInvite(db *gorm.DB, inviteID int64, now int64) error {
	if db == nil {
		return errors.New("apigxmodel: nil db")
	}
	if inviteID <= 0 {
		return fmt.Errorf("%w: invite id is required", ErrInvalidArgument)
	}
	if now <= 0 {
		now = common.GetTimestamp()
	}
	res := db.Model(&Invite{}).
		Where("id = ? AND used_count > 0", inviteID).
		Updates(map[string]any{
			"used_count": gorm.Expr("used_count - 1"),
			"updated_at": now,
			// 若此前因用尽被置为"已用尽"，回补后重新可用
			"status": gorm.Expr("CASE WHEN status = ? THEN ? ELSE status END", InviteStatusExhaust, InviteStatusActive),
		})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// CreateRegistrationInvite 发放注册邀请码（返回一次性明文）。
//
// 与资源邀请分开的入口，避免"用一个接口既能发注册码又能发资源码"造成的口径混淆。
func CreateRegistrationInvite(db *gorm.DB, createdByUserID int64, maxUses int64, expiresAt int64) (string, *Invite, error) {
	if db == nil {
		return "", nil, errors.New("apigxmodel: nil db")
	}
	if createdByUserID <= 0 {
		return "", nil, fmt.Errorf("%w: creator is required", ErrInvalidArgument)
	}
	raw, err := randomInviteToken()
	if err != nil {
		return "", nil, err
	}
	now := common.GetTimestamp()
	invite := &Invite{
		InviteType:      InviteTypeRegistration,
		TokenHash:       hashInviteToken(raw),
		TokenPrefix:     raw[:8],
		TargetScopeType: InviteTargetNone,
		TargetScopeID:   0,
		MaxUses:         maxUses,
		Status:          InviteStatusActive,
		ExpiresAt:       expiresAt,
		CreatedByUserID: createdByUserID,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := db.Create(invite).Error; err != nil {
		return "", nil, err
	}
	return raw, invite, nil
}
