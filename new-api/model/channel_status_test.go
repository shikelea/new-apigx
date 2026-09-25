package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupChannelStatusTest(t *testing.T) {
	t.Helper()
	truncateTables(t)
	require.NoError(t, DB.Exec("DELETE FROM abilities").Error)
	require.NoError(t, DB.Exec("DELETE FROM channels").Error)

	memoryCacheEnabled := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = false
	t.Cleanup(func() {
		common.MemoryCacheEnabled = memoryCacheEnabled
	})
}

func TestUpdateChannelStatusPersistsMultiKeyState(t *testing.T) {
	setupChannelStatusTest(t)

	channel := Channel{
		Name:   "multi-key-status",
		Key:    "key-a\nkey-b",
		Status: common.ChannelStatusEnabled,
		ChannelInfo: ChannelInfo{
			IsMultiKey:           true,
			MultiKeySize:         2,
			MultiKeyMode:         constant.MultiKeyModePolling,
			MultiKeyPollingIndex: 1,
		},
	}
	require.NoError(t, DB.Create(&channel).Error)

	changed := UpdateChannelStatus(channel.Id, "key-a", common.ChannelStatusAutoDisabled, "provider rejected key")
	require.True(t, changed)

	var stored Channel
	require.NoError(t, DB.First(&stored, channel.Id).Error)
	assert.Equal(t, common.ChannelStatusEnabled, stored.Status)
	assert.Equal(t, common.ChannelStatusAutoDisabled, stored.ChannelInfo.MultiKeyStatusList[0])
	assert.Equal(t, "provider rejected key", stored.ChannelInfo.MultiKeyDisabledReason[0])
	assert.NotZero(t, stored.ChannelInfo.MultiKeyDisabledTime[0])
	assert.Equal(t, 1, stored.ChannelInfo.MultiKeyPollingIndex)
}

func TestWeightedPollingAndStableStatusSurviveReorder(t *testing.T) {
	setupChannelStatusTest(t)

	channel := Channel{
		Name:   "stable-weighted-pool",
		Key:    "key-a\nkey-b",
		Status: common.ChannelStatusEnabled,
		ChannelInfo: ChannelInfo{
			IsMultiKey:        true,
			MultiKeySize:      2,
			MultiKeyMode:      constant.MultiKeyModePolling,
			MultiKeyStableIDs: []string{"stable-a", "stable-b"},
			MultiKeyWeights:   []int64{3, 1},
		},
	}
	require.NoError(t, DB.Create(&channel).Error)

	counts := map[string]int{}
	for range 8 {
		key, _, apiErr := channel.GetNextEnabledKey()
		require.Nil(t, apiErr)
		counts[key]++
	}
	assert.Equal(t, 6, counts["key-a"])
	assert.Equal(t, 2, counts["key-b"])

	require.True(t, UpdateChannelStatus(channel.Id, "key-a", common.ChannelStatusAutoDisabled, "provider rejected key"))
	var stored Channel
	require.NoError(t, DB.First(&stored, channel.Id).Error)
	assert.Equal(t, common.ChannelStatusAutoDisabled, stored.ChannelInfo.MultiKeyStatusByStableID["stable-a"])

	// 重排真实槽位后故障仍跟随 stable-a；旧索引镜像指向 0 也不能误伤现在位于 0 的 stable-b。
	stored.Key = "key-b\nkey-a"
	stored.ChannelInfo.MultiKeyStableIDs = []string{"stable-b", "stable-a"}
	stored.ChannelInfo.MultiKeyWeights = []int64{1, 3}
	stored.ChannelInfo.MultiKeyPollingIndex = 0
	require.NoError(t, DB.Model(&Channel{}).Where("id = ?", channel.Id).
		Select("key", "channel_info").Updates(&stored).Error)

	for range 4 {
		key, index, apiErr := stored.GetNextEnabledKey()
		require.Nil(t, apiErr)
		assert.Equal(t, "key-b", key)
		assert.Equal(t, 0, index)
	}
}

func TestGetNextEnabledKeyExcludingNeverRepeatsFailedKey(t *testing.T) {
	setupChannelStatusTest(t)

	for _, mode := range []constant.MultiKeyMode{constant.MultiKeyModePolling, constant.MultiKeyModeRandom} {
		t.Run(string(mode), func(t *testing.T) {
			channel := Channel{
				Name:   "request-exclusion",
				Key:    "key-a\nkey-b\nkey-c",
				Status: common.ChannelStatusEnabled,
				ChannelInfo: ChannelInfo{
					IsMultiKey:        true,
					MultiKeySize:      3,
					MultiKeyMode:      mode,
					MultiKeyStableIDs: []string{"stable-a", "stable-b", "stable-c"},
					MultiKeyWeights:   []int64{16, 1, 1},
				},
			}
			require.NoError(t, DB.Create(&channel).Error)

			excluded := MultiKeyExclusions{StableIDs: map[string]struct{}{
				"stable-a": {},
				"stable-b": {},
			}}
			for range 8 {
				key, index, apiErr := channel.GetNextEnabledKeyExcluding(excluded)
				require.Nil(t, apiErr)
				assert.Equal(t, "key-c", key)
				assert.Equal(t, 2, index)
			}
			assert.True(t, channel.HasEnabledKeyExcluding(excluded))

			excluded.StableIDs["stable-c"] = struct{}{}
			_, _, apiErr := channel.GetNextEnabledKeyExcluding(excluded)
			require.NotNil(t, apiErr)
			assert.False(t, channel.HasEnabledKeyExcluding(excluded))
		})
	}
}

func TestMultiKeyCooldownSkipsThenAutomaticallyExpires(t *testing.T) {
	setupChannelStatusTest(t)

	channel := Channel{
		Name:   "temporary-key-cooldown",
		Key:    "key-a\nkey-b",
		Status: common.ChannelStatusEnabled,
		ChannelInfo: ChannelInfo{
			IsMultiKey:        true,
			MultiKeySize:      2,
			MultiKeyMode:      constant.MultiKeyModePolling,
			MultiKeyStableIDs: []string{"stable-a", "stable-b"},
		},
	}
	referenceTime := int64(10_000)
	channel.ChannelInfo.SetMultiKeyCooldownUntilAt(0, referenceTime+60)
	require.NoError(t, DB.Create(&channel).Error)

	var reloaded Channel
	require.NoError(t, DB.First(&reloaded, channel.Id).Error)
	assert.False(t, reloaded.ChannelInfo.MultiKeySelectableAt(0, referenceTime))
	assert.True(t, reloaded.ChannelInfo.MultiKeySelectableAt(1, referenceTime))
	assert.True(t, reloaded.ChannelInfo.MultiKeySelectableAt(0, referenceTime+60),
		"deadline expiry must restore selection without rewriting the pool")

	// Stable identity remains authoritative after a key reorder.
	reloaded.Key = "key-b\nkey-a"
	reloaded.ChannelInfo.MultiKeyStableIDs = []string{"stable-b", "stable-a"}
	assert.True(t, reloaded.ChannelInfo.MultiKeySelectableAt(0, referenceTime))
	assert.False(t, reloaded.ChannelInfo.MultiKeySelectableAt(1, referenceTime))
}

func TestMultiKeyExclusionFollowsStableIdentityAfterReorder(t *testing.T) {
	setupChannelStatusTest(t)

	channel := Channel{
		Name:   "request-exclusion-reorder",
		Key:    "key-b\nkey-a",
		Status: common.ChannelStatusEnabled,
		ChannelInfo: ChannelInfo{
			IsMultiKey:        true,
			MultiKeySize:      2,
			MultiKeyMode:      constant.MultiKeyModePolling,
			MultiKeyStableIDs: []string{"stable-b", "stable-a"},
		},
	}
	require.NoError(t, DB.Create(&channel).Error)

	key, index, apiErr := channel.GetNextEnabledKeyExcluding(MultiKeyExclusions{
		StableIDs: map[string]struct{}{"stable-a": {}},
		Indexes:   map[int]struct{}{0: {}},
	})
	require.Nil(t, apiErr)
	assert.Equal(t, "key-b", key)
	assert.Equal(t, 0, index, "stable identity takes precedence over a stale positional exclusion")
}

func TestMultiKeyStatusSourcesRestoreIndependently(t *testing.T) {
	info := ChannelInfo{
		IsMultiKey:        true,
		MultiKeySize:      1,
		MultiKeyStableIDs: []string{"stable-a"},
	}

	info.SetMultiKeyStatusAt(0, common.ChannelStatusAutoDisabled, "provider rejected key", 100)
	require.True(t, info.ReplaceMultiKeyStatusMarks(90, []int{0}))
	require.True(t, info.ReplaceMultiKeyStatusMarks(91, []int{0}))
	assert.Equal(t, common.ChannelStatusAutoDisabled, info.MultiKeyStatusAt(0),
		"底座健康状态优先作为兼容镜像，但两个 APIGX 来源都必须被保留")
	assert.True(t, info.HasMultiKeyStatusMark(90))
	assert.True(t, info.HasMultiKeyStatusMark(91))

	info.SetMultiKeyStatusAt(0, common.ChannelStatusEnabled, "", 0)
	assert.Equal(t, 91, info.MultiKeyStatusAt(0), "恢复底座健康不得清掉熔断和容量来源")

	require.True(t, info.ReplaceMultiKeyStatusMarks(91, nil))
	assert.Equal(t, 90, info.MultiKeyStatusAt(0), "熔断恢复后容量来源仍应让 Key 不可选")
	assert.False(t, info.HasMultiKeyStatusMark(91))
	assert.True(t, info.HasMultiKeyStatusMark(90))

	require.True(t, info.ReplaceMultiKeyStatusMarks(90, nil))
	assert.Equal(t, common.ChannelStatusEnabled, info.MultiKeyStatusAt(0))
	assert.Empty(t, info.MultiKeyStatusList)
	assert.Empty(t, info.MultiKeyStatusByStableID)
	assert.Empty(t, info.MultiKeyStatusSourcesByStableID)
}

func TestSaveStatusStateFromSingleKeySnapshotPreservesUnownedColumns(t *testing.T) {
	setupChannelStatusTest(t)

	channel := Channel{
		Name:        "single-key-status",
		Key:         "original-key",
		Status:      common.ChannelStatusEnabled,
		Models:      "original-model",
		Group:       "default",
		UsedQuota:   100,
		ChannelInfo: ChannelInfo{},
	}
	require.NoError(t, DB.Create(&channel).Error)

	stale, err := GetChannelById(channel.Id, true)
	require.NoError(t, err)

	concurrentChannelInfo := ChannelInfo{
		IsMultiKey:           true,
		MultiKeySize:         2,
		MultiKeyMode:         constant.MultiKeyModePolling,
		MultiKeyPollingIndex: 1,
	}
	require.NoError(t, DB.Model(&Channel{}).Where("id = ?", channel.Id).Updates(map[string]any{
		"key":          "rotated-key",
		"used_quota":   gorm.Expr("used_quota + ?", 250),
		"models":       "concurrent-model",
		"channel_info": concurrentChannelInfo,
	}).Error)

	stale.Status = common.ChannelStatusManuallyDisabled
	stale.SetOtherInfo(map[string]any{
		"status_reason": "manual operation",
		"status_time":   int64(1234),
	})
	require.NoError(t, stale.saveStatusState())

	var stored Channel
	require.NoError(t, DB.First(&stored, channel.Id).Error)
	assert.Equal(t, common.ChannelStatusManuallyDisabled, stored.Status)
	assert.Equal(t, "rotated-key", stored.Key)
	assert.Equal(t, int64(350), stored.UsedQuota)
	assert.Equal(t, "concurrent-model", stored.Models)
	assert.Equal(t, concurrentChannelInfo, stored.ChannelInfo)

	otherInfo := stored.GetOtherInfo()
	assert.Equal(t, "manual operation", otherInfo["status_reason"])
	assert.Equal(t, float64(1234), otherInfo["status_time"])
}
