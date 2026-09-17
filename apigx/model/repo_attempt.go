package apigxmodel

import (
	"errors"
	"fmt"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// 出站尝试（`apigx_attempts`）：对账的依据。
//
// 计划书 §9.2 要求的执行顺序是「持久记录请求意图与预算预留 → 取得容量 → 复验权限 →
// **持久记录出站尝试** → 发送」。把尝试先落库的意义在于：进程若在中途退出，
// 事后能凭这条记录知道"当时确实占用了容量/发出了请求"，从而做补偿——
// 这正是对账（reconciliation）需要的**唯一事实来源**。

// AttemptState 取值。
const (
	// AttemptStateStarted 已开始（尚未看到结果）。
	AttemptStateStarted int = 1
	// AttemptStateSucceeded 成功完成。
	AttemptStateSucceeded int = 2
	// AttemptStateFailed 失败完成。
	AttemptStateFailed int = 3
	// AttemptStateAbandoned 被对账判定为**遗弃**（进程中途退出，未留下结果）。
	AttemptStateAbandoned int = 4
)

// BeginAttempt 记录一次出站尝试的开始。
//
// `AttemptUID` 用容量租约 ID：它是该次尝试的幂等键，对账时据此**精确释放**对应的租约
// （容量账本的释放本身就是幂等的，重复释放不会把计数减成负数）。
func BeginAttempt(db *gorm.DB, attempt *Attempt) error {
	if db == nil || attempt == nil {
		return fmt.Errorf("%w: attempt is required", ErrInvalidArgument)
	}
	if attempt.AttemptUID == "" {
		return fmt.Errorf("%w: attempt_uid is required（对账需要它来精确定位租约）", ErrInvalidArgument)
	}
	if attempt.StartedAt == 0 {
		attempt.StartedAt = common.GetTimestamp()
	}
	attempt.State = AttemptStateStarted
	attempt.CreatedAt = common.GetTimestamp()
	return db.Create(attempt).Error
}

// CompleteAttempt 记录尝试的最终状态（按 AttemptUID 定位，幂等）。
//
// 只允许从「已开始」迁移：重复调用（例如失败路径与成功路径各触发一次）不会把已完成的
// 结果覆盖掉，避免对账与审计看到互相矛盾的状态。
func CompleteAttempt(db *gorm.DB, attemptUID string, state int, httpStatus int) error {
	if attemptUID == "" {
		return fmt.Errorf("%w: attempt_uid is required", ErrInvalidArgument)
	}
	if state != AttemptStateSucceeded && state != AttemptStateFailed {
		return fmt.Errorf("%w: state must be succeeded or failed", ErrInvalidArgument)
	}
	res := db.Model(&Attempt{}).
		Where("attempt_uid = ? AND state = ?", attemptUID, AttemptStateStarted).
		Updates(map[string]any{
			"state":        state,
			"http_status":  httpStatus,
			"completed_at": common.GetTimestamp(),
		})
	return res.Error
}

// ListStaleAttempts 返回「已开始但早已超过合理时长」的尝试：这些就是需要补偿的对象。
//
// 判据只用时间：进程崩溃不会留下任何标记，能观察到的事实就是"开始了但一直没结果"。
func ListStaleAttempts(db *gorm.DB, startedBefore int64, limit int) ([]Attempt, error) {
	if db == nil {
		return nil, fmt.Errorf("%w: db is required", ErrInvalidArgument)
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	var attempts []Attempt
	err := db.Where("state = ? AND started_at > 0 AND started_at < ?", AttemptStateStarted, startedBefore).
		Order("started_at ASC").Limit(limit).Find(&attempts).Error
	return attempts, err
}

// MarkAttemptAbandoned 把陈旧尝试标记为遗弃（仅在仍处于「已开始」时生效，幂等）。
func MarkAttemptAbandoned(db *gorm.DB, attemptUID string) error {
	if attemptUID == "" {
		return fmt.Errorf("%w: attempt_uid is required", ErrInvalidArgument)
	}
	return db.Model(&Attempt{}).
		Where("attempt_uid = ? AND state = ?", attemptUID, AttemptStateStarted).
		Updates(map[string]any{
			"state":        AttemptStateAbandoned,
			"completed_at": common.GetTimestamp(),
		}).Error
}

// GetAttemptByUID 按幂等键读取尝试（供对账与排障使用）。
func GetAttemptByUID(db *gorm.DB, attemptUID string) (*Attempt, error) {
	if db == nil || attemptUID == "" {
		return nil, fmt.Errorf("%w: attempt_uid is required", ErrInvalidArgument)
	}
	var attempt Attempt
	err := db.Where("attempt_uid = ?", attemptUID).First(&attempt).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &attempt, nil
}
