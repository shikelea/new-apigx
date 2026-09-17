package channelpool

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	apigxmodel "github.com/QuantumNous/new-api/apigx/model"
	"github.com/QuantumNous/new-api/apigx/secretbox"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	basemodel "github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const testMasterKey = "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"

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
	assert.Equal(t, 3, projection.KeyCount)

	// 底座渠道应进入多 Key 模式，且 Key 按 (weight DESC, id ASC) 排列
	var loaded basemodel.Channel
	require.NoError(t, db.Where("id = ?", baseChannelID).First(&loaded).Error)
	assert.True(t, loaded.ChannelInfo.IsMultiKey, "必须切到多 Key 模式")
	assert.Equal(t, 3, loaded.ChannelInfo.MultiKeySize)
	assert.Equal(t, constant.MultiKeyModePolling, loaded.ChannelInfo.MultiKeyMode)

	lines := loaded.GetKeys()
	require.Len(t, lines, 3)
	assert.Equal(t, []string{"sk-second-bbbbbbb", "sk-first-aaaaaaaa", "sk-third-ccccccc"}, lines,
		"权重高的排前面；权重相同按 ID 升序，保证顺序确定")

	// 索引 → 稳定 ID 的映射必须与写入顺序一致
	assert.Equal(t, "stable-b", mustStable(t, projection, 0))
	assert.Equal(t, "stable-a", mustStable(t, projection, 1))
	assert.Equal(t, "stable-c", mustStable(t, projection, 2))

	idx, ok := projection.IndexOfStableKeyID("stable-c")
	assert.True(t, ok)
	assert.Equal(t, 2, idx)
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

	secrets := []string{"sk-low-aaaaaaaaa", "sk-high-bbbbbbbb"}
	shareChannel := seedShareChannelWithKeys(t, db, owner, baseChannelID, secrets, []int64{1, 5})
	projection, err := ProjectKeyPool(db, shareChannel.ID)
	require.NoError(t, err)

	// 投影后索引 0 = 权重更高的 stable-b
	assert.Equal(t, "stable-b", mustStable(t, projection, 0))

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
	assert.Equal(t, "sk-low-aaaaaaaaa", key)
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
