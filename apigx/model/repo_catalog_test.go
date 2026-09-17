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
