package channelpool

import (
	"context"
	"hash/fnv"
	"time"

	"github.com/QuantumNous/new-api/apigx/capacity"
	apigxmodel "github.com/QuantumNous/new-api/apigx/model"
	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// 对账（T1.7）：补偿「进程在步骤之间退出」留下的残留。
//
// 计划书 §9.2 要求「先持久记录出站尝试，再发送」，本文件就是这条要求的另一半：
// **凭已持久化的尝试记录去回收它占用的容量**。
//
// 为什么不能只依赖租约 TTL：
//   - TTL 到期是**被动**回收，最长要等到 TTL（默认 90s）才释放，期间该槽位一直占着；
//   - TTL 到期不会留下任何痕迹，事后无法回答"当时到底有没有发出去"。
//
// 对账把这两点都补齐：主动、精确（按 AttemptUID 释放）且留痕（标记为遗弃）。

// StaleAttemptGracePeriod 是判定「尝试已陈旧」的宽限期。
//
// 必须明显大于正常请求时长，否则会把**正在跑**的长流误判为遗弃并提前释放它的容量。
// 取 30 分钟：远超当前任何单次调用的预期时长，宁可晚回收也不要误伤。
const StaleAttemptGracePeriod = 30 * time.Minute

// ReconcileResult 是对账的结果计数。
type ReconcileResult struct {
	// Scanned 是本次扫描到的陈旧尝试数。
	Scanned int
	// Released 是成功释放了容量租约的数量。
	Released int
	// Failed 是释放失败的数量（会保留为「已开始」，下次对账重试）。
	Failed int
}

// ReconcileStaleAttempts 对陈旧尝试做一次对账：
//  1. 释放它占用的容量租约（幂等：租约已过期/已释放都算成功）；
//  2. 把它标记为「遗弃」，留下可追溯的痕迹。
//
// 释放失败时不标记遗弃——否则会丢掉"还没回收"这一事实，导致容量永久泄漏且无从发现。
func ReconcileStaleAttempts(ctx context.Context, db *gorm.DB, ctrl *capacity.Controller, startedBefore int64, limit int) (ReconcileResult, error) {
	result := ReconcileResult{}
	if db == nil {
		return result, nil
	}
	attempts, err := apigxmodel.ListStaleAttempts(db, startedBefore, limit)
	if err != nil {
		return result, err
	}
	result.Scanned = len(attempts)

	for i := range attempts {
		attempt := attempts[i]
		// 租约可能已经自然过期（Redis 里已不存在）——Release 对这种情形同样是成功的，
		// 因此不需要先判断"是否还存在"。
		if releaseErr := ctrl.Release(ctx, attempt.AttemptUID); releaseErr != nil {
			result.Failed++
			common.SysError("channelpool: reconcile release failed for attempt " + attempt.AttemptUID + ": " + releaseErr.Error())
			continue
		}
		if markErr := apigxmodel.MarkAttemptAbandoned(db, attempt.AttemptUID); markErr != nil {
			result.Failed++
			common.SysError("channelpool: reconcile mark abandoned failed for attempt " + attempt.AttemptUID + ": " + markErr.Error())
			continue
		}
		result.Released++
	}
	return result, nil
}

// ReconcileStaleAttemptsNow 用默认宽限期做一次对账（供后台任务/运维调用）。
func ReconcileStaleAttemptsNow(ctx context.Context, db *gorm.DB, ctrl *capacity.Controller) (ReconcileResult, error) {
	cutoff := common.GetTimestamp() - int64(StaleAttemptGracePeriod.Seconds())
	return ReconcileStaleAttempts(ctx, db, ctrl, cutoff, 100)
}

// RequestIDFromAttemptUID 由尝试 UID 派生一个稳定的 int64 作为 `apigx_attempts.request_id`。
//
// 说明（阶段 1 的过渡做法）：`request_id` 本应指向 `apigx_requests` 的行号，但阶段 1 还没有
// 持久化请求意图那一步。`(request_id, attempt_index)` 上有唯一索引，若一律填 0 会互相冲突，
// 因此用 UID 的 FNV-64 派生：既保证唯一性，又保持「同一尝试永远得到同一值」的确定性。
// 等 `apigx_requests` 落库后，这里应当改为真实请求行号（届时 `attempt_index` 也才有真实语义）。
func RequestIDFromAttemptUID(attemptUID string) int64 {
	hasher := fnv.New64a()
	_, _ = hasher.Write([]byte(attemptUID))
	return int64(hasher.Sum64())
}
