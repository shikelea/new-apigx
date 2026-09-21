package controller

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	apigxmodel "github.com/QuantumNous/new-api/apigx/model"
	"github.com/QuantumNous/new-api/apigx/qualityhook"
	"github.com/QuantumNous/new-api/apigx/relayhook"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"gorm.io/gorm"
)

// RegisterScheduledSystemTasks wires the periodic channel test, upstream model
// update, and async task polling (Midjourney / Suno / video) jobs into the
// system task framework so a DB lease dedups execution across multiple master
// instances and each run is recorded as one task row. Call this before
// service.StartSystemTaskRunner.
func RegisterScheduledSystemTasks() {
	service.RegisterSystemTaskHandler(channelTestHandler{})
	service.RegisterSystemTaskHandler(modelUpdateHandler{})
	service.RegisterSystemTaskHandler(midjourneyPollHandler{})
	service.RegisterSystemTaskHandler(asyncTaskPollHandler{})
	service.RegisterSystemTaskHandler(breakerProbeHandler{})
}

// channelTestHandler runs the scheduled "test all channels" job. Enablement and
// cadence still come from the monitor settings; only the execution path moved
// into the system task runner.
type channelTestHandler struct{}

func (channelTestHandler) Type() string { return model.SystemTaskTypeChannelTest }

func (channelTestHandler) Enabled() bool {
	return operation_setting.GetMonitorSetting().AutoTestChannelEnabled
}

func (channelTestHandler) Interval() time.Duration {
	minutes := operation_setting.GetMonitorSetting().AutoTestChannelMinutes
	if minutes <= 0 {
		minutes = 10
	}
	return time.Duration(minutes * float64(time.Minute))
}

func (channelTestHandler) NewPayload() any { return nil }

// channelTestTaskPayload controls one channel_test run. A nil/empty payload is a
// scheduled run, which uses the configured monitor ChannelTestMode and does not
// notify. A manual "test all channels" trigger sets Mode=scheduled_all and
// Notify=true to reproduce the legacy manual behavior (test every channel and
// notify root on completion).
type channelTestTaskPayload struct {
	Mode   string `json:"mode,omitempty"`
	Notify bool   `json:"notify,omitempty"`
}

func (channelTestHandler) Run(ctx context.Context, task *model.SystemTask, runnerID string) {
	payload := channelTestTaskPayload{}
	if err := task.DecodePayload(&payload); err != nil {
		finishSystemTaskHandler(task, runnerID, model.SystemTaskStatusFailed, nil, err)
		return
	}
	summary, err := runChannelTestTask(ctx, payload.Mode, payload.Notify, service.NewSystemTaskProgressReporter(task, runnerID))
	if err != nil {
		finishSystemTaskHandler(task, runnerID, model.SystemTaskStatusFailed, nil, err)
		return
	}
	finishSystemTaskHandler(task, runnerID, model.SystemTaskStatusSucceeded, summary, nil)
}

// modelUpdateHandler runs the scheduled upstream model update detection job.
type modelUpdateHandler struct{}

func (modelUpdateHandler) Type() string { return model.SystemTaskTypeModelUpdate }

func (modelUpdateHandler) Enabled() bool {
	return common.GetEnvOrDefaultBool("CHANNEL_UPSTREAM_MODEL_UPDATE_TASK_ENABLED", true)
}

func (modelUpdateHandler) Interval() time.Duration {
	intervalMinutes := common.GetEnvOrDefault(
		"CHANNEL_UPSTREAM_MODEL_UPDATE_TASK_INTERVAL_MINUTES",
		channelUpstreamModelUpdateTaskDefaultIntervalMinutes,
	)
	if intervalMinutes < 1 {
		intervalMinutes = channelUpstreamModelUpdateTaskDefaultIntervalMinutes
	}
	return time.Duration(intervalMinutes) * time.Minute
}

func (modelUpdateHandler) NewPayload() any { return nil }

// modelUpdateTaskPayload controls one model_update run. A scheduled run
// (Manual=false) respects the per-channel minimum check interval and may
// auto-apply detected models when a channel has auto-sync enabled. A manual
// "detect all" trigger sets Manual=true to reproduce the legacy detect-all
// semantics: force a re-check regardless of the interval and never auto-apply,
// so the admin reviews and applies changes explicitly.
type modelUpdateTaskPayload struct {
	Manual bool `json:"manual,omitempty"`
}

func (modelUpdateHandler) Run(ctx context.Context, task *model.SystemTask, runnerID string) {
	payload := modelUpdateTaskPayload{}
	if err := task.DecodePayload(&payload); err != nil {
		finishSystemTaskHandler(task, runnerID, model.SystemTaskStatusFailed, nil, err)
		return
	}
	summary := runChannelUpstreamModelUpdateTaskOnce(ctx, payload.Manual, !payload.Manual, service.NewSystemTaskProgressReporter(task, runnerID))
	finishSystemTaskHandler(task, runnerID, model.SystemTaskStatusSucceeded, summary, nil)
}

// midjourneyPollHandler runs one Midjourney polling pass per scheduled run.
// Enabled() folds the "are there unfinished tasks?" check into enablement so the
// scheduler creates no row when the system is idle; only when at least one
// Midjourney task is in progress does a row get scheduled.
type midjourneyPollHandler struct{}

func (midjourneyPollHandler) Type() string { return model.SystemTaskTypeMidjourneyPoll }

func (midjourneyPollHandler) Enabled() bool {
	return constant.UpdateTask && model.HasUnfinishedMidjourneyTasks()
}

func (midjourneyPollHandler) Interval() time.Duration { return 15 * time.Second }

func (midjourneyPollHandler) NewPayload() any { return nil }

func (midjourneyPollHandler) Run(ctx context.Context, task *model.SystemTask, runnerID string) {
	summary := runMidjourneyTaskUpdateOnce(ctx, service.NewSystemTaskProgressReporter(task, runnerID))
	finishSystemTaskHandler(task, runnerID, model.SystemTaskStatusSucceeded, summary, nil)
}

// asyncTaskPollHandler runs one async-task (Suno/video) polling pass per
// scheduled run. Like midjourneyPollHandler, Enabled() folds in the unfinished
// task existence check so an idle system schedules no rows.
type asyncTaskPollHandler struct{}

func (asyncTaskPollHandler) Type() string { return model.SystemTaskTypeAsyncTaskPoll }

func (asyncTaskPollHandler) Enabled() bool {
	return constant.UpdateTask && (model.HasUnfinishedSyncTasks() || relayhook.HasAsyncTasksForReconciliation())
}

func (asyncTaskPollHandler) Interval() time.Duration { return 15 * time.Second }

func (asyncTaskPollHandler) NewPayload() any { return nil }

func (asyncTaskPollHandler) Run(ctx context.Context, task *model.SystemTask, runnerID string) {
	if err := relayhook.ReconcileAsyncTasks(ctx); err != nil {
		common.SysLog(fmt.Sprintf("async task reconciliation failed: %v", err))
	}
	summary := service.RunTaskPollingOnce(ctx, service.NewSystemTaskProgressReporter(task, runnerID))
	finishSystemTaskHandler(task, runnerID, model.SystemTaskStatusSucceeded, summary, nil)
}

type breakerProbeHandler struct{}

func (breakerProbeHandler) Type() string { return model.SystemTaskTypeBreakerProbe }

func (breakerProbeHandler) Enabled() bool {
	db := apigxmodel.DefaultDB()
	return relayhook.BreakerEnabled() && apigxmodel.HasDueBreakerProbes(db, common.GetTimestamp())
}

func (breakerProbeHandler) Interval() time.Duration { return 15 * time.Second }

func (breakerProbeHandler) NewPayload() any { return nil }

type breakerProbeSummary struct {
	Due         int `json:"due"`
	Probed      int `json:"probed"`
	Succeeded   int `json:"succeeded"`
	Failed      int `json:"failed"`
	AuthBlocked int `json:"auth_blocked"`
	Unsupported int `json:"unsupported"`
	Recovered   int `json:"recovered"`
}

func (breakerProbeHandler) Run(ctx context.Context, task *model.SystemTask, runnerID string) {
	summary, err := runBreakerProbeTask(ctx)
	if err != nil {
		finishSystemTaskHandler(task, runnerID, model.SystemTaskStatusFailed, summary, err)
		return
	}
	finishSystemTaskHandler(task, runnerID, model.SystemTaskStatusSucceeded, summary, nil)
}

func runBreakerProbeTask(ctx context.Context) (breakerProbeSummary, error) {
	db := apigxmodel.DefaultDB()
	if db == nil {
		return breakerProbeSummary{}, fmt.Errorf("APIGX database is not initialized")
	}
	now := common.GetTimestamp()
	states, err := apigxmodel.ListDueBreakerProbes(db, now, 50)
	if err != nil {
		return breakerProbeSummary{}, err
	}
	summary := breakerProbeSummary{Due: len(states)}
	if len(states) == 0 {
		return summary, nil
	}
	testUserID, err := resolveChannelTestUserID(nil)
	if err != nil {
		return summary, err
	}

	for i := range states {
		if ctx != nil && ctx.Err() != nil {
			return summary, ctx.Err()
		}
		state := states[i]
		if state.State == apigxmodel.BreakerProbeStateRecovering {
			if err := restoreAndFinalizeBreakerProbe(db, &state, now); err != nil {
				_ = apigxmodel.DeferBreakerRecovery(db, state.ID, now, "restore_failed")
				summary.Failed++
				continue
			}
			summary.Recovered++
			continue
		}

		var offering apigxmodel.ModelOffering
		if err := db.Where("id = ? AND alive_flag = ?", state.OfferingID, apigxmodel.AliveFlagAlive).First(&offering).Error; err != nil {
			_, _ = apigxmodel.RecordBreakerProbeResult(db, state.ID, now, false, false, "offering_missing")
			summary.Failed++
			continue
		}
		endpointType, supported, resolveErr := breakerProbeEndpoint(db, offering.ID)
		if resolveErr != nil {
			_, _ = apigxmodel.RecordBreakerProbeResult(db, state.ID, now, false, false, "probe_protocol_error")
			summary.Failed++
			continue
		}
		if !supported {
			released, releaseErr := apigxmodel.ReleaseUnprobeableBreaker(db, state.ID, now)
			if releaseErr != nil {
				summary.Failed++
				continue
			}
			summary.Unsupported++
			if released {
				if err := db.Where("id = ?", state.ID).First(&state).Error; err != nil {
					return summary, err
				}
				if err := restoreAndFinalizeBreakerProbe(db, &state, now); err != nil {
					_ = apigxmodel.DeferBreakerRecovery(db, state.ID, now, "restore_failed")
					summary.Failed++
					continue
				}
				summary.Recovered++
			}
			continue
		}
		channel, err := model.GetChannelById(int(state.BaseChannelID), true)
		if err != nil || channel == nil {
			_, _ = apigxmodel.RecordBreakerProbeResult(db, state.ID, now, false, false, "channel_missing")
			summary.Failed++
			continue
		}
		probeChannel := relayhook.PrepareBreakerProbeChannel(channel)
		result := testChannelWithOptions(ctx, probeChannel, testUserID, offering.UpstreamModelID, endpointType, false,
			channelTestOptions{requirePricing: false, recordConsumeLog: false})
		summary.Probed++

		outcome := qualityhook.ClassifyOutcome(result.newAPIError, result.upstreamStatus)
		if recordErr := apigxmodel.RecordQualitySample(db, apigxmodel.QualitySample{
			OfferingID: offering.ID, Outcome: outcome, IsProbe: true,
		}); recordErr != nil {
			common.SysError("apigx breaker probe: record probe sample failed: " + recordErr.Error())
		}
		success := result.localErr == nil && result.newAPIError == nil
		authFailure := result.upstreamStatus == http.StatusUnauthorized || result.upstreamStatus == http.StatusForbidden
		errorCode := breakerProbeErrorCode(result)
		ready, recordErr := apigxmodel.RecordBreakerProbeResult(db, state.ID, now, success, authFailure, errorCode)
		if recordErr != nil {
			return summary, recordErr
		}
		if authFailure {
			summary.AuthBlocked++
			continue
		}
		if !success {
			summary.Failed++
			continue
		}
		summary.Succeeded++
		if ready {
			if err := db.Where("id = ?", state.ID).First(&state).Error; err != nil {
				return summary, err
			}
			if err := restoreAndFinalizeBreakerProbe(db, &state, now); err != nil {
				_ = apigxmodel.DeferBreakerRecovery(db, state.ID, now, "restore_failed")
				summary.Failed++
				continue
			}
			summary.Recovered++
		}
	}
	return summary, nil
}

func breakerProbeEndpoint(db *gorm.DB, offeringID int64) (string, bool, error) {
	modality, err := apigxmodel.BreakerProbeModality(db, offeringID)
	if err != nil {
		return "", false, err
	}
	switch modality {
	case apigxmodel.ModalityChat:
		return "", true, nil
	case apigxmodel.ModalityEmbedding:
		return string(constant.EndpointTypeEmbeddings), true, nil
	case apigxmodel.ModalityImageGeneration:
		return string(constant.EndpointTypeImageGeneration), true, nil
	case apigxmodel.ModalityRerank:
		return string(constant.EndpointTypeJinaRerank), true, nil
	default:
		return "", false, nil
	}
}

func restoreAndFinalizeBreakerProbe(db *gorm.DB, state *apigxmodel.BreakerProbeState, now int64) error {
	if state == nil {
		return fmt.Errorf("breaker probe state is required")
	}
	if err := relayhook.RestoreBreakerChannel(db, *state); err != nil {
		return err
	}
	return apigxmodel.FinalizeBreakerRecovery(db, state.ID, now)
}

func breakerProbeErrorCode(result testResult) string {
	if result.upstreamStatus > 0 {
		return fmt.Sprintf("http_%d", result.upstreamStatus)
	}
	if result.newAPIError != nil {
		code := string(result.newAPIError.GetErrorCode())
		if len(code) > 64 {
			code = code[:64]
		}
		return code
	}
	if result.localErr != nil {
		code := strings.TrimSpace(result.localErr.Error())
		if len(code) > 64 {
			code = code[:64]
		}
		return code
	}
	return ""
}

func finishSystemTaskHandler(task *model.SystemTask, runnerID string, status model.SystemTaskStatus, result any, runErr error) {
	errorMessage := ""
	if runErr != nil {
		errorMessage = runErr.Error()
	}
	if err := model.FinishSystemTask(task.TaskID, runnerID, status, result, errorMessage); err != nil {
		common.SysLog(fmt.Sprintf("system task %s failed to persist result: %v", task.TaskID, err))
	}
}
