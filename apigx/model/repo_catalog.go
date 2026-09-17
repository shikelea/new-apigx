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

// ResolveCanonicalModels 批量解析（广场一页会涉及多个模型名，逐个查库不合适）。
//
// 与单条版本**同一套规则**：只认已确认、命中多个时优先平台全局范围。
// 返回值以**调用方传入的原名**为键，未命中的名字不出现在结果里 —— 调用方据此显示上游名。
func ResolveCanonicalModels(db *gorm.DB, externalModelIDs []string) (map[string]CanonicalModel, error) {
	out := make(map[string]CanonicalModel, len(externalModelIDs))
	if db == nil {
		return out, errors.New("apigxmodel: nil db")
	}

	// 原名 -> 归一化键（保留原名做返回键，避免调用方再去归一化）
	normalizedToOriginal := make(map[string]string, len(externalModelIDs))
	normalizedIDs := make([]string, 0, len(externalModelIDs))
	for _, raw := range externalModelIDs {
		normalized := NormalizeModelIDForAlias(raw)
		if normalized == "" {
			continue
		}
		if _, seen := normalizedToOriginal[normalized]; !seen {
			normalizedToOriginal[normalized] = raw
			normalizedIDs = append(normalizedIDs, normalized)
		}
	}
	if len(normalizedIDs) == 0 {
		return out, nil
	}

	var aliases []ModelAlias
	if err := db.Model(&ModelAlias{}).
		Where("normalized_model_id IN ? AND confirmation_state = ? AND target_canonical_model_id > 0 AND deleted_at IS NULL",
			normalizedIDs, AliasConfirmApproved).
		Order("scope_type ASC, id ASC").
		Find(&aliases).Error; err != nil {
		return out, err
	}
	if len(aliases) == 0 {
		return out, nil
	}

	// 归一化键 -> 目标标准模型 ID（先到先得：已按 scope_type 升序，平台全局优先）
	targetByNormalized := make(map[string]int64, len(aliases))
	targetIDs := make([]int64, 0, len(aliases))
	for _, alias := range aliases {
		if _, exists := targetByNormalized[alias.NormalizedModelID]; exists {
			continue
		}
		targetByNormalized[alias.NormalizedModelID] = alias.TargetCanonicalModelID
		targetIDs = append(targetIDs, alias.TargetCanonicalModelID)
	}

	var models []CanonicalModel
	if err := db.Model(&CanonicalModel{}).Where("id IN ?", targetIDs).Find(&models).Error; err != nil {
		return out, err
	}
	byID := make(map[int64]CanonicalModel, len(models))
	for _, model := range models {
		byID[model.ID] = model
	}

	for normalized, original := range normalizedToOriginal {
		targetID, hit := targetByNormalized[normalized]
		if !hit {
			continue
		}
		model, found := byID[targetID]
		if !found {
			continue
		}
		out[original] = model
	}
	return out, nil
}

// AliasCandidateView 是候选列表对外的一行（含来源与建议目标）。
type AliasCandidateView struct {
	ID                int64
	ExternalModelID   string
	NormalizedModelID string
	ScopeType         int
	ScopeID           int64
	Source            int
	SourceRef         string
	ConfirmationState int
	TargetCanonicalID int64
}

// ListAliasCandidates 列出候选（默认只看待确认的），按 ID 倒序。
//
// 只读：确认/拒绝的动作在下面两个函数里，且都**只从"待确认"推进**。
func ListAliasCandidates(db *gorm.DB, state int, limit, offset int) ([]AliasCandidateView, int64, error) {
	if db == nil {
		return nil, 0, errors.New("apigxmodel: nil db")
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	query := db.Model(&ModelAlias{}).Where("confirmation_state = ? AND deleted_at IS NULL", state)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []ModelAlias
	if err := query.Order("id DESC").Limit(limit).Offset(offset).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	out := make([]AliasCandidateView, 0, len(rows))
	for _, row := range rows {
		out = append(out, AliasCandidateView{
			ID: row.ID, ExternalModelID: row.ExternalModelID, NormalizedModelID: row.NormalizedModelID,
			ScopeType: row.ScopeType, ScopeID: row.ScopeID, Source: row.Source, SourceRef: row.SourceRef,
			ConfirmationState: row.ConfirmationState, TargetCanonicalID: row.TargetCanonicalModelID,
		})
	}
	return out, total, nil
}

// EnsureCanonicalModel 按对外标准 ID 找标准模型；不存在则创建（草稿→生效由调用方决定）。
//
// 存在的意义：管理员确认候选时，往往**还没有**标准模型行 —— 要求他先去别处建一个再回来确认，
// 这一步就会被跳过（于是候选永远堆着）。这里允许"确认时顺手建标准模型"。
func EnsureCanonicalModel(db *gorm.DB, apiModelID, description string, modalityType int) (CanonicalModel, error) {
	if db == nil {
		return CanonicalModel{}, errors.New("apigxmodel: nil db")
	}
	apiModelID = strings.TrimSpace(apiModelID)
	if apiModelID == "" {
		return CanonicalModel{}, fmt.Errorf("%w: api_model_id is required", ErrInvalidArgument)
	}
	var existing CanonicalModel
	err := db.Model(&CanonicalModel{}).Where("api_model_id = ?", apiModelID).First(&existing).Error
	if err == nil {
		// 只补"空着的介绍"：不覆盖已有描述（那是别人维护过的内容）
		if existing.Description == "" && strings.TrimSpace(description) != "" {
			if updErr := db.Model(&CanonicalModel{}).Where("id = ?", existing.ID).
				Updates(map[string]any{"description": description, "updated_at": common.GetTimestamp()}).Error; updErr != nil {
				return CanonicalModel{}, updErr
			}
			existing.Description = description
		}
		return existing, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return CanonicalModel{}, err
	}

	now := common.GetTimestamp()
	created := CanonicalModel{
		APIModelID: apiModelID, Description: description,
		ModalityType: modalityType, Status: 1, CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(&created).Error; err != nil {
		return CanonicalModel{}, err
	}
	return created, nil
}

// ConfirmAliasCandidate 确认一个候选（**只从"待确认"推进**）。
//
// 已确认/已拒绝的行不动：确认是**单向**的，重复提交不会改变结论，
// 也不会把别人已经拒绝过的候选又悄悄确认回来。
func ConfirmAliasCandidate(db *gorm.DB, id, targetCanonicalID, confirmedByUserID int64) error {
	if db == nil {
		return errors.New("apigxmodel: nil db")
	}
	if targetCanonicalID <= 0 {
		return fmt.Errorf("%w: target canonical model is required", ErrInvalidArgument)
	}
	res := db.Model(&ModelAlias{}).
		Where("id = ? AND confirmation_state = ? AND deleted_at IS NULL", id, AliasConfirmPending).
		Updates(map[string]any{
			"relation": AliasRelationConfirmed, "confirmation_state": AliasConfirmApproved,
			"target_canonical_model_id": targetCanonicalID,
			"confirmed_by_user_id":      confirmedByUserID, "confirmed_at": common.GetTimestamp(),
			"updated_at": common.GetTimestamp(),
		})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// RejectAliasCandidate 拒绝一个候选（同样只从"待确认"推进）。
func RejectAliasCandidate(db *gorm.DB, id, confirmedByUserID int64) error {
	if db == nil {
		return errors.New("apigxmodel: nil db")
	}
	res := db.Model(&ModelAlias{}).
		Where("id = ? AND confirmation_state = ? AND deleted_at IS NULL", id, AliasConfirmPending).
		Updates(map[string]any{
			"confirmation_state": AliasConfirmRejected, "relation": AliasRelationCandidate,
			"confirmed_by_user_id": confirmedByUserID, "confirmed_at": common.GetTimestamp(),
			"updated_at": common.GetTimestamp(),
		})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// BulkConfirmAliases 把**归一化后同名**的待确认候选一次性确认到同一个标准模型（§4「批量匹配」）。
//
// 返回确认的行数。只动待确认的行 —— 已确认/已拒绝的保持不变。
func BulkConfirmAliases(db *gorm.DB, normalizedModelID string, targetCanonicalID, confirmedByUserID int64) (int64, error) {
	if db == nil {
		return 0, errors.New("apigxmodel: nil db")
	}
	normalized := NormalizeModelIDForAlias(normalizedModelID)
	if normalized == "" || targetCanonicalID <= 0 {
		return 0, fmt.Errorf("%w: normalized model id and target are required", ErrInvalidArgument)
	}
	now := common.GetTimestamp()
	res := db.Model(&ModelAlias{}).
		Where("normalized_model_id = ? AND confirmation_state = ? AND deleted_at IS NULL", normalized, AliasConfirmPending).
		Updates(map[string]any{
			"relation": AliasRelationConfirmed, "confirmation_state": AliasConfirmApproved,
			"target_canonical_model_id": targetCanonicalID,
			"confirmed_by_user_id":      confirmedByUserID, "confirmed_at": now, "updated_at": now,
		})
	if res.Error != nil {
		return 0, res.Error
	}
	return res.RowsAffected, nil
}
