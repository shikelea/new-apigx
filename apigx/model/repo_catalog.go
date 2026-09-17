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
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// 模型目录（标准模型 + 别名映射）。
//
// # 为什么单独一个文件
//
// 计划书 §4 要求「模型目录：标准模型、别名、版本、能力说明」，§5.2 要求复用底座价格能力并补「模型映射」；
// 而在此之前，`apigx_canonical_models` / `apigx_model_aliases` 两张表**只有 schema、没有任何写入方**
// —— 也就是说这项能力实际上没实现。这里补上最基础的两件事：
//
//  1. **候选登记**：把上游模型名登记为「候选」别名（`Relation=2` / `ConfirmationState=0`）；
//  2. **解析**：按归一化键把上游名解析到标准模型 —— **只认已确认的映射**。
//
// 第 2 条是关键：模型注释写明「候选（仅提示，不自动路由）」，因此候选**绝不**能拿来当结论
// （否则广场会显示一个没人确认过的"标准模型"）。候选到确认之间的工作流（管理员/分享者确认）
// 属后续步骤，接口层再补。

// 别名关系与确认状态（与 models_identity.go 的列注释一致，这里给常量避免散落魔法数字）。
const (
	AliasRelationExact     = 0 // 完全匹配
	AliasRelationConfirmed = 1 // 已确认别名
	AliasRelationCandidate = 2 // 候选（仅提示，不自动路由）

	AliasConfirmPending  = 0 // 待确认
	AliasConfirmApproved = 1 // 已确认
	AliasConfirmRejected = 2 // 已拒绝

	AliasScopePlatform = 0 // 平台全局
)

// NormalizeModelIDForAlias 归一化模型名用于匹配。
//
// 与模型列注释一致：**大小写与空白归一，不做版本号/日期剥离**（§8.2 —— 强行归并会把
// `gpt-4o` 与 `gpt-4o-mini` 这类不同模型混成一个，属于会算错钱的错误）。
func NormalizeModelIDForAlias(raw string) string {
	return strings.ToLower(strings.Join(strings.Fields(raw), " "))
}

// aliasKeyHash 复刻列注释里的定义：SHA-256(scope_type|scope_id|source|normalized_model_id)。
func aliasKeyHash(scopeType int, scopeID int64, source int, normalized string) string {
	sum := sha256.Sum256([]byte(strconv.Itoa(scopeType) + "|" + strconv.FormatInt(scopeID, 10) + "|" +
		strconv.Itoa(source) + "|" + normalized))
	return hex.EncodeToString(sum[:])
}

// RecordAliasCandidate 把上游模型名登记为**候选**别名（幂等）。
//
// 幂等由 `AliasKeyHash` 唯一索引保证：同一个 (范围, 来源, 归一化名) 只会有一行。
// 已存在的行**不覆盖** —— 否则每次同步上游都会把别人已确认的映射打回"待确认"。
func RecordAliasCandidate(db *gorm.DB, scopeType int, scopeID int64, source int, externalModelID string) error {
	if db == nil {
		return errors.New("apigxmodel: nil db")
	}
	normalized := NormalizeModelIDForAlias(externalModelID)
	if normalized == "" {
		return fmt.Errorf("%w: external model id is empty", ErrInvalidArgument)
	}
	hash := aliasKeyHash(scopeType, scopeID, source, normalized)

	var existing int64
	if err := db.Model(&ModelAlias{}).Where("alias_key_hash = ? AND deleted_at IS NULL", hash).Count(&existing).Error; err != nil {
		return err
	}
	if existing > 0 {
		return nil
	}

	now := common.GetTimestamp()
	row := ModelAlias{
		ScopeType:         scopeType,
		ScopeID:           scopeID,
		Source:            0, // 上游目录
		ExternalModelID:   externalModelID,
		NormalizedModelID: normalized,
		AliasKeyHash:      hash,
		Relation:          AliasRelationCandidate,
		ConfirmationState: AliasConfirmPending,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	return db.Create(&row).Error
}

// ResolveCanonicalModel 按上游/外部模型名解析标准模型。
//
// **只认已确认的映射**（`ConfirmationState = 已确认`）：候选与已拒绝一律不返回，
// 未命中时 `ok=false` —— 调用方据此退回"显示上游名"，而不是编一个标准模型出来。
// 命中多条时优先**平台全局**范围（scope_type=0），与"平台维护的映射优先于局部建议"一致。
func ResolveCanonicalModel(db *gorm.DB, externalModelID string) (canonical CanonicalModel, ok bool, err error) {
	if db == nil {
		return CanonicalModel{}, false, errors.New("apigxmodel: nil db")
	}
	normalized := NormalizeModelIDForAlias(externalModelID)
	if normalized == "" {
		return CanonicalModel{}, false, nil
	}

	var alias ModelAlias
	findErr := db.Model(&ModelAlias{}).
		Where("normalized_model_id = ? AND confirmation_state = ? AND target_canonical_model_id > 0 AND deleted_at IS NULL",
			normalized, AliasConfirmApproved).
		Order("scope_type ASC, id ASC").
		First(&alias).Error
	if findErr != nil {
		if errors.Is(findErr, gorm.ErrRecordNotFound) {
			return CanonicalModel{}, false, nil
		}
		return CanonicalModel{}, false, findErr
	}

	// 注意：标准模型表**没有** deleted_at（它用 Status / MergedIntoID 表达弃用与合并，
	// 不做软删除），因此这里不能按 deleted_at 过滤 —— 写错会直接报 "no such column"。
	if err := db.Model(&CanonicalModel{}).
		Where("id = ?", alias.TargetCanonicalModelID).
		First(&canonical).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return CanonicalModel{}, false, nil
		}
		return CanonicalModel{}, false, err
	}
	return canonical, true, nil
}
