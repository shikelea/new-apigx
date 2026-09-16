package channelpool

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	apigxmodel "github.com/QuantumNous/new-api/apigx/model"
	"github.com/QuantumNous/new-api/apigx/secretbox"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	basemodel "github.com/QuantumNous/new-api/model"
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
