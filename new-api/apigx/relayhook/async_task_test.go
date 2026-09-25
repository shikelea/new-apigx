package relayhook

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/apigx/capacity"
	apigxmodel "github.com/QuantumNous/new-api/apigx/model"
	"github.com/QuantumNous/new-api/apigx/secretbox"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func withAsyncTaskDB(t *testing.T, db *gorm.DB) {
	t.Helper()
	previous := asyncTaskDBProvider
	asyncTaskDBProvider = func() *gorm.DB { return db }
	t.Cleanup(func() { asyncTaskDBProvider = previous })
}

func TestResolveAsyncTaskCredentialOptionalDatabaseCompatibility(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "closed.db")), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())
	withAsyncTaskDB(t, db)

	t.Run("ordinary mode treats unavailable optional store as unmanaged", func(t *testing.T) {
		t.Setenv("APIGX_SHARED_MODE", "false")
		credential, managed, resolveErr := ResolveAsyncTaskCredential(101)
		assert.NoError(t, resolveErr)
		assert.False(t, managed)
		assert.Nil(t, credential)
	})

	t.Run("shared mode fails closed", func(t *testing.T) {
		t.Setenv("APIGX_SHARED_MODE", "true")
		credential, managed, resolveErr := ResolveAsyncTaskCredential(101)
		assert.Error(t, resolveErr)
		assert.True(t, managed)
		assert.Nil(t, credential)
	})
}

func TestResolveAsyncTaskCredentialNeverFallsBackAfterSnapshotExists(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "snapshot.db")), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, db.AutoMigrate(&apigxmodel.AsyncTask{}, &apigxmodel.UpstreamKey{}, &apigxmodel.UpstreamKeyCredential{}, &apigxmodel.BreakerProbeState{}))
	require.NoError(t, db.Create(&apigxmodel.AsyncTask{
		PublicTaskID: "apigx_202", BaseTaskID: 202,
		UpstreamKeyID: 303, UpstreamKeyCredentialVersion: 4,
	}).Error)
	withAsyncTaskDB(t, db)
	t.Setenv("APIGX_SHARED_MODE", "false")

	credential, managed, resolveErr := ResolveAsyncTaskCredential(202)
	assert.Error(t, resolveErr)
	assert.True(t, managed, "an existing snapshot must never fall back to the channel's current key")
	assert.Nil(t, credential)
}

func TestResolveAsyncTaskCredentialFailsClosedForManagedChannelWithoutSnapshot(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "managed.db")), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, db.AutoMigrate(&apigxmodel.AsyncTask{}, &apigxmodel.Channel{}, &apigxmodel.AuditEvent{}))
	require.NoError(t, db.Create(&apigxmodel.Channel{BaseChannelID: 404, PublicID: "managed-404"}).Error)
	withAsyncTaskDB(t, db)
	t.Setenv("APIGX_SHARED_MODE", "false")

	credential, managed, resolveErr := ResolveAsyncTaskCredential(999, 404)
	assert.Error(t, resolveErr)
	assert.True(t, managed)
	assert.Nil(t, credential)

	credential, managed, resolveErr = ResolveAsyncTaskCredential(999, 405)
	assert.NoError(t, resolveErr)
	assert.False(t, managed)
	assert.Nil(t, credential)
}

func TestAsyncTaskCapabilityUsesProtocolEvidence(t *testing.T) {
	assert.Equal(t, 8, asyncTaskCapability(&relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeVideoSubmit}, ""))
	assert.Equal(t, 4, asyncTaskCapability(&relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeImagesEdits}, ""))
	assert.Equal(t, 6, asyncTaskCapability(&relaycommon.RelayInfo{RequestURLPath: "/suno/submit/music"}, "generate"))
	assert.Equal(t, 0, asyncTaskCapability(&relaycommon.RelayInfo{RequestURLPath: "/v1/tasks/custom"}, "generate"),
		"unknown plugin capabilities must remain unknown rather than being mislabeled as video")
}

func TestFillAsyncTaskAttributionFreezesModelIdentity(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "model-attribution.db")), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, db.AutoMigrate(apigxmodel.AllModels()...))

	canonical := &apigxmodel.CanonicalModel{APIModelID: "canonical-video", ModalityType: 7, Status: 1}
	require.NoError(t, db.Create(canonical).Error)
	offering := &apigxmodel.ModelOffering{
		PublicID: "offer-model-snapshot", CanonicalModelID: canonical.ID,
		UpstreamModelID: "vendor-video", OfferingKeyHash: "model-snapshot", Status: 2,
	}
	require.NoError(t, db.Create(offering).Error)
	task := &apigxmodel.AsyncTask{OfferingID: offering.ID}

	fillAsyncTaskAttribution(db, &relaycommon.RelayInfo{OriginModelName: "client-video-alias"}, task)

	assert.EqualValues(t, canonical.ID, task.CanonicalModelID)
	assert.Equal(t, "client-video-alias", task.ApiModelID)

	task.ApiModelID = ""
	fillAsyncTaskAttribution(db, &relaycommon.RelayInfo{}, task)
	assert.Equal(t, canonical.APIModelID, task.ApiModelID)
}

func TestRecordAsyncTaskSnapshotQueuesRepairAndKeepsLease(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "record-repair.db")), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, db.AutoMigrate(apigxmodel.AllModels()...))
	withAsyncTaskDB(t, db)

	channel := &apigxmodel.Channel{PublicID: "repair-channel", OwnerUserID: 91, BaseChannelID: 9191}
	require.NoError(t, db.Create(channel).Error)
	const leaseID = "snapshot-repair-lease"
	lease := &Lease{id: leaseID, stop: make(chan struct{})}
	c, _ := gin.CreateTestContext(nil)
	common.SetContextKey(c, constant.ContextKeyChannelId, int(channel.BaseChannelID))
	c.Set(leaseContextKey, lease)

	callbackName := "test:fail_async_snapshot_only"
	require.NoError(t, db.Callback().Create().Before("gorm:create").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Table == (apigxmodel.AsyncTask{}).TableName() {
			tx.AddError(errors.New("injected async snapshot failure"))
		}
	}))
	RecordAsyncTaskSnapshot(c, &relaycommon.RelayInfo{
		UserId: 77, TokenId: 88,
		ChannelMeta: &relaycommon.ChannelMeta{ChannelId: int(channel.BaseChannelID)},
	}, 76001, "upstream-76001", "generate", "IN_PROGRESS", 0, 0)
	require.NoError(t, db.Callback().Create().Remove(callbackName))

	_, err = apigxmodel.GetAsyncTaskByBaseTaskID(db, 76001)
	assert.ErrorIs(t, err, apigxmodel.ErrNotFound)
	assert.True(t, apigxmodel.HasPendingAsyncTaskSnapshotRepairs(db))
	_, transferred := asyncLeases.Load(leaseID)
	assert.True(t, transferred)
	assert.Empty(t, leaseIDFromContext(c), "request lifecycle must no longer own the durable task lease")

	require.NoError(t, ReconcileAsyncTasks(context.Background()))
	stored, err := apigxmodel.GetAsyncTaskByBaseTaskID(db, 76001)
	require.NoError(t, err)
	assert.Equal(t, "upstream-76001", stored.UpstreamTaskID)
	assert.False(t, apigxmodel.HasPendingAsyncTaskSnapshotRepairs(db))
	asyncLeases.Delete(leaseID)
}

func TestResolveAsyncTaskGrantVersionIsScopedToSelectedResource(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "grant.db")), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, db.AutoMigrate(&apigxmodel.Grant{}))
	active := []apigxmodel.Grant{
		{SubjectType: apigxmodel.GrantSubjectUser, SubjectID: 77, ResourceType: apigxmodel.GrantResourceGroup, ResourceID: 88, GiftVersion: 3},
		{SubjectType: apigxmodel.GrantSubjectAPIKey, SubjectID: 99, ResourceType: apigxmodel.GrantResourceOffering, ResourceID: 111, GiftVersion: 4},
		{SubjectType: apigxmodel.GrantSubjectUser, SubjectID: 77, ResourceType: apigxmodel.GrantResourceGroup, ResourceID: 999, GiftVersion: 90},
		{SubjectType: apigxmodel.GrantSubjectUser, SubjectID: 77, ResourceType: apigxmodel.GrantResourceOffering, ResourceID: 111, GiftVersion: 80, RevokedAt: 1},
	}
	require.NoError(t, db.Create(&active).Error)

	version := resolveAsyncTaskGrantVersion(db, &apigxmodel.AsyncTask{
		UserID: 77, ApiKeyID: 99, ShareGroupID: 88, OfferingID: 111,
	})
	assert.EqualValues(t, 4, version)
}

func TestReconcileAsyncTaskRestoresLeaseAndWaitsForRealTerminalState(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "reconcile.db")), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, db.AutoMigrate(&apigxmodel.AsyncTask{}, &apigxmodel.Attempt{}, &apigxmodel.AuditEvent{}))
	withAsyncTaskDB(t, db)

	mini := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mini.Addr()})
	previousRedis := common.RDB
	common.RDB = rdb
	t.Cleanup(func() {
		common.RDB = previousRedis
		_ = rdb.Close()
	})

	const leaseID = "async-recovery-lease"
	dimension := capacity.AsyncTaskSlot("channel", 7001)
	ctrl := capacity.New(rdb)
	require.NoError(t, ctrl.Reserve(context.Background(), leaseID, []capacity.Dimension{
		{Key: dimension, Limit: 1, Amount: 1},
	}, 90*time.Second))
	require.NoError(t, db.Create(&apigxmodel.AsyncTask{
		PublicTaskID: "apigx_recovery", BaseTaskID: 72001,
		Status: apigxmodel.AsyncTaskStatusRunning, TimeoutAt: common.GetTimestamp() - 1,
		ReservationUID: leaseID,
	}).Error)

	require.NoError(t, ReconcileAsyncTasks(context.Background()))
	_, restored := asyncLeases.Load(leaseID)
	require.True(t, restored, "reconciliation must resume renewal for a live task lease")
	usage, err := ctrl.Usage(context.Background(), dimension)
	require.NoError(t, err)
	assert.EqualValues(t, 1, usage)
	reloaded, err := apigxmodel.GetAsyncTaskByBaseTaskID(db, 72001)
	require.NoError(t, err)
	assert.Equal(t, apigxmodel.AsyncTaskStatusReconciling, reloaded.Status)
	assert.False(t, apigxmodel.IsAsyncTaskTerminal(reloaded.Status), "local timeout must not pretend the upstream stopped")

	require.NoError(t, SyncAsyncTaskFromBase(72001, "SUCCESS", "completed", 0, common.GetTimestamp()))
	usage, err = ctrl.Usage(context.Background(), dimension)
	require.NoError(t, err)
	assert.Zero(t, usage)
	_, restored = asyncLeases.Load(leaseID)
	assert.False(t, restored)

	// A duplicate terminal callback cannot release another task's capacity or
	// move the state again.
	require.NoError(t, SyncAsyncTaskFromBase(72001, "SUCCESS", "duplicate", 0, common.GetTimestamp()))
	usage, err = ctrl.Usage(context.Background(), dimension)
	require.NoError(t, err)
	assert.Zero(t, usage)
}

func TestResolveAsyncTaskCredentialSurvivesKeyRotationAndDeletion(t *testing.T) {
	t.Setenv(secretbox.KeyEnvVar, strings.Repeat("4a", secretbox.KeyBytes))
	secretbox.ResetKeyCacheForTest()
	t.Cleanup(secretbox.ResetKeyCacheForTest)

	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "history.db")), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, db.AutoMigrate(
		&apigxmodel.ShareGroup{}, &apigxmodel.Channel{}, &apigxmodel.UpstreamKey{},
		&apigxmodel.UpstreamKeyCredential{}, &apigxmodel.AsyncTask{},
		&apigxmodel.BreakerProbeState{},
	))
	withAsyncTaskDB(t, db)

	const owner = int64(88)
	group := &apigxmodel.ShareGroup{OwnerUserID: owner, Name: "history"}
	require.NoError(t, apigxmodel.CreateShareGroup(db, group))
	channel := &apigxmodel.Channel{OwnerUserID: owner, ShareGroupID: group.ID, Name: "history", BaseChannelID: 808}
	require.NoError(t, apigxmodel.CreateChannel(db, channel))
	v1Envelope, err := secretbox.Encrypt("upstream-secret-v1")
	require.NoError(t, err)
	v1Fingerprint, err := secretbox.Fingerprint("upstream-secret-v1")
	require.NoError(t, err)
	key := &apigxmodel.UpstreamKey{
		ChannelID: channel.ID, StableKeyID: "stable-history", SecretRef: v1Envelope,
		SecretFingerprint: v1Fingerprint, Status: apigxmodel.UpstreamKeyStatusEnabled,
	}
	require.NoError(t, apigxmodel.CreateUpstreamKey(db, owner, key))
	require.NoError(t, db.Create(&apigxmodel.AsyncTask{
		PublicTaskID: "history-v1", BaseTaskID: 73001,
		UpstreamKeyID: key.ID, UpstreamKeyCredentialVersion: 1,
	}).Error)

	v2Envelope, err := secretbox.Encrypt("upstream-secret-v2")
	require.NoError(t, err)
	v2Fingerprint, err := secretbox.Fingerprint("upstream-secret-v2")
	require.NoError(t, err)
	rotated, err := apigxmodel.RotateUpstreamKeyCredential(db, owner, key.ID, v2Envelope, v2Fingerprint)
	require.NoError(t, err)
	require.EqualValues(t, 2, rotated.CredentialVersion)
	require.NoError(t, db.Create(&apigxmodel.AsyncTask{
		PublicTaskID: "history-v2", BaseTaskID: 73002,
		UpstreamKeyID: key.ID, UpstreamKeyCredentialVersion: 2,
	}).Error)
	require.NoError(t, apigxmodel.DeleteUpstreamKey(db, owner, key.ID))

	v1, managed, err := ResolveAsyncTaskCredential(73001, 808)
	require.NoError(t, err)
	require.True(t, managed)
	require.NotNil(t, v1)
	assert.Equal(t, "upstream-secret-v1", v1.Secret)
	v2, managed, err := ResolveAsyncTaskCredential(73002, 808)
	require.NoError(t, err)
	require.True(t, managed)
	require.NotNil(t, v2)
	assert.Equal(t, "upstream-secret-v2", v2.Secret)
}

func TestResolveAsyncTaskCredentialRepairsDurableMissingSnapshot(t *testing.T) {
	t.Setenv(secretbox.KeyEnvVar, strings.Repeat("5b", secretbox.KeyBytes))
	secretbox.ResetKeyCacheForTest()
	t.Cleanup(secretbox.ResetKeyCacheForTest)

	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "repair.db")), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, db.AutoMigrate(
		&apigxmodel.AuditEvent{}, &apigxmodel.AsyncTask{},
		&apigxmodel.UpstreamKey{}, &apigxmodel.UpstreamKeyCredential{},
	))
	withAsyncTaskDB(t, db)

	envelope, err := secretbox.Encrypt("repair-secret")
	require.NoError(t, err)
	require.NoError(t, db.Create(&apigxmodel.UpstreamKeyCredential{
		UpstreamKeyID: 909, CredentialVersion: 3, SecretRef: envelope,
	}).Error)
	snapshot := &apigxmodel.AsyncTask{
		PublicTaskID: "apigx_repair_909", BaseTaskID: 75001,
		UpstreamKeyID: 909, UpstreamKeyCredentialVersion: 3,
		Status: apigxmodel.AsyncTaskStatusRunning,
	}
	require.NoError(t, apigxmodel.QueueAsyncTaskSnapshotRepair(db, snapshot, "snapshot_insert_failed"))

	credential, managed, err := ResolveAsyncTaskCredential(snapshot.BaseTaskID)
	require.NoError(t, err)
	require.True(t, managed)
	require.NotNil(t, credential)
	assert.Equal(t, "repair-secret", credential.Secret)
	stored, err := apigxmodel.GetAsyncTaskByBaseTaskID(db, snapshot.BaseTaskID)
	require.NoError(t, err)
	assert.EqualValues(t, 909, stored.UpstreamKeyID)
	assert.False(t, apigxmodel.HasPendingAsyncTaskSnapshotRepairs(db))
}

func TestSyncAsyncTaskCanceledKeepsDistinctTerminalState(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "canceled.db")), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, db.AutoMigrate(&apigxmodel.AsyncTask{}))
	withAsyncTaskDB(t, db)
	require.NoError(t, db.Create(&apigxmodel.AsyncTask{
		PublicTaskID: "apigx_canceled", BaseTaskID: 74001,
		Status: apigxmodel.AsyncTaskStatusRunning,
	}).Error)

	require.NoError(t, SyncAsyncTaskCanceled(74001, "user_canceled", common.GetTimestamp()))
	stored, err := apigxmodel.GetAsyncTaskByBaseTaskID(db, 74001)
	require.NoError(t, err)
	assert.Equal(t, apigxmodel.AsyncTaskStatusCanceled, stored.Status)
	assert.Equal(t, "user_canceled", stored.StatusReason)

	require.NoError(t, SyncAsyncTaskCanceled(74001, "duplicate", common.GetTimestamp()))
	stored, err = apigxmodel.GetAsyncTaskByBaseTaskID(db, 74001)
	require.NoError(t, err)
	assert.Equal(t, "user_canceled", stored.StatusReason, "duplicate callbacks must not rewrite terminal evidence")
}
