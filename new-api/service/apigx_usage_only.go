package service

import (
	"fmt"
	"sync/atomic"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
)

// APIGX 免费共享模式（usage_only）
//
// 计划书 §6 与 §11 阶段 1 要求：免费共享模式下**保持鉴权、额度与计量**，
// 但取消「用户充值余额」作为调用前置条件。本文件的实现方式是**替换资金来源**，
// 而不是改价格、也不是伪造余额：
//
//	✅ 新增 UsageOnlyFunding：只记账、不动钱包与订阅
//	✅ 跳过 NewBillingSession 中 tryWallet 的余额前置检查
//	❌ 不走 FreeModel 短路（relay/request_billing.go 会直接 return nil，
//	   既不建计费会话也不结算，计量与预算预留都会丢失）
//	❌ 不把模型价格或倍率设为 0
//	❌ 不给用户超大余额（会触发 shouldTrust 旁路，连令牌额度预扣都跳过）
//
// 计量不受影响：令牌（平台 Key）额度的原子预留仍由 BillingSession.preConsume 负责，
// 用量与估算成本仍走结算与日志链路。
//
// 配置：环境变量 APIGX_USAGE_ONLY，默认 false，读取方式与底座其它开关一致。
var usageOnlyMode atomic.Bool

func init() {
	usageOnlyMode.Store(common.GetEnvOrDefaultBool("APIGX_USAGE_ONLY", false))
}

// IsUsageOnlyMode 报告当前是否处于 APIGX 免费共享模式。
func IsUsageOnlyMode() bool { return usageOnlyMode.Load() }

// SetUsageOnlyMode 运行期切换模式（供测试使用；未来的后台配置也经由这里生效）。
func SetUsageOnlyMode(on bool) { usageOnlyMode.Store(on) }

// UsageOnlyFunding 是免费共享模式的资金来源：**只记账、不动任何资金**。
//
// 三个方法都是空操作，因此没有充值余额的合法用户也能调用。
// 注意：资金侧的「不超支」保护在本模式下不适用——真正的容量与预算保护由
// 令牌额度原子预留与 APIGX 自己的容量控制承担（docs/stage0/metering-state.md §2、§7）。
type UsageOnlyFunding struct{}

func (u *UsageOnlyFunding) Source() string { return BillingSourceUsageOnly }

func (u *UsageOnlyFunding) PreConsume(_ int) error { return nil }

func (u *UsageOnlyFunding) Settle(_ int) error { return nil }

func (u *UsageOnlyFunding) Refund() error { return nil }

// newUsageOnlySession 在免费共享模式下创建计费会话。
//
// 与 tryWallet 的差别只有两点：
//  1. 不再因用户额度为 0 或不足而拒绝（这正是「取消充值余额前置条件」）；
//  2. 资金来源换成 UsageOnlyFunding。
//
// 其余行为完全一致：令牌额度预扣、结算、退款、幂等守卫都走原有链路。
func newUsageOnlySession(c *gin.Context, relayInfo *relaycommon.RelayInfo, preConsumedQuota int) (*BillingSession, *types.NewAPIError) {
	// 仍然把用户额度写回 relayInfo，供日志与展示使用；但**不作为准入条件**。
	if userQuota, err := model.GetUserQuota(relayInfo.UserId, false); err == nil {
		relayInfo.UserQuota = userQuota
	}
	session := &BillingSession{
		relayInfo: relayInfo,
		funding:   &UsageOnlyFunding{},
	}
	if apiErr := session.preConsume(c, preConsumedQuota); apiErr != nil {
		return nil, apiErr
	}
	logger.LogInfo(c, fmt.Sprintf("APIGX usage_only 模式：用户 %d 无充值余额前置，仅记账 (quota=%d)", relayInfo.UserId, preConsumedQuota))
	return session, nil
}
