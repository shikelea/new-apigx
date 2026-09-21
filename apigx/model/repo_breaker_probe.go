package apigxmodel

import (
	"errors"
	"fmt"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	BreakerProbeStateOpen        = 1
	BreakerProbeStateRecovering  = 2
	BreakerProbeStateAuthBlocked = 3
	BreakerProbeStateClosed      = 4

	breakerProbeInitialBackoffSeconds = int64(60)
	breakerProbeMaximumBackoffSeconds = int64(30 * 60)
	breakerProbeSuccessDelaySeconds   = int64(15)
	breakerProbeSuccessThreshold      = 2
)

// BreakerProbeModality resolves the administrator-confirmed protocol for an
// offering. Missing catalog identity stays unknown: guessing from a vendor
// model id can send a chat payload to an image, audio, or video endpoint.
func BreakerProbeModality(db *gorm.DB, offeringID int64) (int, error) {
	if db == nil || offeringID <= 0 {
		return 0, fmt.Errorf("%w: breaker probe offering is required", ErrInvalidArgument)
	}
	var row struct {
		ModalityType int
	}
	err := db.Table((ModelOffering{}).TableName()+" AS offering").
		Select("canonical.modality_type AS modality_type").
		Joins("JOIN "+(CanonicalModel{}).TableName()+" AS canonical ON canonical.id = offering.canonical_model_id").
		Where("offering.id = ? AND offering.alive_flag = ?", offeringID, AliveFlagAlive).
		Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, nil
	}
	return row.ModalityType, err
}

// BreakerProbeSupportsModality reports whether the existing channel-test path
// can issue a bounded synchronous probe for the protocol. Audio requires a
// real upload or returns binary media, while video and plugin protocols create
// asynchronous billable jobs; those stay on request-level failover until a
// protocol-specific health probe exists.
func BreakerProbeSupportsModality(modality int) bool {
	switch modality {
	case ModalityChat, ModalityEmbedding, ModalityImageGeneration, ModalityRerank:
		return true
	default:
		return false
	}
}

func CanActivelyProbeOffering(db *gorm.DB, offeringID int64) (bool, error) {
	modality, err := BreakerProbeModality(db, offeringID)
	return BreakerProbeSupportsModality(modality), err
}

// ReleaseUnprobeableBreaker moves a legacy/open durable state to recovery so
// deployments upgraded from an earlier build cannot leave an audio, video, or
// unknown-protocol channel permanently removed from routing.
func ReleaseUnprobeableBreaker(db *gorm.DB, id, now int64) (bool, error) {
	if db == nil || id <= 0 {
		return false, fmt.Errorf("%w: breaker probe id is required", ErrInvalidArgument)
	}
	if now <= 0 {
		now = common.GetTimestamp()
	}
	result := db.Model(&BreakerProbeState{}).
		Where("id = ? AND state = ?", id, BreakerProbeStateOpen).
		Updates(map[string]any{
			"state": BreakerProbeStateRecovering, "next_probe_at": now,
			"recovery_baseline_ts": alignToMinute(now) + 60,
			"last_error_code":      "active_probe_unsupported", "updated_at": now,
		})
	return result.RowsAffected > 0, result.Error
}

// OpenBreakerProbe records ownership of a breaker disable before the base
// channel is removed from routing. Repeated open decisions do not postpone an
// already scheduled probe. An auth-blocked state is only rearmed by a
// credential change.
func OpenBreakerProbe(db *gorm.DB, offeringID, baseChannelID, now, cooldownSeconds int64) error {
	if db == nil || offeringID <= 0 || baseChannelID <= 0 {
		return fmt.Errorf("%w: complete breaker probe identity is required", ErrInvalidArgument)
	}
	if now <= 0 {
		now = common.GetTimestamp()
	}
	if cooldownSeconds <= 0 {
		cooldownSeconds = breakerProbeInitialBackoffSeconds
	}
	return db.Transaction(func(tx *gorm.DB) error {
		var state BreakerProbeState
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("offering_id = ?", offeringID).First(&state).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return tx.Create(&BreakerProbeState{
				OfferingID: offeringID, BaseChannelID: baseChannelID,
				State: BreakerProbeStateOpen, BackoffSeconds: breakerProbeInitialBackoffSeconds,
				OpenedAt: now, NextProbeAt: now + cooldownSeconds, OwnsChannelDisable: true,
				CreatedAt: now, UpdatedAt: now,
			}).Error
		}
		if err != nil {
			return err
		}
		if state.State != BreakerProbeStateClosed {
			return nil
		}
		return tx.Model(&BreakerProbeState{}).Where("id = ?", state.ID).Updates(map[string]any{
			"base_channel_id":      baseChannelID,
			"state":                BreakerProbeStateOpen,
			"consecutive_success":  0,
			"backoff_seconds":      breakerProbeInitialBackoffSeconds,
			"opened_at":            now,
			"next_probe_at":        now + cooldownSeconds,
			"last_probe_at":        0,
			"recovery_baseline_ts": 0,
			"last_error_code":      "",
			"owns_channel_disable": true,
			"updated_at":           now,
		}).Error
	})
}

func HasDueBreakerProbes(db *gorm.DB, now int64) bool {
	if db == nil {
		return false
	}
	if now <= 0 {
		now = common.GetTimestamp()
	}
	var count int64
	err := db.Model(&BreakerProbeState{}).
		Where("state IN ? AND next_probe_at > 0 AND next_probe_at <= ?",
			[]int{BreakerProbeStateOpen, BreakerProbeStateRecovering}, now).
		Limit(1).Count(&count).Error
	return err == nil && count > 0
}

func ListDueBreakerProbes(db *gorm.DB, now int64, limit int) ([]BreakerProbeState, error) {
	if db == nil {
		return nil, gorm.ErrInvalidDB
	}
	if now <= 0 {
		now = common.GetTimestamp()
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var states []BreakerProbeState
	err := db.Where("state IN ? AND next_probe_at > 0 AND next_probe_at <= ?",
		[]int{BreakerProbeStateOpen, BreakerProbeStateRecovering}, now).
		Order("next_probe_at ASC, id ASC").Limit(limit).Find(&states).Error
	return states, err
}

// RecordBreakerProbeResult advances the persisted state machine. ready is true
// after the second consecutive success; the caller must then restore the base
// channel and finalize the state.
func RecordBreakerProbeResult(db *gorm.DB, id, now int64, success, authFailure bool, errorCode string) (ready bool, err error) {
	if db == nil || id <= 0 {
		return false, fmt.Errorf("%w: breaker probe id is required", ErrInvalidArgument)
	}
	if now <= 0 {
		now = common.GetTimestamp()
	}
	err = db.Transaction(func(tx *gorm.DB) error {
		var state BreakerProbeState
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", id).First(&state).Error; err != nil {
			return err
		}
		if state.State != BreakerProbeStateOpen {
			return nil
		}
		updates := map[string]any{
			"last_probe_at": now,
			"updated_at":    now,
		}
		if authFailure {
			updates["state"] = BreakerProbeStateAuthBlocked
			updates["consecutive_success"] = 0
			updates["next_probe_at"] = 0
			updates["last_error_code"] = errorCode
			return tx.Model(&BreakerProbeState{}).Where("id = ?", id).Updates(updates).Error
		}
		if success {
			streak := state.ConsecutiveSuccess + 1
			updates["consecutive_success"] = streak
			updates["backoff_seconds"] = breakerProbeInitialBackoffSeconds
			updates["last_error_code"] = ""
			if streak >= breakerProbeSuccessThreshold {
				updates["state"] = BreakerProbeStateRecovering
				updates["next_probe_at"] = now
				// Minute buckets cannot separate samples inside one minute. Start the
				// recovered reliability baseline at the next full minute so the old
				// failure bucket cannot immediately reopen the breaker.
				updates["recovery_baseline_ts"] = alignToMinute(now) + 60
				ready = true
			} else {
				updates["next_probe_at"] = now + breakerProbeSuccessDelaySeconds
			}
			return tx.Model(&BreakerProbeState{}).Where("id = ?", id).Updates(updates).Error
		}

		delay := state.BackoffSeconds
		if delay < breakerProbeInitialBackoffSeconds {
			delay = breakerProbeInitialBackoffSeconds
		}
		nextBackoff := delay * 2
		if nextBackoff > breakerProbeMaximumBackoffSeconds {
			nextBackoff = breakerProbeMaximumBackoffSeconds
		}
		updates["consecutive_success"] = 0
		updates["backoff_seconds"] = nextBackoff
		updates["next_probe_at"] = now + delay
		updates["last_error_code"] = errorCode
		return tx.Model(&BreakerProbeState{}).Where("id = ?", id).Updates(updates).Error
	})
	return ready, err
}

func FinalizeBreakerRecovery(db *gorm.DB, id, now int64) error {
	if db == nil || id <= 0 {
		return fmt.Errorf("%w: breaker probe id is required", ErrInvalidArgument)
	}
	if now <= 0 {
		now = common.GetTimestamp()
	}
	return db.Model(&BreakerProbeState{}).
		Where("id = ? AND state = ?", id, BreakerProbeStateRecovering).
		Updates(map[string]any{
			"state": BreakerProbeStateClosed, "next_probe_at": 0,
			"owns_channel_disable": false, "updated_at": now,
		}).Error
}

func DeferBreakerRecovery(db *gorm.DB, id, now int64, errorCode string) error {
	if db == nil || id <= 0 {
		return fmt.Errorf("%w: breaker probe id is required", ErrInvalidArgument)
	}
	if now <= 0 {
		now = common.GetTimestamp()
	}
	return db.Model(&BreakerProbeState{}).
		Where("id = ? AND state = ?", id, BreakerProbeStateRecovering).
		Updates(map[string]any{
			"next_probe_at":   now + breakerProbeSuccessDelaySeconds,
			"last_error_code": errorCode, "updated_at": now,
		}).Error
}

func BreakerRecoveryBaseline(db *gorm.DB, offeringID int64) (int64, error) {
	if db == nil || offeringID <= 0 {
		return 0, nil
	}
	var state BreakerProbeState
	err := db.Select("recovery_baseline_ts").Where("offering_id = ?", offeringID).First(&state).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, nil
	}
	return state.RecoveryBaselineTs, err
}

func BreakerProbeBlocksTraffic(db *gorm.DB, offeringID int64) (bool, error) {
	if db == nil || offeringID <= 0 {
		return false, nil
	}
	var count int64
	err := db.Model(&BreakerProbeState{}).
		Where("offering_id = ? AND state IN ?", offeringID,
			[]int{BreakerProbeStateOpen, BreakerProbeStateRecovering, BreakerProbeStateAuthBlocked}).
		Count(&count).Error
	return count > 0, err
}

// BreakerChannelMayRestore is true only when no other offering on the same
// base channel still needs probes or credential repair.
func BreakerChannelMayRestore(db *gorm.DB, baseChannelID int64) (bool, error) {
	if db == nil || baseChannelID <= 0 {
		return false, nil
	}
	var count int64
	err := db.Model(&BreakerProbeState{}).
		Where("base_channel_id = ? AND state IN ?", baseChannelID,
			[]int{BreakerProbeStateOpen, BreakerProbeStateAuthBlocked}).Count(&count).Error
	return count == 0, err
}

// RearmAuthBlockedBreakerProbesForChannel resumes probes only after the owner
// changes the credential pool. Ordinary scheduler passes never retry 401/403.
func RearmAuthBlockedBreakerProbesForChannel(db *gorm.DB, shareChannelID, now int64) error {
	if db == nil || shareChannelID <= 0 {
		return nil
	}
	if now <= 0 {
		now = common.GetTimestamp()
	}
	var channel Channel
	if err := db.Select("base_channel_id").Where("id = ?", shareChannelID).First(&channel).Error; err != nil {
		return err
	}
	return db.Model(&BreakerProbeState{}).
		Where("base_channel_id = ? AND state = ?", channel.BaseChannelID, BreakerProbeStateAuthBlocked).
		Updates(map[string]any{
			"state": BreakerProbeStateOpen, "consecutive_success": 0,
			"backoff_seconds": breakerProbeInitialBackoffSeconds,
			"next_probe_at":   now, "last_error_code": "", "updated_at": now,
		}).Error
}
