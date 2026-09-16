package capacity

import (
	"errors"
	"fmt"
)

// LimitMode 是「限制维度」的四态枚举。
//
// 依据 CODE_STYLE.md 第 11 条与 docs/stage0/schema-draft.md §3.3：
// **未配置 / 禁用 / 继承默认 / 明确不限 / 明确值** 必须分别存储，
// 空字段不得被解释成「不限」——这是底座与新表共同的硬约束。
type LimitMode = int

const (
	// LimitModeUnset 未配置（未表态）：值列必须为 0，且**不得**解释为不限。
	LimitModeUnset LimitMode = 0
	// LimitModeDisabled 禁用：该资源不能用于此维度。
	LimitModeDisabled LimitMode = 1
	// LimitModeInherit 继承上级默认：显式向上查找。
	LimitModeInherit LimitMode = 2
	// LimitModeUnlimited 明确不限。
	LimitModeUnlimited LimitMode = 3
	// LimitModeValue 明确值：必须 > 0。
	LimitModeValue LimitMode = 4
)

// ErrInvalidPolicy 表示四态与值列的组合非法。
var ErrInvalidPolicy = errors.New("capacity: invalid policy")

// Policy 是一条容量限制记录（对应 `apigx_capacity_policies` 的一行，
// 或者由环境变量/平台默认构造出的临时策略）。
type Policy struct {
	Mode  LimitMode
	Value int64
}

// Validate 校验四态与值列的一致性（计划书 §3.3：校验落在 Go 侧，数据库层不写 CHECK）。
func (p Policy) Validate() error {
	if p.Mode < LimitModeUnset || p.Mode > LimitModeValue {
		return fmt.Errorf("%w: unknown mode %d", ErrInvalidPolicy, p.Mode)
	}
	if p.Mode == LimitModeValue {
		if p.Value <= 0 {
			return fmt.Errorf("%w: mode=明确值 requires value > 0 (got %d)", ErrInvalidPolicy, p.Value)
		}
		return nil
	}
	if p.Value != 0 {
		return fmt.Errorf("%w: mode=%d requires value == 0 (got %d)", ErrInvalidPolicy, p.Mode, p.Value)
	}
	return nil
}

// LimitDecision 是解析结论。
type LimitDecision int

const (
	// DecisionUnset 整条链都没有配置：**不等于不限**，由调用方决定如何处理。
	DecisionUnset LimitDecision = iota
	// DecisionDisabled 链上出现了「禁用」。
	DecisionDisabled
	// DecisionUnlimited 链上出现了「明确不限」。
	DecisionUnlimited
	// DecisionLimited 解析出一个明确上限。
	DecisionLimited
)

func (d LimitDecision) String() string {
	switch d {
	case DecisionUnset:
		return "unset"
	case DecisionDisabled:
		return "disabled"
	case DecisionUnlimited:
		return "unlimited"
	case DecisionLimited:
		return "limited"
	default:
		return fmt.Sprintf("unknown(%d)", int(d))
	}
}

// Resolution 是限制链的解析结果。
type Resolution struct {
	Decision LimitDecision
	Value    int64
}

// Resolve 沿「最具体 → 最一般」的顺序解析限制链。
//
// chain[0] 应当是最具体的来源（例如某个上游 Key），最后一项是最一般的默认值。
// 语义：
//   - `明确值` / `明确不限` / `禁用` 三者都会**立即终止**查找（更具体的表态优先）；
//   - `继承上级` 与 `未配置` 都会**继续向上查找**（二者的区别在存储与展示层，不在解析结果）；
//   - 整条链都未配置时返回 `DecisionUnset` —— 调用方必须显式决定如何处理，
//     绝不能把它当成「不限」。
func Resolve(chain ...Policy) (Resolution, error) {
	for i, p := range chain {
		if err := p.Validate(); err != nil {
			return Resolution{}, fmt.Errorf("chain[%d]: %w", i, err)
		}
		switch p.Mode {
		case LimitModeValue:
			return Resolution{Decision: DecisionLimited, Value: p.Value}, nil
		case LimitModeUnlimited:
			return Resolution{Decision: DecisionUnlimited}, nil
		case LimitModeDisabled:
			return Resolution{Decision: DecisionDisabled}, nil
		case LimitModeInherit, LimitModeUnset:
			continue
		}
	}
	return Resolution{Decision: DecisionUnset}, nil
}

// IsEnforceable 报告该解析结果是否应当在本层施加约束。
//
// 只有「明确值」才产生约束：明确不限不约束；未配置不约束（调用方需另行处理这一过渡行为）；
// 禁用则由调用方按「拒绝」处理，不走这里。
func (r Resolution) IsEnforceable() bool {
	return r.Decision == DecisionLimited && r.Value > 0
}
