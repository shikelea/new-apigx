package apigxmodel

import (
	"errors"
	"os"
	"slices"
	"strings"
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
		assertRelayVoteDialect(t, db)
		assertLongSecretRefDialect(t, db)
		assertRelayCheckDialect(t, db)
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
		assertRelayVoteDialect(t, db)
		assertLongSecretRefDialect(t, db)
		assertRelayCheckDialect(t, db)

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
	assertRelayVoteDialect(t, db)
	assertLongSecretRefDialect(t, db)
	assertRelayCheckDialect(t, db)
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
		confirmOfferingIdentityForTest(t, tx, offering.ID, reviewer)

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

var errRollbackRelayVoteMatrix = errors.New("rollback relay vote matrix fixture")

// assertRelayVoteDialect covers the per-user vote dedup on every dialect: the
// ON CONFLICT DO NOTHING insert, the conditional flip and the clamped
// decrement must keep counters consistent on SQLite, PostgreSQL and MySQL.
func assertRelayVoteDialect(t *testing.T, db *gorm.DB) {
	t.Helper()
	err := db.Transaction(func(tx *gorm.DB) error {
		const owner, voterA, voterB int64 = 9_850_001, 9_850_002, 9_850_003
		share := &RelayShare{OwnerUserID: owner, Title: "vote-matrix", BaseURL: "https://vote-matrix.example", Kind: RelayKindCharity, Protocol: "new-api"}
		require.NoError(t, CreateRelayShare(tx, share))
		counts := func() (int64, int64) {
			var stored RelayShare
			require.NoError(t, tx.First(&stored, share.ID).Error)
			return stored.VoteUsable, stored.VoteUnavailable
		}

		require.NoError(t, AddRelayVote(tx, share.ID, voterA, true))
		require.NoError(t, AddRelayVote(tx, share.ID, voterA, true), "repeated vote must be a no-op")
		usable, unavailable := counts()
		assert.EqualValues(t, 1, usable)
		assert.EqualValues(t, 0, unavailable)

		require.NoError(t, AddRelayVote(tx, share.ID, voterA, false), "switching side moves the vote")
		usable, unavailable = counts()
		assert.EqualValues(t, 0, usable)
		assert.EqualValues(t, 1, unavailable)

		require.NoError(t, AddRelayVote(tx, share.ID, voterB, false))
		usable, unavailable = counts()
		assert.EqualValues(t, 0, usable)
		assert.EqualValues(t, 2, unavailable)

		mine, voteErr := GetRelayVote(tx, share.ID, voterA)
		require.NoError(t, voteErr)
		require.NotNil(t, mine)
		assert.False(t, *mine)
		none, voteErr := GetRelayVote(tx, share.ID, owner)
		require.NoError(t, voteErr)
		assert.Nil(t, none)
		assert.ErrorIs(t, AddRelayVote(tx, share.ID, 0, true), ErrInvalidArgument)
		return errRollbackRelayVoteMatrix
	})
	require.ErrorIs(t, err, errRollbackRelayVoteMatrix)
}

var errRollbackLongSecretRefMatrix = errors.New("rollback long secret ref matrix fixture")

// assertLongSecretRefDialect 固定住「长 Key 也能新建渠道 / 添加 Key」这个回归。
//
// 背景：secret_ref 存的是信封（前缀 + base64(nonce) + ":" + base64(密文+tag)），长度约为明文的
// 1.36 倍再加 34 字节。该列曾是 varchar(255)，于是超过约 155 字节的明文（Google 服务账号 JSON、
// 长 JWT 等）在 MySQL 上会因超长而写入失败，在 SQLite 上则静默写入、但与 MySQL 行为不一致。
// 这里用远超 255 字节的信封长度直接验证列宽，并读回比对，确保三种方言语义一致。
func assertLongSecretRefDialect(t *testing.T, db *gorm.DB) {
	t.Helper()
	err := db.Transaction(func(tx *gorm.DB) error {
		const owner int64 = 9_860_001
		group := &ShareGroup{
			OwnerUserID: owner, Name: "long-secret-matrix",
			DefaultVisibility: ShareGroupVisibilityPublic, PublishStatus: ShareGroupPublishPublished,
		}
		require.NoError(t, CreateShareGroup(tx, group))
		channel := &Channel{OwnerUserID: owner, ShareGroupID: group.ID, Name: "long-secret-matrix", BaseChannelID: 9_860_001}
		require.NoError(t, CreateChannel(tx, channel))

		// 2 KiB 明文对应的信封约 2.8 KiB，远超旧上限；用固定长度而不是随机值，便于定位。
		envelope := "apigxv1:" + strings.Repeat("A", 2776)
		require.Greater(t, len(envelope), 255)
		key := &UpstreamKey{
			ChannelID: channel.ID, Name: "long-secret", StableKeyID: "key-long-secret",
			SecretRef: envelope, SecretFingerprint: strings.Repeat("b", 64),
		}
		require.NoError(t, CreateUpstreamKey(tx, owner, key), "长信封必须能写入 secret_ref")

		var stored UpstreamKey
		require.NoError(t, tx.First(&stored, key.ID).Error)
		assert.Equal(t, envelope, stored.SecretRef, "读回的信封不能被截断")

		// 历史版本表镜像同一个信封，列宽必须一致。
		var credential UpstreamKeyCredential
		require.NoError(t, tx.Where("upstream_key_id = ?", key.ID).First(&credential).Error)
		assert.Equal(t, envelope, credential.SecretRef, "凭据历史表的信封也不能被截断")
		return errRollbackLongSecretRefMatrix
	})
	require.ErrorIs(t, err, errRollbackLongSecretRefMatrix)
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

var errRollbackRelayCheckMatrix = errors.New("rollback relay check matrix fixture")

// assertRelayCheckDialect 固定住持续检测与众测三张表的跨方言语义：
//   - 多选模型列表是 TEXT + common.Marshal，不能被截断；
//   - 到期推进用条件更新，重复调用不得把下一次时间推两倍（多调度节点场景）；
//   - 采样按「中转 + 模型 + 类型」分行，非主人只能读到公开项；
//   - 众测公布是单向的，且不属于自己的记录返回 ErrNotFound 而不是暴露存在性。
func assertRelayCheckDialect(t *testing.T, db *gorm.DB) {
	t.Helper()
	err := db.Transaction(func(tx *gorm.DB) error {
		const owner, other int64 = 9_860_001, 9_860_002
		share := &RelayShare{OwnerUserID: owner, Title: "check-matrix", BaseURL: "https://check-matrix.example", Kind: RelayKindCharity, Protocol: "new-api"}
		require.NoError(t, CreateRelayShare(tx, share))

		config := &RelayCheckConfig{
			RelayShareID: share.ID, OwnerUserID: owner,
			AvailabilityMode: CheckVisibilityPublic, AvailabilityInterval: CheckInterval15Min,
			DegradeMode: CheckVisibilityPublic, DegradeInterval: CheckInterval1Hour,
			DilutionMode: CheckVisibilityOwner, DilutionInterval: CheckInterval3Hour,
		}
		require.NoError(t, config.SetModels(ProbeKindDegrade, []string{"gpt-4o", "gpt-4o", "  ", "gpt-4.1"}))
		require.NoError(t, config.SetModels(ProbeKindDilution, []string{"gpt-4o"}))
		require.NoError(t, UpsertRelayCheckConfig(tx, config))
		// 去重与去空必须在写入前完成。
		assert.Equal(t, []string{"gpt-4o", "gpt-4.1"}, config.Models(ProbeKindDegrade))

		stored, getErr := GetRelayCheckConfig(tx, share.ID)
		require.NoError(t, getErr)
		assert.Equal(t, []string{"gpt-4o", "gpt-4.1"}, stored.Models(ProbeKindDegrade))
		assert.Equal(t, CheckInterval15Min, stored.AvailabilityInterval)

		// 间隔只接受四个挡位；开了检测但没选模型必须报错。
		bad := *config
		bad.AvailabilityInterval = 600
		assert.ErrorIs(t, UpsertRelayCheckConfig(tx, &bad), ErrInvalidArgument)
		empty := &RelayCheckConfig{RelayShareID: share.ID, OwnerUserID: owner, AvailabilityMode: CheckVisibilityOff,
			AvailabilityInterval: CheckInterval30Min, DegradeMode: CheckVisibilityPublic, DegradeInterval: CheckInterval1Hour,
			DilutionMode: CheckVisibilityOff, DilutionInterval: CheckInterval3Hour}
		assert.ErrorIs(t, UpsertRelayCheckConfig(tx, empty), ErrInvalidArgument)

		now := common.GetTimestamp()
		due, dueErr := ListDueCheckConfigs(tx, ProbeKindAvailability, now, 10)
		require.NoError(t, dueErr)
		require.NotEmpty(t, due)

		require.NoError(t, MarkCheckRan(tx, stored.ID, ProbeKindAvailability, now, CheckInterval15Min))
		afterFirst, getErr := GetRelayCheckConfig(tx, share.ID)
		require.NoError(t, getErr)
		assert.EqualValues(t, now+CheckInterval15Min, afterFirst.NextAvailabilityAt)
		// 第二个调度节点重复标记时，条件不再成立，到期时间不得被推第二次。
		require.NoError(t, MarkCheckRan(tx, stored.ID, ProbeKindAvailability, now, CheckInterval15Min))
		afterSecond, getErr := GetRelayCheckConfig(tx, share.ID)
		require.NoError(t, getErr)
		assert.EqualValues(t, afterFirst.NextAvailabilityAt, afterSecond.NextAvailabilityAt)

		// 主人一键暂停后，调度不再取到这条配置。
		require.NoError(t, tx.Model(&RelayCheckConfig{}).Where("id = ?", stored.ID).
			UpdateColumns(map[string]any{"owner_paused": true, "next_availability_at": 0}).Error)
		paused, dueErr := ListDueCheckConfigs(tx, ProbeKindAvailability, now, 10)
		require.NoError(t, dueErr)
		for _, item := range paused {
			assert.NotEqual(t, stored.ID, item.ID, "owner-paused config must not be scheduled")
		}
		_, dueErr = ListDueCheckConfigs(tx, "nope", now, 10)
		assert.ErrorIs(t, dueErr, ErrInvalidArgument)

		// 采样按模型分行；非主人只能读到公开项。
		for _, sample := range []*RelayProbeSample{
			{RelayShareID: share.ID, Kind: ProbeKindDegrade, Model: "gpt-4o", Status: RelayCheckHealthy, Score: 90, Visibility: CheckVisibilityPublic, Evidence: "ok"},
			{RelayShareID: share.ID, Kind: ProbeKindDegrade, Model: "gpt-4.1", Status: RelayCheckFailed, Score: 10, Visibility: CheckVisibilityOwner, Evidence: "wrong answer"},
			{RelayShareID: share.ID, Kind: ProbeKindSpeed, Model: "gpt-4o", Status: RelayCheckHealthy, FirstTokenMs: 420, OutputTPS: 63, Visibility: CheckVisibilityPublic},
		} {
			require.NoError(t, CreateRelayProbeSample(tx, sample))
			assert.Positive(t, sample.ProbedAt)
		}
		assert.ErrorIs(t, CreateRelayProbeSample(tx, &RelayProbeSample{RelayShareID: share.ID, Kind: ProbeKindDegrade}), ErrInvalidArgument)

		ownerView, listErr := ListRelayProbeSamples(tx, share.ID, ProbeKindDegrade, true, 50)
		require.NoError(t, listErr)
		assert.Len(t, ownerView, 2)
		publicView, listErr := ListRelayProbeSamples(tx, share.ID, ProbeKindDegrade, false, 50)
		require.NoError(t, listErr)
		require.Len(t, publicView, 1)
		assert.Equal(t, "gpt-4o", publicView[0].Model)

		// 众测：只属于提交者，草稿可公布一次，公布后锁定；他人公布返回 ErrNotFound。
		test := &Crowdtest{SubmitterUserID: owner, Benchmark: BenchmarkPelican, Model: "gpt-4o",
			Score: 100, Artifact: "<svg viewBox=\"0 0 1 1\"><circle r=\"1\"/></svg>", Visibility: CrowdtestVisibilityDraft}
		require.NoError(t, CreateCrowdtest(tx, test))
		assert.Zero(t, test.PublishedAt)
		assert.ErrorIs(t, CreateCrowdtest(tx, &Crowdtest{SubmitterUserID: owner, Benchmark: "sudoku", Model: "gpt-4o"}), ErrInvalidArgument)
		assert.ErrorIs(t, CreateCrowdtest(tx, &Crowdtest{SubmitterUserID: owner, Benchmark: BenchmarkCandy}), ErrInvalidArgument)

		hidden, listErr := ListPublicCrowdtests(tx, "", 50)
		require.NoError(t, listErr)
		assert.Empty(t, hidden, "draft crowdtests must stay private")
		// 草稿对提交者自己仍然可见，否则测完还没公布就找不回来了。
		mine, listErr := ListMyCrowdtests(tx, owner, 50)
		require.NoError(t, listErr)
		require.Len(t, mine, 1)

		assert.ErrorIs(t, PublishCrowdtest(tx, test.PublicID, other), ErrNotFound)
		require.NoError(t, PublishCrowdtest(tx, test.PublicID, owner))
		published, listErr := ListPublicCrowdtests(tx, BenchmarkPelican, 50)
		require.NoError(t, listErr)
		require.Len(t, published, 1)
		assert.Positive(t, published[0].PublishedAt)
		// SVG 原文要能原样穿过 TEXT 列，众测页面靠它渲染图片。
		assert.Contains(t, published[0].Artifact, "<circle")
		// 题型过滤生效：糖果题的列表里不该出现这条鹈鹕记录。
		otherKind, listErr := ListPublicCrowdtests(tx, BenchmarkCandy, 50)
		require.NoError(t, listErr)
		assert.Empty(t, otherKind)
		_, listErr = ListPublicCrowdtests(tx, "sudoku", 50)
		assert.ErrorIs(t, listErr, ErrInvalidArgument)
		// 再次公布是空操作而不是二次写入：已公布记录对任何人只读。
		assert.ErrorIs(t, PublishCrowdtest(tx, test.PublicID, owner), ErrNotFound)

		byUser, countErr := CountCrowdtestsSince(tx, 0, owner)
		require.NoError(t, countErr)
		assert.EqualValues(t, 1, byUser)
		stranger, countErr := CountCrowdtestsSince(tx, 0, other)
		require.NoError(t, countErr)
		assert.Zero(t, stranger, "crowdtest quota is per submitter")
		_, countErr = CountCrowdtestsSince(tx, 0, 0)
		assert.ErrorIs(t, countErr, ErrInvalidArgument)
		return errRollbackRelayCheckMatrix
	})
	require.ErrorIs(t, err, errRollbackRelayCheckMatrix)
}
