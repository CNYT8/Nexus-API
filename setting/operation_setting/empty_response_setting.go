package operation_setting

import (
	"errors"
	"math"

	"github.com/QuantumNous/new-api/setting/config"
)

const (
	defaultEmptyResponsePeriodDays    = 3
	defaultEmptyResponseRefundPercent = 70
	minEmptyResponsePeriodDays        = 1
	maxEmptyResponsePeriodDays        = 30
	maxEmptyResponseRefundPercent     = 100
)

type EmptyResponseSetting struct {
	Enabled       bool `json:"enabled"`
	PeriodDays    int  `json:"period_days"`
	RefundPercent int  `json:"refund_percent"`
}

var emptyResponseSetting = EmptyResponseSetting{
	Enabled:       false,
	PeriodDays:    defaultEmptyResponsePeriodDays,
	RefundPercent: defaultEmptyResponseRefundPercent,
}

func init() {
	config.GlobalConfig.Register("empty_response_setting", &emptyResponseSetting)
}

func NormalizeEmptyResponseSetting(value EmptyResponseSetting) EmptyResponseSetting {
	if value.PeriodDays <= 0 {
		value.PeriodDays = defaultEmptyResponsePeriodDays
	}
	if value.PeriodDays > maxEmptyResponsePeriodDays {
		value.PeriodDays = maxEmptyResponsePeriodDays
	}
	if value.RefundPercent <= 0 {
		value.RefundPercent = defaultEmptyResponseRefundPercent
	}
	if value.RefundPercent > maxEmptyResponseRefundPercent {
		value.RefundPercent = maxEmptyResponseRefundPercent
	}
	return value
}

func ValidateEmptyResponseSetting(value EmptyResponseSetting) error {
	if value.PeriodDays < minEmptyResponsePeriodDays || value.PeriodDays > maxEmptyResponsePeriodDays {
		return errors.New("记录周期必须在 1 到 30 天之间")
	}
	if value.RefundPercent < 1 || value.RefundPercent > maxEmptyResponseRefundPercent {
		return errors.New("赔付百分比必须在 1 到 100 之间")
	}
	return nil
}

func GetEmptyResponseSetting() EmptyResponseSetting {
	return NormalizeEmptyResponseSetting(emptyResponseSetting)
}

func SetEmptyResponseSetting(value EmptyResponseSetting) error {
	value = NormalizeEmptyResponseSetting(value)
	if err := ValidateEmptyResponseSetting(value); err != nil {
		return err
	}
	emptyResponseSetting = value
	return nil
}

func CalcEmptyResponseRefundQuota(quota int, percent int) int {
	if quota <= 0 || percent <= 0 || percent > maxEmptyResponseRefundPercent {
		return 0
	}
	return int(math.Ceil(float64(quota) * float64(percent) / 100))
}
