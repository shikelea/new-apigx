package apigxmodel

import (
	"errors"
	"fmt"

	"gorm.io/gorm"
)

// 容量策略（`apigx_capacity_policies`）的读取。
//
// 每个维度都是「模式 + 值」的四态组合（见 docs/stage0/schema-draft.md §3.3）：
// 未配置 / 禁用 / 继承 / 明确不限 / 明确值。**读取层不做解释**，只把原始值交给
// `apigx/capacity` 的四态解析器（`capacity.Resolve`），避免两处各有一套解释规则。

// CapacityScope 是策略的适用范围（`ScopeType`）。
const (
	// CapacityScopePlatform 平台级默认。
	CapacityScopePlatform int = 0
	// CapacityScopeChannel 分享渠道。
	CapacityScopeChannel int = 1
	// CapacityScopeUpstreamKey 单把上游凭据。
	CapacityScopeUpstreamKey int = 2
	// CapacityScopeUpstreamAccount 上游账户池。
	CapacityScopeUpstreamAccount int = 3
	// CapacityScopeUser 使用者。
	CapacityScopeUser int = 4
)

// GetCapacityPolicy 按主键读取策略；不存在返回 ErrNotFound。
func GetCapacityPolicy(db *gorm.DB, id int64) (*CapacityPolicy, error) {
	if db == nil || id <= 0 {
		return nil, fmt.Errorf("%w: policy id is required", ErrInvalidArgument)
	}
	var policy CapacityPolicy
	err := db.Where("id = ?", id).First(&policy).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &policy, nil
}

// FindCapacityPolicyByScope 按「所有者 + 范围」读取策略；不存在返回 ErrNotFound。
//
// 唯一索引是 (ScopeType, ScopeID)，因此同一范围只会有一条策略。
func FindCapacityPolicyByScope(db *gorm.DB, ownerUserID int64, scopeType int, scopeID int64) (*CapacityPolicy, error) {
	if db == nil || scopeID <= 0 {
		return nil, fmt.Errorf("%w: scope id is required", ErrInvalidArgument)
	}
	var policy CapacityPolicy
	err := db.Where("owner_user_id = ? AND scope_type = ? AND scope_id = ?", ownerUserID, scopeType, scopeID).
		First(&policy).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &policy, nil
}

// Concurrency 返回「并发」维度的原始四态值（模式 + 值）。
func (p *CapacityPolicy) Concurrency() (int, int64) {
	if p == nil {
		return 0, 0
	}
	return p.MaxConcurrencyMode, p.MaxConcurrency
}

// RPM 返回「每分钟请求数」维度的原始四态值。
func (p *CapacityPolicy) RPM() (int, int64) {
	if p == nil {
		return 0, 0
	}
	return p.RpmMode, p.RpmLimit
}

// TPM 返回「每分钟 token 数」维度的原始四态值。
func (p *CapacityPolicy) TPM() (int, int64) {
	if p == nil {
		return 0, 0
	}
	return p.TpmMode, p.TpmLimit
}
