package apigxmodel

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrAsyncTaskTransition = errors.New("apigx: invalid async task transition")

const (
	asyncTaskSnapshotRepairPendingAction   = "async_task_snapshot_repair_pending"
	asyncTaskSnapshotRepairCompletedAction = "async_task_snapshot_repair_completed"
	asyncTaskSnapshotRepairTargetType      = "async_task_snapshot"
)

type asyncTaskSnapshotRepairEnvelope struct {
	Snapshot   AsyncTask `json:"snapshot"`
	ReasonCode string    `json:"reason_code"`
}

const (
	AsyncTaskStatusPendingSubmit = 0
	AsyncTaskStatusRunning       = 1
	AsyncTaskStatusSucceeded     = 2
	AsyncTaskStatusFailed        = 3
	AsyncTaskStatusCanceled      = 4
	AsyncTaskStatusUnknown       = 5
	AsyncTaskStatusReconciling   = 6

	AsyncTaskReservationHeld      = 0
	AsyncTaskReservationReleasing = 1
	AsyncTaskReservationReleased  = 2
)

func IsAsyncTaskTerminal(status int) bool {
	return status == AsyncTaskStatusSucceeded || status == AsyncTaskStatusFailed || status == AsyncTaskStatusCanceled
}

// CreateAsyncTaskSnapshot 只创建一次不可变快照；重复 base_task_id 返回原记录。
func CreateAsyncTaskSnapshot(db *gorm.DB, task *AsyncTask) (*AsyncTask, bool, error) {
	if db == nil || task == nil || task.BaseTaskID <= 0 || task.PublicTaskID == "" {
		return nil, false, fmt.Errorf("%w: complete async task snapshot is required", ErrInvalidArgument)
	}
	now := common.GetTimestamp()
	if task.CreatedAt == 0 {
		task.CreatedAt = now
	}
	if task.UpdatedAt == 0 {
		task.UpdatedAt = now
	}
	result := db.Clauses(clause.OnConflict{DoNothing: true}).Create(task)
	if result.Error != nil {
		return nil, false, result.Error
	}
	if result.RowsAffected > 0 {
		return task, true, nil
	}
	existing, err := GetAsyncTaskByBaseTaskID(db, task.BaseTaskID)
	return existing, false, err
}

func GetAsyncTaskByBaseTaskID(db *gorm.DB, baseTaskID int64) (*AsyncTask, error) {
	if db == nil || baseTaskID <= 0 {
		return nil, ErrNotFound
	}
	var task AsyncTask
	if err := db.Where("base_task_id = ?", baseTaskID).First(&task).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &task, nil
}

// QueueAsyncTaskSnapshotRepair persists a credential-free copy of a snapshot
// that could not be inserted. Audit events are immutable and already part of
// the frozen schema, so the pending/completed event pair acts as a durable
// repair queue without introducing another overlapping outbox table.
func QueueAsyncTaskSnapshotRepair(db *gorm.DB, task *AsyncTask, reasonCode string) error {
	if db == nil || task == nil || task.BaseTaskID <= 0 || task.PublicTaskID == "" {
		return fmt.Errorf("%w: complete async task repair snapshot is required", ErrInvalidArgument)
	}
	envelope := asyncTaskSnapshotRepairEnvelope{Snapshot: *task, ReasonCode: reasonCode}
	payload, err := common.Marshal(envelope)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(payload)
	now := common.GetTimestamp()
	event := &AuditEvent{
		EventUID:       asyncTaskSnapshotRepairEventUID(asyncTaskSnapshotRepairPendingAction, task.BaseTaskID),
		Category:       "system_repair",
		Action:         asyncTaskSnapshotRepairPendingAction,
		TargetType:     asyncTaskSnapshotRepairTargetType,
		TargetID:       task.BaseTaskID,
		TargetPublicID: task.PublicTaskID,
		AfterDigest:    hex.EncodeToString(digest[:]),
		ChangeSummary:  string(payload),
		OccurredAt:     now,
		CreatedAt:      now,
	}
	return db.Clauses(clause.OnConflict{DoNothing: true}).Create(event).Error
}

// GetPendingAsyncTaskSnapshotRepair returns the immutable repair payload only
// while no matching completion event exists.
func GetPendingAsyncTaskSnapshotRepair(db *gorm.DB, baseTaskID int64) (*AsyncTask, bool, error) {
	if db == nil || baseTaskID <= 0 {
		return nil, false, nil
	}
	var event AuditEvent
	query := pendingAsyncTaskSnapshotRepairQuery(db).Select("pending.*").
		Where("pending.target_id = ?", baseTaskID).
		Order("pending.id ASC").Limit(1)
	if err := query.Scan(&event).Error; err != nil {
		return nil, false, err
	}
	if event.ID == 0 {
		return nil, false, nil
	}
	task, err := decodeAsyncTaskSnapshotRepair(event.ChangeSummary)
	return task, err == nil, err
}

func ListPendingAsyncTaskSnapshotRepairs(db *gorm.DB, limit int) ([]AsyncTask, error) {
	if db == nil {
		return nil, gorm.ErrInvalidDB
	}
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	var events []AuditEvent
	if err := pendingAsyncTaskSnapshotRepairQuery(db).Select("pending.*").
		Order("pending.id ASC").Limit(limit).Scan(&events).Error; err != nil {
		return nil, err
	}
	tasks := make([]AsyncTask, 0, len(events))
	for i := range events {
		task, err := decodeAsyncTaskSnapshotRepair(events[i].ChangeSummary)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, *task)
	}
	return tasks, nil
}

func HasPendingAsyncTaskSnapshotRepairs(db *gorm.DB) bool {
	if db == nil {
		return false
	}
	var count int64
	err := pendingAsyncTaskSnapshotRepairQuery(db).Count(&count).Error
	return err == nil && count > 0
}

func MarkAsyncTaskSnapshotRepairCompleted(db *gorm.DB, task *AsyncTask) error {
	if db == nil || task == nil || task.BaseTaskID <= 0 {
		return fmt.Errorf("%w: repaired async task is required", ErrInvalidArgument)
	}
	now := common.GetTimestamp()
	event := &AuditEvent{
		EventUID:       asyncTaskSnapshotRepairEventUID(asyncTaskSnapshotRepairCompletedAction, task.BaseTaskID),
		Category:       "system_repair",
		Action:         asyncTaskSnapshotRepairCompletedAction,
		TargetType:     asyncTaskSnapshotRepairTargetType,
		TargetID:       task.BaseTaskID,
		TargetPublicID: task.PublicTaskID,
		OccurredAt:     now,
		CreatedAt:      now,
	}
	return db.Clauses(clause.OnConflict{DoNothing: true}).Create(event).Error
}

func pendingAsyncTaskSnapshotRepairQuery(db *gorm.DB) *gorm.DB {
	table := AuditEvent{}.TableName()
	return db.Table(table+" AS pending").
		Where("pending.action = ? AND pending.target_type = ?", asyncTaskSnapshotRepairPendingAction, asyncTaskSnapshotRepairTargetType).
		Where("NOT EXISTS (SELECT 1 FROM "+table+" AS completed WHERE completed.action = ? AND completed.target_type = pending.target_type AND completed.target_id = pending.target_id)",
			asyncTaskSnapshotRepairCompletedAction)
}

func decodeAsyncTaskSnapshotRepair(payload string) (*AsyncTask, error) {
	var envelope asyncTaskSnapshotRepairEnvelope
	if err := common.Unmarshal([]byte(payload), &envelope); err != nil {
		return nil, err
	}
	if envelope.Snapshot.BaseTaskID <= 0 || envelope.Snapshot.PublicTaskID == "" {
		return nil, fmt.Errorf("%w: invalid async task repair payload", ErrInvalidArgument)
	}
	return &envelope.Snapshot, nil
}

func asyncTaskSnapshotRepairEventUID(action string, baseTaskID int64) string {
	prefix := "async-snapshot-pending-"
	if action == asyncTaskSnapshotRepairCompletedAction {
		prefix = "async-snapshot-completed-"
	}
	return prefix + fmt.Sprint(baseTaskID)
}

func allowedAsyncTaskTransition(from, to int) bool {
	if from == to {
		return true
	}
	if IsAsyncTaskTerminal(from) || to == AsyncTaskStatusPendingSubmit {
		return false
	}
	switch from {
	case AsyncTaskStatusPendingSubmit:
		return to == AsyncTaskStatusRunning || to == AsyncTaskStatusFailed || to == AsyncTaskStatusUnknown || to == AsyncTaskStatusReconciling
	case AsyncTaskStatusRunning:
		return IsAsyncTaskTerminal(to) || to == AsyncTaskStatusUnknown || to == AsyncTaskStatusReconciling
	case AsyncTaskStatusUnknown:
		return IsAsyncTaskTerminal(to) || to == AsyncTaskStatusReconciling
	case AsyncTaskStatusReconciling:
		return to == AsyncTaskStatusRunning || IsAsyncTaskTerminal(to) || to == AsyncTaskStatusUnknown
	default:
		return false
	}
}

// AdvanceAsyncTaskStatus 在行锁内执行单向状态转换；终态不可被重复回调改写。
func AdvanceAsyncTaskStatus(db *gorm.DB, baseTaskID int64, next int, reason string, startAt, finishAt int64) (*AsyncTask, bool, error) {
	var out AsyncTask
	changed := false
	err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("base_task_id = ?", baseTaskID).First(&out).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrNotFound
			}
			return err
		}
		if !allowedAsyncTaskTransition(out.Status, next) {
			return fmt.Errorf("%w: %d -> %d", ErrAsyncTaskTransition, out.Status, next)
		}
		if out.Status == next {
			return nil
		}
		now := common.GetTimestamp()
		updates := map[string]any{"status": next, "status_reason": reason, "updated_at": now}
		if startAt > 0 && out.StartAt == 0 {
			updates["start_at"] = startAt
			out.StartAt = startAt
		}
		if IsAsyncTaskTerminal(next) {
			if finishAt <= 0 {
				finishAt = now
			}
			updates["finish_at"] = finishAt
			out.FinishAt = finishAt
		}
		previousStatus := out.Status
		result := tx.Model(&AsyncTask{}).Where("id = ? AND status = ?", out.ID, previousStatus).Updates(updates)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			// The WHERE status predicate is the final CAS guard for dialects where
			// SELECT FOR UPDATE is unavailable (notably SQLite). Re-read the winner
			// instead of reporting that this caller advanced the state.
			if err := tx.Where("id = ?", out.ID).First(&out).Error; err != nil {
				return err
			}
			if out.Status == next {
				return nil
			}
			return fmt.Errorf("%w: concurrent %d -> %d", ErrAsyncTaskTransition, previousStatus, out.Status)
		}
		out.Status = next
		out.StatusReason = reason
		out.UpdatedAt = now
		changed = true
		return nil
	})
	return &out, changed, err
}

// ClaimAsyncTaskReservationRelease 把终态任务的租约变为待释放。state=1 会被恢复任务重试，
// Redis Release 本身幂等，因此多实例重复执行也不会减成负数。
func ClaimAsyncTaskReservationRelease(db *gorm.DB, baseTaskID int64) (*AsyncTask, bool, error) {
	var out AsyncTask
	claimed := false
	err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("base_task_id = ?", baseTaskID).First(&out).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrNotFound
			}
			return err
		}
		if !IsAsyncTaskTerminal(out.Status) || out.ReservationUID == "" || out.ReservationReleaseState == AsyncTaskReservationReleased {
			return nil
		}
		if out.ReservationReleaseState == AsyncTaskReservationHeld {
			if err := tx.Model(&AsyncTask{}).Where("id = ? AND reservation_release_state = ?", out.ID, AsyncTaskReservationHeld).
				Updates(map[string]any{"reservation_release_state": AsyncTaskReservationReleasing, "updated_at": common.GetTimestamp()}).Error; err != nil {
				return err
			}
			out.ReservationReleaseState = AsyncTaskReservationReleasing
		}
		claimed = true
		return nil
	})
	return &out, claimed, err
}

func MarkAsyncTaskReservationReleased(db *gorm.DB, baseTaskID int64) error {
	now := common.GetTimestamp()
	return db.Model(&AsyncTask{}).
		Where("base_task_id = ? AND reservation_release_state <> ?", baseTaskID, AsyncTaskReservationReleased).
		Updates(map[string]any{
			"reservation_release_state": AsyncTaskReservationReleased,
			"reservation_released_at":   now,
			"updated_at":                now,
		}).Error
}

// ListAsyncTasksForReconciliation 返回需要恢复续租/轮询或补做释放的任务。
func ListAsyncTasksForReconciliation(db *gorm.DB, now int64, limit int) ([]AsyncTask, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	var tasks []AsyncTask
	err := db.Where("reservation_release_state = ? OR status IN ? OR (timeout_at > 0 AND timeout_at <= ? AND status IN ?)",
		AsyncTaskReservationReleasing,
		[]int{AsyncTaskStatusRunning, AsyncTaskStatusUnknown, AsyncTaskStatusReconciling},
		now, []int{AsyncTaskStatusRunning, AsyncTaskStatusUnknown, AsyncTaskStatusReconciling}).
		Order("updated_at ASC, id ASC").Limit(limit).Find(&tasks).Error
	return tasks, err
}
