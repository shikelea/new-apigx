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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// 模型目录：候选登记 + 解析。
//
// 最关键的一条：**候选不得当结论**（模型注释写明"候选仅提示，不自动路由"），
// 否则广场会显示一个没人确认过的"标准模型"。
func TestAliasCandidateNeverResolves(t *testing.T) {
	db := newRepoDB(t)

	require.NoError(t, RecordAliasCandidate(db, AliasScopePlatform, 0, 0, "  GLM-5.3-Flash "))

	// 归一化：大小写与空白归一，但**不做版本号剥离**
	var row ModelAlias
	require.NoError(t, db.Where("normalized_model_id = ?", "glm-5.3-flash").First(&row).Error)
	assert.Equal(t, AliasRelationCandidate, row.Relation)
	assert.Equal(t, AliasConfirmPending, row.ConfirmationState)

	// 候选阶段：解析必须**失败**（ok=false），而不是给出一个未确认的标准模型
	_, ok, err := ResolveCanonicalModel(db, "glm-5.3-flash")
	require.NoError(t, err)
	assert.False(t, ok, "候选映射不得当作结论")

	// 确认之后才解析得出来
	canonical := CanonicalModel{APIModelID: "glm-5.3-flash", ModalityType: 1, Status: 1}
	require.NoError(t, db.Create(&canonical).Error)
	require.NoError(t, db.Model(&ModelAlias{}).Where("id = ?", row.ID).
		Updates(map[string]any{"relation": AliasRelationConfirmed, "confirmation_state": AliasConfirmApproved,
			"target_canonical_model_id": canonical.ID}).Error)

	got, ok, err := ResolveCanonicalModel(db, "GLM-5.3-Flash")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "glm-5.3-flash", got.APIModelID)
}

// 同一 (范围, 来源, 归一化名) 只登记一次，且**不覆盖**已有行（否则每次同步都会把确认打回待确认）。
func TestAliasCandidateIsIdempotent(t *testing.T) {
	db := newRepoDB(t)

	require.NoError(t, RecordAliasCandidate(db, AliasScopePlatform, 0, 0, "m-1"))
	require.NoError(t, RecordAliasCandidate(db, AliasScopePlatform, 0, 0, "M-1  "))

	var count int64
	require.NoError(t, db.Model(&ModelAlias{}).Count(&count).Error)
	assert.Equal(t, int64(1), count, "归一化后同名只应有一行")

	// 已确认的映射不会被后续登记覆盖
	require.NoError(t, db.Model(&ModelAlias{}).Where("1 = 1").Update("confirmation_state", AliasConfirmApproved).Error)
	require.NoError(t, RecordAliasCandidate(db, AliasScopePlatform, 0, 0, "m-1"))
	var row ModelAlias
	require.NoError(t, db.First(&row).Error)
	assert.Equal(t, AliasConfirmApproved, row.ConfirmationState, "REGISTER 不得把已确认打回待确认")
}

// 没有映射时返回 ok=false，而不是编一个标准模型。
func TestResolveCanonicalModelMissReturnsFalse(t *testing.T) {
	db := newRepoDB(t)
	_, ok, err := ResolveCanonicalModel(db, "never-registered")
	require.NoError(t, err)
	assert.False(t, ok)
}

func createIdentityTestOffering(
	t *testing.T,
	db *gorm.DB,
	owner, baseChannelID int64,
	name, upstreamModelID string,
) *ModelOffering {
	t.Helper()
	group := &ShareGroup{OwnerUserID: owner, Name: name, DefaultVisibility: ShareGroupVisibilityPublic}
	require.NoError(t, CreateShareGroup(db, group))
	channel := &Channel{OwnerUserID: owner, ShareGroupID: group.ID, Name: name, BaseChannelID: baseChannelID}
	require.NoError(t, CreateChannel(db, channel))
	offering := &ModelOffering{ChannelID: channel.ID, UpstreamModelID: upstreamModelID}
	require.NoError(t, CreateModelOffering(db, owner, offering))
	return offering
}

func TestOfferingIdentityAppearsOnlyAfterSubmission(t *testing.T) {
	db := newRepoDB(t)
	const owner, reviewer int64 = 8101, 8199
	offering := createIdentityTestOffering(t, db, owner, 81_001, "identity-submit", "Vendor-Model-X")

	var count int64
	require.NoError(t, db.Model(&ModelAlias{}).Count(&count).Error)
	assert.Zero(t, count, "draft imports must not enter model identity review")

	target := &CanonicalModel{APIModelID: "standard-model-x", ModalityType: ModalityChat, Status: 1}
	require.NoError(t, db.Create(target).Error)
	proposal, err := SetOfferingAliasProposal(db, owner, offering.ID, target.ID)
	require.NoError(t, err)
	assert.Equal(t, AliasConfirmPending, proposal.ConfirmationState)
	_, err = SetOfferingAliasProposal(db, owner+1, offering.ID, target.ID)
	assert.ErrorIs(t, err, ErrNotFound)

	require.NoError(t, SubmitModelOffering(db, owner, offering.ID))
	items, total, err := ListAliasCandidates(db, AliasConfirmPending, 20, 0)
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Len(t, items, 1)
	assert.Equal(t, offering.ID, items[0].OfferingID)
	assert.Equal(t, target.ID, items[0].TargetCanonicalID)

	result, err := ConfirmAliasCandidate(db, items[0].ID, target.ID, reviewer)
	require.NoError(t, err)
	assert.EqualValues(t, 1, result.MigratedOfferings)
	var stored ModelOffering
	require.NoError(t, db.First(&stored, offering.ID).Error)
	assert.Equal(t, target.ID, stored.CanonicalModelID)
}

func TestRejectedIdentityReturnsOfferingForRevision(t *testing.T) {
	db := newRepoDB(t)
	const owner, reviewer int64 = 8201, 8299
	offering := createIdentityTestOffering(t, db, owner, 82_001, "identity-reject", "vendor-rejected")
	require.NoError(t, SubmitModelOffering(db, owner, offering.ID))
	proposal, exists, err := GetOfferingAliasProposal(db, offering.ID)
	require.NoError(t, err)
	require.True(t, exists)
	require.NoError(t, RejectAliasCandidate(db, proposal.ID, reviewer))

	var stored ModelOffering
	require.NoError(t, db.First(&stored, offering.ID).Error)
	assert.Equal(t, OfferingStatusRejected, stored.Status)
	assert.Zero(t, stored.CanonicalModelID)

	target := &CanonicalModel{APIModelID: "revised-standard", ModalityType: ModalityChat, Status: 1}
	require.NoError(t, db.Create(target).Error)
	_, err = SetOfferingAliasProposal(db, owner, offering.ID, target.ID)
	require.NoError(t, err)
	require.NoError(t, SubmitModelOffering(db, owner, offering.ID))
	proposal, exists, err = GetOfferingAliasProposal(db, offering.ID)
	require.NoError(t, err)
	require.True(t, exists)
	assert.Equal(t, AliasConfirmPending, proposal.ConfirmationState)
	assert.Equal(t, target.ID, proposal.TargetCanonicalModelID)
}

func TestEditingConfirmedIdentityMigratesExistingOfferings(t *testing.T) {
	db := newRepoDB(t)
	const reviewer int64 = 8399
	first := createIdentityTestOffering(t, db, 8301, 83_001, "identity-first", "Vendor-Same")
	second := createIdentityTestOffering(t, db, 8302, 83_002, "identity-second", " vendor-same ")
	require.NoError(t, SubmitModelOffering(db, 8301, first.ID))
	require.NoError(t, SubmitModelOffering(db, 8302, second.ID))
	firstProposal, _, err := GetOfferingAliasProposal(db, first.ID)
	require.NoError(t, err)
	secondProposal, _, err := GetOfferingAliasProposal(db, second.ID)
	require.NoError(t, err)

	original := &CanonicalModel{APIModelID: "standard-original", ModalityType: ModalityChat, Status: 1}
	replacement := &CanonicalModel{APIModelID: "standard-replacement", ModalityType: ModalityChat, Status: 1}
	require.NoError(t, db.Create(original).Error)
	require.NoError(t, db.Create(replacement).Error)
	result, err := ConfirmAliasCandidate(db, firstProposal.ID, original.ID, reviewer)
	require.NoError(t, err)
	assert.EqualValues(t, 2, result.MigratedOfferings)

	result, err = ConfirmAliasCandidate(db, secondProposal.ID, replacement.ID, reviewer)
	require.NoError(t, err)
	assert.EqualValues(t, 2, result.MigratedOfferings)
	for _, id := range []int64{first.ID, second.ID} {
		var offering ModelOffering
		require.NoError(t, db.First(&offering, id).Error)
		assert.Equal(t, replacement.ID, offering.CanonicalModelID)
	}
	var old CanonicalModel
	require.NoError(t, db.First(&old, original.ID).Error)
	assert.Equal(t, 3, old.Status)
	assert.Equal(t, replacement.ID, old.MergedIntoID)
	resolved, ok, err := ResolveCanonicalModel(db, "VENDOR-SAME")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, replacement.ID, resolved.ID)
}
