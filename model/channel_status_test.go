package model

import (
	"errors"
	"fmt"
	"sync"
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
	channelSyncLock.Lock()
	oldChannels, oldRouting := channelsIDM, group2model2channels
	oldAdvanced := channel2advancedCustomConfig
	channelsIDM = nil
	group2model2channels = nil
	channel2advancedCustomConfig = nil
	channelSyncLock.Unlock()
	common.MemoryCacheEnabled = false
	t.Cleanup(func() {
		common.MemoryCacheEnabled = memoryCacheEnabled
		channelSyncLock.Lock()
		channelsIDM, group2model2channels = oldChannels, oldRouting
		channel2advancedCustomConfig = oldAdvanced
		channelSyncLock.Unlock()
	})
}

func newStatusTestChannel(t *testing.T, mode constant.MultiKeyMode) *Channel {
	t.Helper()
	channel := &Channel{
		Name: "status-regression", Key: "key-a\nkey-b\nkey-c",
		Status: common.ChannelStatusEnabled, Models: "test-model", Group: "default",
		ChannelInfo: ChannelInfo{IsMultiKey: true, MultiKeySize: 3, MultiKeyMode: mode},
	}
	require.NoError(t, DB.Create(channel).Error)
	require.NoError(t, DB.Create(&Ability{ChannelId: channel.Id, Group: "default", Model: "test-model", Enabled: true}).Error)
	return channel
}

func TestGetNextEnabledKeyUsesCurrentState(t *testing.T) {
	for _, cache := range []bool{false, true} {
		for _, mode := range []constant.MultiKeyMode{constant.MultiKeyModePolling, constant.MultiKeyModeRandom} {
			t.Run(fmt.Sprintf("cache=%t/mode=%s", cache, mode), func(t *testing.T) {
				setupChannelStatusTest(t)
				channel := newStatusTestChannel(t, mode)
				common.MemoryCacheEnabled = cache
				InitChannelCache()
				stale, err := CacheGetChannel(channel.Id)
				require.NoError(t, err)
				// Another request disables two keys after this caller got its snapshot.
				require.True(t, UpdateChannelStatus(channel.Id, "key-a", common.ChannelStatusAutoDisabled, "denied-a"))
				require.True(t, UpdateChannelStatus(channel.Id, "key-b", common.ChannelStatusAutoDisabled, "denied-b"))
				for i := 0; i < 4; i++ {
					key, index, apiErr := stale.GetNextEnabledKey()
					require.Nil(t, apiErr)
					assert.Equal(t, "key-c", key)
					assert.Equal(t, 2, index)
				}
				stored, err := GetChannelById(channel.Id, true)
				require.NoError(t, err)
				assert.Equal(t, map[int]int{0: common.ChannelStatusAutoDisabled, 1: common.ChannelStatusAutoDisabled}, stored.ChannelInfo.MultiKeyStatusList)
				assert.Equal(t, "denied-a", stored.ChannelInfo.MultiKeyDisabledReason[0])
				require.True(t, UpdateChannelStatus(channel.Id, "key-c", common.ChannelStatusAutoDisabled, "denied-c"))
				_, _, apiErr := stale.GetNextEnabledKey()
				require.NotNil(t, apiErr, "stale snapshots must not select disabled keys")
			})
		}
	}
}

func TestUpdateChannelStatusReenablesIndividualKey(t *testing.T) {
	for _, cache := range []bool{false, true} {
		t.Run(fmt.Sprintf("cache=%t", cache), func(t *testing.T) {
			setupChannelStatusTest(t)
			channel := newStatusTestChannel(t, constant.MultiKeyModePolling)
			common.MemoryCacheEnabled = cache
			InitChannelCache()
			require.True(t, UpdateChannelStatus(channel.Id, "key-a", common.ChannelStatusAutoDisabled, "denied"))
			// Advance cursor only in cache when cache is enabled.
			_, index, apiErr := channel.GetNextEnabledKey()
			require.Nil(t, apiErr)
			require.Equal(t, 1, index)
			require.True(t, UpdateChannelStatus(channel.Id, "key-a", common.ChannelStatusEnabled, "recovered"))
			stored, err := GetChannelById(channel.Id, true)
			require.NoError(t, err)
			assert.Equal(t, common.ChannelStatusEnabled, stored.Status)
			assert.NotContains(t, stored.ChannelInfo.MultiKeyStatusList, 0)
			assert.NotContains(t, stored.ChannelInfo.MultiKeyDisabledReason, 0)
			assert.NotContains(t, stored.ChannelInfo.MultiKeyDisabledTime, 0)
			_, index, apiErr = channel.GetNextEnabledKey()
			require.Nil(t, apiErr)
			assert.Equal(t, 2, index, "status update must preserve the newer polling cursor")
			for _, key := range channel.GetKeys() {
				require.True(t, UpdateChannelStatus(channel.Id, key, common.ChannelStatusAutoDisabled, "denied"))
			}
			require.True(t, UpdateChannelStatus(channel.Id, "key-a", common.ChannelStatusEnabled, "recovered"))
			var ability Ability
			require.NoError(t, DB.Where("channel_id = ?", channel.Id).First(&ability).Error)
			assert.True(t, ability.Enabled)
			if cache {
				selected, err := GetRandomSatisfiedChannel("default", "test-model", 0, "")
				require.NoError(t, err)
				require.NotNil(t, selected, "re-enabled channel must be restored to routing")
				assert.Equal(t, channel.Id, selected.Id)
			}
		})
	}
}

func TestUpdateChannelStatusFailureLeavesCacheAndAbilitiesUnchanged(t *testing.T) {
	for _, multi := range []bool{false, true} {
		t.Run(fmt.Sprintf("multi=%t", multi), func(t *testing.T) {
			setupChannelStatusTest(t)
			channel := newStatusTestChannel(t, constant.MultiKeyModePolling)
			channel.ChannelInfo.IsMultiKey = multi
			require.NoError(t, channel.SaveChannelInfo())
			common.MemoryCacheEnabled = true
			InitChannelCache()
			before, err := CacheGetChannel(channel.Id)
			require.NoError(t, err)
			callback := "test:fail-channel-status-update"
			require.NoError(t, DB.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
				if tx.Statement.Table == "channels" {
					tx.AddError(errors.New("injected channel update failure"))
				}
			}))
			t.Cleanup(func() { DB.Callback().Update().Remove(callback) })
			// Empty key requests aggregate status; nonempty key exercises map mutation.
			for _, key := range []string{"key-a", ""} {
				require.False(t, UpdateChannelStatus(channel.Id, key, common.ChannelStatusAutoDisabled, "denied"))
				after, err := CacheGetChannel(channel.Id)
				require.NoError(t, err)
				assert.Equal(t, before, after)
				assert.Equal(t, common.ChannelStatusEnabled, after.Status)
				assert.Empty(t, after.ChannelInfo.MultiKeyStatusList)
				stored, err := GetChannelById(channel.Id, true)
				require.NoError(t, err)
				assert.Equal(t, common.ChannelStatusEnabled, stored.Status)
				assert.Empty(t, stored.ChannelInfo.MultiKeyStatusList)
				var ability Ability
				require.NoError(t, DB.Where("channel_id = ?", channel.Id).First(&ability).Error)
				assert.True(t, ability.Enabled)
				selected, err := GetRandomSatisfiedChannel("default", "test-model", 0, "")
				require.NoError(t, err)
				require.NotNil(t, selected)
			}
		})
	}
}

func TestGetNextEnabledKeySingleKeyNeedsNoCacheEntry(t *testing.T) {
	setupChannelStatusTest(t)
	common.MemoryCacheEnabled = true
	channel := Channel{Key: "single-key"}
	key, index, apiErr := channel.GetNextEnabledKey()
	require.Nil(t, apiErr)
	assert.Equal(t, "single-key", key)
	assert.Zero(t, index)
}

func TestUpdateChannelStatusAbilityFailureRollsBack(t *testing.T) {
	setupChannelStatusTest(t)
	channel := newStatusTestChannel(t, constant.MultiKeyModePolling)
	common.MemoryCacheEnabled = true
	InitChannelCache()
	callback := "test:fail-ability-status-update"
	require.NoError(t, DB.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == "abilities" {
			tx.AddError(errors.New("injected ability update failure"))
		}
	}))
	t.Cleanup(func() { DB.Callback().Update().Remove(callback) })
	require.False(t, UpdateChannelStatus(channel.Id, "", common.ChannelStatusAutoDisabled, "denied"))
	stored, err := GetChannelById(channel.Id, true)
	require.NoError(t, err)
	assert.Equal(t, common.ChannelStatusEnabled, stored.Status)
	assert.Empty(t, stored.OtherInfo)
	cached, err := CacheGetChannel(channel.Id)
	require.NoError(t, err)
	assert.Equal(t, common.ChannelStatusEnabled, cached.Status)
	assert.Empty(t, cached.OtherInfo)
	selected, err := GetRandomSatisfiedChannel("default", "test-model", 0, "")
	require.NoError(t, err)
	require.NotNil(t, selected)
}

func TestChannelPollingConcurrentStatusUpdates(t *testing.T) {
	for _, cache := range []bool{false, true} {
		t.Run(fmt.Sprintf("cache=%t", cache), func(t *testing.T) {
			setupChannelStatusTest(t)
			channel := newStatusTestChannel(t, constant.MultiKeyModePolling)
			common.MemoryCacheEnabled = cache
			InitChannelCache()
			var wg sync.WaitGroup
			for worker := 0; worker < 4; worker++ {
				wg.Add(1)
				go func(worker int) {
					defer wg.Done()
					for i := 0; i < 20; i++ {
						if worker == 0 {
							if !UpdateChannelStatus(channel.Id, "key-a", common.ChannelStatusAutoDisabled, "denied") ||
								!UpdateChannelStatus(channel.Id, "key-a", common.ChannelStatusEnabled, "recovered") {
								t.Error("status update failed")
							}
						} else {
							snapshot, err := CacheGetChannel(channel.Id)
							if err != nil {
								t.Error(err)
								return
							}
							_, _, apiErr := snapshot.GetNextEnabledKey()
							if apiErr != nil {
								t.Error(apiErr)
							}
						}
					}
				}(worker)
			}
			wg.Wait()
			require.True(t, UpdateChannelStatus(channel.Id, "key-a", common.ChannelStatusAutoDisabled, "final"))
			for i := 0; i < 5; i++ {
				_, index, apiErr := channel.GetNextEnabledKey()
				require.Nil(t, apiErr)
				assert.NotEqual(t, 0, index)
			}
			stored, err := GetChannelById(channel.Id, true)
			require.NoError(t, err)
			assert.Equal(t, common.ChannelStatusAutoDisabled, stored.ChannelInfo.MultiKeyStatusList[0])
		})
	}
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
	// 轮询游标由同一个 per-channel 锁保护，不能被状态更新覆盖。
	assert.Equal(t, 1, stored.ChannelInfo.MultiKeyPollingIndex)
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
	stale.SetOtherInfo(map[string]interface{}{
		"status_reason": "manual operation",
		"status_time":   int64(1234),
	})
	require.NoError(t, stale.saveStatusState())

	var stored Channel
	require.NoError(t, DB.First(&stored, channel.Id).Error)
	assert.Equal(t, common.ChannelStatusManuallyDisabled, stored.Status)
	// 窄写只允许改 status/other_info,不能回滚并发修改的 key/models/used_quota/channel_info。
	assert.Equal(t, "rotated-key", stored.Key)
	assert.Equal(t, int64(350), stored.UsedQuota)
	assert.Equal(t, "concurrent-model", stored.Models)
	assert.Equal(t, concurrentChannelInfo, stored.ChannelInfo)

	otherInfo := stored.GetOtherInfo()
	assert.Equal(t, "manual operation", otherInfo["status_reason"])
	assert.Equal(t, float64(1234), otherInfo["status_time"])
}

// MySQL 的 map 更新在字段值无变化时会返回 RowsAffected=0,saveStatusState
// 不能把它当成失败;重复写入相同状态必须继续返回 nil。
func TestSaveStatusStateAllowsNoopUpdate(t *testing.T) {
	setupChannelStatusTest(t)

	channel := Channel{
		Name:   "noop-status",
		Key:    "noop-key",
		Status: common.ChannelStatusEnabled,
	}
	require.NoError(t, DB.Create(&channel).Error)

	require.NoError(t, channel.saveStatusState())
	require.NoError(t, channel.saveStatusState())

	var stored Channel
	require.NoError(t, DB.First(&stored, channel.Id).Error)
	assert.Equal(t, common.ChannelStatusEnabled, stored.Status)
}
