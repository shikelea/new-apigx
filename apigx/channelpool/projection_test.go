package channelpool

import (
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/apigx/capacity"
	apigxmodel "github.com/QuantumNous/new-api/apigx/model"
	"github.com/QuantumNous/new-api/apigx/secretbox"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	basemodel "github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

const testMasterKey = "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"

var errRollbackBaseChannelLifecycle = errors.New("rollback base channel lifecycle fixture")

func setupProjectionEnv(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "apigx_channelpool_test.db")
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })

	require.NoError(t, apigxmodel.Migrate(db))
	// 复用的前提是底座渠道表存在；测试里单独建它，不走底座完整迁移
	// 底座 channel.Update() 会同步 abilities（路由能力表），因此测试库也要有它——
	// 这正是复用底座写路径的好处：路由能力不会与渠道状态脱节。
	require.NoError(t, db.AutoMigrate(&basemodel.Channel{}, &basemodel.Ability{}))

	// 底座 model 层用的是**全局 DB**（`basemodel.DB`）。生产由启动路径设置；
	// 测试必须显式指向同一个库，否则 `channel.Update()` 会空指针。
	previousDB := basemodel.DB
	basemodel.DB = db
	t.Cleanup(func() { basemodel.DB = previousDB })

	previous, had := os.LookupEnv(secretbox.KeyEnvVar)
	require.NoError(t, os.Setenv(secretbox.KeyEnvVar, testMasterKey))
	secretbox.ResetKeyCacheForTest()
	t.Cleanup(func() {
		if had {
			_ = os.Setenv(secretbox.KeyEnvVar, previous)
		} else {
			_ = os.Unsetenv(secretbox.KeyEnvVar)
		}
		secretbox.ResetKeyCacheForTest()
	})
	return db
}

func TestBaseChannelRetirementOnConfiguredDialects(t *testing.T) {
	tests := []struct {
		name string
		open func(*testing.T) *gorm.DB
	}{
		{name: "sqlite", open: func(t *testing.T) *gorm.DB {
			db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "base-lifecycle.db")), &gorm.Config{})
			require.NoError(t, err)
			return db
		}},
		{name: "postgres", open: func(t *testing.T) *gorm.DB {
			dsn := os.Getenv("APIGX_TEST_PG_DSN")
			if dsn == "" {
				t.Skip("未设置 APIGX_TEST_PG_DSN：PostgreSQL B3 生命周期矩阵未执行")
			}
			db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
			require.NoError(t, err)
			return db
		}},
		{name: "mysql", open: func(t *testing.T) *gorm.DB {
			dsn := os.Getenv("APIGX_TEST_MYSQL_DSN")
			if dsn == "" {
				t.Skip("未设置 APIGX_TEST_MYSQL_DSN：MySQL B3 生命周期矩阵未执行")
			}
			db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
			require.NoError(t, err)
			return db
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			db := tc.open(t)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			t.Cleanup(func() { _ = sqlDB.Close() })
			migration := db
			if db.Dialector.Name() == "mysql" {
				migration = db.Set("gorm:table_options", "ENGINE=InnoDB")
			}
			require.NoError(t, migration.AutoMigrate(&basemodel.Channel{}, &basemodel.Ability{}))

			err = db.Transaction(func(tx *gorm.DB) error {
				base := &basemodel.Channel{
					Id: 9_820_001, Name: "b3-lifecycle", Key: "sk-one\nsk-two", Models: "model-a",
					Group: "default", Status: common.ChannelStatusEnabled,
					ChannelInfo: basemodel.ChannelInfo{IsMultiKey: true, MultiKeySize: 2},
				}
				require.NoError(t, tx.Create(base).Error)
				require.NoError(t, tx.Create(&basemodel.Ability{
					Group: "default", Model: "model-a", ChannelId: base.Id, Enabled: true,
				}).Error)

				exists, lifecycleErr := QuiesceBaseChannel(tx, int64(base.Id))
				require.NoError(t, lifecycleErr)
				assert.True(t, exists)
				var quiesced basemodel.Channel
				require.NoError(t, tx.Where("id = ?", base.Id).First(&quiesced).Error)
				assert.Equal(t, common.ChannelStatusManuallyDisabled, quiesced.Status)
				var ability basemodel.Ability
				require.NoError(t, tx.Where("channel_id = ?", base.Id).First(&ability).Error)
				assert.False(t, ability.Enabled)

				require.NoError(t, ClearBaseChannelProjection(tx, int64(base.Id)))
				require.NoError(t, ClearBaseChannelProjection(tx, int64(base.Id)), "重复清理必须幂等")
				var cleared basemodel.Channel
				require.NoError(t, tx.Where("id = ?", base.Id).First(&cleared).Error)
				assert.Equal(t, common.ChannelStatusManuallyDisabled, cleared.Status)
				assert.Empty(t, cleared.Key)
				assert.Empty(t, cleared.Models)
				assert.False(t, cleared.ChannelInfo.IsMultiKey)
				assert.Zero(t, cleared.ChannelInfo.MultiKeySize)
				var abilityCount int64
				require.NoError(t, tx.Model(&basemodel.Ability{}).Where("channel_id = ?", base.Id).Count(&abilityCount).Error)
				assert.Zero(t, abilityCount)
				return errRollbackBaseChannelLifecycle
			})
			require.ErrorIs(t, err, errRollbackBaseChannelLifecycle)
		})
	}
}

// seedShareChannelWithKeys 建好「分享者 → 分组 → 渠道 → N 把 Key」与对应的底座渠道。
func seedShareChannelWithKeys(t *testing.T, db *gorm.DB, owner int64, baseChannelID int64, secrets []string, weights []int64) *apigxmodel.Channel {
	t.Helper()
	group := &apigxmodel.ShareGroup{OwnerUserID: owner, Name: "投影测试组", DefaultVisibility: 1, PublishStatus: 1}
	require.NoError(t, apigxmodel.CreateShareGroup(db, group))
	shareChannel := &apigxmodel.Channel{
		OwnerUserID: owner, ShareGroupID: group.ID, Name: "投影测试渠道",
		Protocol: "openai", BaseURL: "http://stub", BaseChannelID: baseChannelID,
		KeyPoolMode: apigxmodel.KeyPoolModePolling,
	}
	require.NoError(t, apigxmodel.CreateChannel(db, shareChannel))

	for i, secret := range secrets {
		envelope, err := secretbox.Encrypt(secret)
		require.NoError(t, err)
		fingerprint, err := secretbox.Fingerprint(secret)
		require.NoError(t, err)
		weight := int64(1)
		if i < len(weights) {
			weight = weights[i]
		}
		key := &apigxmodel.UpstreamKey{
			ChannelID:         shareChannel.ID,
			StableKeyID:       "stable-" + string(rune('a'+i)),
			Name:              "k" + string(rune('a'+i)),
			SecretRef:         envelope,
			SecretFingerprint: fingerprint,
			Status:            apigxmodel.UpstreamKeyStatusEnabled,
			Weight:            weight,
		}
		require.NoError(t, apigxmodel.CreateUpstreamKey(db, owner, key))
	}
	return shareChannel
}

func TestProjectKeyPoolWritesBaseMultiKeyChannel(t *testing.T) {
	db := setupProjectionEnv(t)
	const owner int64 = 9001
	const baseChannelID int64 = 4242

	base := &basemodel.Channel{Id: int(baseChannelID), Name: "底座影子渠道", Key: "placeholder", Status: common.ChannelStatusEnabled, Group: "default"}
	require.NoError(t, db.Create(base).Error)

	secrets := []string{"sk-first-aaaaaaaa", "sk-second-bbbbbbb", "sk-third-ccccccc"}
	shareChannel := seedShareChannelWithKeys(t, db, owner, baseChannelID, secrets, []int64{1, 5, 1})

	projection, err := ProjectKeyPool(db, shareChannel.ID)
	require.NoError(t, err)
	assert.Equal(t, 7, projection.KeyCount, "权重 1:5:1 → 7 个槽位（权重 = 流量占比）")

	// 底座渠道应进入多 Key 模式，且 Key 按权重**展开**（权重 = 流量占比，见 T2.6）
	var loaded basemodel.Channel
	require.NoError(t, db.Where("id = ?", baseChannelID).First(&loaded).Error)
	assert.True(t, loaded.ChannelInfo.IsMultiKey, "必须切到多 Key 模式")
	assert.Equal(t, 7, loaded.ChannelInfo.MultiKeySize, "权重 1:5:1 应展开为 7 个槽位（权重 = 流量占比）")
	assert.Equal(t, constant.MultiKeyModePolling, loaded.ChannelInfo.MultiKeyMode)

	lines := loaded.GetKeys()
	require.Len(t, lines, 7)
	assert.Equal(t, []string{"sk-second-bbbbbbb", "sk-second-bbbbbbb", "sk-second-bbbbbbb", "sk-second-bbbbbbb",
		"sk-second-bbbbbbb", "sk-first-aaaaaaaa", "sk-third-ccccccc"}, lines,
		"权重高的占更多槽位；权重相同按 ID 升序，保证顺序确定")

	// 索引 → 稳定 ID 的映射必须与写入顺序一致（前 5 个槽位都是权重 5 的那把）
	assert.Equal(t, "stable-b", mustStable(t, projection, 0))
	assert.Equal(t, "stable-b", mustStable(t, projection, 4))
	assert.Equal(t, "stable-a", mustStable(t, projection, 5))
	assert.Equal(t, "stable-c", mustStable(t, projection, 6))

	// 同一把 Key 有多个槽位时，按稳定 ID 反查只给出一个（首个）槽位
	idx, ok := projection.IndexOfStableKeyID("stable-c")
	assert.True(t, ok)
	assert.Equal(t, 6, idx)
}

func mustStable(t *testing.T, p *Projection, index int) string {
	t.Helper()
	stable, ok := p.StableKeyIDForIndex(index)
	require.True(t, ok, "索引 %d 应有对应凭据", index)
	return stable
}

// 只投影可用 Key：分享者显式停用 / 已到期 / 冷却中的凭据不应继续发往上游。
func TestProjectKeyPoolSkipsUnusableKeys(t *testing.T) {
	db := setupProjectionEnv(t)
	const owner int64 = 9002
	const baseChannelID int64 = 4243
	require.NoError(t, db.Create(&basemodel.Channel{Id: int(baseChannelID), Name: "底座", Key: "x", Status: common.ChannelStatusEnabled}).Error)

	secrets := []string{"sk-live-aaaaaaaa", "sk-disabled-bbbb", "sk-expired-cccccc"}
	shareChannel := seedShareChannelWithKeys(t, db, owner, baseChannelID, secrets, nil)

	now := common.GetTimestamp()
	require.NoError(t, db.Model(&apigxmodel.UpstreamKey{}).
		Where("channel_id = ? AND stable_key_id = ?", shareChannel.ID, "stable-b").
		Update("status", apigxmodel.UpstreamKeyStatusDisabled).Error)
	require.NoError(t, db.Model(&apigxmodel.UpstreamKey{}).
		Where("channel_id = ? AND stable_key_id = ?", shareChannel.ID, "stable-c").
		Update("expires_at", now-1).Error)

	projection, err := ProjectKeyPool(db, shareChannel.ID)
	require.NoError(t, err)
	assert.Equal(t, 1, projection.KeyCount)

	var loaded basemodel.Channel
	require.NoError(t, db.Where("id = ?", baseChannelID).First(&loaded).Error)
	keys := loaded.GetKeys()
	require.Len(t, keys, 1)
	assert.Equal(t, "sk-live-aaaaaaaa", keys[0])
	assert.Equal(t, 1, loaded.ChannelInfo.MultiKeySize)
}

// 重投影必须清空按索引存放的旧状态：Key 顺序变了，旧状态会套到别的凭据上。
func TestProjectKeyPoolResetsStaleIndexState(t *testing.T) {
	db := setupProjectionEnv(t)
	const owner int64 = 9003
	const baseChannelID int64 = 4244
	require.NoError(t, db.Create(&basemodel.Channel{
		Id: int(baseChannelID), Name: "底座", Key: "x", Status: common.ChannelStatusEnabled,
		ChannelInfo: basemodel.ChannelInfo{
			IsMultiKey:             true,
			MultiKeySize:           2,
			MultiKeyMode:           constant.MultiKeyModePolling,
			MultiKeyPollingIndex:   1,
			MultiKeyStatusList:     map[int]int{0: common.ChannelStatusAutoDisabled},
			MultiKeyDisabledReason: map[int]string{0: "provider rejected key"},
			MultiKeyDisabledTime:   map[int]int64{0: 123456},
		},
	}).Error)

	shareChannel := seedShareChannelWithKeys(t, db, owner, baseChannelID, []string{"sk-new-aaaaaaaa", "sk-new-bbbbbbbb"}, nil)

	_, err := ProjectKeyPool(db, shareChannel.ID)
	require.NoError(t, err)

	var loaded basemodel.Channel
	require.NoError(t, db.Where("id = ?", baseChannelID).First(&loaded).Error)
	assert.Empty(t, loaded.ChannelInfo.MultiKeyStatusList, "索引变了，旧禁用状态必须清空")
	assert.Empty(t, loaded.ChannelInfo.MultiKeyDisabledReason)
	assert.Empty(t, loaded.ChannelInfo.MultiKeyDisabledTime)
	assert.Zero(t, loaded.ChannelInfo.MultiKeyPollingIndex, "轮询游标必须归零")
}

func TestProjectKeyPoolFailsWhenNoUsableKey(t *testing.T) {
	db := setupProjectionEnv(t)
	const owner int64 = 9004
	const baseChannelID int64 = 4245
	require.NoError(t, db.Create(&basemodel.Channel{Id: int(baseChannelID), Name: "底座", Key: "x", Status: common.ChannelStatusEnabled}).Error)

	shareChannel := seedShareChannelWithKeys(t, db, owner, baseChannelID, []string{"sk-only-aaaaaaaa"}, nil)
	require.NoError(t, db.Model(&apigxmodel.UpstreamKey{}).
		Where("channel_id = ?", shareChannel.ID).
		Update("status", apigxmodel.UpstreamKeyStatusInvalid).Error)

	_, err := ProjectKeyPool(db, shareChannel.ID)
	assert.ErrorIs(t, err, ErrNoUsableKey, "整池无可用凭据必须报错，让渠道停止接收新请求")
}

// 单把凭据解不开（信封损坏/换过主密钥）时跳过它，其余 Key 仍要能投影。
func TestProjectKeyPoolSkipsUndecryptableKey(t *testing.T) {
	db := setupProjectionEnv(t)
	const owner int64 = 9005
	const baseChannelID int64 = 4246
	require.NoError(t, db.Create(&basemodel.Channel{Id: int(baseChannelID), Name: "底座", Key: "x", Status: common.ChannelStatusEnabled}).Error)

	shareChannel := seedShareChannelWithKeys(t, db, owner, baseChannelID, []string{"sk-good-aaaaaaaa", "sk-broken-bbbbbb"}, nil)
	require.NoError(t, db.Model(&apigxmodel.UpstreamKey{}).
		Where("channel_id = ? AND stable_key_id = ?", shareChannel.ID, "stable-b").
		Update("secret_ref", "enc:v1:AAAA:BBBB").Error)

	projection, err := ProjectKeyPool(db, shareChannel.ID)
	require.NoError(t, err)
	assert.Equal(t, 1, projection.KeyCount, "坏掉的那把应被跳过，不能拖垮整池")
	assert.Equal(t, "stable-a", mustStable(t, projection, 0))

	var loaded basemodel.Channel
	require.NoError(t, db.Where("id = ?", baseChannelID).First(&loaded).Error)
	keys := loaded.GetKeys()
	require.Len(t, keys, 1)
	assert.True(t, strings.HasPrefix(keys[0], "sk-good-"))
}

// ---- 逐 Key 统计回填 ----

// 底座的运行期只提供 Key 索引，统计必须翻译回稳定凭据 ID。
func TestRecordKeyOutcomeTranslatesIndexToStableKey(t *testing.T) {
	db := setupProjectionEnv(t)
	const owner int64 = 9101
	const baseChannelID int64 = 5252
	require.NoError(t, db.Create(&basemodel.Channel{Id: int(baseChannelID), Name: "底座", Key: "x", Status: common.ChannelStatusEnabled}).Error)

	// 权重 5 的那把会被投影到索引 0
	shareChannel := seedShareChannelWithKeys(t, db, owner, baseChannelID,
		[]string{"sk-low-aaaaaaaaa", "sk-high-bbbbbbbb"}, []int64{1, 5})

	_, err := ProjectKeyPool(db, shareChannel.ID)
	require.NoError(t, err)

	require.NoError(t, RecordKeyOutcome(db, int(baseChannelID), 0, KeyOutcome{Success: true}))

	var high apigxmodel.UpstreamKey
	require.NoError(t, db.Where("channel_id = ? AND stable_key_id = ?", shareChannel.ID, "stable-b").First(&high).Error)
	assert.Positive(t, high.LastOKAt, "索引 0 对应权重最高的 stable-b，成功时间应记在它身上")
	assert.Zero(t, high.LastErrorAt)

	var low apigxmodel.UpstreamKey
	require.NoError(t, db.Where("channel_id = ? AND stable_key_id = ?", shareChannel.ID, "stable-a").First(&low).Error)
	assert.Zero(t, low.LastOKAt, "不该记到另一把凭据上")
}

// 鉴权失效 → 只把该 Key 标为失效（Key 级故障隔离），不动渠道与其它 Key。
func TestRecordKeyOutcomeIsolatesAuthFailureToSingleKey(t *testing.T) {
	db := setupProjectionEnv(t)
	const owner int64 = 9102
	const baseChannelID int64 = 5253
	require.NoError(t, db.Create(&basemodel.Channel{Id: int(baseChannelID), Name: "底座", Key: "x", Status: common.ChannelStatusEnabled}).Error)

	shareChannel := seedShareChannelWithKeys(t, db, owner, baseChannelID,
		[]string{"sk-one-aaaaaaaa", "sk-two-bbbbbbbb"}, nil)
	_, err := ProjectKeyPool(db, shareChannel.ID)
	require.NoError(t, err)

	require.NoError(t, RecordKeyOutcome(db, int(baseChannelID), 1, KeyOutcome{
		Success: false, AuthFailed: true, ErrorCode: "upstream_401_invalid_key",
	}))

	var failed apigxmodel.UpstreamKey
	require.NoError(t, db.Where("channel_id = ? AND stable_key_id = ?", shareChannel.ID, "stable-b").First(&failed).Error)
	assert.Equal(t, apigxmodel.UpstreamKeyStatusInvalid, failed.Status)
	assert.Equal(t, "upstream_401_invalid_key", failed.LastErrorCode)
	assert.Positive(t, failed.LastErrorAt)

	var other apigxmodel.UpstreamKey
	require.NoError(t, db.Where("channel_id = ? AND stable_key_id = ?", shareChannel.ID, "stable-a").First(&other).Error)
	assert.Equal(t, apigxmodel.UpstreamKeyStatusEnabled, other.Status, "其它 Key 不受影响")

	// 渠道本身也不应被改动（Key 级隔离）
	var base basemodel.Channel
	require.NoError(t, db.Where("id = ?", baseChannelID).First(&base).Error)
	assert.Equal(t, common.ChannelStatusEnabled, base.Status)
}

func TestRecordKeyOutcomeSetsCooldown(t *testing.T) {
	db := setupProjectionEnv(t)
	const owner int64 = 9103
	const baseChannelID int64 = 5254
	require.NoError(t, db.Create(&basemodel.Channel{Id: int(baseChannelID), Name: "底座", Key: "x", Status: common.ChannelStatusEnabled}).Error)

	shareChannel := seedShareChannelWithKeys(t, db, owner, baseChannelID, []string{"sk-cool-aaaaaaaa"}, nil)
	_, err := ProjectKeyPool(db, shareChannel.ID)
	require.NoError(t, err)

	now := common.GetTimestamp()
	require.NoError(t, RecordKeyOutcome(db, int(baseChannelID), 0, KeyOutcome{CooldownSeconds: 120}))

	var key apigxmodel.UpstreamKey
	require.NoError(t, db.Where("channel_id = ?", shareChannel.ID).First(&key).Error)
	assert.Greater(t, key.CooldownUntil, now+60, "冷却应延伸到未来")
	assert.False(t, apigxmodel.IsUpstreamKeyUsable(&key, now), "冷却期内该 Key 不可用")
}

// 非我们的渠道（管理员自建）没有可记对象：静默跳过，不报错、不误写。
func TestRecordKeyOutcomeIgnoresForeignChannel(t *testing.T) {
	db := setupProjectionEnv(t)
	require.NoError(t, db.Create(&basemodel.Channel{Id: 9999, Name: "管理员自建", Key: "x", Status: common.ChannelStatusEnabled}).Error)

	assert.NoError(t, RecordKeyOutcome(db, 9999, 0, KeyOutcome{Success: true}))
}

// 索引不在当前投影里时不得猜测（池刚变更过），必须报错让调用方跳过本次统计。
func TestRecordKeyOutcomeRejectsUnknownIndex(t *testing.T) {
	db := setupProjectionEnv(t)
	const owner int64 = 9104
	const baseChannelID int64 = 5255
	require.NoError(t, db.Create(&basemodel.Channel{Id: int(baseChannelID), Name: "底座", Key: "x", Status: common.ChannelStatusEnabled}).Error)

	shareChannel := seedShareChannelWithKeys(t, db, owner, baseChannelID, []string{"sk-solo-aaaaaaaa"}, nil)
	_, err := ProjectKeyPool(db, shareChannel.ID)
	require.NoError(t, err)

	err = RecordKeyOutcome(db, int(baseChannelID), 7, KeyOutcome{Success: true})
	assert.ErrorIs(t, err, apigxmodel.ErrNotFound, "未知索引不得被猜成某把凭据")
}

// ---- 请求路径接入（默认关闭） ----

// 关闭时必须是彻底的空操作：不查询、不改任何行。它挂在每次失败处理上，属热路径。
func TestRecordFailureFromContextIsNoopWhenDisabled(t *testing.T) {
	db := setupProjectionEnv(t)
	const owner int64 = 9201
	const baseChannelID int64 = 6262
	require.NoError(t, db.Create(&basemodel.Channel{Id: int(baseChannelID), Name: "底座", Key: "x", Status: common.ChannelStatusEnabled}).Error)
	shareChannel := seedShareChannelWithKeys(t, db, owner, baseChannelID, []string{"sk-noop-aaaaaaaa"}, nil)
	_, err := ProjectKeyPool(db, shareChannel.ID)
	require.NoError(t, err)

	previous := dbProvider
	SetDBProvider(func() *gorm.DB { return db })
	t.Cleanup(func() { SetDBProvider(previous); SetKeyStatsEnabled(false) })

	SetKeyStatsEnabled(false)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	common.SetContextKey(ctx, constant.ContextKeyChannelIsMultiKey, true)
	common.SetContextKey(ctx, constant.ContextKeyChannelMultiKeyIndex, 0)
	RecordFailureFromContext(ctx, int(baseChannelID), 500, "boom")

	var key apigxmodel.UpstreamKey
	require.NoError(t, db.Where("channel_id = ?", shareChannel.ID).First(&key).Error)
	assert.Zero(t, key.LastErrorAt, "关闭时不得写入任何统计")
}

// 开启后：401 记为「凭据失效」并只隔离该 Key；非多 Key 渠道跳过；统计失败不影响请求。
func TestRecordFailureFromContextRecordsAuthFailure(t *testing.T) {
	db := setupProjectionEnv(t)
	const owner int64 = 9202
	const baseChannelID int64 = 6263
	require.NoError(t, db.Create(&basemodel.Channel{Id: int(baseChannelID), Name: "底座", Key: "x", Status: common.ChannelStatusEnabled}).Error)
	shareChannel := seedShareChannelWithKeys(t, db, owner, baseChannelID,
		[]string{"sk-first-aaaaaaa", "sk-second-bbbbbb"}, nil)
	_, err := ProjectKeyPool(db, shareChannel.ID)
	require.NoError(t, err)

	previous := dbProvider
	SetDBProvider(func() *gorm.DB { return db })
	t.Cleanup(func() { SetDBProvider(previous); SetKeyStatsEnabled(false) })
	SetKeyStatsEnabled(true)

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	common.SetContextKey(ctx, constant.ContextKeyChannelIsMultiKey, true)
	common.SetContextKey(ctx, constant.ContextKeyChannelMultiKeyIndex, 1)
	RecordFailureFromContext(ctx, int(baseChannelID), 401, "invalid_api_key")

	var second apigxmodel.UpstreamKey
	require.NoError(t, db.Where("channel_id = ? AND stable_key_id = ?", shareChannel.ID, "stable-b").First(&second).Error)
	assert.Equal(t, apigxmodel.UpstreamKeyStatusInvalid, second.Status, "401 应把该 Key 标为失效")
	assert.Equal(t, "401_invalid_api_key", second.LastErrorCode)
	assert.Positive(t, second.LastErrorAt)

	var first apigxmodel.UpstreamKey
	require.NoError(t, db.Where("channel_id = ? AND stable_key_id = ?", shareChannel.ID, "stable-a").First(&first).Error)
	assert.Equal(t, apigxmodel.UpstreamKeyStatusEnabled, first.Status, "另一把不受影响")

	// 非多 Key 渠道（上下文无标记）应被跳过
	singleCtx, _ := gin.CreateTestContext(httptest.NewRecorder())
	RecordFailureFromContext(singleCtx, int(baseChannelID), 500, "x")
	assert.NoError(t, nil)
}

// 400 之类的业务错误不应被当成凭据失效（否则一次参数错误就会停掉一把好 Key）。
func TestRecordFailureFromContextDoesNotInvalidateOnClientError(t *testing.T) {
	db := setupProjectionEnv(t)
	const owner int64 = 9203
	const baseChannelID int64 = 6264
	require.NoError(t, db.Create(&basemodel.Channel{Id: int(baseChannelID), Name: "底座", Key: "x", Status: common.ChannelStatusEnabled}).Error)
	shareChannel := seedShareChannelWithKeys(t, db, owner, baseChannelID, []string{"sk-client-aaaaaa"}, nil)
	_, err := ProjectKeyPool(db, shareChannel.ID)
	require.NoError(t, err)

	previous := dbProvider
	SetDBProvider(func() *gorm.DB { return db })
	t.Cleanup(func() { SetDBProvider(previous); SetKeyStatsEnabled(false) })
	SetKeyStatsEnabled(true)

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	common.SetContextKey(ctx, constant.ContextKeyChannelIsMultiKey, true)
	common.SetContextKey(ctx, constant.ContextKeyChannelMultiKeyIndex, 0)
	RecordFailureFromContext(ctx, int(baseChannelID), 400, "invalid_request_error")

	var key apigxmodel.UpstreamKey
	require.NoError(t, db.Where("channel_id = ?", shareChannel.ID).First(&key).Error)
	assert.Equal(t, apigxmodel.UpstreamKeyStatusEnabled, key.Status, "业务错误不得使凭据失效")
	assert.Equal(t, "400_invalid_request_error", key.LastErrorCode, "但仍要留下错误记录")
}

// **最关键的一条**：失效必须同时落到底座，否则底座会继续使用已失效的凭据。
//
// 这条证明的是「我们的统计 → 底座的真实选择行为」这一整条链路：
// 记一次 401 之后，再问底座要下一把 Key，它必须跳过那一把。
func TestAuthFailureMakesBaseSkipTheFailedKey(t *testing.T) {
	db := setupProjectionEnv(t)
	const owner int64 = 9301
	const baseChannelID int64 = 7272
	require.NoError(t, db.Create(&basemodel.Channel{
		Id: int(baseChannelID), Name: "底座", Key: "x", Status: common.ChannelStatusEnabled,
		ChannelInfo: basemodel.ChannelInfo{IsMultiKey: true, MultiKeyMode: constant.MultiKeyModePolling},
	}).Error)

	// **等权**：本用例要验证的是"失效 Key 被跳过、换用另一把"，
	// 因此两把 Key 权重相同（等权时不做加权展开，一把一个槽位，见 T2.6）。
	// 若给成 1:5，权重高的那把会有 5 个槽位，标记掉索引 0 之后底座跳到索引 1——
	// **还是同一把 Key**，那验证的就不是本用例想验证的事了。
	secrets := []string{"sk-low-aaaaaaaaa", "sk-high-bbbbbbbb"}
	shareChannel := seedShareChannelWithKeys(t, db, owner, baseChannelID, secrets, []int64{1, 1})
	projection, err := ProjectKeyPool(db, shareChannel.ID)
	require.NoError(t, err)

	// 等权 → 按 ID 升序，索引 0 = stable-a
	assert.Equal(t, "stable-a", mustStable(t, projection, 0))
	assert.Equal(t, "stable-b", mustStable(t, projection, 1))

	// 让索引 0 的凭据被上游拒绝
	require.NoError(t, RecordKeyOutcome(db, int(baseChannelID), 0, KeyOutcome{
		AuthFailed: true, ErrorCode: "401_invalid_api_key",
	}))

	// 底座自己的状态表必须被标记（否则它会继续用这把）
	base, err := basemodel.GetChannelById(int(baseChannelID), true)
	require.NoError(t, err)
	require.Equal(t, common.ChannelStatusAutoDisabled, base.ChannelInfo.MultiKeyStatusList[0],
		"底座必须把该下标标为不可用，否则 Key 级隔离没有生效")

	// 底座随后提供的必然是另一把凭据（这里就用底座自己的选择函数验证）
	key, index, apiErr := base.GetNextEnabledKey()
	require.Nil(t, apiErr)
	assert.Equal(t, 1, index, "底座必须跳过已失效的索引 0")
	assert.Equal(t, "sk-high-bbbbbbbb", key, "应换用另一把 Key（索引 1）")
}

// 成功路径：关闭时空操作；开启后把 LastOKAt 记到**本次实际使用的那把**凭据上。
func TestRecordSuccessFromContextWritesLastOKAt(t *testing.T) {
	db := setupProjectionEnv(t)
	const owner int64 = 9401
	const baseChannelID int64 = 8282
	require.NoError(t, db.Create(&basemodel.Channel{Id: int(baseChannelID), Name: "底座", Key: "x", Status: common.ChannelStatusEnabled}).Error)
	shareChannel := seedShareChannelWithKeys(t, db, owner, baseChannelID,
		[]string{"sk-ok-one-aaaaaa", "sk-ok-two-bbbbbb"}, nil)
	_, err := ProjectKeyPool(db, shareChannel.ID)
	require.NoError(t, err)

	previous := dbProvider
	SetDBProvider(func() *gorm.DB { return db })
	t.Cleanup(func() { SetDBProvider(previous); SetKeyStatsEnabled(false) })

	// 关闭时：一行都不该被写
	SetKeyStatsEnabled(false)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	common.SetContextKey(ctx, constant.ContextKeyChannelIsMultiKey, true)
	common.SetContextKey(ctx, constant.ContextKeyChannelMultiKeyIndex, 0)
	RecordSuccessFromContext(ctx, int(baseChannelID))
	var before apigxmodel.UpstreamKey
	require.NoError(t, db.Where("channel_id = ? AND stable_key_id = ?", shareChannel.ID, "stable-a").First(&before).Error)
	assert.Zero(t, before.LastOKAt, "关闭时不得写入")

	// 开启后：索引 0 的那把应被记上成功时间
	SetKeyStatsEnabled(true)
	RecordSuccessFromContext(ctx, int(baseChannelID))
	var after apigxmodel.UpstreamKey
	require.NoError(t, db.Where("channel_id = ? AND stable_key_id = ?", shareChannel.ID, "stable-a").First(&after).Error)
	assert.Positive(t, after.LastOKAt, "成功时间必须记在本次使用的那把凭据上")

	// 另一把不受影响
	var other apigxmodel.UpstreamKey
	require.NoError(t, db.Where("channel_id = ? AND stable_key_id = ?", shareChannel.ID, "stable-b").First(&other).Error)
	assert.Zero(t, other.LastOKAt)

	// 非多 Key 渠道：跳过
	singleCtx, _ := gin.CreateTestContext(httptest.NewRecorder())
	RecordSuccessFromContext(singleCtx, int(baseChannelID))
}

// 统计回填**绝不能**改动底座渠道：早期实现复用 ProjectKeyPool，等于每次记录统计都重写
// 底座渠道并清空它按下标的状态表与轮询游标——Key 健康状态会被一次次抹掉。
func TestRecordKeyOutcomeDoesNotTouchBaseChannel(t *testing.T) {
	db := setupProjectionEnv(t)
	const owner int64 = 9501
	const baseChannelID int64 = 9292
	require.NoError(t, db.Create(&basemodel.Channel{
		Id: int(baseChannelID), Name: "底座", Key: "x", Status: common.ChannelStatusEnabled,
		ChannelInfo: basemodel.ChannelInfo{IsMultiKey: true, MultiKeyMode: constant.MultiKeyModePolling},
	}).Error)

	shareChannel := seedShareChannelWithKeys(t, db, owner, baseChannelID,
		[]string{"sk-stat-one-aaaaa", "sk-stat-two-bbbbb"}, nil)
	_, err := ProjectKeyPool(db, shareChannel.ID)
	require.NoError(t, err)

	// 人为制造「底座已有运行期状态」：索引 1 被底座禁用、轮询游标停在 1
	require.NoError(t, db.Model(&basemodel.Channel{}).Where("id = ?", int(baseChannelID)).
		Updates(map[string]any{
			"channel_info": basemodel.ChannelInfo{
				IsMultiKey:           true,
				MultiKeySize:         2,
				MultiKeyMode:         constant.MultiKeyModePolling,
				MultiKeyPollingIndex: 1,
				MultiKeyStatusList:   map[int]int{1: common.ChannelStatusAutoDisabled},
			},
		}).Error)

	// 记统计（成功 + 失败各一次）
	require.NoError(t, RecordKeyOutcome(db, int(baseChannelID), 0, KeyOutcome{Success: true}))
	require.NoError(t, RecordKeyOutcome(db, int(baseChannelID), 0, KeyOutcome{ErrorCode: "500_boom"}))

	var base basemodel.Channel
	require.NoError(t, db.Where("id = ?", int(baseChannelID)).First(&base).Error)
	assert.Equal(t, common.ChannelStatusAutoDisabled, base.ChannelInfo.MultiKeyStatusList[1],
		"统计回填不得抹掉底座的 Key 健康状态")
	assert.Equal(t, 1, base.ChannelInfo.MultiKeyPollingIndex, "统计回填不得重置轮询游标")
	assert.Equal(t, 2, base.ChannelInfo.MultiKeySize)
}

// **回归测试**：隔离之后索引空间会分歧。
//
// 底座隔离索引 0 之后继续用索引 1；而若按「当前可用的 Key」重算映射，
// 已失效的那把会被过滤掉、另一把就变成了索引 0 —— 底座的索引 1 于是找不到对应凭据，
// 统计落空（若索引碰巧对齐，更会静默记到错误的凭据上）。
// 映射必须以**底座渠道实际的 Key 列表**为准。
func TestRecordKeyOutcomeAfterIsolationStillMapsCorrectly(t *testing.T) {
	db := setupProjectionEnv(t)
	const owner int64 = 9601
	const baseChannelID int64 = 9393
	require.NoError(t, db.Create(&basemodel.Channel{
		Id: int(baseChannelID), Name: "底座", Key: "x", Status: common.ChannelStatusEnabled,
		ChannelInfo: basemodel.ChannelInfo{IsMultiKey: true, MultiKeyMode: constant.MultiKeyModePolling},
	}).Error)

	// 权重相同 -> 投影顺序为 id 升序：索引 0 = stable-a，索引 1 = stable-b
	shareChannel := seedShareChannelWithKeys(t, db, owner, baseChannelID,
		[]string{"sk-iso-one-aaaaaa", "sk-iso-two-bbbbbb"}, nil)
	_, err := ProjectKeyPool(db, shareChannel.ID)
	require.NoError(t, err)

	// 模拟「索引 0 已被隔离」：我们这边标为失效 + 底座状态表标为不可用
	require.NoError(t, db.Model(&apigxmodel.UpstreamKey{}).
		Where("channel_id = ? AND stable_key_id = ?", shareChannel.ID, "stable-a").
		Updates(map[string]any{"status": apigxmodel.UpstreamKeyStatusInvalid}).Error)
	var base basemodel.Channel
	require.NoError(t, db.Where("id = ?", int(baseChannelID)).First(&base).Error)
	base.ChannelInfo.MultiKeyStatusList = map[int]int{0: common.ChannelStatusAutoDisabled}
	require.NoError(t, db.Model(&basemodel.Channel{}).Where("id = ?", int(baseChannelID)).
		Update("channel_info", base.ChannelInfo).Error)

	// 底座这时用的是索引 1 —— 统计必须落到 stable-b 上
	require.NoError(t, RecordKeyOutcome(db, int(baseChannelID), 1, KeyOutcome{Success: true}))

	var b apigxmodel.UpstreamKey
	require.NoError(t, db.Where("channel_id = ? AND stable_key_id = ?", shareChannel.ID, "stable-b").First(&b).Error)
	assert.Positive(t, b.LastOKAt, "索引 1 的成功统计必须落到 stable-b（旧实现会因索引分歧而漏记）")

	var a apigxmodel.UpstreamKey
	require.NoError(t, db.Where("channel_id = ? AND stable_key_id = ?", shareChannel.ID, "stable-a").First(&a).Error)
	assert.Zero(t, a.LastOKAt, "不得记到已隔离的那把上")
}

// ---- 索引映射缓存 ----

// 缓存必须能命中（避免每次统计都解密全部凭据），且**在池子变化后失效**。
func TestMappingCacheHitsAndInvalidates(t *testing.T) {
	db := setupProjectionEnv(t)
	ResetMappingCacheForTest()
	t.Cleanup(ResetMappingCacheForTest)
	const owner int64 = 9701
	const baseChannelID int64 = 9494
	require.NoError(t, db.Create(&basemodel.Channel{
		Id: int(baseChannelID), Name: "底座", Key: "x", Status: common.ChannelStatusEnabled,
		ChannelInfo: basemodel.ChannelInfo{IsMultiKey: true, MultiKeyMode: constant.MultiKeyModePolling},
	}).Error)

	shareChannel := seedShareChannelWithKeys(t, db, owner, baseChannelID,
		[]string{"sk-cache-one-aaaa", "sk-cache-two-bbbb"}, nil)
	_, err := ProjectKeyPool(db, shareChannel.ID) // apply 内部会失效缓存
	require.NoError(t, err)

	first, err := MapBaseKeyIndexesCached(db, shareChannel.ID)
	require.NoError(t, err)
	require.Equal(t, 2, first.KeyCount)

	// 命中同一份对象（不是重建）
	second, err := MapBaseKeyIndexesCached(db, shareChannel.ID)
	require.NoError(t, err)
	assert.Same(t, first, second, "TTL 内应命中缓存而不是重新解密构建")

	// 显式失效后必须重建（拿到的是新对象）
	InvalidateMappingCache(shareChannel.ID)
	third, err := MapBaseKeyIndexesCached(db, shareChannel.ID)
	require.NoError(t, err)
	assert.NotSame(t, first, third, "失效后必须重新构建")
	assert.Equal(t, 2, third.KeyCount)
}

// 统计走缓存路径时，索引翻译结果必须与不带缓存时一致。
func TestCachedMappingStillTranslatesCorrectly(t *testing.T) {
	db := setupProjectionEnv(t)
	ResetMappingCacheForTest()
	t.Cleanup(ResetMappingCacheForTest)
	const owner int64 = 9702
	const baseChannelID int64 = 9495
	require.NoError(t, db.Create(&basemodel.Channel{
		Id: int(baseChannelID), Name: "底座", Key: "x", Status: common.ChannelStatusEnabled,
		ChannelInfo: basemodel.ChannelInfo{IsMultiKey: true, MultiKeyMode: constant.MultiKeyModePolling},
	}).Error)

	shareChannel := seedShareChannelWithKeys(t, db, owner, baseChannelID,
		[]string{"sk-ct-one-aaaaaa", "sk-ct-two-bbbbbb"}, nil)
	_, err := ProjectKeyPool(db, shareChannel.ID)
	require.NoError(t, err)

	require.NoError(t, RecordKeyOutcome(db, int(baseChannelID), 1, KeyOutcome{Success: true}))

	var b apigxmodel.UpstreamKey
	require.NoError(t, db.Where("channel_id = ? AND stable_key_id = ?", shareChannel.ID, "stable-b").First(&b).Error)
	assert.Positive(t, b.LastOKAt, "缓存路径下索引 1 仍应翻译到 stable-b")
}

// ---- 上游 Key / 账户池容量维度 ----

func TestBuildKeyCapacityDimensions(t *testing.T) {
	db := setupProjectionEnv(t)
	ResetMappingCacheForTest()
	t.Cleanup(ResetMappingCacheForTest)
	const owner int64 = 9801
	const baseChannelID int64 = 9595
	require.NoError(t, db.Create(&basemodel.Channel{
		Id: int(baseChannelID), Name: "底座", Key: "x", Status: common.ChannelStatusEnabled,
		ChannelInfo: basemodel.ChannelInfo{IsMultiKey: true, MultiKeyMode: constant.MultiKeyModePolling},
	}).Error)

	shareChannel := seedShareChannelWithKeys(t, db, owner, baseChannelID,
		[]string{"sk-cap-one-aaaaaa", "sk-cap-two-bbbbbb"}, nil)
	_, err := ProjectKeyPool(db, shareChannel.ID)
	require.NoError(t, err)

	// 把索引 0 的那把挂到一个共享账户池上（stable-a 是 id 较小者，投影在索引 0）
	var keyA apigxmodel.UpstreamKey
	require.NoError(t, db.Where("channel_id = ? AND stable_key_id = ?", shareChannel.ID, "stable-a").First(&keyA).Error)
	require.NoError(t, db.Model(&apigxmodel.UpstreamKey{}).Where("id = ?", keyA.ID).
		Update("upstream_account_id", 4321).Error)

	// 两个维度都配了上限 -> 应产出两条维度，且分别指向我们的 Key ID 与账户池 ID
	dims := BuildKeyCapacityDimensions(db, int(baseChannelID), 0, KeyCapacityLimits{
		UpstreamKeyLimit: 3, UpstreamAccountLimit: 2,
	})
	require.Len(t, dims, 2)
	assert.Equal(t, capacity.UpstreamKeyConcurrency(keyA.ID), dims[0].Key)
	assert.EqualValues(t, 3, dims[0].Limit)
	assert.Equal(t, capacity.UpstreamAccountConcurrency(4321), dims[1].Key)
	assert.EqualValues(t, 2, dims[1].Limit)

	// 未配置上限维度时不产出任何维度（0 = 未配置，不是「不限」）
	assert.Empty(t, BuildKeyCapacityDimensions(db, int(baseChannelID), 0, KeyCapacityLimits{}))
	assert.Empty(t, BuildKeyCapacityDimensions(db, int(baseChannelID), 0, KeyCapacityLimits{UpstreamKeyLimit: 0, UpstreamAccountLimit: 0}))

	// 只配 Key 维度：另一把没有账户池，因此只有一条维度
	onlyKey := BuildKeyCapacityDimensions(db, int(baseChannelID), 1, KeyCapacityLimits{UpstreamKeyLimit: 1, UpstreamAccountLimit: 5})
	require.Len(t, onlyKey, 1, "未归属账户池的凭据不应产生账户池维度")
	assert.EqualValues(t, 1, onlyKey[0].Limit)

	// 索引解析不到身份时**不产出维度**（池子刚变化过，不应把内部抖动放大成用户可见的错误）
	assert.Empty(t, BuildKeyCapacityDimensions(db, int(baseChannelID), 9, KeyCapacityLimits{UpstreamKeyLimit: 1}))
	// 非我们的渠道同样不产出
	assert.Empty(t, BuildKeyCapacityDimensions(db, 999999, 0, KeyCapacityLimits{UpstreamKeyLimit: 1}))
}

// ---- 对账：补偿进程在步骤之间退出（T1.7） ----

// 对账必须**主动、精确、留痕**：按 AttemptUID 释放陈旧尝试占用的容量，并标记为遗弃。
func TestReconcileReleasesStaleAttempts(t *testing.T) {
	db := setupProjectionEnv(t)
	ResetMappingCacheForTest()
	t.Cleanup(ResetMappingCacheForTest)
	const owner int64 = 9901
	const baseChannelID int64 = 9797
	require.NoError(t, db.Create(&basemodel.Channel{
		Id: int(baseChannelID), Name: "底座", Key: "x", Status: common.ChannelStatusEnabled,
		ChannelInfo: basemodel.ChannelInfo{IsMultiKey: true, MultiKeyMode: constant.MultiKeyModePolling},
	}).Error)

	// 对账要用真实 Redis：投影相关的用例本身不依赖它，因此这里自建连接
	// （Lua 的原子性与幂等释放无法用 mock 证明）。
	addr := os.Getenv("APIGX_TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("未设置 APIGX_TEST_REDIS_ADDR：对账未在真实 Redis 上验证（不允许当作通过）")
	}
	rdb := redis.NewClient(&redis.Options{Addr: addr, DB: 15})
	require.NoError(t, rdb.Ping(t.Context()).Err(), "连接 Redis 失败")
	t.Cleanup(func() { _ = rdb.Close() })
	previous := common.RDB
	common.RDB = rdb
	t.Cleanup(func() { common.RDB = previous })

	ctrl := capacity.NewWithPrefix(rdb, "apigx:captest:reconcile:")
	ctx := t.Context()
	t.Cleanup(func() { _ = ctrl.Drop(ctx, capacity.ChannelConcurrency(baseChannelID)) })

	// 模拟「进程崩溃」：租约被占用 + 尝试已落库为「已开始」，但一直没有结果
	staleUID := "stale-attempt-uid"
	require.NoError(t, ctrl.Reserve(ctx, staleUID, []capacity.Dimension{
		{Key: capacity.ChannelConcurrency(baseChannelID), Limit: 5, Amount: 1},
	}, time.Minute))
	require.NoError(t, apigxmodel.BeginAttempt(db, &apigxmodel.Attempt{
		AttemptUID: staleUID, RequestID: RequestIDFromAttemptUID(staleUID),
		SelectedChannelID: baseChannelID, BaseChannelID: baseChannelID,
		StartedAt: common.GetTimestamp() - 3600, // 一小时前开始（远超宽限期）
	}))
	usage, err := ctrl.Usage(ctx, capacity.ChannelConcurrency(baseChannelID))
	require.NoError(t, err)
	require.EqualValues(t, 1, usage, "崩溃后槽位仍被占用")

	// 对账
	result, err := ReconcileStaleAttempts(ctx, db, ctrl, common.GetTimestamp()-60, 10)
	require.NoError(t, err)
	assert.Equal(t, 1, result.Scanned)
	assert.Equal(t, 1, result.Released)
	assert.Zero(t, result.Failed)

	usage, err = ctrl.Usage(ctx, capacity.ChannelConcurrency(baseChannelID))
	require.NoError(t, err)
	assert.EqualValues(t, 0, usage, "对账必须主动释放陈旧尝试占用的容量")

	attempt, err := apigxmodel.GetAttemptByUID(db, staleUID)
	require.NoError(t, err)
	assert.Equal(t, apigxmodel.AttemptStateAbandoned, attempt.State, "必须留下被遗弃的痕迹")
	assert.Positive(t, attempt.CompletedAt)

	// 幂等：再对账一次不应重复处理（状态已不是「已开始」）
	result, err = ReconcileStaleAttempts(ctx, db, ctrl, common.GetTimestamp()-60, 10)
	require.NoError(t, err)
	assert.Zero(t, result.Scanned, "已处理的尝试不应再次被扫出")
}

// 未超宽限期的尝试**不得**被回收：那会把正在跑的长流误判为遗弃。
func TestReconcileIgnoresFreshAttempts(t *testing.T) {
	db := setupProjectionEnv(t)
	uid := "fresh-attempt-uid"
	require.NoError(t, apigxmodel.BeginAttempt(db, &apigxmodel.Attempt{
		AttemptUID: uid, RequestID: RequestIDFromAttemptUID(uid), StartedAt: common.GetTimestamp(),
	}))

	ctrl := capacity.NewWithPrefix(common.RDB, "apigx:captest:reconcile-fresh:")
	// 宽限期取「一小时前」，因此刚开始的尝试不在范围内
	result, err := ReconcileStaleAttempts(t.Context(), db, ctrl, common.GetTimestamp()-3600, 10)
	require.NoError(t, err)
	assert.Zero(t, result.Scanned, "未陈旧的尝试不得被对账")

	attempt, err := apigxmodel.GetAttemptByUID(db, uid)
	require.NoError(t, err)
	assert.Equal(t, apigxmodel.AttemptStateStarted, attempt.State)
}

// 完成（成功/失败）是幂等的：重复调用不得覆盖已有结果。
func TestCompleteAttemptIsIdempotent(t *testing.T) {
	db := setupProjectionEnv(t)
	uid := "complete-attempt-uid"
	require.NoError(t, apigxmodel.BeginAttempt(db, &apigxmodel.Attempt{
		AttemptUID: uid, RequestID: RequestIDFromAttemptUID(uid), StartedAt: common.GetTimestamp(),
	}))

	require.NoError(t, apigxmodel.CompleteAttempt(db, uid, apigxmodel.AttemptStateSucceeded, 200))
	attempt, err := apigxmodel.GetAttemptByUID(db, uid)
	require.NoError(t, err)
	assert.Equal(t, apigxmodel.AttemptStateSucceeded, attempt.State)
	assert.Equal(t, 200, attempt.HttpStatus)

	// 第二次（例如失败路径又触发一次）不得把已成功改成失败
	require.NoError(t, apigxmodel.CompleteAttempt(db, uid, apigxmodel.AttemptStateFailed, 500))
	attempt, err = apigxmodel.GetAttemptByUID(db, uid)
	require.NoError(t, err)
	assert.Equal(t, apigxmodel.AttemptStateSucceeded, attempt.State, "已完成的结果不得被覆盖")
	assert.Equal(t, 200, attempt.HttpStatus)

	// 非法状态被拒
	assert.ErrorIs(t, apigxmodel.CompleteAttempt(db, uid, 99, 0), apigxmodel.ErrInvalidArgument)
}

// ---- Key 加权与剩余容量（T2.6） ----

// 加权轮询的经典形式：把 Key 按权重展开，底座的轮询/随机就会按比例选中。
func TestBuildWeightedKeyIndexesProportionalToWeight(t *testing.T) {
	expanded := BuildWeightedKeyIndexes([]KeyWeight{{Index: 0, Weight: 3}, {Index: 1, Weight: 1}})
	require.Len(t, expanded, 4)
	assert.Equal(t, []int{0, 0, 0, 1}, expanded, "权重 3:1 应展开为 3 次与 1 次")

	// 权重 0 视为 1：**不能等于"没有机会"**（否则没配权重的 Key 永远选不到）
	zero := BuildWeightedKeyIndexes([]KeyWeight{{Index: 0, Weight: 0}, {Index: 1, Weight: 2}})
	require.Len(t, zero, 3)
	assert.Contains(t, zero, 0, "权重 0 的 Key 仍应出现一次")
	assert.Equal(t, 2, countIndex(zero, 1))

	// 展开上限：权重写错（例如 10000）不得把列表撑爆
	huge := BuildWeightedKeyIndexes([]KeyWeight{{Index: 0, Weight: 10_000}, {Index: 1, Weight: 1}})
	assert.Len(t, huge, 17, "权重写错时展开次数必须被上限截断（16 + 1）")

	// 顺序确定：与传入顺序无关，按索引升序展开
	reordered := BuildWeightedKeyIndexes([]KeyWeight{{Index: 1, Weight: 1}, {Index: 0, Weight: 3}})
	assert.Equal(t, expanded, reordered, "展开结果必须与传入顺序无关（可复现）")

	assert.Empty(t, BuildWeightedKeyIndexes(nil))
}

// 剩余容量：已满的 Key 要被标出来让底座跳过；未配置上限**不等于**已满。
func TestSaturatedKeyIndexes(t *testing.T) {
	saturated := SaturatedKeyIndexes([]KeyLoad{
		{Index: 0, InUse: 2, Limit: 2},  // 已满
		{Index: 1, InUse: 1, Limit: 2},  // 有余量
		{Index: 2, InUse: 99, Limit: 0}, // 未配置上限：不得算作已满
		{Index: 3, InUse: 5, Limit: 3},  // 超限
	})
	assert.Equal(t, []int{0, 3}, saturated)
	assert.Empty(t, SaturatedKeyIndexes(nil))
}

// 状态表合并：只回收**我们自己**写的标记，底座自己的禁用原因必须原样保留。
func TestMergeKeyStatusListKeepsBaseMarks(t *testing.T) {
	const baseAutoDisabled = common.ChannelStatusAutoDisabled
	current := map[int]int{
		0: baseAutoDisabled,                   // 底座因鉴权失败自动禁用
		1: apigxmodel.StatusCapacitySaturated, // 我们上一轮标的"容量满"
	}

	// 本轮：索引 1 已恢复余量，索引 2 变为已满
	merged := MergeKeyStatusList(current, []int{1}, []int{2}, apigxmodel.StatusCapacitySaturated)

	assert.Equal(t, baseAutoDisabled, merged[0], "底座自己的禁用原因不得被我们抹掉")
	_, ourOldMarkStillThere := merged[1]
	assert.False(t, ourOldMarkStillThere, "我们上一轮的标记在恢复后应被回收（缺失即启用）")
	assert.Equal(t, apigxmodel.StatusCapacitySaturated, merged[2])

	// 全部清空时返回 nil（底座把 nil 状态表视为"全部启用"）
	assert.Nil(t, MergeKeyStatusList(map[int]int{1: apigxmodel.StatusCapacitySaturated}, []int{1}, nil,
		apigxmodel.StatusCapacitySaturated))
}

func countIndex(indexes []int, target int) int {
	count := 0
	for _, index := range indexes {
		if index == target {
			count++
		}
	}
	return count
}

// 加权投影：权重按占比展开，且**每个槽位仍映射回同一把 Key**；
// 解不开的凭据被跳过时**下标不得串位**（阶段 1 曾在此处出过回归）。
func TestProjectedPoolExpandsKeysByWeight(t *testing.T) {
	db := setupProjectionEnv(t)
	ResetMappingCacheForTest()
	t.Cleanup(ResetMappingCacheForTest)

	const owner int64 = 9951
	const baseChannelID int64 = 9897
	require.NoError(t, db.Create(&basemodel.Channel{
		Id: int(baseChannelID), Name: "底座", Key: "x", Status: common.ChannelStatusEnabled,
	}).Error)
	group := &apigxmodel.ShareGroup{OwnerUserID: owner, Name: "加权组", PublicID: "wg1"}
	require.NoError(t, apigxmodel.CreateShareGroup(db, group))
	channel := &apigxmodel.Channel{
		PublicID: "wc1", OwnerUserID: owner, ShareGroupID: group.ID,
		Name: "加权渠道", BaseChannelID: baseChannelID, KeyPoolMode: apigxmodel.KeyPoolModePolling,
	}
	require.NoError(t, apigxmodel.CreateChannel(db, channel))

	// 两把 Key：权重 3:1；**外加一把解不开的凭据**放在中间，专门验证下标不串位
	entries := []struct {
		stable string
		secret string
		weight int64
		broken bool
	}{
		{"stable-a", "sk-aaa", 3, false},
		{"stable-broken", "sk-broken", 1, true},
		{"stable-b", "sk-bbb", 1, false},
	}
	for i, spec := range entries {
		secretRef := ""
		if spec.broken {
			secretRef = "enc:v1:deadbeef:deadbeef" // 解不开：会被跳过
		} else {
			encrypted, err := secretbox.Encrypt(spec.secret)
			require.NoError(t, err)
			secretRef = encrypted
		}
		fingerprint, err := secretbox.Fingerprint(spec.secret + "-" + spec.stable)
		require.NoError(t, err)
		require.NoError(t, db.Create(&apigxmodel.UpstreamKey{
			ChannelID: channel.ID, StableKeyID: spec.stable, Name: spec.stable,
			SecretRef: secretRef, SecretFingerprint: fingerprint,
			Status: apigxmodel.UpstreamKeyStatusEnabled, Weight: spec.weight,
			CredentialVersion: 1, CreatedAt: common.GetTimestamp() + int64(i),
		}).Error)
	}

	projection, err := ProjectKeyPool(db, channel.ID)
	require.NoError(t, err)
	require.NotNil(t, projection)
	assert.Equal(t, 4, projection.KeyCount, "权重 3:1（解不开的那把被跳过）应展开为 4 个槽位")

	var base basemodel.Channel
	require.NoError(t, db.Where("id = ?", baseChannelID).First(&base).Error)
	assert.Equal(t, 4, len(strings.Split(base.Key, "\n")))
	assert.Equal(t, 4, base.ChannelInfo.MultiKeySize)

	countA, countB := 0, 0
	for index := range 4 {
		switch projection.IndexToStableKey[index] {
		case "stable-a":
			countA++
		case "stable-b":
			countB++
		case "stable-broken":
			t.Fatalf("解不开的凭据不得出现在投影里")
		}
	}
	assert.Equal(t, 3, countA, "权重高的 Key 应占 3 个槽位")
	assert.Equal(t, 1, countB)
}

// 上游 Base URL 归一化：真实 API 回扫发现的形态（用户填 `.../v1` 会被底座再拼一层）。
func TestNormalizeUpstreamBaseURL(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"去掉结尾多余的 v1（最常见）", "https://tokenrhythm.studio/v1", "https://tokenrhythm.studio"},
		{"去掉结尾斜杠与 v1", "https://tokenrhythm.studio/v1/", "https://tokenrhythm.studio"},
		{"带路径前缀的 v1", "https://host/api/v1", "https://host/api"},
		{"根形式保持不变", "https://tokenrhythm.studio", "https://tokenrhythm.studio"},
		{"结尾斜杠被规范化", "https://host/", "https://host"},
		{"不敢猜的形态原样保留", "https://host/api", "https://host/api"},
		{"本地地址也适用", "http://127.0.0.1:8100/v1", "http://127.0.0.1:8100"},
		{"空串安全", "", ""},
		{"只有 v1 时不改坏", "/v1", "/v1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, NormalizeUpstreamBaseURL(tc.in))
		})
	}
}
