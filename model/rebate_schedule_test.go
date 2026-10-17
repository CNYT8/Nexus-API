package model

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/require"
)

func TestRebatePayoutScheduleAlignment(t *testing.T) {
	for _, tc := range []struct {
		name, mode, zone, now, want string
		hour, minute, interval      int
	}{
		{"daily-before", "scheduled", "Asia/Shanghai", "2026-09-27T22:59:59Z", "2026-09-27T23:00:00Z", 7, 0, 24},
		{"daily-boundary", "scheduled", "Asia/Shanghai", "2026-09-27T23:00:00Z", "2026-09-28T23:00:00Z", 7, 0, 24},
		{"half-day-morning", "half_day", "UTC", "2026-09-27T06:00:00Z", "2026-09-27T07:30:00Z", 7, 30, 24},
		{"half-day-boundary", "half_day", "UTC", "2026-09-27T07:30:00Z", "2026-09-27T19:30:00Z", 7, 30, 24},
		{"half-day-wrapped", "half_day", "UTC", "2026-09-27T20:30:00Z", "2026-09-28T08:30:00Z", 20, 30, 24},
		{"custom-epoch-7h", "custom", "UTC", "1970-01-02T07:30:00Z", "1970-01-02T11:30:00Z", 7, 30, 7},
		{"custom-before-epoch", "custom", "UTC", "1969-12-31T23:59:59Z", "1970-01-01T00:00:00Z", 0, 0, 1},
		{"custom-720h", "custom", "UTC", "1970-01-01T07:30:00Z", "1970-01-31T07:30:00Z", 7, 30, 720},
		{"custom-zone", "custom", "Asia/Shanghai", "1969-12-31T23:30:00Z", "1970-01-01T00:30:00Z", 7, 30, 1},
		{"daily-spring-dst", "scheduled", "America/New_York", "2026-03-07T12:00:00Z", "2026-03-08T11:00:00Z", 7, 0, 24},
		{"daily-fall-dst", "scheduled", "America/New_York", "2026-10-31T11:00:00Z", "2026-11-01T12:00:00Z", 7, 0, 24},
		{"daily-midnight-gap", "scheduled", "America/New_York", "2026-03-08T07:00:00Z", "2026-03-09T06:30:00Z", 2, 30, 24},
		{"immediate", "immediate", "UTC", "2026-09-27T13:44:12Z", "2026-09-27T13:44:12Z", 7, 0, 24},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setting := rebateRewardSetting()
			setting.PayoutMode, setting.SiteTimezone = tc.mode, tc.zone
			setting.PayoutHour, setting.PayoutMinute, setting.PayoutIntervalHours = tc.hour, tc.minute, tc.interval
			now, err := time.Parse(time.RFC3339, tc.now)
			require.NoError(t, err)
			next, err := nextRebatePayoutTime(setting, now)
			require.NoError(t, err)
			require.Equal(t, tc.want, next.Format(time.RFC3339))
		})
	}
}

func TestRebateCapPeriodUsesSiteCalendar(t *testing.T) {
	setting := rebateRewardSetting()
	setting.SiteTimezone = "Asia/Shanghai"
	now := time.Date(2026, 9, 30, 16, 0, 0, 0, time.UTC)
	for _, tc := range []struct{ period, want, capType string }{
		{operation_setting.RebateLimitPeriodDaily, "2026-10-01", RebateCapTypeInviterDaily},
		{operation_setting.RebateLimitPeriodMonthly, "2026-10", RebateCapTypeInviterMonthly},
		{operation_setting.RebateLimitPeriodPermanent, "lifetime", RebateCapTypeInviterLifetime},
	} {
		setting.InviterLimitPeriod = tc.period
		period, capType, err := rebateInviterCapIdentity(setting, now)
		require.NoError(t, err)
		require.Equal(t, tc.want, period)
		require.Equal(t, tc.capType, capType)
	}
}

func TestRebatePayoutScheduleRejectsInvalidInput(t *testing.T) {
	setting := rebateRewardSetting()
	_, err := nextRebatePayoutTime(setting, time.Time{})
	require.Error(t, err)
	for _, interval := range []int{-1, 0, 721} {
		setting.PayoutMode, setting.PayoutIntervalHours = operation_setting.RebatePayoutCustom, interval
		_, err = nextRebatePayoutTime(setting, time.Now())
		require.Error(t, err)
	}
	setting = rebateRewardSetting()
	setting.SiteTimezone = "Not/AZone"
	_, err = nextRebatePayoutTime(setting, time.Now())
	require.Error(t, err)
}
