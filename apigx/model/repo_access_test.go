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
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// 访问控制：邀请发放与**原子核销**、以及访问判定。
//
// 最要紧的一条：**并发兑换同一码时只能有一个成功** —— 否则"单次邀请"就是假的，
// 一个码会被多人同时用掉（次数记账与授权数量对不上）。

// 这一组用例都需要一个分组作为授权目标。
func createAccessTestGroup(t *testing.T, owner int64) (*gorm.DB, int64) {
	t.Helper()
	db := newRepoDB(t)
	group := &ShareGroup{OwnerUserID: owner, Name: "受邀分组", DefaultVisibility: ShareGroupVisibilityLimited, PublishStatus: 1}
	require.NoError(t, CreateShareGroup(db, group))
	return db, group.ID
}

func TestRedeemAccessInviteIsAtomicSingleUse(t *testing.T) {
	const owner, redeemerA, redeemerB int64 = 9701, 9702, 9703
	db, groupID := createAccessTestGroup(t, owner)

	// 单次邀请
	token, invite, err := CreateAccessInvite(db, owner, InviteTargetGroup, groupID, 1, 0, true)
	require.NoError(t, err)
	require.NotEmpty(t, token)
	assert.Equal(t, InviteStatusActive, invite.Status)

	// 明文**不入库**（只存哈希与前缀）
	var stored Invite
	require.NoError(t, db.Where("id = ?", invite.ID).First(&stored).Error)
	assert.NotEqual(t, token, stored.TokenHash, "库里不得存明文邀请码")
	assert.Equal(t, hashInviteToken(token), stored.TokenHash)
	assert.Equal(t, token[:8], stored.TokenPrefix)

	// 两个用户**并发**兑换同一个单次码：只能有一个成功
	var wg sync.WaitGroup
	results := make([]error, 2)
	for i, user := range []int64{redeemerA, redeemerB} {
		wg.Add(1)
		go func(idx int, uid int64) {
			defer wg.Done()
			_, results[idx] = RedeemAccessInvite(db, token, uid)
		}(i, user)
	}
	wg.Wait()

	successes := 0
	for _, err := range results {
		if err == nil {
			successes++
		} else {
			require.True(t, errors.Is(err, ErrNotFound), "落败方应当是 ErrNotFound（不区分原因）")
		}
	}
	assert.Equal(t, 1, successes, "单次邀请并发兑换只能有一个成功")

	// 用尽后状态推进
	var after Invite
	require.NoError(t, db.Where("id = ?", invite.ID).First(&after).Error)
	assert.EqualValues(t, 1, after.UsedCount)
	assert.Equal(t, InviteStatusExhaust, after.Status)
}

func TestRedeemAccessInviteRejectsExpiredAndRevoked(t *testing.T) {
	const owner, redeemer int64 = 9711, 9712
	db, groupID := createAccessTestGroup(t, owner)

	// 已过期：expires_at 在过去
	expired, expiredInvite, err := CreateAccessInvite(db, owner, InviteTargetGroup, groupID, 1, 1000, true)
	require.NoError(t, err)
	_, err = RedeemAccessInvite(db, expired, redeemer)
	assert.True(t, errors.Is(err, ErrNotFound), "过期邀请必须被拒")

	// 已撤销
	revoked, revokedInvite, err := CreateAccessInvite(db, owner, InviteTargetGroup, groupID, 1, 0, true)
	require.NoError(t, err)
	require.NoError(t, db.Model(&Invite{}).Where("id = ?", revokedInvite.ID).
		Updates(map[string]any{"status": InviteStatusRevoked}).Error)
	_, err = RedeemAccessInvite(db, revoked, redeemer)
	assert.True(t, errors.Is(err, ErrNotFound), "已撤销邀请必须被拒")

	// 不存在的码：同一结果（不泄露"是否存在"）
	_, err = RedeemAccessInvite(db, "not-a-real-token", redeemer)
	assert.True(t, errors.Is(err, ErrNotFound))

	// 上面三次都失败 ⇒ 不应当产生任何授权
	var count int64
	require.NoError(t, db.Model(&Grant{}).Count(&count).Error)
	assert.EqualValues(t, 0, count, "失败的兑换不得产生授权")
	_ = expiredInvite
}

func TestRedeemAccessInviteGrantsAreIdempotent(t *testing.T) {
	const owner, redeemer int64 = 9721, 9722
	db, groupID := createAccessTestGroup(t, owner)

	// 不限次数的邀请：同一人兑换两次不应产生两条授权
	token, _, err := CreateAccessInvite(db, owner, InviteTargetGroup, groupID, 0, 0, true)
	require.NoError(t, err)
	_, err = RedeemAccessInvite(db, token, redeemer)
	require.NoError(t, err)
	_, err = RedeemAccessInvite(db, token, redeemer)
	require.NoError(t, err)

	var count int64
	require.NoError(t, db.Model(&Grant{}).
		Where("subject_type = ? AND subject_id = ? AND resource_id = ?", GrantSubjectUser, redeemer, groupID).
		Count(&count).Error)
	assert.EqualValues(t, 1, count, "同一主体对同一资源只应有一条授权（幂等）")

	var invite Invite
	require.NoError(t, db.Where("token_hash = ?", hashInviteToken(token)).First(&invite).Error)
	assert.EqualValues(t, 2, invite.UsedCount)
	assert.Equal(t, InviteStatusActive, invite.Status, "不限次数的邀请不会被置为用尽")
}

func TestSubjectCanAccessResource(t *testing.T) {
	const owner, invited, stranger int64 = 9731, 9732, 9733
	db, groupID := createAccessTestGroup(t, owner)

	// 所有者本人可访问
	ok, err := SubjectCanAccessResource(db, GrantSubjectUser, owner, GrantResourceGroup, groupID, 0)
	require.NoError(t, err)
	assert.True(t, ok, "所有者本人应当可访问")

	// 陌生人不可访问
	ok, err = SubjectCanAccessResource(db, GrantSubjectUser, stranger, GrantResourceGroup, groupID, 0)
	require.NoError(t, err)
	assert.False(t, ok, "未受邀者不可访问")

	// 兑换后受邀者可访问
	token, _, err := CreateAccessInvite(db, owner, InviteTargetGroup, groupID, 1, 0, true)
	require.NoError(t, err)
	_, err = RedeemAccessInvite(db, token, invited)
	require.NoError(t, err)
	ok, err = SubjectCanAccessResource(db, GrantSubjectUser, invited, GrantResourceGroup, groupID, 0)
	require.NoError(t, err)
	assert.True(t, ok, "兑换后应当可访问")

	// 授权**到期**后不可访问
	require.NoError(t, db.Model(&Grant{}).Where("subject_id = ?", invited).
		Update("expires_at", 1000).Error)
	ok, err = SubjectCanAccessResource(db, GrantSubjectUser, invited, GrantResourceGroup, groupID, 5000)
	require.NoError(t, err)
	assert.False(t, ok, "授权到期后不可访问")

	// 授权显式撤销后也不可访问（不能只看 expires_at）。
	require.NoError(t, db.Model(&Grant{}).Where("subject_id = ?", invited).
		Updates(map[string]any{"expires_at": 0, "revoked_at": 4000}).Error)
	ok, err = SubjectCanAccessResource(db, GrantSubjectUser, invited, GrantResourceGroup, groupID, 3000)
	require.NoError(t, err)
	assert.False(t, ok, "授权撤销后不可访问")
}

func TestResolveSourceAccessSnapshotIdentityMatrix(t *testing.T) {
	const ownerA, ownerB, viewer, stranger int64 = 9751, 9752, 9753, 9754
	db := newRepoDB(t)

	type seeded struct {
		group    *ShareGroup
		channel  *Channel
		offering *ModelOffering
	}
	seed := func(owner int64, visibility int, baseID int64, name string) seeded {
		t.Helper()
		group := &ShareGroup{
			OwnerUserID: owner, Name: name, DefaultVisibility: visibility,
			PublishStatus: ShareGroupPublishPublished,
		}
		require.NoError(t, CreateShareGroup(db, group))
		channel := &Channel{
			OwnerUserID: owner, ShareGroupID: group.ID, Name: name + "渠道", BaseChannelID: baseID,
		}
		require.NoError(t, CreateChannel(db, channel))
		offering := &ModelOffering{ChannelID: channel.ID, UpstreamModelID: "acl-model"}
		require.NoError(t, CreateModelOffering(db, owner, offering))
		require.NoError(t, db.Model(&ModelOffering{}).Where("id = ?", offering.ID).
			Update("status", OfferingStatusPublished).Error)
		return seeded{group: group, channel: channel, offering: offering}
	}

	public := seed(ownerA, ShareGroupVisibilityPublic, 91001, "公开")
	private := seed(ownerA, ShareGroupVisibilityPrivate, 91002, "私有")
	limited := seed(ownerB, ShareGroupVisibilityLimited, 91003, "受限")
	suspended := seed(ownerB, ShareGroupVisibilityPublic, 91004, "暂停")
	require.NoError(t, SetShareGroupPublishStatus(db, suspended.group.ID, ShareGroupPublishSuspended))

	allowedIDs := func(snapshot SourceAccessSnapshot) map[int64]bool {
		result := map[int64]bool{}
		for id := range snapshot.AllowedOfferingIDs {
			result[id] = true
		}
		return result
	}

	ownerSnapshot, err := ResolveSourceAccessSnapshot(db, ownerA, 0, "acl-model", 10_000)
	require.NoError(t, err)
	assert.Equal(t, map[int64]bool{public.offering.ID: true, private.offering.ID: true}, allowedIDs(ownerSnapshot))
	assert.Len(t, ownerSnapshot.ManagedBaseChannelIDs, 4, "暂停资源仍是 APIGX 管理渠道，必须留在 deny 集合中")

	strangerSnapshot, err := ResolveSourceAccessSnapshot(db, stranger, 0, "acl-model", 10_000)
	require.NoError(t, err)
	assert.Equal(t, map[int64]bool{public.offering.ID: true}, allowedIDs(strangerSnapshot))

	groupGrant, err := CreateGrantForUser(db, ownerB, GrantResourceGroup, limited.group.ID, viewer, 0)
	require.NoError(t, err)
	viewerSnapshot, err := ResolveSourceAccessSnapshot(db, viewer, 0, "acl-model", 10_000)
	require.NoError(t, err)
	assert.Equal(t, map[int64]bool{public.offering.ID: true, limited.offering.ID: true}, allowedIDs(viewerSnapshot))

	// 撤销分组授权，再授予单个 Offering：粒度必须准确，且 revoked_at 必须实时生效。
	require.NoError(t, db.Model(&Grant{}).Where("id = ?", groupGrant.ID).Update("revoked_at", 9_000).Error)
	_, err = CreateGrantForUser(db, ownerA, GrantResourceOffering, private.offering.ID, viewer, 20_000)
	require.NoError(t, err)
	viewerSnapshot, err = ResolveSourceAccessSnapshot(db, viewer, 0, "acl-model", 10_000)
	require.NoError(t, err)
	assert.Equal(t, map[int64]bool{public.offering.ID: true, private.offering.ID: true}, allowedIDs(viewerSnapshot))

	// 平台 Key 也可作为授权主体；用户本身没有这条私有资源授权。
	const platformKeyID int64 = 88001
	require.NoError(t, db.Create(&Grant{
		SubjectType: GrantSubjectAPIKey, SubjectID: platformKeyID,
		ResourceType: GrantResourceOffering, ResourceID: private.offering.ID,
		GrantedByUserID: ownerA, CreatedAt: 1, UpdatedAt: 1,
	}).Error)
	keySnapshot, err := ResolveSourceAccessSnapshot(db, stranger, platformKeyID, "acl-model", 10_000)
	require.NoError(t, err)
	assert.Equal(t, map[int64]bool{public.offering.ID: true, private.offering.ID: true}, allowedIDs(keySnapshot))
}

func TestResolveSourceAccessSnapshotExpandsCanonicalModelAliases(t *testing.T) {
	const owner, viewer int64 = 9761, 9762
	db := newRepoDB(t)
	canonical := &CanonicalModel{APIModelID: "canonical-flash", Status: 1}
	require.NoError(t, db.Create(canonical).Error)

	makeOffering := func(name, upstream string, baseID int64) int64 {
		t.Helper()
		group := &ShareGroup{
			OwnerUserID: owner, Name: name, DefaultVisibility: ShareGroupVisibilityPublic,
			PublishStatus: ShareGroupPublishPublished,
		}
		require.NoError(t, CreateShareGroup(db, group))
		channel := &Channel{OwnerUserID: owner, ShareGroupID: group.ID, Name: name + "渠道", BaseChannelID: baseID}
		require.NoError(t, CreateChannel(db, channel))
		offering := &ModelOffering{ChannelID: channel.ID, UpstreamModelID: upstream, CanonicalModelID: canonical.ID}
		require.NoError(t, CreateModelOffering(db, owner, offering))
		require.NoError(t, db.Model(&ModelOffering{}).Where("id = ?", offering.ID).
			Updates(map[string]any{"canonical_model_id": canonical.ID, "status": OfferingStatusPublished}).Error)
		return offering.ID
	}

	canonicalOffering := makeOffering("标准", "canonical-flash", 91101)
	aliasOffering := makeOffering("别名", "provider-flash-v1", 91102)

	for _, requestedModel := range []string{"canonical-flash", "provider-flash-v1"} {
		snapshot, err := ResolveSourceAccessSnapshot(db, viewer, 0, requestedModel, 10_000)
		require.NoError(t, err)
		assert.Contains(t, snapshot.AllowedOfferingIDs, canonicalOffering, requestedModel)
		assert.Contains(t, snapshot.AllowedOfferingIDs, aliasOffering, requestedModel)
		assert.ElementsMatch(t, []int{91101, 91102}, snapshot.AllowedBaseChannelIDs, requestedModel)
	}
}

// 注册邀请（§5 邀请准入）：**补偿回补**必须真的把名额还回去，且类型不得串用（§10.1）。
//
// 回补的用途：注册流程里"先核销、后建用户"，建用户失败时要把名额还回去（失败不该消耗邀请码）。
func TestRegistrationInviteRefundRestoresUse(t *testing.T) {
	db := newRepoDB(t)
	const adminUser int64 = 9901

	token, invite, err := CreateRegistrationInvite(db, adminUser, 1, 0)
	require.NoError(t, err)
	require.NotEmpty(t, token)
	assert.Equal(t, InviteTypeRegistration, invite.InviteType)

	// 先当作资源邀请核销 ⇒ 必须失败（两类邀请校验逻辑独立，不能串用）
	if _, err := RedeemAccessInvite(db, token, 9902); !errors.Is(err, ErrNotFound) {
		t.Fatalf("注册邀请码不得被当作资源邀请核销，实际: %v", err)
	}

	// 正常核销一次 ⇒ 用尽
	claimed, err := RedeemRegistrationInvite(db, token, 0)
	require.NoError(t, err)
	require.NotNil(t, claimed)

	var afterRedeem Invite
	require.NoError(t, db.Where("id = ?", invite.ID).First(&afterRedeem).Error)
	assert.EqualValues(t, 1, afterRedeem.UsedCount)
	assert.Equal(t, InviteStatusExhaust, afterRedeem.Status)

	// 已用尽 ⇒ 再核销必须失败
	if _, err := RedeemRegistrationInvite(db, token, 0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("已用尽的注册邀请码不得再核销，实际: %v", err)
	}

	// 补偿回补 ⇒ 名额归还、状态回到有效、**并且真的可以再次使用**
	require.NoError(t, RefundRegistrationInvite(db, invite.ID, 0))

	var afterRefund Invite
	require.NoError(t, db.Where("id = ?", invite.ID).First(&afterRefund).Error)
	assert.EqualValues(t, 0, afterRefund.UsedCount, "回补后使用次数应回到 0")
	assert.Equal(t, InviteStatusActive, afterRefund.Status, "回补后应重新可用")

	again, err := RedeemRegistrationInvite(db, token, 0)
	require.NoError(t, err, "回补之后应当可以再次核销")
	assert.NotNil(t, again)

	// 反向隔离：资源邀请码不得被当作注册邀请核销
	group := &ShareGroup{OwnerUserID: adminUser, Name: "隔离验证组", DefaultVisibility: ShareGroupVisibilityPublic, PublishStatus: 1}
	require.NoError(t, CreateShareGroup(db, group))
	accessToken, _, err := CreateAccessInvite(db, adminUser, InviteTargetGroup, group.ID, 1, 0, true)
	require.NoError(t, err)
	if _, err := RedeemRegistrationInvite(db, accessToken, 0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("资源邀请码不得被当作注册邀请核销，实际: %v", err)
	}
}
