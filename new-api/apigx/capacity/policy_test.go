package capacity

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 四态语义的守门测试。
//
// 这里保护的是 CODE_STYLE.md 第 11 条：**未配置 ≠ 不限**。
// 一旦有人把「未配置」当成「不限」，容量控制就会在没配置的情况下静默放开全部请求。
func TestPolicyValidateEnforcesModeValuePairing(t *testing.T) {
	tests := []struct {
		name    string
		policy  Policy
		wantErr bool
	}{
		{"未配置 + 值为 0", Policy{Mode: LimitModeUnset, Value: 0}, false},
		{"禁用 + 值为 0", Policy{Mode: LimitModeDisabled, Value: 0}, false},
		{"继承 + 值为 0", Policy{Mode: LimitModeInherit, Value: 0}, false},
		{"明确不限 + 值为 0", Policy{Mode: LimitModeUnlimited, Value: 0}, false},
		{"明确值 + 正数", Policy{Mode: LimitModeValue, Value: 7}, false},
		{"明确值 + 0（非法）", Policy{Mode: LimitModeValue, Value: 0}, true},
		{"明确值 + 负数（非法）", Policy{Mode: LimitModeValue, Value: -1}, true},
		{"未配置 + 非 0（非法）", Policy{Mode: LimitModeUnset, Value: 5}, true},
		{"明确不限 + 非 0（非法）", Policy{Mode: LimitModeUnlimited, Value: 5}, true},
		{"未知模式（非法）", Policy{Mode: 99, Value: 0}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.policy.Validate()
			if tt.wantErr {
				require.Error(t, err)
				assert.ErrorIs(t, err, ErrInvalidPolicy)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestResolveChainSemantics(t *testing.T) {
	tests := []struct {
		name  string
		chain []Policy
		want  Resolution
	}{
		{
			name:  "最具体是明确值 → 立即采用",
			chain: []Policy{{Mode: LimitModeValue, Value: 3}, {Mode: LimitModeValue, Value: 99}},
			want:  Resolution{Decision: DecisionLimited, Value: 3},
		},
		{
			name:  "最具体未配置 → 向上查找",
			chain: []Policy{{Mode: LimitModeUnset}, {Mode: LimitModeValue, Value: 5}},
			want:  Resolution{Decision: DecisionLimited, Value: 5},
		},
		{
			name:  "显式继承 → 向上查找",
			chain: []Policy{{Mode: LimitModeInherit}, {Mode: LimitModeValue, Value: 8}},
			want:  Resolution{Decision: DecisionLimited, Value: 8},
		},
		{
			name:  "最具体明确不限 → 终止，不再看下层更严格的限制",
			chain: []Policy{{Mode: LimitModeUnlimited}, {Mode: LimitModeValue, Value: 1}},
			want:  Resolution{Decision: DecisionUnlimited},
		},
		{
			name:  "最具体禁用 → 终止",
			chain: []Policy{{Mode: LimitModeDisabled}, {Mode: LimitModeValue, Value: 1}},
			want:  Resolution{Decision: DecisionDisabled},
		},
		{
			name:  "整条链都未配置 → Unset（**绝不是不限**）",
			chain: []Policy{{Mode: LimitModeUnset}, {Mode: LimitModeUnset}},
			want:  Resolution{Decision: DecisionUnset},
		},
		{
			name:  "空链 → Unset",
			chain: nil,
			want:  Resolution{Decision: DecisionUnset},
		},
		{
			name:  "继承到链尾仍无表态 → Unset",
			chain: []Policy{{Mode: LimitModeInherit}, {Mode: LimitModeInherit}},
			want:  Resolution{Decision: DecisionUnset},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Resolve(tt.chain...)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestResolveRejectsInvalidPolicyInChain(t *testing.T) {
	_, err := Resolve(Policy{Mode: LimitModeValue, Value: 0})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidPolicy)

	_, err = Resolve(Policy{Mode: LimitModeUnset, Value: 3})
	require.Error(t, err)
}

// IsEnforceable 决定「这一层要不要真的去预留」。只有明确值才约束。
func TestResolutionIsEnforceableOnlyForExplicitValue(t *testing.T) {
	assert.True(t, Resolution{Decision: DecisionLimited, Value: 1}.IsEnforceable())
	assert.False(t, Resolution{Decision: DecisionUnlimited}.IsEnforceable())
	assert.False(t, Resolution{Decision: DecisionUnset}.IsEnforceable(), "未配置不得被当成可执行约束")
	assert.False(t, Resolution{Decision: DecisionDisabled}.IsEnforceable(), "禁用由调用方按拒绝处理")
	assert.False(t, Resolution{Decision: DecisionLimited, Value: 0}.IsEnforceable())
}
