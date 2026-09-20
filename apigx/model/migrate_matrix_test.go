package apigxmodel

import (
	"errors"
	"os"
	"slices"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// 三库兼容矩阵（底座 AGENTS.md 硬要求：SQLite / MySQL ≥ 5.7.8 / PostgreSQL ≥ 9.6
// 必须**各自在真实实例上**验证；单库通过不算通过）。
//
// 由环境变量驱动，未配置的方言会**明确跳过并打印原因**，不会静默略过：
//
//	APIGX_TEST_PG_DSN    postgres://user:pass@host:port/db?sslmode=disable
//	APIGX_TEST_MYSQL_DSN user:pass@tcp(host:port)/db?charset=utf8mb4&parseTime=True&loc=UTC
//
// 每种方言都做两件事：
//  1. 在**全新库**上迁移，并断言建出 ExpectedTableCount 张 apigx_ 表；
//  2. **连续迁移两次**，断言第二次无错且表集合不变（幂等）。
//
// 「从底座旧库升级」的场景由外部脚本驱动：同一个测试直接指向底座已迁移过的库即可
// （此时 apigx_ 表是新增的，底座表原样保留）。
func TestMigrateMatrixOnConfiguredDialects(t *testing.T) {
	t.Run("sqlite", func(t *testing.T) {
		db := newSQLiteDB(t)
		require.NoError(t, Migrate(db))
		assert.Len(t, apigxTableNames(t, db), ExpectedTableCount)
		require.NoError(t, Migrate(db), "第二次迁移必须幂等")
		assert.Len(t, apigxTableNames(t, db), ExpectedTableCount)
		assertOfferingReviewDialect(t, db)
		assertManualOfferingPriceDialect(t, db)
		assertQualityRoutingDialect(t, db)
		assertStageCAsyncTaskDialect(t, db)
		assertStageDUsageSummaryDialect(t, db)
	})

	t.Run("postgres", func(t *testing.T) {
		dsn := os.Getenv("APIGX_TEST_PG_DSN")
		if dsn == "" {
			t.Skip("未设置 APIGX_TEST_PG_DSN：PostgreSQL 矩阵未执行（不允许当作通过）")
		}
		db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
		require.NoError(t, err, "连接 PostgreSQL 失败")
		assertPostgresMatrix(t, db)
	})

	t.Run("mysql", func(t *testing.T) {
		dsn := os.Getenv("APIGX_TEST_MYSQL_DSN")
		if dsn == "" {
			t.Skip("未设置 APIGX_TEST_MYSQL_DSN：MySQL 矩阵未执行（不允许当作通过）")
		}
		db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
		require.NoError(t, err, "连接 MySQL 失败")
		require.NoError(t, Migrate(db))
		require.NoError(t, Migrate(db), "第二次迁移必须幂等")
		// MySQL 与 PostgreSQL 的表清单都用 information_schema 查
		assert.Len(t, informationSchemaApigxTables(t, db, "table_name"), ExpectedTableCount)
		assertOfferingReviewDialect(t, db)
		assertManualOfferingPriceDialect(t, db)
		assertQualityRoutingDialect(t, db)
		assertStageCAsyncTaskDialect(t, db)
		assertStageDUsageSummaryDialect(t, db)

		// 存储引擎必须是 InnoDB。这不是洁癖：
		//  - 预留、额度原子扣减、幂等记录都依赖**事务与行锁**，MyISAM 没有事务，这些保证会静默失效；
		//  - MyISAM 的索引长度上限是 1000 字节（InnoDB DYNAMIC 是 3072），底座原生表
		//    （如 passkey_credentials.credential_id varchar(512) 上的唯一索引）会直接建不出来。
		// 本机首次接入时就踩到了：phpStudy 的 my.ini 里 default-storage-engine=MyIsam，
		// 结果 27 张表全部建成 MyISAM、底座自身 FATAL 起不来。此断言用于防止再次静默通过。
		var engine string
		row := db.Raw("SELECT engine FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = ?",
			TablePrefix+"user_profiles").Row()
		require.NoError(t, row.Scan(&engine), "读取 MySQL 存储引擎失败")
		assert.Equal(t, "InnoDB", engine,
			"APIGX 的表必须使用 InnoDB：MyISAM 无事务（预留/原子扣减会失效）且索引上限仅 1000 字节")
	})
}

func assertPostgresMatrix(t *testing.T, db *gorm.DB) {
	t.Helper()
	require.NoError(t, Migrate(db))
	assert.Len(t, informationSchemaApigxTables(t, db, "table_name"), ExpectedTableCount)
	require.NoError(t, Migrate(db), "第二次迁移必须幂等")
	assert.Len(t, informationSchemaApigxTables(t, db, "table_name"), ExpectedTableCount)
	assertOfferingReviewDialect(t, db)
	assertManualOfferingPriceDialect(t, db)
	assertQualityRoutingDialect(t, db)
	assertStageCAsyncTaskDialect(t, db)
	assertStageDUsageSummaryDialect(t, db)
}

var errRollbackOfferingReviewMatrix = errors.New("rollback offering review matrix fixture")

// assertOfferingReviewDialect 在每个真实方言上执行 B2 的 JOIN、条件更新、嵌套事务和审计写入。
// 外层事务最终主动回滚，矩阵库可反复使用且不会积累夹具。
func assertOfferingReviewDialect(t *testing.T, db *gorm.DB) {
	t.Helper()
	err := db.Transaction(func(tx *gorm.DB) error {
		const owner, reviewer int64 = 9_810_001, 9_810_099
		_, pendingBefore, listErr := ListOfferingsForReview(tx, OfferingStatusPending, 0, 1)
		require.NoError(t, listErr)
		group := &ShareGroup{
			OwnerUserID: owner, Name: "review-matrix",
			DefaultVisibility: ShareGroupVisibilityPublic, PublishStatus: ShareGroupPublishPublished,
		}
		require.NoError(t, CreateShareGroup(tx, group))
		channel := &Channel{OwnerUserID: owner, ShareGroupID: group.ID, Name: "review-matrix", BaseChannelID: 9_810_001}
		require.NoError(t, CreateChannel(tx, channel))
		offering := &ModelOffering{ChannelID: channel.ID, UpstreamModelID: "review-matrix-model"}
		require.NoError(t, CreateModelOffering(tx, owner, offering))
		require.NoError(t, SubmitModelOffering(tx, owner, offering.ID))

		items, total, listErr := ListOfferingsForReview(tx, OfferingStatusPending, int(pendingBefore), 20)
		require.NoError(t, listErr)
		require.EqualValues(t, pendingBefore+1, total)
		require.NotEmpty(t, items)
		assert.Equal(t, offering.ID, items[0].OfferingID)
		assert.Equal(t, owner, items[0].OwnerUserID)

		result, reviewErr := ReviewModelOffering(tx, offering.ID, OfferingStatusPublished, OfferingReviewContext{
			ReviewerUserID: reviewer,
			ReviewerRole:   common.RoleAdminUser,
			ReasonCode:     "matrix_approval",
			RequestID:      "matrix-request",
		})
		require.NoError(t, reviewErr)
		assert.Equal(t, owner, result.OwnerUserID)
		allItems, allTotal, listErr := ListOfferingsForReview(tx, -1, 0, 100)
		require.NoError(t, listErr)
		assert.GreaterOrEqual(t, allTotal, int64(1))
		assert.True(t, slices.ContainsFunc(allItems, func(item OfferingReviewItem) bool {
			return item.OfferingID == offering.ID && item.Status == OfferingStatusPublished
		}), "status=-1 must include the published offering")
		var event AuditEvent
		require.NoError(t, tx.Where("target_type = ? AND target_id = ?", "model_offering", offering.ID).First(&event).Error)
		assert.Equal(t, reviewer, event.ActorUserID)
		assert.Equal(t, "offering_publish", event.Action)
		_, reviewErr = ReviewModelOffering(tx, offering.ID, OfferingStatusRejected, OfferingReviewContext{
			ReviewerUserID: reviewer, ReviewerRole: common.RoleAdminUser, ReasonCode: "duplicate",
		})
		assert.ErrorIs(t, reviewErr, ErrInvalidArgument)
		return errRollbackOfferingReviewMatrix
	})
	require.ErrorIs(t, err, errRollbackOfferingReviewMatrix)
}

var errRollbackManualOfferingPriceMatrix = errors.New("rollback manual offering price matrix fixture")

// assertManualOfferingPriceDialect verifies the owner-scoped manual price
// snapshot and its optimistic offering attachment on every supported database.
func assertManualOfferingPriceDialect(t *testing.T, db *gorm.DB) {
	t.Helper()
	err := db.Transaction(func(tx *gorm.DB) error {
		const owner int64 = 9_815_001
		group := &ShareGroup{OwnerUserID: owner, Name: "manual-price-matrix"}
		require.NoError(t, CreateShareGroup(tx, group))
		channel := &Channel{
			OwnerUserID: owner, ShareGroupID: group.ID,
			Name: "manual-price-matrix", BaseChannelID: 9_815_001,
		}
		require.NoError(t, CreateChannel(tx, channel))
		offering := &ModelOffering{ChannelID: channel.ID, UpstreamModelID: "manual-price-matrix-model"}
		require.NoError(t, CreateModelOffering(tx, owner, offering))

		rule, priceErr := CreateManualOfferingPriceSnapshot(tx, owner, offering.ID, []PriceRuleItem{{
			Dimension: 0, UnitScale: 0, UnitPriceMicro: 1_250_000,
		}})
		require.NoError(t, priceErr)
		require.NotNil(t, rule)
		assert.True(t, rule.ManualOverride)
		assert.True(t, rule.Locked)

		stored, getErr := GetModelOffering(tx, owner, offering.ID)
		require.NoError(t, getErr)
		assert.Equal(t, rule.ID, stored.PriceRuleID)
		assert.EqualValues(t, offering.Revision+1, stored.Revision)
		require.NoError(t, SubmitModelOffering(tx, owner, offering.ID))

		_, priceErr = CreateManualOfferingPriceSnapshot(tx, owner, offering.ID, []PriceRuleItem{{
			Dimension: 0, UnitScale: 0, UnitPriceMicro: 2_000_000,
		}})
		assert.ErrorIs(t, priceErr, ErrInvalidArgument)
		return errRollbackManualOfferingPriceMatrix
	})
	assert.ErrorIs(t, err, errRollbackManualOfferingPriceMatrix)
}

var errRollbackQualityRoutingMatrix = errors.New("rollback quality routing matrix fixture")

// assertQualityRoutingDialect 覆盖 B4 新依赖的原子桶累加与 6 小时汇总：
// 平台拒绝和探测不进入可靠性分母，吞吐分子/分母在三种方言上保持一致。
func assertQualityRoutingDialect(t *testing.T, db *gorm.DB) {
	t.Helper()
	err := db.Transaction(func(tx *gorm.DB) error {
		const offeringID int64 = 9_820_001
		const bucketTs int64 = 1_700_000_000 - 1_700_000_000%60
		for range MinSamplesForQuality {
			require.NoError(t, RecordQualitySample(tx, QualitySample{
				OfferingID: offeringID, BucketTs: bucketTs, Outcome: QualityOutcomeSuccess,
				LatencyMs: 1000, OutputTokens: 100, GenerationMs: 1000,
			}))
		}
		for range 5 {
			require.NoError(t, RecordQualitySample(tx, QualitySample{
				OfferingID: offeringID, BucketTs: bucketTs, Outcome: QualityOutcomeRejected,
				LatencyMs: 1,
			}))
		}
		for range 3 {
			require.NoError(t, RecordQualitySample(tx, QualitySample{
				OfferingID: offeringID, BucketTs: bucketTs, Outcome: QualityOutcomeSuccess,
				LatencyMs: 1, OutputTokens: 999, GenerationMs: 1, IsProbe: true,
			}))
		}

		view, viewErr := ResolveQualityView(tx, offeringID, QualityWindowSixHour, bucketTs+30)
		require.NoError(t, viewErr)
		assert.EqualValues(t, MinSamplesForQuality, view.SampleCount)
		assert.EqualValues(t, MinSamplesForQuality, view.SuccessCount)
		assert.EqualValues(t, 5, view.PlatformRejects)
		assert.EqualValues(t, 2_000, view.OutputTokensSum)
		assert.EqualValues(t, 20_000, view.GenerationMsSum)
		return errRollbackQualityRoutingMatrix
	})
	require.ErrorIs(t, err, errRollbackQualityRoutingMatrix)
}

var errRollbackStageCAsyncTaskMatrix = errors.New("rollback stage c async task matrix fixture")

// assertStageCAsyncTaskDialect executes the migration-sensitive Stage C path
// on every real dialect: the new base_token_id column, credential history after
// rotation/deletion, one-way async state transitions, and durable snapshot
// repair events all have to behave the same on SQLite, PostgreSQL, and MySQL.
func assertStageCAsyncTaskDialect(t *testing.T, db *gorm.DB) {
	t.Helper()
	assert.True(t, db.Migrator().HasColumn(&AsyncTask{}, "base_token_id"))
	err := db.Transaction(func(tx *gorm.DB) error {
		const owner int64 = 9_830_001
		group := &ShareGroup{OwnerUserID: owner, Name: "stage-c-matrix"}
		require.NoError(t, CreateShareGroup(tx, group))
		channel := &Channel{
			OwnerUserID: owner, ShareGroupID: group.ID, Name: "stage-c-matrix",
			BaseChannelID: 9_830_001,
		}
		require.NoError(t, CreateChannel(tx, channel))
		key := &UpstreamKey{
			ChannelID: channel.ID, StableKeyID: "stage-c-key",
			SecretRef: "ciphertext-v1", SecretFingerprint: "fingerprint-v1",
			Status: UpstreamKeyStatusEnabled,
		}
		require.NoError(t, CreateUpstreamKey(tx, owner, key))
		rotated, rotateErr := RotateUpstreamKeyCredential(tx, owner, key.ID, "ciphertext-v2", "fingerprint-v2")
		require.NoError(t, rotateErr)
		require.EqualValues(t, 2, rotated.CredentialVersion)
		require.NoError(t, DeleteUpstreamKey(tx, owner, key.ID))
		v1, resolveErr := ResolveUpstreamKeyCredential(tx, key.ID, 1)
		require.NoError(t, resolveErr)
		assert.Equal(t, "ciphertext-v1", v1.SecretRef)

		task := &AsyncTask{
			PublicTaskID: "stage-c-matrix-task", BaseTaskID: 9_830_001,
			UserID: owner, BaseTokenID: 9_830_088, ChannelID: channel.ID,
			UpstreamKeyID: key.ID, UpstreamKeyCredentialVersion: 1,
			Status: AsyncTaskStatusRunning,
		}
		_, created, createErr := CreateAsyncTaskSnapshot(tx, task)
		require.NoError(t, createErr)
		require.True(t, created)
		stored, getErr := GetAsyncTaskByBaseTaskID(tx, task.BaseTaskID)
		require.NoError(t, getErr)
		assert.EqualValues(t, 9_830_088, stored.BaseTokenID)
		_, changed, advanceErr := AdvanceAsyncTaskStatus(tx, task.BaseTaskID, AsyncTaskStatusSucceeded, "completed", 0, common.GetTimestamp())
		require.NoError(t, advanceErr)
		assert.True(t, changed)
		_, _, advanceErr = AdvanceAsyncTaskStatus(tx, task.BaseTaskID, AsyncTaskStatusFailed, "late_failure", 0, 0)
		assert.ErrorIs(t, advanceErr, ErrAsyncTaskTransition)

		repair := &AsyncTask{
			PublicTaskID: "stage-c-matrix-repair", BaseTaskID: 9_830_002,
			UserID: owner, BaseTokenID: 9_830_089, ChannelID: channel.ID,
			UpstreamKeyID: key.ID, UpstreamKeyCredentialVersion: 2,
			Status: AsyncTaskStatusRunning,
		}
		require.NoError(t, QueueAsyncTaskSnapshotRepair(tx, repair, "snapshot_insert_failed"))
		pending, found, pendingErr := GetPendingAsyncTaskSnapshotRepair(tx, repair.BaseTaskID)
		require.NoError(t, pendingErr)
		require.True(t, found)
		_, _, createErr = CreateAsyncTaskSnapshot(tx, pending)
		require.NoError(t, createErr)
		require.NoError(t, MarkAsyncTaskSnapshotRepairCompleted(tx, pending))
		assert.False(t, HasPendingAsyncTaskSnapshotRepairs(tx))
		return errRollbackStageCAsyncTaskMatrix
	})
	require.ErrorIs(t, err, errRollbackStageCAsyncTaskMatrix)
}

var errRollbackStageDUsageSummaryMatrix = errors.New("rollback stage d usage summary matrix fixture")

// assertStageDUsageSummaryDialect 覆盖 D4 新依赖的跨方言聚合：COUNT DISTINCT/CASE、
// 最新校正替换、成本状态分列与渠道/Key/模型三个分组必须在三种数据库上语义一致。
func assertStageDUsageSummaryDialect(t *testing.T, db *gorm.DB) {
	t.Helper()
	err := db.Transaction(func(tx *gorm.DB) error {
		const (
			owner int64 = 9_840_001
			now   int64 = 1_800_300_000
		)
		group := &ShareGroup{OwnerUserID: owner, Name: "stage-d-matrix"}
		require.NoError(t, CreateShareGroup(tx, group))
		channel := &Channel{
			OwnerUserID: owner, ShareGroupID: group.ID, Name: "stage-d-matrix",
			BaseChannelID: 9_840_001,
		}
		require.NoError(t, CreateChannel(tx, channel))
		key := &UpstreamKey{
			ChannelID: channel.ID, StableKeyID: "stage-d-key", Name: "stage-d-key",
			SecretRef: "stage-d-ciphertext", SecretFingerprint: "stage-d-fingerprint",
			Status: UpstreamKeyStatusEnabled,
		}
		require.NoError(t, CreateUpstreamKey(tx, owner, key))
		request := &Request{RequestUID: "stage-d-request", ApiModelID: "stage-d-model", CreatedAt: now}
		require.NoError(t, tx.Create(request).Error)
		for index, status := range []int{429, 200} {
			require.NoError(t, tx.Create(&Attempt{
				AttemptUID: "stage-d-attempt-" + string(rune('0'+index)), RequestID: request.ID,
				AttemptIndex: index, SelectedChannelID: channel.ID, UpstreamKeyID: key.ID,
				State: AttemptStateSucceeded, HttpStatus: status, StartedAt: now + int64(index), TtftMs: 50,
			}).Error)
		}
		original := &UsageEvent{
			EventUID: "stage-d-usage", Kind: UsageEventKindUsage, OwnerUserID: owner,
			ApiModelID: "stage-d-model", ChannelID: channel.ID, UpstreamKeyID: key.ID,
			Capability: CapabilityChat, Dimension: UsageDimensionInputTokens,
			QuantityMilli: 100_000, UnitScale: UsageUnitScalePerMillionTokens,
			Trust: UsageTrustLocalEstimated, CostState: UsageCostStateReferenceEstimated,
			CostUsdMicro: 100, OccurredAt: now, CreatedAt: now,
		}
		require.NoError(t, RecordUsageEvent(tx, original))
		require.NoError(t, AppendUsageCorrection(tx, original.ID, &UsageEvent{
			EventUID: "stage-d-correction", QuantityMilli: 120_000,
			Trust: UsageTrustUpstreamReported, CostState: UsageCostStateReferenceEstimated, CostUsdMicro: 120,
		}))
		require.NoError(t, RecordUsageEvent(tx, &UsageEvent{
			EventUID: "stage-d-unpriced", Kind: UsageEventKindUsage, OwnerUserID: owner,
			ApiModelID: "stage-d-model", ChannelID: channel.ID, UpstreamKeyID: key.ID,
			Capability: CapabilityChat, Dimension: UsageDimensionOutputTokens,
			QuantityMilli: 10_000, UnitScale: UsageUnitScalePerMillionTokens,
			Trust: UsageTrustUpstreamReported, CostState: UsageCostStateUnpriced,
			OccurredAt: now, CreatedAt: now,
		}))

		for _, groupBy := range []OwnerUsageGroupBy{OwnerUsageByChannel, OwnerUsageByKey, OwnerUsageByModel} {
			items, hasMore, summaryErr := SummarizeOwnerUsage(tx, owner, groupBy, now-1, now+2, 1, 20)
			require.NoError(t, summaryErr)
			require.False(t, hasMore)
			require.Len(t, items, 1)
			assert.EqualValues(t, 1, items[0].RequestCount)
			assert.EqualValues(t, 2, items[0].AttemptCount)
			assert.EqualValues(t, 1, items[0].RetryCount)
			assert.EqualValues(t, 120, items[0].EstimatedCostUSDMicro)
			assert.EqualValues(t, 1, items[0].UnpricedEventCount)
		}
		return errRollbackStageDUsageSummaryMatrix
	})
	require.ErrorIs(t, err, errRollbackStageDUsageSummaryMatrix)
}

// informationSchemaApigxTables 通过 information_schema 统计 apigx_ 表，
// 同时适用于 PostgreSQL 与 MySQL（SQLite 用 sqlite_master，见 apigxTableNames）。
func informationSchemaApigxTables(t *testing.T, db *gorm.DB, column string) []string {
	t.Helper()
	// 按方言选择「当前库/schema」表达式：PostgreSQL 是 current_schema()，MySQL 是 DATABASE()。
	// 不用「先试一个再回退」的写法——那会在日志里留下一条假的错误，掩盖真正的问题。
	schemaExpr := "current_schema()"
	if db.Dialector.Name() == "mysql" {
		schemaExpr = "DATABASE()"
	}
	var names []string
	query := "SELECT " + column + " FROM information_schema.tables WHERE table_schema = " + schemaExpr +
		" AND substr(" + column + ", 1, 6) = ?"
	require.NoError(t, db.Raw(query, TablePrefix).Scan(&names).Error)
	return names
}

// 说明：sqlite 驱动在此文件被引用是为了保证 matrix 测试在只跑本文件时也能编译；
// 真正的 SQLite 用例在 migrate_test.go。
var _ = sqlite.Open
