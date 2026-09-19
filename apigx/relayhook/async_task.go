package relayhook

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	apigxmodel "github.com/QuantumNous/new-api/apigx/model"
	"github.com/QuantumNous/new-api/apigx/secretbox"
	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
)

const (
	AsyncTaskStatusPendingSubmit = apigxmodel.AsyncTaskStatusPendingSubmit
	AsyncTaskStatusRunning       = apigxmodel.AsyncTaskStatusRunning
	AsyncTaskStatusSucceeded     = apigxmodel.AsyncTaskStatusSucceeded
	AsyncTaskStatusFailed        = apigxmodel.AsyncTaskStatusFailed
	AsyncTaskStatusCanceled      = apigxmodel.AsyncTaskStatusCanceled
	AsyncTaskStatusUnknown       = apigxmodel.AsyncTaskStatusUnknown
	AsyncTaskStatusReconciling   = apigxmodel.AsyncTaskStatusReconciling
)

type AsyncTaskCredential struct {
	Snapshot *apigxmodel.AsyncTask
	Secret   string
}

var asyncTaskDBProvider = func() *gorm.DB { return apigxmodel.DefaultDB() }

// RecordAsyncTaskSnapshot 在任务持久化且提交结算成功后创建完整归属快照，
// 随后把请求租约移交给任务。响应结束不释放该租约，只有终态处理能幂等释放。
func RecordAsyncTaskSnapshot(
	c *gin.Context,
	relayInfo *relaycommon.RelayInfo,
	baseTaskID int64,
	upstreamTaskID, action, baseStatus string,
	startAt, finishAt int64,
) {
	if c == nil || relayInfo == nil || baseTaskID <= 0 {
		return
	}
	db := asyncTaskDBProvider()
	if db == nil {
		return
	}

	owner, channelID, offeringID, keyID := resolveUsageAttribution(db, c)
	if channelID <= 0 {
		// This is a regular New API channel. Creating a zero-attribution APIGX
		// snapshot would make later polling treat the task as managed and fail its
		// credential lookup, so unmanaged tasks deliberately stay on the base path.
		return
	}
	now := common.GetTimestamp()
	snapshot := &apigxmodel.AsyncTask{
		PublicTaskID:   "apigx_" + strconv.FormatInt(baseTaskID, 10),
		BaseTaskID:     baseTaskID,
		UpstreamTaskID: upstreamTaskID,
		UserID:         int64(relayInfo.UserId),
		BaseTokenID:    int64(relayInfo.TokenId),
		OfferingID:     offeringID,
		ChannelID:      channelID,
		UpstreamKeyID:  keyID,
		OwnerUserID:    owner,
		Action:         action,
		Capability:     asyncTaskCapability(relayInfo, action),
		Status:         asyncStatusFromBase(baseStatus),
		SubmitAt:       now,
		StartAt:        startAt,
		FinishAt:       finishAt,
		TimeoutAt:      now + int64(common.GetEnvOrDefault("APIGX_ASYNC_TASK_TIMEOUT_SECONDS", 86400)),
		BillingSettled: true,
		ReservationUID: leaseIDFromContext(c),
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	fillAsyncTaskAttribution(db, relayInfo, snapshot)
	stored, _, err := apigxmodel.CreateAsyncTaskSnapshot(db, snapshot)
	if err != nil {
		common.SysError("relayhook: record async task snapshot failed: " + err.Error())
		if queueErr := apigxmodel.QueueAsyncTaskSnapshotRepair(db, snapshot, "snapshot_insert_failed"); queueErr != nil {
			common.SysError("relayhook: queue async task snapshot repair failed: " + queueErr.Error())
			return
		}
		// The repair payload is now durable. Keep renewing the same reservation
		// until reconciliation materializes the snapshot; the request defer must
		// not release capacity while the upstream task is still running.
		if snapshot.ReservationUID != "" && !TransferLeaseToAsyncTask(c, snapshot.ReservationUID) {
			common.SysError("relayhook: async task repair lease transfer failed; reconciliation will restore it")
		}
		return
	}
	if stored == nil {
		return
	}
	if apigxmodel.IsAsyncTaskTerminal(stored.Status) {
		if err := releaseAsyncTaskReservation(stored.BaseTaskID); err != nil {
			common.SysError("relayhook: release immediate async task reservation failed: " + err.Error())
		}
		return
	}
	if stored.ReservationUID != "" && !TransferLeaseToAsyncTask(c, stored.ReservationUID) {
		common.SysError("relayhook: async task lease transfer failed; reconciliation will recover it")
	}
}

func fillAsyncTaskAttribution(db *gorm.DB, relayInfo *relaycommon.RelayInfo, task *apigxmodel.AsyncTask) {
	if db == nil || relayInfo == nil || task == nil {
		return
	}
	var channel apigxmodel.Channel
	if task.ChannelID > 0 && db.Unscoped().Where("id = ?", task.ChannelID).First(&channel).Error == nil {
		task.ShareGroupID = channel.ShareGroupID
		task.CapacityPolicyID = channel.CapacityPolicyID
	}
	var key apigxmodel.UpstreamKey
	if task.UpstreamKeyID > 0 && db.Unscoped().Where("id = ?", task.UpstreamKeyID).First(&key).Error == nil {
		task.UpstreamKeyCredentialVersion = key.CredentialVersion
		task.UpstreamAccountID = key.UpstreamAccountID
		if key.CapacityPolicyID > 0 {
			task.CapacityPolicyID = key.CapacityPolicyID
		}
		if key.PriceRuleID > 0 {
			task.PriceRuleID = key.PriceRuleID
		}
		_, _ = apigxmodel.ResolveUpstreamKeyCredential(db, key.ID, key.CredentialVersion)
	}
	modelName := strings.TrimSpace(relayInfo.OriginModelName)
	if relayInfo.ChannelMeta != nil && strings.TrimSpace(relayInfo.UpstreamModelName) != "" {
		modelName = strings.TrimSpace(relayInfo.UpstreamModelName)
	}
	if task.OfferingID == 0 && task.ChannelID > 0 && modelName != "" {
		if offeringID, found, err := apigxmodel.FindOfferingByUpstreamModel(db, task.ChannelID, modelName); err == nil && found {
			task.OfferingID = offeringID
		}
	}
	if task.OfferingID > 0 {
		var offering apigxmodel.ModelOffering
		if db.Unscoped().Where("id = ?", task.OfferingID).First(&offering).Error == nil {
			if task.PriceRuleID == 0 {
				task.PriceRuleID = offering.PriceRuleID
			}
			if task.CapacityPolicyID == 0 {
				task.CapacityPolicyID = offering.CapacityPolicyID
			}
		}
	}
	if task.UserID > 0 && task.BaseTokenID > 0 {
		if platformKey, err := apigxmodel.GetAPIKeyByToken(db, task.UserID, task.BaseTokenID); err == nil {
			task.ApiKeyID = platformKey.ID
		}
	}
	if task.CapacityPolicyID > 0 {
		var policy apigxmodel.CapacityPolicy
		if db.Where("id = ?", task.CapacityPolicyID).First(&policy).Error == nil {
			task.PolicyVersion = policy.PolicyVersion
		}
	}
	if task.PriceRuleID > 0 {
		var rule apigxmodel.PriceRule
		if db.Where("id = ?", task.PriceRuleID).First(&rule).Error == nil {
			task.FxRateMicro = rule.FxRateMicro
		}
	}
	task.GrantVersion = resolveAsyncTaskGrantVersion(db, task)
}

func resolveAsyncTaskGrantVersion(db *gorm.DB, task *apigxmodel.AsyncTask) int64 {
	if db == nil || task == nil || task.UserID <= 0 || (task.ShareGroupID <= 0 && task.OfferingID <= 0) {
		return 0
	}
	now := common.GetTimestamp()
	subjectSQL := "(subject_type = ? AND subject_id = ?)"
	subjectArgs := []any{apigxmodel.GrantSubjectUser, task.UserID}
	if task.ApiKeyID > 0 {
		subjectSQL = "(" + subjectSQL + " OR (subject_type = ? AND subject_id = ?))"
		subjectArgs = append(subjectArgs, apigxmodel.GrantSubjectAPIKey, task.ApiKeyID)
	}
	resourceClauses := make([]string, 0, 2)
	resourceArgs := make([]any, 0, 4)
	if task.ShareGroupID > 0 {
		resourceClauses = append(resourceClauses, "(resource_type = ? AND resource_id = ?)")
		resourceArgs = append(resourceArgs, apigxmodel.GrantResourceGroup, task.ShareGroupID)
	}
	if task.OfferingID > 0 {
		resourceClauses = append(resourceClauses, "(resource_type = ? AND resource_id = ?)")
		resourceArgs = append(resourceArgs, apigxmodel.GrantResourceOffering, task.OfferingID)
	}
	var version int64
	query := db.Model(&apigxmodel.Grant{}).
		Select("COALESCE(MAX(gift_version), 0)").
		Where(subjectSQL, subjectArgs...).
		Where("("+strings.Join(resourceClauses, " OR ")+")", resourceArgs...).
		Where("revoked_at = 0 AND (expires_at = 0 OR expires_at > ?)", now)
	if query.Scan(&version).Error != nil {
		return 0
	}
	return version
}

func asyncTaskCapability(relayInfo *relaycommon.RelayInfo, action string) int {
	if relayInfo == nil {
		return 0
	}
	switch relayInfo.RelayMode {
	case relayconstant.RelayModeVideoSubmit, relayconstant.RelayModeMidjourneyVideo:
		return 8
	case relayconstant.RelayModeImagesGenerations, relayconstant.RelayModeMidjourneyImagine:
		return 3
	case relayconstant.RelayModeImagesEdits, relayconstant.RelayModeMidjourneyEdits:
		return 4
	case relayconstant.RelayModeAudioTranscription, relayconstant.RelayModeAudioTranslation:
		return 5
	case relayconstant.RelayModeAudioSpeech:
		return 6
	}
	identity := strings.ToLower(strings.Join([]string{relayInfo.RequestURLPath, action}, " "))
	switch {
	case strings.Contains(identity, "video"):
		return 8
	case strings.Contains(identity, "image"), strings.Contains(identity, "midjourney"), strings.Contains(identity, "/mj"):
		return 3
	case strings.Contains(identity, "audio"), strings.Contains(identity, "music"), strings.Contains(identity, "suno"):
		return 6
	default:
		return 0
	}
}

func asyncStatusFromBase(status string) int {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "SUCCESS":
		return AsyncTaskStatusSucceeded
	case "FAILURE":
		return AsyncTaskStatusFailed
	case "UNKNOWN":
		return AsyncTaskStatusUnknown
	default:
		return AsyncTaskStatusRunning
	}
}

// ResolveAsyncTaskCredential 返回任务提交时绑定的凭据版本。存在 APIGX 快照时任何缺失都失败关闭，
// 调用方不得退回当前渠道 Key；没有快照表示底座历史/非 APIGX 任务，可沿用兼容路径。
func ResolveAsyncTaskCredential(baseTaskID int64, baseChannelID ...int) (*AsyncTaskCredential, bool, error) {
	db := asyncTaskDBProvider()
	if db == nil {
		return nil, false, nil
	}
	task, err := apigxmodel.GetAsyncTaskByBaseTaskID(db, baseTaskID)
	if errors.Is(err, apigxmodel.ErrNotFound) {
		if repaired, found, repairErr := repairAsyncTaskSnapshot(db, baseTaskID); repairErr != nil {
			return nil, true, repairErr
		} else if found {
			task = repaired
			err = nil
		}
	}
	if errors.Is(err, apigxmodel.ErrNotFound) {
		if len(baseChannelID) > 0 && baseChannelID[0] > 0 {
			var count int64
			channelErr := db.Model(&apigxmodel.Channel{}).
				Where("base_channel_id = ?", baseChannelID[0]).Count(&count).Error
			if channelErr != nil {
				if common.GetEnvOrDefaultBool("APIGX_SHARED_MODE", false) {
					return nil, true, channelErr
				}
				return nil, false, nil
			}
			if count > 0 {
				return nil, true, errors.New("apigx: managed async task credential snapshot missing")
			}
		}
		return nil, false, nil
	}
	if err != nil {
		// A regular New API deployment may never initialize APIGX and older tests may
		// leave its optional handle closed. Preserve that compatibility only while
		// shared mode is disabled. In shared mode, an unreadable snapshot store must
		// fail closed because falling back to the channel's current key can cross a
		// credential rotation boundary.
		if !common.GetEnvOrDefaultBool("APIGX_SHARED_MODE", false) {
			return nil, false, nil
		}
		return nil, true, err
	}
	credential, err := apigxmodel.ResolveUpstreamKeyCredential(db, task.UpstreamKeyID, task.UpstreamKeyCredentialVersion)
	if err != nil {
		return nil, true, fmt.Errorf("resolve credential snapshot: %w", err)
	}
	secret, err := secretbox.Decrypt(credential.SecretRef)
	if err != nil {
		return nil, true, fmt.Errorf("decrypt credential snapshot: %w", err)
	}
	return &AsyncTaskCredential{Snapshot: task, Secret: secret}, true, nil
}

// SyncAsyncTaskFromBase 把底座轮询结果单向推进到 APIGX 状态机，并在终态只释放一次租约。
func SyncAsyncTaskFromBase(baseTaskID int64, baseStatus, reason string, startAt, finishAt int64) error {
	db := asyncTaskDBProvider()
	if db == nil || baseTaskID <= 0 {
		return nil
	}
	next := asyncStatusFromBase(baseStatus)
	_, _, err := apigxmodel.AdvanceAsyncTaskStatus(db, baseTaskID, next, sanitizeAsyncReason(reason), startAt, finishAt)
	if errors.Is(err, apigxmodel.ErrNotFound) {
		return nil
	}
	if err != nil && !errors.Is(err, apigxmodel.ErrAsyncTaskTransition) &&
		!common.GetEnvOrDefaultBool("APIGX_SHARED_MODE", false) {
		// APIGX is optional outside shared mode; a stale/closed optional handle
		// must not break the base task polling loop.
		return nil
	}
	if err != nil && !errors.Is(err, apigxmodel.ErrAsyncTaskTransition) {
		return err
	}
	if apigxmodel.IsAsyncTaskTerminal(next) {
		return releaseAsyncTaskReservation(baseTaskID)
	}
	return nil
}

// SyncAsyncTaskCanceled records a cancellation only after the selected adaptor
// confirms the upstream terminal state. It shares the same idempotent release
// path as success/failure callbacks.
func SyncAsyncTaskCanceled(baseTaskID int64, reason string, finishAt int64) error {
	db := asyncTaskDBProvider()
	if db == nil || baseTaskID <= 0 {
		return nil
	}
	_, _, err := apigxmodel.AdvanceAsyncTaskStatus(db, baseTaskID, AsyncTaskStatusCanceled,
		sanitizeAsyncReason(reason), 0, finishAt)
	if errors.Is(err, apigxmodel.ErrNotFound) {
		return nil
	}
	if err != nil && !errors.Is(err, apigxmodel.ErrAsyncTaskTransition) &&
		!common.GetEnvOrDefaultBool("APIGX_SHARED_MODE", false) {
		return nil
	}
	if err != nil && !errors.Is(err, apigxmodel.ErrAsyncTaskTransition) {
		return err
	}
	return releaseAsyncTaskReservation(baseTaskID)
}

func releaseAsyncTaskReservation(baseTaskID int64) error {
	db := asyncTaskDBProvider()
	if db == nil {
		return nil
	}
	task, claimed, err := apigxmodel.ClaimAsyncTaskReservationRelease(db, baseTaskID)
	if err != nil || !claimed || task == nil {
		return err
	}
	if err := releaseAsyncTaskLease(task.ReservationUID); err != nil {
		return err
	}
	attemptState := apigxmodel.AttemptStateSucceeded
	httpStatus := http.StatusOK
	if task.Status != AsyncTaskStatusSucceeded {
		attemptState = apigxmodel.AttemptStateFailed
		httpStatus = http.StatusBadGateway
	}
	if task.Status == AsyncTaskStatusCanceled {
		httpStatus = 499
	}
	if err := apigxmodel.CompleteAttempt(db, task.ReservationUID, attemptState, httpStatus); err != nil {
		common.SysError("relayhook: complete async attempt failed: " + err.Error())
	}
	return apigxmodel.MarkAsyncTaskReservationReleased(db, baseTaskID)
}

// ReconcileAsyncTasks 恢复进程重启后的租约续期，并补做终态释放。超时任务进入对账态，
// 仍由同一轮询链查询真实上游终态，不能因本地超时就假设上游已经停止。
func ReconcileAsyncTasks(ctx context.Context) error {
	db := asyncTaskDBProvider()
	if db == nil {
		return nil
	}
	pending, err := apigxmodel.ListPendingAsyncTaskSnapshotRepairs(db, 500)
	if err != nil {
		return err
	}
	for i := range pending {
		if ctx != nil {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
		}
		if _, _, repairErr := repairAsyncTaskSnapshot(db, pending[i].BaseTaskID); repairErr != nil {
			common.SysError("relayhook: repair async task snapshot failed: " + repairErr.Error())
		}
	}
	now := common.GetTimestamp()
	tasks, err := apigxmodel.ListAsyncTasksForReconciliation(db, now, 500)
	if err != nil {
		return err
	}
	for i := range tasks {
		if ctx != nil {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
		}
		task := &tasks[i]
		if apigxmodel.IsAsyncTaskTerminal(task.Status) || task.ReservationReleaseState == apigxmodel.AsyncTaskReservationReleasing {
			if err := releaseAsyncTaskReservation(task.BaseTaskID); err != nil {
				common.SysError("relayhook: reconcile async task release failed: " + err.Error())
			}
			continue
		}
		if task.TimeoutAt > 0 && task.TimeoutAt <= now && task.Status != AsyncTaskStatusReconciling {
			_, _, _ = apigxmodel.AdvanceAsyncTaskStatus(db, task.BaseTaskID, AsyncTaskStatusReconciling, "timeout_pending_upstream_reconciliation", 0, 0)
		}
		if err := restoreAsyncTaskLease(task); err != nil {
			_, _, _ = apigxmodel.AdvanceAsyncTaskStatus(db, task.BaseTaskID, AsyncTaskStatusReconciling, "capacity_restore_failed", 0, 0)
			common.SysError("relayhook: restore async task lease failed: " + err.Error())
		}
	}
	return nil
}

func HasAsyncTasksForReconciliation() bool {
	db := asyncTaskDBProvider()
	if db == nil {
		return false
	}
	if apigxmodel.HasPendingAsyncTaskSnapshotRepairs(db) {
		return true
	}
	var count int64
	err := db.Model(&apigxmodel.AsyncTask{}).
		Where("reservation_release_state = ? OR status IN ?", apigxmodel.AsyncTaskReservationReleasing,
			[]int{AsyncTaskStatusRunning, AsyncTaskStatusUnknown, AsyncTaskStatusReconciling}).
		Count(&count).Error
	return err == nil && count > 0
}

func repairAsyncTaskSnapshot(db *gorm.DB, baseTaskID int64) (*apigxmodel.AsyncTask, bool, error) {
	pending, found, err := apigxmodel.GetPendingAsyncTaskSnapshotRepair(db, baseTaskID)
	if err != nil || !found {
		return nil, found, err
	}
	stored, _, err := apigxmodel.CreateAsyncTaskSnapshot(db, pending)
	if err != nil {
		return nil, true, err
	}
	if err := apigxmodel.MarkAsyncTaskSnapshotRepairCompleted(db, stored); err != nil {
		return nil, true, err
	}
	if apigxmodel.IsAsyncTaskTerminal(stored.Status) {
		if err := releaseAsyncTaskReservation(stored.BaseTaskID); err != nil {
			return nil, true, err
		}
	}
	return stored, true, nil
}

func sanitizeAsyncReason(reason string) string {
	reason = strings.TrimSpace(reason)
	if len(reason) > 128 {
		reason = reason[:128]
	}
	return reason
}
