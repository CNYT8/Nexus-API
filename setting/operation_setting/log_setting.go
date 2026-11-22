package operation_setting

import (
	"errors"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/config"
)

const (
	LogMaxRangeDaysOptionKey      = "log_setting.max_range_days"
	LogUserExportEnabledOptionKey = "log_setting.user_export_enabled"
	DefaultLogMaxRangeDays        = 365
	LegacyLogDefaultRangeDays     = 31
	// Bound multiplication by seconds/day even when options come from storage.
	MaxLogRangeDays int64 = (1<<63 - 1) / 86400
)

type LogSetting struct {
	UserExportEnabled bool `json:"user_export_enabled"`
	MaxRangeDays      int  `json:"max_range_days"`
}

var logSetting = LogSetting{UserExportEnabled: true, MaxRangeDays: DefaultLogMaxRangeDays}

func init() {
	config.GlobalConfig.Register("log_setting", &logSetting)
}

// Return a snapshot under the same lock used by option updates.
func GetLogSetting() LogSetting {
	common.OptionMapRWMutex.RLock()
	value := logSetting
	common.OptionMapRWMutex.RUnlock()
	if value.MaxRangeDays < 1 || int64(value.MaxRangeDays) > MaxLogRangeDays {
		value.MaxRangeDays = DefaultLogMaxRangeDays
	}
	return value
}

func (s LogSetting) MaxRangeSeconds() int64 {
	if s.MaxRangeDays < 1 || int64(s.MaxRangeDays) > MaxLogRangeDays {
		return DefaultLogMaxRangeDays * 86400
	}
	return int64(s.MaxRangeDays) * 86400
}

func (s LogSetting) DefaultRangeDays() int {
	days := int(s.MaxRangeSeconds() / 86400)
	if days > LegacyLogDefaultRangeDays {
		return LegacyLogDefaultRangeDays
	}
	return days
}

func ValidateLogSettingField(field, value string) error {
	switch field {
	case "max_range_days":
		days, err := strconv.Atoi(value)
		if err != nil || days < 1 || int64(days) > MaxLogRangeDays {
			return errors.New("日志查询最大跨度必须为正整数天数且不能超出时间戳范围")
		}
	case "user_export_enabled":
		if value != "true" && value != "false" {
			return errors.New("普通用户日志导出开关必须为 true 或 false")
		}
	default:
		return errors.New("未知日志配置项")
	}
	return nil
}
