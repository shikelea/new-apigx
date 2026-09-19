package controller

import (
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/apigx/relayhook"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay"
	relaychannel "github.com/QuantumNous/new-api/relay/channel"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

var taskCancelAdaptorProvider = func(task *model.Task) relaychannel.TaskAdaptor {
	if task == nil {
		return nil
	}
	return relay.GetTaskAdaptor(task.Platform)
}

var taskCancelCredentialResolver = relayhook.ResolveAsyncTaskCredential

// CancelTask cancels only through an adaptor that explicitly implements
// TaskCancellationAdaptor. Unsupported channels return a stable capability
// error; they are never marked canceled locally.
func CancelTask(c *gin.Context) {
	taskID := strings.TrimSpace(c.Param("task_id"))
	if taskID == "" {
		taskID = strings.TrimSpace(c.Param("video_id"))
	}
	if taskID == "" {
		taskID = strings.TrimSpace(c.Param("key"))
	}
	task, exists, err := model.GetByTaskId(c.GetInt("id"), taskID)
	if err != nil {
		writeTaskCancelError(c, http.StatusInternalServerError, "task_cancel_query_failed", "Failed to query task")
		return
	}
	if !exists || task == nil {
		writeTaskCancelError(c, http.StatusNotFound, "task_not_found", "Task not found")
		return
	}
	if task.Status == model.TaskStatusSuccess || task.Status == model.TaskStatusFailure {
		writeTaskCancelError(c, http.StatusConflict, "task_already_terminal", "Task is already terminal")
		return
	}
	if relayhook.CheckSourceAllowed(c, task.ChannelId, taskSourceModelName(task)) != nil {
		writeTaskCancelError(c, http.StatusServiceUnavailable, "no_available_source", "No available source for the requested model")
		return
	}
	adaptor := taskCancelAdaptorProvider(task)
	canceler, supported := adaptor.(relaychannel.TaskCancellationAdaptor)
	if !supported {
		writeTaskCancelError(c, http.StatusNotImplemented, "task_cancellation_not_supported", "This channel does not support task cancellation")
		return
	}
	channel, err := model.CacheGetChannel(task.ChannelId)
	if err != nil {
		writeTaskCancelError(c, http.StatusServiceUnavailable, "task_channel_unavailable", "Task channel is unavailable")
		return
	}
	key := channel.Key
	if task.PrivateData.Key != "" {
		key = task.PrivateData.Key
	}
	credential, managed, err := taskCancelCredentialResolver(task.ID, task.ChannelId)
	if err != nil {
		writeTaskCancelError(c, http.StatusServiceUnavailable, "task_credential_unavailable", "Task credential snapshot is unavailable")
		return
	}
	if managed {
		key = credential.Secret
	}
	baseURL := channel.GetBaseURL()
	if baseURL == "" {
		baseURL = constant.GetChannelBaseURL(channel.Type)
	}
	adaptor.Init(&relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{
		ChannelType: channel.Type, ChannelId: channel.Id, ChannelBaseUrl: baseURL,
		ApiKey: key, ChannelSetting: channel.GetSetting(),
	}})
	confirmed, err := canceler.CancelTask(c.Request.Context(), baseURL, key, task, channel.GetSetting().Proxy)
	if err != nil {
		writeTaskCancelError(c, http.StatusBadGateway, "task_cancel_upstream_failed", "Upstream cancellation failed")
		return
	}
	if !confirmed {
		c.JSON(http.StatusAccepted, gin.H{"id": task.TaskID, "status": "cancel_pending"})
		return
	}
	won, err := service.FinalizeCanceledTask(c.Request.Context(), adaptor, task)
	if err != nil {
		writeTaskCancelError(c, http.StatusInternalServerError, "task_cancel_finalize_failed", "Failed to finalize cancellation")
		return
	}
	if !won {
		writeTaskCancelError(c, http.StatusConflict, "task_already_terminal", "Task is already terminal")
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": task.TaskID, "status": "canceled"})
}

func writeTaskCancelError(c *gin.Context, status int, code, message string) {
	if c == nil {
		return
	}
	if message == "" {
		message = http.StatusText(status)
	}
	c.JSON(status, gin.H{"error": gin.H{"message": message, "type": code, "code": code}})
}
