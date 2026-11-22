package model

import (
	"errors"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func setupRebateConfigTest(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:rebate-config-"+common.GetUUID()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(&Option{}))
	oldDB := DB
	oldSetting := operation_setting.GetRebateSetting()
	common.OptionMapRWMutex.Lock()
	oldOptions := common.OptionMap
	common.OptionMap = make(map[string]string)
	common.OptionMapRWMutex.Unlock()
	DB = db
	require.NoError(t, operation_setting.UpdateRebateSetting(operation_setting.DefaultRebateSetting()))
	t.Cleanup(func() {
		DB = oldDB
		require.NoError(t, operation_setting.UpdateRebateSetting(oldSetting))
		common.OptionMapRWMutex.Lock()
		common.OptionMap = oldOptions
		common.OptionMapRWMutex.Unlock()
		require.NoError(t, sqlDB.Close())
	})
	return db
}

func copyRebateFields(fields map[string]string) map[string]string {
	result := make(map[string]string, len(fields))
	for key, value := range fields {
		result[key] = value
	}
	return result
}

func TestRebateConfigRequiresCompleteValidatedGroup(t *testing.T) {
	setupRebateConfigTest(t)
	before, err := GetRebateConfig()
	require.NoError(t, err)
	for _, modify := range []func(map[string]string){
		func(fields map[string]string) { fields["rate_basis_points"] = "10001" },
		func(fields map[string]string) { fields["inviter_eligible_money_limit_micros"] = "-1" },
		func(fields map[string]string) { fields["payout_interval_hours"] = "0" },
		func(fields map[string]string) { fields["site_timezone"] = "not/a/timezone" },
		func(fields map[string]string) { delete(fields, "inviter_reward_limit_micros") },
		func(fields map[string]string) { fields["unknown"] = "1" },
	} {
		fields := copyRebateFields(before.Fields)
		fields["enabled"] = "true"
		modify(fields)
		_, err := UpdateRebateConfig(fields, before.Revision)
		require.Error(t, err)
		after, err := GetRebateConfig()
		require.NoError(t, err)
		require.Equal(t, before, after)
		require.False(t, operation_setting.GetRebateSetting().Enabled)
	}
	fields := copyRebateFields(before.Fields)
	fields["enabled"] = "true"
	fields["inviter_reward_limit_micros"] = "9223372036854775807"
	after, err := UpdateRebateConfig(fields, before.Revision)
	require.NoError(t, err)
	require.Equal(t, fields, after.Fields)
	require.NotEqual(t, before.Revision, after.Revision)
	require.Equal(t, "9223372036854775807", after.Fields["inviter_reward_limit_micros"])
	require.True(t, operation_setting.GetRebateSetting().Enabled)
}

func TestRebateConfigStorageFailureRollsBackEnabledAndCache(t *testing.T) {
	db := setupRebateConfigTest(t)
	before, err := GetRebateConfig()
	require.NoError(t, err)
	_, err = UpdateRebateConfig(before.Fields, before.Revision)
	require.NoError(t, err)
	cacheBefore := operation_setting.GetRebateSetting()
	// The enabled row sorts before this cap: fail after an earlier write has
	// happened, proving the transaction rolls it back rather than just validation.
	require.NoError(t, db.Exec(`CREATE TRIGGER reject_rebate_cap BEFORE UPDATE ON options
WHEN NEW.key = 'rebate_setting.inviter_eligible_money_limit_micros'
BEGIN SELECT RAISE(ABORT, 'injected rebate cap failure'); END`).Error)
	fields := copyRebateFields(before.Fields)
	fields["enabled"] = "true"
	fields["inviter_eligible_money_limit_micros"] = "1234567"
	_, err = UpdateRebateConfig(fields, before.Revision)
	require.ErrorContains(t, err, "injected rebate cap failure")
	after, err := GetRebateConfig()
	require.NoError(t, err)
	require.Equal(t, before, after)
	require.Equal(t, cacheBefore, operation_setting.GetRebateSetting())
	common.OptionMapRWMutex.RLock()
	cachedEnabled := common.OptionMap["rebate_setting.enabled"]
	common.OptionMapRWMutex.RUnlock()
	require.Equal(t, "false", cachedEnabled)
}

func TestRebateConfigLegacyWriteUsesPersistedSnapshotAndInvalidatesRevision(t *testing.T) {
	db := setupRebateConfigTest(t)
	before, err := GetRebateConfig()
	require.NoError(t, err)
	// Simulate another instance writing the database without refreshing this
	// process's cache. A legacy single-field save must preserve that value.
	require.NoError(t, db.Clauses(clause.OnConflict{UpdateAll: true}).Create(&Option{
		Key: "rebate_setting.inviter_reward_limit_micros", Value: "9007199254740993",
	}).Error)
	require.NoError(t, UpdateOption("rebate_setting.rate_basis_points", "725"))
	after, err := GetRebateConfig()
	require.NoError(t, err)
	require.Equal(t, "9007199254740993", after.Fields["inviter_reward_limit_micros"])
	require.Equal(t, "725", after.Fields["rate_basis_points"])
	_, err = UpdateRebateConfig(before.Fields, before.Revision)
	require.ErrorIs(t, err, ErrRebateConfigConflict)
	stale := operation_setting.DefaultRebateSetting()
	require.NoError(t, operation_setting.UpdateRebateSetting(stale))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		setting, err := GetRebateSettingTx(tx)
		if err == nil {
			require.EqualValues(t, 725, setting.RateBasisPoints)
			require.EqualValues(t, 9007199254740993, setting.InviterRewardLimit)
		}
		return err
	}))
	require.Error(t, UpdateOption(rebateConfigBarrierOptionKey, "tampered"))
}

func TestRebateConfigConcurrentSameRevisionHasSingleWinner(t *testing.T) {
	setupRebateConfigTest(t)
	before, err := GetRebateConfig()
	require.NoError(t, err)
	start := make(chan struct{})
	results := make(chan error, 2)
	var workers sync.WaitGroup
	for _, rate := range []string{"100", "200"} {
		fields := copyRebateFields(before.Fields)
		fields["rate_basis_points"] = rate
		workers.Add(1)
		go func(fields map[string]string) {
			defer workers.Done()
			<-start
			_, err := UpdateRebateConfig(fields, before.Revision)
			results <- err
		}(fields)
	}
	close(start)
	workers.Wait()
	close(results)
	var successes, conflicts int
	for err := range results {
		if err == nil {
			successes++
		} else if errors.Is(err, ErrRebateConfigConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	require.Equal(t, 1, successes)
	require.Equal(t, 1, conflicts)
}
