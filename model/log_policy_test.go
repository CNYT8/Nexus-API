package model

import (
	"context"
	"encoding/json"
	"strconv"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func newLogPolicyTestDB(t *testing.T, models ...interface{}) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	connection, err := db.DB()
	require.NoError(t, err)
	connection.SetMaxOpenConns(1)
	t.Cleanup(func() { require.NoError(t, connection.Close()) })
	require.NoError(t, db.AutoMigrate(models...))
	return db
}

func setLogPolicyForTest(t *testing.T, days int, enabled bool) {
	t.Helper()
	settings := config.GlobalConfig.Get("log_setting").(*operation_setting.LogSetting)
	common.OptionMapRWMutex.Lock()
	original := *settings
	*settings = operation_setting.LogSetting{MaxRangeDays: days, UserExportEnabled: enabled}
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		*settings = original
		common.OptionMapRWMutex.Unlock()
	})
}

func TestLogPolicyConfiguredWindowBoundaries(t *testing.T) {
	const end int64 = 2000000000
	for _, days := range []int{7, 31, 365, 730} {
		t.Run(strconv.Itoa(days), func(t *testing.T) {
			setLogPolicyForTest(t, days, true)
			start, normalizedEnd, err := NormalizeUserLogWindow(0, end)
			require.NoError(t, err)
			window := days
			if window > 31 {
				window = 31
			}
			require.Equal(t, end, normalizedEnd)
			require.Equal(t, int64(window)*86400, normalizedEnd-start)
			_, _, err = NormalizeUserLogWindow(end-int64(days)*86400, end)
			require.NoError(t, err)
			_, _, err = NormalizeUserLogWindow(end-int64(days)*86400-1, end)
			require.Error(t, err)
			_, _, err = NormalizeUserLogWindow(end+1, end)
			require.Error(t, err)
			_, _, err = NormalizeUserLogWindow(1, -1)
			require.Error(t, err)
			start, normalizedEnd, err = NormalizeUserLogWindow(0, 1)
			require.NoError(t, err)
			require.Zero(t, start)
			require.EqualValues(t, 1, normalizedEnd)
		})
	}
}

func TestLogPolicyOptionValidationBeforePersistence(t *testing.T) {
	db := newLogPolicyTestDB(t, &Option{})
	originalDB := DB
	DB = db
	t.Cleanup(func() { DB = originalDB })
	setLogPolicyForTest(t, 365, true)
	common.OptionMapRWMutex.Lock()
	originalOptions := common.OptionMap
	common.OptionMap = make(map[string]string)
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		common.OptionMap = originalOptions
		common.OptionMapRWMutex.Unlock()
	})
	for _, value := range []string{"0", "-1", "1.5", "365.0", "9223372036854775808"} {
		require.Error(t, UpdateOption(operation_setting.LogMaxRangeDaysOptionKey, value))
		require.Error(t, UpdateOptionsBulk(map[string]string{
			operation_setting.LogMaxRangeDaysOptionKey:      value,
			operation_setting.LogUserExportEnabledOptionKey: "false",
		}))
	}
	require.Error(t, UpdateOption(operation_setting.LogUserExportEnabledOptionKey, "TRUE"))
	require.Error(t, UpdateOption("log_setting.unrecognized", "1"))
	var count int64
	require.NoError(t, db.Model(&Option{}).Count(&count).Error)
	require.Zero(t, count, "invalid updates must never reach persistence")
	require.Equal(t, operation_setting.LogSetting{MaxRangeDays: 365, UserExportEnabled: true}, operation_setting.GetLogSetting())
	require.NoError(t, UpdateOptionsBulk(map[string]string{
		operation_setting.LogMaxRangeDaysOptionKey:      "31",
		operation_setting.LogUserExportEnabledOptionKey: "false",
	}))
	require.Equal(t, operation_setting.LogSetting{MaxRangeDays: 31, UserExportEnabled: false}, operation_setting.GetLogSetting())
	var persisted Option
	require.NoError(t, db.Where(&Option{Key: operation_setting.LogMaxRangeDaysOptionKey}).First(&persisted).Error)
	require.Equal(t, "31", persisted.Value)
	require.NoError(t, UpdateOption(operation_setting.LogMaxRangeDaysOptionKey, "365"))
	require.Equal(t, 365, operation_setting.GetLogSetting().MaxRangeDays)
}

func TestLogExportLeasePerUserCancellationAndRelease(t *testing.T) {
	originalRedis := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = originalRedis })
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx, release, err := AcquireLogExportLease(parent, 27001)
	require.NoError(t, err)
	defer release()
	var contenders sync.WaitGroup
	results := make(chan error, 16)
	for i := 0; i < cap(results); i++ {
		contenders.Add(1)
		go func() {
			defer contenders.Done()
			_, stop, err := AcquireLogExportLease(context.Background(), 27001)
			if stop != nil {
				stop()
			}
			results <- err
		}()
	}
	contenders.Wait()
	close(results)
	for err := range results {
		require.ErrorIs(t, err, ErrLogExportInProgress)
	}
	_, otherRelease, err := AcquireLogExportLease(context.Background(), 27002)
	require.NoError(t, err)
	otherRelease()
	cancel()
	require.ErrorIs(t, ctx.Err(), context.Canceled)
	// Cancellation stops the stream; ownership remains until its handler exits.
	_, _, err = AcquireLogExportLease(context.Background(), 27001)
	require.ErrorIs(t, err, ErrLogExportInProgress)
	release()
	release()
	_, nextRelease, err := AcquireLogExportLease(context.Background(), 27001)
	require.NoError(t, err)
	nextRelease()
	_, _, err = AcquireLogExportLease(parent, 27001)
	require.ErrorIs(t, err, context.Canceled)
	_, _, err = AcquireLogExportLease(context.Background(), 0)
	require.Error(t, err)
}

func TestLogExportLeaseRedisUnavailableFailsClosed(t *testing.T) {
	originalRedis, originalClient := common.RedisEnabled, common.RDB
	common.RedisEnabled, common.RDB = true, nil
	t.Cleanup(func() { common.RedisEnabled, common.RDB = originalRedis, originalClient })
	_, _, err := AcquireLogExportLease(context.Background(), 27003)
	require.Error(t, err)
	require.NotErrorIs(t, err, ErrLogExportInProgress)
	common.RedisEnabled = false
	_, release, err := AcquireLogExportLease(context.Background(), 27003)
	require.NoError(t, err, "an acquisition failure must release the local owner slot")
	release()
}

func TestLogExportUserPrivacyAndExactBillingMetadata(t *testing.T) {
	logs := []*Log{
		{Id: 901, Type: LogTypeConsume, ModelName: "public-model", ChannelId: 9, ChannelName: "private-channel", Other: `{"number":9007199254740993,"model_ratio":1.25,"upstream_model_name":"private-model","is_model_mapped":true,"channel_id":9,"channel_name":"private-channel","admin_info":{"use_channel":[9]},"root_info":{"secret":"root"},"audit_info":{"token_ref":"private"},"stream_status":{"error":"private"}}`},
		{Id: 900, Type: LogTypeError, Content: "https://private-channel/upstream private-model secret-key", ModelName: "private-model", ChannelId: 9, ChannelName: "private-channel", RequestId: "client-correlation", Other: `{"unrecognized_diagnostic":"private-model"}`},
	}
	FormatUserLogExport(logs, 200)
	require.Equal(t, 201, logs[0].Id)
	require.Equal(t, 202, logs[1].Id)
	require.Equal(t, "public-model", logs[0].ModelName)
	var metadata map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(logs[0].Other), &metadata))
	require.Equal(t, "9007199254740993", string(metadata["number"]))
	require.Equal(t, "1.25", string(metadata["model_ratio"]))
	encoded, err := json.Marshal(logs)
	require.NoError(t, err)
	for _, secret := range []string{"private-channel", "private-model", "secret-key", "token_ref", "use_channel", "stream_status"} {
		require.NotContains(t, string(encoded), secret)
	}
	for _, entry := range logs {
		require.Zero(t, entry.ChannelId)
		require.Empty(t, entry.ChannelName)
	}
	require.Empty(t, logs[1].ModelName)
	require.Equal(t, "{}", logs[1].Other)
	require.Equal(t, "client-correlation", logs[1].RequestId)
}

func TestLogExportChannelNamesUsePrimaryDatabase(t *testing.T) {
	primary := newLogPolicyTestDB(t, &Channel{})
	logDB := newLogPolicyTestDB(t, &Log{})
	originalDB, originalLogDB := DB, LOG_DB
	DB, LOG_DB = primary, logDB
	t.Cleanup(func() { DB, LOG_DB = originalDB, originalLogDB })
	channel := Channel{Name: "primary-channel", Key: "private-key"}
	require.NoError(t, primary.Create(&channel).Error)
	rows := []*Log{{ChannelId: channel.Id}, {ChannelId: channel.Id}, {ChannelId: channel.Id + 1000}, nil}
	require.NoError(t, PopulateLogExportChannelNames(context.Background(), rows))
	require.Equal(t, "primary-channel", rows[0].ChannelName)
	require.Equal(t, "primary-channel", rows[1].ChannelName)
	require.Empty(t, rows[2].ChannelName)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, PopulateLogExportChannelNames(ctx, rows), context.Canceled)
}
