package apigxmodel

import (
	"errors"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func TestAsyncTaskSnapshotIsImmutableAndIdempotent(t *testing.T) {
	db := newRepoDB(t)
	original := &AsyncTask{
		PublicTaskID: "apigx_async_1", BaseTaskID: 71001, UpstreamTaskID: "upstream-a",
		UserID: 81, ChannelID: 91, UpstreamKeyID: 101, UpstreamKeyCredentialVersion: 3,
		Status: AsyncTaskStatusRunning,
	}
	stored, created, err := CreateAsyncTaskSnapshot(db, original)
	require.NoError(t, err)
	require.True(t, created)
	require.NotNil(t, stored)

	duplicate := &AsyncTask{
		PublicTaskID: "apigx_async_changed", BaseTaskID: original.BaseTaskID,
		UpstreamTaskID: "upstream-b", UpstreamKeyID: 999, Status: AsyncTaskStatusFailed,
	}
	stored, created, err = CreateAsyncTaskSnapshot(db, duplicate)
	require.NoError(t, err)
	assert.False(t, created)
	require.NotNil(t, stored)
	assert.Equal(t, "apigx_async_1", stored.PublicTaskID)
	assert.Equal(t, "upstream-a", stored.UpstreamTaskID)
	assert.EqualValues(t, 101, stored.UpstreamKeyID)
	assert.Equal(t, AsyncTaskStatusRunning, stored.Status)
}

func TestAsyncTaskStatusOnlyMovesForward(t *testing.T) {
	db := newRepoDB(t)
	task := &AsyncTask{PublicTaskID: "apigx_async_2", BaseTaskID: 71002, Status: AsyncTaskStatusPendingSubmit}
	_, _, err := CreateAsyncTaskSnapshot(db, task)
	require.NoError(t, err)

	running, changed, err := AdvanceAsyncTaskStatus(db, task.BaseTaskID, AsyncTaskStatusRunning, "accepted", 100, 0)
	require.NoError(t, err)
	assert.True(t, changed)
	assert.Equal(t, AsyncTaskStatusRunning, running.Status)
	assert.EqualValues(t, 100, running.StartAt)

	done, changed, err := AdvanceAsyncTaskStatus(db, task.BaseTaskID, AsyncTaskStatusSucceeded, "completed", 200, 300)
	require.NoError(t, err)
	assert.True(t, changed)
	assert.Equal(t, AsyncTaskStatusSucceeded, done.Status)
	assert.EqualValues(t, 100, done.StartAt, "the first observed start time is immutable")
	assert.EqualValues(t, 300, done.FinishAt)

	doneAgain, changed, err := AdvanceAsyncTaskStatus(db, task.BaseTaskID, AsyncTaskStatusSucceeded, "duplicate_callback", 0, 999)
	require.NoError(t, err)
	assert.False(t, changed)
	assert.Equal(t, "completed", doneAgain.StatusReason)
	assert.EqualValues(t, 300, doneAgain.FinishAt)

	_, changed, err = AdvanceAsyncTaskStatus(db, task.BaseTaskID, AsyncTaskStatusFailed, "late_failure", 0, 0)
	assert.ErrorIs(t, err, ErrAsyncTaskTransition)
	assert.False(t, changed)
}

func TestAsyncTaskStatusCASMissIsNotReportedAsChanged(t *testing.T) {
	db := newRepoDB(t)
	task := &AsyncTask{PublicTaskID: "apigx_async_cas_miss", BaseTaskID: 71006, Status: AsyncTaskStatusRunning}
	_, _, err := CreateAsyncTaskSnapshot(db, task)
	require.NoError(t, err)

	callbackName := "test:force_async_task_cas_miss"
	require.NoError(t, db.Callback().Update().Before("gorm:update").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Table == (AsyncTask{}).TableName() {
			tx.Statement.AddClause(clause.Where{Exprs: []clause.Expression{clause.Expr{SQL: "1 = 0"}}})
		}
	}))
	t.Cleanup(func() { _ = db.Callback().Update().Remove(callbackName) })

	current, changed, err := AdvanceAsyncTaskStatus(db, task.BaseTaskID, AsyncTaskStatusSucceeded, "completed", 0, 300)
	assert.ErrorIs(t, err, ErrAsyncTaskTransition)
	assert.False(t, changed)
	require.NotNil(t, current)
	assert.Equal(t, AsyncTaskStatusRunning, current.Status)

	require.NoError(t, db.Callback().Update().Remove(callbackName))
	reloaded, err := GetAsyncTaskByBaseTaskID(db, task.BaseTaskID)
	require.NoError(t, err)
	assert.Equal(t, AsyncTaskStatusRunning, reloaded.Status)
	assert.Zero(t, reloaded.FinishAt)
}

func TestAsyncTaskReservationReleaseCanRecoverButFinishesOnce(t *testing.T) {
	db := newRepoDB(t)
	task := &AsyncTask{
		PublicTaskID: "apigx_async_3", BaseTaskID: 71003,
		Status: AsyncTaskStatusSucceeded, ReservationUID: "async-lease-71003",
	}
	_, _, err := CreateAsyncTaskSnapshot(db, task)
	require.NoError(t, err)

	claimed, ok, err := ClaimAsyncTaskReservationRelease(db, task.BaseTaskID)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, AsyncTaskReservationReleasing, claimed.ReservationReleaseState)

	// A process may crash after Redis release and before the DB acknowledgement.
	// Re-claiming the releasing state is deliberate; Redis release is idempotent.
	retry, ok, err := ClaimAsyncTaskReservationRelease(db, task.BaseTaskID)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, AsyncTaskReservationReleasing, retry.ReservationReleaseState)

	require.NoError(t, MarkAsyncTaskReservationReleased(db, task.BaseTaskID))
	_, ok, err = ClaimAsyncTaskReservationRelease(db, task.BaseTaskID)
	require.NoError(t, err)
	assert.False(t, ok, "an acknowledged release must never be performed again")

	reloaded, err := GetAsyncTaskByBaseTaskID(db, task.BaseTaskID)
	require.NoError(t, err)
	assert.Equal(t, AsyncTaskReservationReleased, reloaded.ReservationReleaseState)
	assert.NotZero(t, reloaded.ReservationReleasedAt)
}

func TestAsyncTaskTimeoutIsListedWithoutAssumingTerminalState(t *testing.T) {
	db := newRepoDB(t)
	now := common.GetTimestamp()
	task := &AsyncTask{
		PublicTaskID: "apigx_async_4", BaseTaskID: 71004,
		Status: AsyncTaskStatusRunning, TimeoutAt: now - 1, ReservationUID: "async-lease-71004",
	}
	_, _, err := CreateAsyncTaskSnapshot(db, task)
	require.NoError(t, err)

	listed, err := ListAsyncTasksForReconciliation(db, now, 10)
	require.NoError(t, err)
	require.Len(t, listed, 1)
	assert.Equal(t, AsyncTaskStatusRunning, listed[0].Status, "a local timeout does not claim the upstream stopped")
	assert.False(t, IsAsyncTaskTerminal(listed[0].Status))

	_, _, err = AdvanceAsyncTaskStatus(db, task.BaseTaskID, AsyncTaskStatusReconciling, "timeout_pending_upstream_reconciliation", 0, 0)
	require.NoError(t, err)
	_, _, err = AdvanceAsyncTaskStatus(db, task.BaseTaskID, AsyncTaskStatusRunning, "upstream_still_running", 0, 0)
	require.NoError(t, err)

	_, _, err = AdvanceAsyncTaskStatus(db, task.BaseTaskID, AsyncTaskStatusPendingSubmit, "invalid_rewind", 0, 0)
	assert.True(t, errors.Is(err, ErrAsyncTaskTransition))
}

func TestAsyncTaskSnapshotRepairQueueIsDurableAndCompletesOnce(t *testing.T) {
	db := newRepoDB(t)
	snapshot := &AsyncTask{
		PublicTaskID: "apigx_repair_1", BaseTaskID: 71005, UpstreamTaskID: "upstream-repair",
		UserID: 82, ChannelID: 92, UpstreamKeyID: 102, UpstreamKeyCredentialVersion: 7,
		Status: AsyncTaskStatusRunning, ReservationUID: "async-lease-71005",
	}
	require.NoError(t, QueueAsyncTaskSnapshotRepair(db, snapshot, "snapshot_insert_failed"))
	require.NoError(t, QueueAsyncTaskSnapshotRepair(db, snapshot, "duplicate"), "queue writes must be idempotent")
	assert.True(t, HasPendingAsyncTaskSnapshotRepairs(db))

	pending, found, err := GetPendingAsyncTaskSnapshotRepair(db, snapshot.BaseTaskID)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, snapshot.UpstreamTaskID, pending.UpstreamTaskID)
	assert.Equal(t, snapshot.UpstreamKeyCredentialVersion, pending.UpstreamKeyCredentialVersion)
	assert.Equal(t, snapshot.ReservationUID, pending.ReservationUID)

	listed, err := ListPendingAsyncTaskSnapshotRepairs(db, 10)
	require.NoError(t, err)
	require.Len(t, listed, 1)
	require.NoError(t, MarkAsyncTaskSnapshotRepairCompleted(db, pending))
	require.NoError(t, MarkAsyncTaskSnapshotRepairCompleted(db, pending), "completion is idempotent")
	assert.False(t, HasPendingAsyncTaskSnapshotRepairs(db))
	_, found, err = GetPendingAsyncTaskSnapshotRepair(db, snapshot.BaseTaskID)
	require.NoError(t, err)
	assert.False(t, found)
}
