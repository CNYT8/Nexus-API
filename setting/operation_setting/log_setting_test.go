package operation_setting

import (
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

func TestLogSettingDefaultsAndValidation(t *testing.T) {
	require.Equal(t, LogSetting{UserExportEnabled: true, MaxRangeDays: 365}, GetLogSetting())
	for _, value := range []string{"1", "31", "365", "366", strconv.FormatInt(MaxLogRangeDays, 10)} {
		require.NoError(t, ValidateLogSettingField("max_range_days", value), value)
	}
	for _, value := range []string{"", "0", "-1", "31.5", "365.0", "1e2", " 365", "NaN", strconv.FormatInt(MaxLogRangeDays+1, 10), "9223372036854775808"} {
		require.Error(t, ValidateLogSettingField("max_range_days", value), value)
	}
	for _, value := range []string{"true", "false"} {
		require.NoError(t, ValidateLogSettingField("user_export_enabled", value))
	}
	for _, value := range []string{"", "TRUE", "1", " false "} {
		require.Error(t, ValidateLogSettingField("user_export_enabled", value))
	}
	require.Error(t, ValidateLogSettingField("unknown", "true"))
}

func TestLogSettingLegacyWindowAndInvalidStoredRange(t *testing.T) {
	common.OptionMapRWMutex.Lock()
	original := logSetting
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		logSetting = original
		common.OptionMapRWMutex.Unlock()
	})
	for _, tc := range []struct {
		stored, max, window int
	}{
		{0, 365, 31}, {-1, 365, 31}, {7, 7, 7}, {31, 31, 31}, {365, 365, 31}, {730, 730, 31},
	} {
		common.OptionMapRWMutex.Lock()
		logSetting = LogSetting{MaxRangeDays: tc.stored, UserExportEnabled: false}
		common.OptionMapRWMutex.Unlock()
		value := GetLogSetting()
		require.Equal(t, tc.max, value.MaxRangeDays)
		require.Equal(t, int64(tc.max)*86400, value.MaxRangeSeconds())
		require.Equal(t, tc.window, value.DefaultRangeDays())
		require.False(t, value.UserExportEnabled)
	}
	require.Equal(t, int64(365*86400), (LogSetting{MaxRangeDays: 0}).MaxRangeSeconds())
}
