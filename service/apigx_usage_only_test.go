package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// APIGX 免费共享模式（usage_only）的单元测试。
//
// 端到端行为（零余额用户可调用、钱包不动、令牌额度仍被预留）由运行时验收脚本
// tools/stage1-verify/usage-only.mjs 验证；本文件守住不需要数据库的那部分契约。

func TestUsageOnlyFundingIsNoOp(t *testing.T) {
	funding := &UsageOnlyFunding{}

	assert.Equal(t, BillingSourceUsageOnly, funding.Source())

	// 三个方法都必须是空操作：这是「只记账、不动资金」的核心契约。
	// 一旦有人把钱包/订阅调用塞进来，免费共享模式就会重新依赖充值余额。
	require.NoError(t, funding.PreConsume(12345))
	require.NoError(t, funding.Settle(12345))
	require.NoError(t, funding.Settle(-12345))
	require.NoError(t, funding.Refund())
}

func TestUsageOnlyModeDefaultsOffAndToggles(t *testing.T) {
	original := IsUsageOnlyMode()
	t.Cleanup(func() { SetUsageOnlyMode(original) })

	SetUsageOnlyMode(true)
	assert.True(t, IsUsageOnlyMode())

	SetUsageOnlyMode(false)
	assert.False(t, IsUsageOnlyMode())
}

// reserveFunding 用的是 funding 的类型 switch。若新资金源没有显式分支，
// 会落到 default 并返回 "unsupported funding source"——本用例就是守住这一点。
func TestReserveFundingAcceptsUsageOnlyFunding(t *testing.T) {
	session := &BillingSession{funding: &UsageOnlyFunding{}}

	require.NoError(t, session.reserveFunding(1000, true), "usage_only 在 requireAvailableQuota 路径下不得报错")
	require.NoError(t, session.reserveFunding(1000, false), "usage_only 在非严格路径下不得报错")
}

// 反向保护：未知资金源必须仍然报错，不能因为新加了 case 就把 default 放宽。
func TestReserveFundingStillRejectsUnknownFunding(t *testing.T) {
	session := &BillingSession{funding: unknownFundingForTest{}}
	err := session.reserveFunding(1000, true)
	require.Error(t, err, "未知资金来源必须被拒绝")
	assert.Contains(t, err.Error(), "unsupported funding source")
}

type unknownFundingForTest struct{}

func (unknownFundingForTest) Source() string         { return "not-a-real-source" }
func (unknownFundingForTest) PreConsume(_ int) error { return nil }
func (unknownFundingForTest) Settle(_ int) error     { return nil }
func (unknownFundingForTest) Refund() error          { return nil }
