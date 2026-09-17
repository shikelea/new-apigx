package routing

import (
	"fmt"

	apigxmodel "github.com/QuantumNous/new-api/apigx/model"
)

// 熔断与错误归因（T2.8）。
//
// # 为什么从统计桶推导，而不是再建一张"熔断状态表"
//
// 熔断要回答的只是"这条供给最近是不是持续失败"。这个答案**已经**由分钟桶提供了
// （T2.4）。再存一份状态意味着两处可能不一致，还要处理进程重启后的状态恢复；
// 而推导式熔断**随窗口滚动自愈**：失败样本过期后自动恢复，不需要任何补偿逻辑
// （与计划书"恢复并对账后再开放"的精神一致，但更简单）。
//
// # 归因红线：**平台自身的拒绝不得参与熔断**
//
// 准入拒绝（来源不允许/容量/配额）与用户取消都发生在我们这边或用户侧，
// 与上游质量无关。拿它们熔断分享者的供给会**误伤健康渠道**——
// 这正是"错误归因"要避免的典型后果。因此失败率的分母/分子只取
// **上游失败 + 超时**与**完整成功**，平台拒绝与取消两者都不计入。

// BreakerState 是熔断状态。
type BreakerState int

const (
	// BreakerClosed 正常：参与路由。
	BreakerClosed BreakerState = 0
	// BreakerOpen 熔断打开：**从候选中摘除**，直到冷却结束。
	BreakerOpen BreakerState = 1
	// BreakerHalfOpen 半开：冷却已过，放行少量流量试探是否恢复。
	BreakerHalfOpen BreakerState = 2
)

// String 返回状态标识（用于可解释结果）。
func (s BreakerState) String() string {
	switch s {
	case BreakerClosed:
		return "closed"
	case BreakerOpen:
		return "open"
	case BreakerHalfOpen:
		return "half_open"
	default:
		return "unknown"
	}
}

// BreakerConfig 是熔断阈值（可配置，与计划书"数值均可配置"一致）。
type BreakerConfig struct {
	// MinAttempts 是触发熔断所需的**最小合格尝试数**。
	// 少于它时不熔断：一两次失败不足以说明上游坏了（与"样本不足不给结论"同一原则）。
	MinAttempts int64
	// FailureRatePpm 是失败率阈值（百万分比）；达到即熔断。
	FailureRatePpm int64
	// CooldownSeconds 是熔断后的冷却时长；冷却结束进入半开。
	CooldownSeconds int64
}

// DefaultBreakerConfig 是初始阈值（保守起点，不是调优结论）。
func DefaultBreakerConfig() BreakerConfig {
	return BreakerConfig{MinAttempts: 5, FailureRatePpm: 500_000, CooldownSeconds: 60}
}

// BreakerVerdict 是一次熔断判定的结果。
type BreakerVerdict struct {
	State BreakerState
	// Attempts 是参与判定的合格尝试数（上游成功 + 上游失败 + 超时）。
	Attempts int64
	// FailureRatePpm 是基于合格尝试的失败率。
	FailureRatePpm int64
	// Reason 是简短说明（用于可解释结果）。
	Reason string
}

// EvaluateBreaker 依据质量视图与当前时间判定熔断状态。
//
// 判定顺序：样本不足 → 正常；失败率不足阈值 → 正常；否则按冷却时间区分
// "打开"与"半开"。
func EvaluateBreaker(view apigxmodel.QualityView, now int64, cfg BreakerConfig) BreakerVerdict {
	if cfg.MinAttempts <= 0 {
		cfg.MinAttempts = DefaultBreakerConfig().MinAttempts
	}
	if cfg.FailureRatePpm <= 0 {
		cfg.FailureRatePpm = DefaultBreakerConfig().FailureRatePpm
	}
	if cfg.CooldownSeconds <= 0 {
		cfg.CooldownSeconds = DefaultBreakerConfig().CooldownSeconds
	}

	// **只统计上游侧**：完整成功 + 上游失败 + 超时
	successes := view.SuccessCount
	upstreamFailures := view.UpstreamFailures + view.TimeoutCount
	attempts := successes + upstreamFailures
	verdict := BreakerVerdict{Attempts: attempts}

	if attempts < cfg.MinAttempts {
		verdict.State = BreakerClosed
		verdict.Reason = fmt.Sprintf("insufficient_attempts(%d<%d)", attempts, cfg.MinAttempts)
		return verdict
	}
	verdict.FailureRatePpm = upstreamFailures * 1_000_000 / attempts
	if verdict.FailureRatePpm < cfg.FailureRatePpm {
		verdict.State = BreakerClosed
		verdict.Reason = fmt.Sprintf("failure_rate_ok(%dppm)", verdict.FailureRatePpm)
		return verdict
	}
	if view.LatestUpstreamFailureTs > 0 && now-view.LatestUpstreamFailureTs >= cfg.CooldownSeconds {
		// 冷却已过：放行试探（半开），**不永久封禁**——上游恢复了也要能被重新用上
		verdict.State = BreakerHalfOpen
		verdict.Reason = fmt.Sprintf("cooldown_elapsed(%ds)", now-view.LatestUpstreamFailureTs)
		return verdict
	}
	verdict.State = BreakerOpen
	verdict.Reason = fmt.Sprintf("failure_rate_high(%dppm>=%dppm)", verdict.FailureRatePpm, cfg.FailureRatePpm)
	return verdict
}
