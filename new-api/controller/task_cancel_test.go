package controller

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/apigx/relayhook"
	"github.com/QuantumNous/new-api/model"
	relaychannel "github.com/QuantumNous/new-api/relay/channel"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type cancellationTestAdaptor struct {
	relaychannel.TaskAdaptor
	confirmed bool
	err       error
	key       string
}

func (a *cancellationTestAdaptor) Init(*relaycommon.RelayInfo) {}
func (a *cancellationTestAdaptor) AdjustBillingOnComplete(*model.Task, *relaycommon.TaskInfo) int {
	return 0
}
func (a *cancellationTestAdaptor) CancelTask(_ context.Context, _ string, key string, _ *model.Task, _ string) (bool, error) {
	a.key = key
	return a.confirmed, a.err
}

func newTaskCancelContext(task *model.Task) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Set("id", task.UserId)
	c.Params = gin.Params{{Key: "key", Value: task.TaskID}}
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/tasks/"+task.TaskID+"/cancel", nil)
	return c, recorder
}

func setupCancelableTask(t *testing.T) *model.Task {
	t.Helper()
	task := setupGenericTaskTest(t)
	task.Status = model.TaskStatusInProgress
	task.Progress = "50%"
	task.FinishTime = 0
	task.PrivateData.Key = "historical-base-key"
	require.NoError(t, model.DB.Save(task).Error)
	previousGuard := relayhook.IsSourceGuardEnabled()
	relayhook.SetSourceGuardEnabled(false)
	t.Cleanup(func() { relayhook.SetSourceGuardEnabled(previousGuard) })
	previousResolver := taskCancelCredentialResolver
	taskCancelCredentialResolver = func(int64, ...int) (*relayhook.AsyncTaskCredential, bool, error) {
		return nil, false, nil
	}
	t.Cleanup(func() { taskCancelCredentialResolver = previousResolver })
	return task
}

func TestCancelTaskRejectsUnsupportedAdaptorWithoutChangingTask(t *testing.T) {
	task := setupCancelableTask(t)
	previous := taskCancelAdaptorProvider
	taskCancelAdaptorProvider = func(*model.Task) relaychannel.TaskAdaptor { return nil }
	t.Cleanup(func() { taskCancelAdaptorProvider = previous })
	c, recorder := newTaskCancelContext(task)

	CancelTask(c)

	assert.Equal(t, http.StatusNotImplemented, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "task_cancellation_not_supported")
	var persisted model.Task
	require.NoError(t, model.DB.First(&persisted, task.ID).Error)
	assert.Equal(t, model.TaskStatus(model.TaskStatusInProgress), persisted.Status)
}

func TestCancelTaskWaitsForUpstreamConfirmation(t *testing.T) {
	task := setupCancelableTask(t)
	adaptor := &cancellationTestAdaptor{confirmed: false}
	previous := taskCancelAdaptorProvider
	taskCancelAdaptorProvider = func(*model.Task) relaychannel.TaskAdaptor { return adaptor }
	t.Cleanup(func() { taskCancelAdaptorProvider = previous })
	c, recorder := newTaskCancelContext(task)

	CancelTask(c)

	assert.Equal(t, http.StatusAccepted, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "cancel_pending")
	var persisted model.Task
	require.NoError(t, model.DB.First(&persisted, task.ID).Error)
	assert.Equal(t, model.TaskStatus(model.TaskStatusInProgress), persisted.Status)
}

func TestCancelTaskDoesNotExposeUpstreamError(t *testing.T) {
	task := setupCancelableTask(t)
	adaptor := &cancellationTestAdaptor{err: errors.New("provider body contains sk-upstream-secret")}
	previous := taskCancelAdaptorProvider
	taskCancelAdaptorProvider = func(*model.Task) relaychannel.TaskAdaptor { return adaptor }
	t.Cleanup(func() { taskCancelAdaptorProvider = previous })
	c, recorder := newTaskCancelContext(task)

	CancelTask(c)

	assert.Equal(t, http.StatusBadGateway, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "task_cancel_upstream_failed")
	assert.NotContains(t, recorder.Body.String(), "sk-upstream-secret")
	var persisted model.Task
	require.NoError(t, model.DB.First(&persisted, task.ID).Error)
	assert.Equal(t, model.TaskStatus(model.TaskStatusInProgress), persisted.Status)
}

func TestCancelTaskFinalizesOnlyAfterUpstreamConfirmation(t *testing.T) {
	task := setupCancelableTask(t)
	adaptor := &cancellationTestAdaptor{confirmed: true}
	previous := taskCancelAdaptorProvider
	taskCancelAdaptorProvider = func(*model.Task) relaychannel.TaskAdaptor { return adaptor }
	t.Cleanup(func() { taskCancelAdaptorProvider = previous })
	c, recorder := newTaskCancelContext(task)

	CancelTask(c)

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Contains(t, recorder.Body.String(), `"status":"canceled"`)
	assert.Equal(t, "historical-base-key", adaptor.key)
	var persisted model.Task
	require.NoError(t, model.DB.First(&persisted, task.ID).Error)
	assert.Equal(t, model.TaskStatus(model.TaskStatusFailure), persisted.Status)
	assert.Equal(t, "user_canceled", persisted.FailReason)
}
