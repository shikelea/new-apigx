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

package apigxapi

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/apigx/model"
	"github.com/gin-gonic/gin"
)

// 访问控制接口（计划书 §4「访问控制：公开、邀请兑换、指定用户、仅自己；访问撤销与到期」、§5「邀请准入」）。
//
// 安全要点（对应 OWASP ASVS 与 Cheat Sheet 的相关要求，详见本轮 TASK_LOG 记录）：
//   - 邀请码是**凭证**：只存 SHA-256 哈希，明文仅在创建响应里出现一次，不入日志；
//   - **原子核销、单次使用**：并发兑换同一码只有一个成功（见 model.RedeemAccessInvite）；
//   - **不可枚举**：兑换失败（不存在/已撤销/已过期/已用尽）返回同一个 404，不区分原因；
//   - **越权防护**：发邀请/授权/撤销/查授权列表都要求调用者是**资源所有者**，
//     非所有者一律 404（不确认"资源存在但不属于你"）。

// createAccessInviteRequest 发放邀请（资源访问邀请，或注册邀请）。
type createAccessInviteRequest struct {
	// InviteType 0=注册邀请（§5「邀请准入」）/ 1=资源访问邀请（默认）。
	// 两类邀请**校验逻辑独立**（§10.1），因此走同一个入口但分支处理。
	InviteType      int   `json:"invite_type"`
	TargetScopeType int   `json:"target_scope_type"` // 1=分享分组 / 2=模型供给
	TargetScopeID   int64 `json:"target_scope_id"`
	MaxUses         int64 `json:"max_uses"`   // 0=不限次数
	ExpiresAt       int64 `json:"expires_at"` // UTC epoch 秒；0=不过期
	RevokeGrants    bool  `json:"revoke_grants"`
}

// createAccessInvite 发放邀请；**明文邀请码只在这里返回一次**。
func (h *handler) createAccessInvite(c *gin.Context) {
	owner, valid := h.ownerID(c)
	if !valid {
		return
	}
	var req createAccessInviteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "invalid_body", "invalid request body")
		return
	}
	if req.MaxUses < 0 || req.ExpiresAt < 0 {
		fail(c, http.StatusBadRequest, "invalid_body", "max_uses and expires_at must not be negative")
		return
	}
	// 注册邀请（§5）：**只由平台侧发放**，且与资源邀请的校验逻辑完全分开。
	// 它不绑定任何资源、也不携带任何角色信息 ⇒ 不可能借邀请码提权（§10.1）。
	if req.InviteType == apigxmodel.InviteTypeRegistration {
		if !requirePlatformReviewer(c) {
			return
		}
		token, invite, err := apigxmodel.CreateRegistrationInvite(h.db, owner, req.MaxUses, req.ExpiresAt)
		if err != nil {
			failFromRepo(c, err)
			return
		}
		ok(c, gin.H{
			"token":        token, // 明文**仅此一次**
			"token_prefix": invite.TokenPrefix,
			"id":           invite.ID,
			"invite_type":  invite.InviteType,
			"max_uses":     invite.MaxUses,
			"expires_at":   invite.ExpiresAt,
			"status":       invite.Status,
		})
		return
	}

	// 注意两套枚举**不同**：邀请范围 1=分组/2=供给，授权资源 0=分组/1=供给。
	// 混用会把"分组"当成"供给"去查，结果永远是"资源不存在"（这个坑已被用例抓到过一次）。
	if req.TargetScopeType != apigxmodel.InviteTargetGroup && req.TargetScopeType != apigxmodel.InviteTargetOffering {
		fail(c, http.StatusBadRequest, "invalid_body", "unsupported target_scope_type")
		return
	}
	resourceType := apigxmodel.GrantResourceGroup
	if req.TargetScopeType == apigxmodel.InviteTargetOffering {
		resourceType = apigxmodel.GrantResourceOffering
	}

	// 校验归属：不是自己的资源一律 404（不确认"存在但不属于你"）
	resourceOwnerID, ownerErr := apigxmodel.ResourceOwnerUserID(h.db, resourceType, req.TargetScopeID)
	if ownerErr != nil {
		failFromRepo(c, ownerErr)
		return
	}
	if resourceOwnerID != owner {
		fail(c, http.StatusNotFound, "not_found", "resource not found")
		return
	}

	token, invite, err := apigxmodel.CreateAccessInvite(h.db, owner, req.TargetScopeType, req.TargetScopeID,
		req.MaxUses, req.ExpiresAt, req.RevokeGrants)
	if err != nil {
		failFromRepo(c, err)
		return
	}
	ok(c, gin.H{
		// token 明文**仅此一次**：库里只有哈希，之后再无法取回
		"token":             token,
		"token_prefix":      invite.TokenPrefix,
		"id":                invite.ID,
		"target_scope_type": invite.TargetScopeType,
		"target_scope_id":   invite.TargetScopeID,
		"max_uses":          invite.MaxUses,
		"expires_at":        invite.ExpiresAt,
		"revoke_grants":     invite.RevokeGrants,
		"status":            invite.Status,
	})
}

// listAccessInvites 列出自己创建的邀请（不含哈希与明文）。
func (h *handler) listAccessInvites(c *gin.Context) {
	owner, valid := h.ownerID(c)
	if !valid {
		return
	}
	rows, err := apigxmodel.ListAccessInvitesByCreator(h.db, owner, 100)
	if err != nil {
		failFromRepo(c, err)
		return
	}
	items := make([]gin.H, 0, len(rows))
	for _, row := range rows {
		items = append(items, gin.H{
			"id": row.ID, "token_prefix": row.TokenPrefix,
			"target_scope_type": row.TargetScopeType, "target_scope_id": row.TargetScopeID,
			"max_uses": row.MaxUses, "used_count": row.UsedCount, "status": row.Status,
			"expires_at": row.ExpiresAt, "revoke_grants": row.RevokeGrants, "created_at": row.CreatedAt,
		})
	}
	ok(c, gin.H{"items": items, "total": len(items)})
}

// revokeAccessInvite 撤销邀请（可选连带撤销由它产生的授权）。
func (h *handler) revokeAccessInvite(c *gin.Context) {
	owner, valid := h.ownerID(c)
	if !valid {
		return
	}
	id, valid := paramID(c, "id")
	if !valid {
		return
	}
	revokeGrants := c.Query("revoke_grants") == "true"
	if err := apigxmodel.RevokeAccessInvite(h.db, owner, id, revokeGrants); err != nil {
		failFromRepo(c, err)
		return
	}
	ok(c, gin.H{"id": id, "status": apigxmodel.InviteStatusRevoked, "revoke_grants": revokeGrants})
}

// redeemAccessInvite 访客**兑换**邀请码，换取对该资源的访问权（§4「邀请兑换」）。
func (h *handler) redeemAccessInvite(c *gin.Context) {
	owner, valid := h.ownerID(c)
	if !valid {
		return
	}
	var req struct {
		Token string `json:"token"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "invalid_body", "invalid request body")
		return
	}
	if strings.TrimSpace(req.Token) == "" {
		fail(c, http.StatusBadRequest, "invalid_body", "token is required")
		return
	}
	grant, err := apigxmodel.RedeemAccessInvite(h.db, req.Token, owner)
	if err != nil {
		// 失败原因**不区分**（不存在/已撤销/已过期/已用尽同一结果），避免被用来枚举有效邀请码
		failFromRepo(c, err)
		return
	}
	ok(c, gin.H{
		"grant_id":         grant.ID,
		"resource_type":    grant.ResourceType,
		"resource_id":      grant.ResourceID,
		"scope_kind":       grant.ScopeKind,
		"source_invite_id": grant.SourceInviteID,
		"expires_at":       grant.ExpiresAt,
	})
}

// createGrant 把资源授权给**指定用户**（§4「指定用户」）。
func (h *handler) createGrant(c *gin.Context) {
	owner, valid := h.ownerID(c)
	if !valid {
		return
	}
	var req struct {
		ResourceType int   `json:"resource_type"`
		ResourceID   int64 `json:"resource_id"`
		TargetUserID int64 `json:"target_user_id"`
		ExpiresAt    int64 `json:"expires_at"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "invalid_body", "invalid request body")
		return
	}
	grant, err := apigxmodel.CreateGrantForUser(h.db, owner, req.ResourceType, req.ResourceID, req.TargetUserID, req.ExpiresAt)
	if err != nil {
		failFromRepo(c, err)
		return
	}
	ok(c, gin.H{
		"id": grant.ID, "subject_type": grant.SubjectType, "subject_id": grant.SubjectID,
		"resource_type": grant.ResourceType, "resource_id": grant.ResourceID,
		"scope_kind": grant.ScopeKind, "expires_at": grant.ExpiresAt,
	})
}

// listGrants 列出自己资源上的授权（§4「访问撤销与到期」需要能看到）。
func (h *handler) listGrants(c *gin.Context) {
	owner, valid := h.ownerID(c)
	if !valid {
		return
	}
	resourceType, err := strconv.Atoi(c.DefaultQuery("resource_type", "0"))
	if err != nil {
		fail(c, http.StatusBadRequest, "invalid_query", "resource_type must be a number")
		return
	}
	resourceID, err := strconv.ParseInt(c.DefaultQuery("resource_id", "0"), 10, 64)
	if err != nil || resourceID <= 0 {
		fail(c, http.StatusBadRequest, "invalid_query", "resource_id must be a positive number")
		return
	}
	rows, err := apigxmodel.ListGrantsForResource(h.db, owner, resourceType, resourceID, 100)
	if err != nil {
		failFromRepo(c, err)
		return
	}
	items := make([]gin.H, 0, len(rows))
	for _, row := range rows {
		items = append(items, gin.H{
			"id": row.ID, "subject_type": row.SubjectType, "subject_id": row.SubjectID,
			"scope_kind": row.ScopeKind, "source_invite_id": row.SourceInviteID,
			"expires_at": row.ExpiresAt, "created_at": row.CreatedAt,
		})
	}
	ok(c, gin.H{"items": items, "total": len(items)})
}

// deleteGrant 撤销一条授权（§4「访问撤销」）。
func (h *handler) deleteGrant(c *gin.Context) {
	owner, valid := h.ownerID(c)
	if !valid {
		return
	}
	id, valid := paramID(c, "id")
	if !valid {
		return
	}
	if err := apigxmodel.DeleteGrant(h.db, owner, id); err != nil {
		failFromRepo(c, err)
		return
	}
	ok(c, gin.H{"id": id, "deleted": true})
}
