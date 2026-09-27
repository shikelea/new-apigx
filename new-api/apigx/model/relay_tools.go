package apigxmodel

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	RelayKindCharity    = "charity"
	RelayKindCommercial = "commercial"

	RelayStatusDraft     = 0
	RelayStatusPublished = 1
	RelayStatusSuspended = 2

	RelayCheckUnknown  = "unknown"
	RelayCheckHealthy  = "healthy"
	RelayCheckDegraded = "degraded"
	RelayCheckFailed   = "failed"

	AuditStatusQueued    = "queued"
	AuditStatusRunning   = "running"
	AuditStatusCompleted = "completed"
	AuditStatusFailed    = "failed"

	AuditCheckPassed  = "passed"
	AuditCheckWarning = "warning"
	AuditCheckFailed  = "failed"
	AuditCheckSkipped = "skipped"
	// AuditCheckInconclusive means the probes ran but produced no usable signal
	// (e.g. every error probe returned 200); it must never be shown as "clean".
	AuditCheckInconclusive = "inconclusive"
)

// RelayShare 是中转站资料与检测摘要。它只保存公开资料和脱敏状态，凭据独立存储。
type RelayShare struct {
	ID              int64          `gorm:"primaryKey"`
	PublicID        string         `gorm:"size:32;not null;default:'';uniqueIndex:idx_apigx_relayshare_pub"`
	OwnerUserID     int64          `gorm:"bigint;not null;default:0;index:idx_apigx_relayshare_owner"`
	Title           string         `gorm:"size:120;not null;default:''"`
	BaseURL         string         `gorm:"size:512;not null;default:''"`
	Kind            string         `gorm:"size:16;not null;default:'';index:idx_apigx_relayshare_kind"`
	Protocol        string         `gorm:"size:32;not null;default:''"`
	Content         string         `gorm:"type:text"`
	Status          int            `gorm:"type:int;not null;default:0;index:idx_apigx_relayshare_status"`
	CheckStatus     string         `gorm:"size:16;not null;default:''"`
	CheckReason     string         `gorm:"size:255;not null;default:''"`
	LastCheckedAt   int64          `gorm:"bigint;not null;default:0"`
	LatencyMs       int64          `gorm:"bigint;not null;default:0"`
	FirstTokenMs    int64          `gorm:"bigint;not null;default:0"`
	ModelCount      int            `gorm:"type:int;not null;default:0"`
	VoteUsable      int64          `gorm:"bigint;not null;default:0"`
	VoteUnavailable int64          `gorm:"bigint;not null;default:0"`
	AliveFlag       int64          `gorm:"bigint;not null;default:0"`
	CreatedAt       int64          `gorm:"bigint;not null;default:0"`
	UpdatedAt       int64          `gorm:"bigint;not null;default:0"`
	DeletedAt       gorm.DeletedAt `gorm:"index:idx_apigx_relayshare_del"`
}

func (RelayShare) TableName() string { return TablePrefix + "relay_shares" }

// RelayCredential 保存中转站检测凭据的密文引用。SecretRef 永不出现在 API 响应。
type RelayCredential struct {
	ID            int64          `gorm:"primaryKey"`
	RelayShareID  int64          `gorm:"bigint;not null;index:idx_apigx_relaycred_share"`
	OwnerUserID   int64          `gorm:"bigint;not null;index:idx_apigx_relaycred_owner"`
	SecretRef     string         `gorm:"type:text;not null"`
	Fingerprint   string         `gorm:"type:char(64);not null;index:idx_apigx_relaycred_fp"`
	KeySuffix     string         `gorm:"size:8;not null;default:''"`
	Enabled       bool           `gorm:"not null"`
	LastCheckedAt int64          `gorm:"bigint;not null;default:0"`
	NextCheckAt   int64          `gorm:"bigint;not null;default:0"`
	FailureCount  int            `gorm:"type:int;not null;default:0"`
	CreatedAt     int64          `gorm:"bigint;not null;default:0"`
	UpdatedAt     int64          `gorm:"bigint;not null;default:0"`
	DeletedAt     gorm.DeletedAt `gorm:"index:idx_apigx_relaycred_del"`
}

func (RelayCredential) TableName() string { return TablePrefix + "relay_credentials" }

// AuditRun 保存一次供应商审计的摘要，不保留原始凭据、prompt 或完整响应。
type AuditRun struct {
	ID              int64          `gorm:"primaryKey"`
	PublicID        string         `gorm:"size:32;not null;default:'';uniqueIndex:idx_apigx_audit_pub"`
	OwnerUserID     int64          `gorm:"bigint;not null;index:idx_apigx_audit_owner"`
	RelayShareID    int64          `gorm:"bigint;not null;default:0;index:idx_apigx_audit_share"`
	Model           string         `gorm:"size:191;not null;default:''"`
	Status          string         `gorm:"size:16;not null;default:'';index:idx_apigx_audit_status"`
	RiskLevel       string         `gorm:"size:16;not null;default:''"`
	SampleCount     int            `gorm:"type:int;not null;default:0"`
	StartedAt       int64          `gorm:"bigint;not null;default:0"`
	FinishedAt      int64          `gorm:"bigint;not null;default:0"`
	EvidenceSummary string         `gorm:"type:text"`
	ErrorMessage    string         `gorm:"size:255;not null;default:''"`
	CreatedAt       int64          `gorm:"bigint;not null;default:0"`
	UpdatedAt       int64          `gorm:"bigint;not null;default:0"`
	DeletedAt       gorm.DeletedAt `gorm:"index:idx_apigx_audit_del"`
}

func (AuditRun) TableName() string { return TablePrefix + "audit_runs" }

type AuditCheck struct {
	ID              int64  `gorm:"primaryKey"`
	AuditRunID      int64  `gorm:"bigint;not null;index:idx_apigx_auditcheck_run"`
	CheckID         string `gorm:"size:32;not null;index:idx_apigx_auditcheck_id"`
	Name            string `gorm:"size:100;not null;default:''"`
	Category        string `gorm:"size:32;not null;default:''"`
	Status          string `gorm:"size:16;not null;default:''"`
	SampleCount     int    `gorm:"type:int;not null;default:0"`
	LatencyMs       int64  `gorm:"bigint;not null;default:0"`
	EvidenceSummary string `gorm:"size:500;not null;default:''"`
	Confidence      string `gorm:"size:16;not null;default:''"`
	CreatedAt       int64  `gorm:"bigint;not null;default:0"`
}

func (AuditCheck) TableName() string { return TablePrefix + "audit_checks" }

// CheckinSite 保存用户授权的平台签到站点；凭据只保留 secretbox 信封。
type CheckinSite struct {
	ID                    int64          `gorm:"primaryKey"`
	OwnerUserID           int64          `gorm:"bigint;not null;index:idx_apigx_checkin_owner"`
	Name                  string         `gorm:"size:100;not null;default:''"`
	BaseURL               string         `gorm:"size:512;not null;default:''"`
	CredentialRef         string         `gorm:"type:text;not null"`
	CredentialFingerprint string         `gorm:"type:char(64);not null;index:idx_apigx_checkin_fp"`
	CredentialKind        string         `gorm:"size:16;not null;default:''"`
	Enabled               bool           `gorm:"not null"`
	Timezone              string         `gorm:"size:64;not null;default:'Asia/Shanghai'"`
	ScheduleMinute        int            `gorm:"type:int;not null;default:540"`
	LastRunAt             int64          `gorm:"bigint;not null;default:0"`
	NextRunAt             int64          `gorm:"bigint;not null;default:0"`
	LastStatus            string         `gorm:"size:16;not null;default:''"`
	LastMessage           string         `gorm:"size:255;not null;default:''"`
	FailureCount          int            `gorm:"type:int;not null;default:0"`
	CreatedAt             int64          `gorm:"bigint;not null;default:0"`
	UpdatedAt             int64          `gorm:"bigint;not null;default:0"`
	DeletedAt             gorm.DeletedAt `gorm:"index:idx_apigx_checkin_del"`
}

func (CheckinSite) TableName() string { return TablePrefix + "checkin_sites" }

// RelayVote keeps one usability vote per user and relay share so counters on
// RelayShare cannot be inflated by repeated requests from the same account.
type RelayVote struct {
	ID           int64 `gorm:"primaryKey"`
	RelayShareID int64 `gorm:"bigint;not null;uniqueIndex:idx_apigx_relayvote_share_user,priority:1"`
	UserID       int64 `gorm:"bigint;not null;uniqueIndex:idx_apigx_relayvote_share_user,priority:2"`
	Usable       bool  `gorm:"not null"`
	CreatedAt    int64 `gorm:"bigint;not null;default:0"`
	UpdatedAt    int64 `gorm:"bigint;not null;default:0"`
}

func (RelayVote) TableName() string { return TablePrefix + "relay_votes" }

func newRelayPublicID() string { return common.GetRandomString(24) }

func ValidateRelayKind(kind string) bool {
	return kind == RelayKindCharity || kind == RelayKindCommercial
}

// NextCheckinRunAt returns the next wall-clock occurrence for a daily check-in.
// The stored timestamp is UTC epoch seconds; ScheduleMinute is local to Timezone.
func NextCheckinRunAt(now int64, timezone string, scheduleMinute int) (int64, error) {
	if scheduleMinute < 0 || scheduleMinute > 1439 {
		return 0, fmt.Errorf("%w: invalid schedule minute", ErrInvalidArgument)
	}
	loc := time.UTC
	if strings.TrimSpace(timezone) != "" {
		loaded, err := time.LoadLocation(timezone)
		if err != nil {
			return 0, fmt.Errorf("%w: invalid timezone", ErrInvalidArgument)
		}
		loc = loaded
	}
	current := time.Unix(now, 0).In(loc)
	target := time.Date(current.Year(), current.Month(), current.Day(), scheduleMinute/60, scheduleMinute%60, 0, 0, loc)
	if !target.After(current) {
		// Rebuild from the calendar date instead of adding 24h so the local
		// wall-clock time stays fixed across DST changes.
		target = time.Date(current.Year(), current.Month(), current.Day()+1, scheduleMinute/60, scheduleMinute%60, 0, 0, loc)
	}
	return target.Unix(), nil
}

func (r *RelayShare) Validate() error {
	r.Title = strings.TrimSpace(r.Title)
	r.BaseURL = strings.TrimSpace(r.BaseURL)
	r.Kind = strings.TrimSpace(r.Kind)
	if r.OwnerUserID <= 0 || r.Title == "" || r.BaseURL == "" {
		return fmt.Errorf("%w: owner, title and base_url are required", ErrInvalidArgument)
	}
	if !ValidateRelayKind(r.Kind) {
		return fmt.Errorf("%w: kind must be charity or commercial", ErrInvalidArgument)
	}
	return nil
}
func CreateRelayShare(db *gorm.DB, share *RelayShare) error {
	if share == nil {
		return fmt.Errorf("%w: nil relay share", ErrInvalidArgument)
	}
	if err := share.Validate(); err != nil {
		return err
	}
	if share.PublicID == "" {
		share.PublicID = newRelayPublicID()
	}
	// Relay shares are user-submitted public resources in the first release.
	// They become visible immediately after the credential is stored; administrators
	// can still suspend a share without exposing drafts through the public API.
	share.Status = RelayStatusPublished
	share.CheckStatus = RelayCheckUnknown
	share.AliveFlag = AliveFlagAlive
	now := common.GetTimestamp()
	share.CreatedAt = now
	share.UpdatedAt = now
	return db.Create(share).Error
}
func GetRelayShareByOwner(db *gorm.DB, ownerID, id int64) (*RelayShare, error) {
	var share RelayShare
	err := db.Where("id = ? AND owner_user_id = ?", id, ownerID).First(&share).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	return &share, err
}
func ListRelayShares(db *gorm.DB, ownerID int64, includeDraft bool, limit int) ([]RelayShare, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	// Public discovery must always be restricted to published, live shares.
	// Owner management is the only path that may include the owner's drafts or
	// suspended records, and it remains scoped by owner_user_id.
	q := db.Where("alive_flag = ?", AliveFlagAlive)
	if ownerID > 0 && includeDraft {
		q = q.Where("owner_user_id = ?", ownerID)
	} else {
		q = q.Where("status = ?", RelayStatusPublished)
	}
	var shares []RelayShare
	return shares, q.Order("updated_at DESC, id DESC").Limit(limit).Find(&shares).Error
}
func relayVoteField(usable bool) string {
	if usable {
		return "vote_usable"
	}
	return "vote_unavailable"
}

// AddRelayVote records one vote per user per relay share. Repeating the same
// vote is a no-op; switching sides moves the count instead of adding another
// vote, so a single account cannot inflate either counter.
func AddRelayVote(db *gorm.DB, id, userID int64, usable bool) error {
	if id <= 0 || userID <= 0 {
		return fmt.Errorf("%w: relay share and user are required", ErrInvalidArgument)
	}
	return db.Transaction(func(tx *gorm.DB) error {
		now := common.GetTimestamp()
		vote := RelayVote{RelayShareID: id, UserID: userID, Usable: usable, CreatedAt: now, UpdatedAt: now}
		created := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&vote)
		if created.Error != nil {
			return created.Error
		}
		if created.RowsAffected == 1 {
			field := relayVoteField(usable)
			return tx.Model(&RelayShare{}).Where("id = ? AND alive_flag = ?", id, AliveFlagAlive).
				UpdateColumn(field, gorm.Expr(field+" + ?", 1)).Error
		}
		// Only the request that actually flips the stored vote may move the
		// counters; the conditional update makes concurrent flips race-safe.
		flipped := tx.Model(&RelayVote{}).Where("relay_share_id = ? AND user_id = ? AND usable = ?", id, userID, !usable).
			Updates(map[string]any{"usable": usable, "updated_at": now})
		if flipped.Error != nil || flipped.RowsAffected == 0 {
			return flipped.Error
		}
		add, sub := relayVoteField(usable), relayVoteField(!usable)
		return tx.Model(&RelayShare{}).Where("id = ? AND alive_flag = ?", id, AliveFlagAlive).UpdateColumns(map[string]any{
			add: gorm.Expr(add+" + ?", 1),
			sub: gorm.Expr("CASE WHEN " + sub + " > 0 THEN " + sub + " - 1 ELSE 0 END"),
		}).Error
	})
}

// GetRelayVote returns the viewer's current vote, or nil when they have not voted.
func GetRelayVote(db *gorm.DB, id, userID int64) (*bool, error) {
	var vote RelayVote
	err := db.Where("relay_share_id = ? AND user_id = ?", id, userID).First(&vote).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &vote.Usable, nil
}

func CreateRelayCredential(db *gorm.DB, credential *RelayCredential) error {
	if credential == nil || credential.OwnerUserID <= 0 || credential.RelayShareID <= 0 || credential.SecretRef == "" || credential.Fingerprint == "" {
		return fmt.Errorf("%w: invalid relay credential", ErrInvalidArgument)
	}
	credential.Enabled = true
	now := common.GetTimestamp()
	credential.CreatedAt = now
	credential.UpdatedAt = now
	return db.Create(credential).Error
}
func GetRelayCredential(db *gorm.DB, ownerID, relayID int64) (*RelayCredential, error) {
	var credential RelayCredential
	err := db.Where("owner_user_id = ? AND relay_share_id = ? AND deleted_at IS NULL AND enabled = ?", ownerID, relayID, true).Order("id DESC").First(&credential).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	return &credential, err
}

func CreateAuditRun(db *gorm.DB, run *AuditRun) error {
	if run == nil || run.OwnerUserID <= 0 || run.Model == "" {
		return fmt.Errorf("%w: owner and model are required", ErrInvalidArgument)
	}
	if run.PublicID == "" {
		run.PublicID = newRelayPublicID()
	}
	if run.Status == "" {
		run.Status = AuditStatusQueued
	}
	now := common.GetTimestamp()
	run.CreatedAt = now
	run.UpdatedAt = now
	return db.Create(run).Error
}
func CreateAuditCheck(db *gorm.DB, check *AuditCheck) error {
	if check == nil || check.AuditRunID <= 0 || check.CheckID == "" {
		return fmt.Errorf("%w: invalid audit check", ErrInvalidArgument)
	}
	if check.CreatedAt == 0 {
		check.CreatedAt = common.GetTimestamp()
	}
	return db.Create(check).Error
}

func CreateCheckinSite(db *gorm.DB, site *CheckinSite) error {
	if site == nil || site.OwnerUserID <= 0 || strings.TrimSpace(site.Name) == "" || strings.TrimSpace(site.BaseURL) == "" || site.CredentialRef == "" || site.CredentialFingerprint == "" {
		return fmt.Errorf("%w: invalid checkin site", ErrInvalidArgument)
	}
	site.Name = strings.TrimSpace(site.Name)
	site.BaseURL = strings.TrimRight(strings.TrimSpace(site.BaseURL), "/")
	if site.Timezone == "" {
		site.Timezone = "Asia/Shanghai"
	}
	site.Enabled = true
	now := common.GetTimestamp()
	site.CreatedAt = now
	site.UpdatedAt = now
	return db.Create(site).Error
}
func ListCheckinSites(db *gorm.DB, ownerID int64, limit int) ([]CheckinSite, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var sites []CheckinSite
	return sites, db.Where("owner_user_id = ? AND deleted_at IS NULL", ownerID).Order("updated_at DESC, id DESC").Limit(limit).Find(&sites).Error
}
func GetCheckinSite(db *gorm.DB, ownerID, id int64) (*CheckinSite, error) {
	var site CheckinSite
	err := db.Where("id = ? AND owner_user_id = ? AND deleted_at IS NULL", id, ownerID).First(&site).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	return &site, err
}

func ListDueCheckinSites(db *gorm.DB, now int64, limit int) ([]CheckinSite, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	var sites []CheckinSite
	err := db.Where("enabled = ? AND deleted_at IS NULL AND (next_run_at = 0 OR next_run_at <= ?)", true, now).
		Order("next_run_at ASC, id ASC").Limit(limit).Find(&sites).Error
	return sites, err
}

func UpdateCheckinSiteRun(db *gorm.DB, id int64, now, next int64, status, message string, failureCount int) error {
	return db.Model(&CheckinSite{}).Where("id = ? AND deleted_at IS NULL", id).Updates(map[string]any{
		"last_run_at": now, "next_run_at": next, "last_status": status,
		"last_message": message, "failure_count": failureCount, "updated_at": now,
	}).Error
}
