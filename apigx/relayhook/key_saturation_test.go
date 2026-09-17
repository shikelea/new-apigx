package relayhook

import (
	"path/filepath"
	"testing"
	"time"

	apigxmodel "github.com/QuantumNous/new-api/apigx/model"
	"github.com/QuantumNous/new-api/common"
	basemodel "github.com/QuantumNous/new-api/model"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// Key 级"剩余容量"的运行时刷新（T2.6 最后一块）。
//
// 这里测的都是**确定性**行为：给定"哪些索引满载"，状态表该不该有标记是确定的——
// 因此单测比并发 E2E 更能说明问题（并发场景里还混着准入拒绝的影响，两种原因分不开）。
// Redis 计数到"哪些索引满载"的换算在 `SaturatedKeyIndexes` 里，已有自己的用例。

func setupKeySaturationEnv(t *testing.T, keyCount int) (*gorm.DB, int) {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "apigx_key_sat_test.db")
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, apigxmodel.Migrate(db))
	require.NoError(t, db.AutoMigrate(&basemodel.Channel{}))
	previousBaseDB := basemodel.DB
	basemodel.DB = db
	t.Cleanup(func() { basemodel.DB = previousBaseDB })

	baseChannelID := int(910000 + time.Now().UnixNano()%10000)
	priority := int64(0)
	weight := uint(0)
	require.NoError(t, db.Create(&basemodel.Channel{
		Id: baseChannelID, Name: "饱和度底座", Key: "sk-a\nsk-b", Status: common.ChannelStatusEnabled,
		Priority: &priority, Weight: &weight,
		ChannelInfo: basemodel.ChannelInfo{
			IsMultiKey: true, MultiKeySize: keyCount,
			// 索引 0 是**底座自己**的禁用原因：我们任何时候都不得动它
			MultiKeyStatusList: map[int]int{0: common.ChannelStatusAutoDisabled},
		},
	}).Error)
	return db, baseChannelID
}

// 满载的索引被标记；恢复后被回收；**底座自己的禁用原因始终不动**。
func TestApplyKeySaturationMarksAndClears(t *testing.T) {
	db, baseChannelID := setupKeySaturationEnv(t, 2)

	applyKeySaturationMarks(db, baseChannelID, []int{1})
	var marked basemodel.Channel
	require.NoError(t, db.Where("id = ?", baseChannelID).First(&marked).Error)
	assert.Equal(t, apigxmodel.StatusCapacitySaturated, marked.ChannelInfo.MultiKeyStatusList[1],
		"满载的索引应被标记（底座据此跳过该 Key，而不是把请求交给它再拒绝）")
	assert.Equal(t, common.ChannelStatusAutoDisabled, marked.ChannelInfo.MultiKeyStatusList[0],
		"底座自己的禁用原因不得被我们抹掉")

	// 容量释放 → 只回收我们自己的标记
	applyKeySaturationMarks(db, baseChannelID, nil)
	var restored basemodel.Channel
	require.NoError(t, db.Where("id = ?", baseChannelID).First(&restored).Error)
	_, stillMarked := restored.ChannelInfo.MultiKeyStatusList[1]
	assert.False(t, stillMarked, "容量恢复后应回收我们的标记（缺失即启用）")
	assert.Equal(t, common.ChannelStatusAutoDisabled, restored.ChannelInfo.MultiKeyStatusList[0],
		"回收不得波及底座自己的标记")
}

// 标记未变化时**不写库**（避免每请求都产生一次无意义的写）。
func TestApplyKeySaturationSkipsRedundantWrite(t *testing.T) {
	db, baseChannelID := setupKeySaturationEnv(t, 2)

	applyKeySaturationMarks(db, baseChannelID, []int{1})
	var first basemodel.Channel
	require.NoError(t, db.Where("id = ?", baseChannelID).First(&first).Error)

	// 再标一次同样的集合：内容等价 → 提前返回（这里用"内容不变"来断言，不依赖写入次数）
	applyKeySaturationMarks(db, baseChannelID, []int{1})
	var second basemodel.Channel
	require.NoError(t, db.Where("id = ?", baseChannelID).First(&second).Error)
	assert.True(t, equalStatusLists(first.ChannelInfo.MultiKeyStatusList, second.ChannelInfo.MultiKeyStatusList))
	assert.Equal(t, apigxmodel.StatusCapacitySaturated, second.ChannelInfo.MultiKeyStatusList[1])
}

// 非多 Key 渠道不标记（没有可标记的条目）。
func TestApplyKeySaturationSkipsNonMultiKeyChannel(t *testing.T) {
	db, baseChannelID := setupKeySaturationEnv(t, 0)
	require.NoError(t, db.Model(&basemodel.Channel{}).Where("id = ?", baseChannelID).
		Update("channel_info", basemodel.ChannelInfo{IsMultiKey: false}).Error)

	applyKeySaturationMarks(db, baseChannelID, []int{0})
	var channel basemodel.Channel
	require.NoError(t, db.Where("id = ?", baseChannelID).First(&channel).Error)
	assert.False(t, channel.ChannelInfo.IsMultiKey)
	assert.Empty(t, channel.ChannelInfo.MultiKeyStatusList)
}

// 节流：同一渠道在间隔内只刷新一次（这是"每请求路径"上必须有的保护）。
func TestKeySaturationRefreshIsThrottled(t *testing.T) {
	ResetKeySaturationThrottleForTest()
	now := time.Now()
	assert.True(t, throttleAllows(12345, now), "首次应允许")
	assert.False(t, throttleAllows(12345, now.Add(time.Second)), "间隔内应被节流")
	assert.True(t, throttleAllows(12345, now.Add(keySaturationRefreshInterval+time.Second)), "超过间隔后应允许")
	assert.True(t, throttleAllows(54321, now), "不同渠道互不影响")
}
