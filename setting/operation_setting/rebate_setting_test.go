package operation_setting

import (
	"encoding/json"
	"math"
	"strconv"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUpdateRebateSettingFieldPreservesConcurrentUpdates(t *testing.T) {
	original := GetRebateSetting()
	t.Cleanup(func() { require.NoError(t, UpdateRebateSetting(original)) })

	initial := RebateSetting{
		PayoutMode:         RebatePayoutImmediate,
		PayoutHour:         7,
		PayoutMinute:       0,
		InviterLimitPeriod: RebateLimitPeriodMonthly,
		SiteTimezone:       "UTC",
	}
	require.NoError(t, UpdateRebateSetting(initial))

	var wg sync.WaitGroup
	errCh := make(chan error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		errCh <- UpdateRebateSettingField("rate_basis_points", "500")
	}()
	go func() {
		defer wg.Done()
		errCh <- UpdateRebateSettingField("payout_hour", "9")
	}()
	wg.Wait()
	close(errCh)
	for err := range errCh {
		require.NoError(t, err)
	}

	updated := GetRebateSetting()
	require.EqualValues(t, 500, updated.RateBasisPoints)
	require.Equal(t, 9, updated.PayoutHour)
}

func TestUpdateRebateSettingFieldRejectsInvalidValuesWithoutMutation(t *testing.T) {
	original := GetRebateSetting()
	t.Cleanup(func() { require.NoError(t, UpdateRebateSetting(original)) })

	before := GetRebateSetting()
	require.Error(t, UpdateRebateSettingField("rate_basis_points", "10001"))
	require.Error(t, UpdateRebateSettingField("site_timezone", "Invalid/Zone"))
	require.Error(t, UpdateRebateSettingField("unknown", "value"))
	require.Equal(t, before, GetRebateSetting())
}

func TestRebateSettingFixedContractAndBounds(t *testing.T) {
	original := GetRebateSetting()
	t.Cleanup(func() { require.NoError(t, UpdateRebateSetting(original)) })
	initial := RebateSetting{PayoutMode: RebatePayoutScheduled, InviterLimitPeriod: RebateLimitPeriodMonthly, SiteTimezone: "UTC"}
	require.NoError(t, UpdateRebateSetting(initial))
	require.Equal(t, 24, GetRebateSetting().PayoutIntervalHours)
	for _, mode := range []string{RebatePayoutImmediate, RebatePayoutScheduled, RebatePayoutHalfDay, RebatePayoutCustom} {
		require.NoError(t, UpdateRebateSettingFields(map[string]string{
			"payout_mode": mode, "payout_interval_hours": "720",
			"inviter_eligible_money_limit_micros": strconv.FormatInt(math.MaxInt64, 10),
			"inviter_reward_limit_micros":         "150000000",
		}))
		snapshot := GetRebateSetting()
		require.EqualValues(t, math.MaxInt64, snapshot.InviterEligibleMoneyLimit)
		require.EqualValues(t, 150000000, snapshot.InviterRewardLimit)
		payload, err := json.Marshal(snapshot)
		require.NoError(t, err)
		var fields map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(payload, &fields))
		require.Equal(t, "720", string(fields["payout_interval_hours"]))
		require.Equal(t, "9223372036854775807", string(fields["inviter_eligible_money_limit_micros"]))
	}
	for _, field := range []string{"payout_interval_hours", "inviter_eligible_money_limit_micros"} {
		for _, value := range []string{"-1", "1.5", "NaN", "Inf", "9223372036854775808"} {
			before := GetRebateSetting()
			require.Error(t, UpdateRebateSettingField(field, value))
			require.Equal(t, before, GetRebateSetting())
		}
	}
	for _, value := range []string{"0", "721"} {
		require.Error(t, UpdateRebateSettingField("payout_interval_hours", value))
	}
	require.NoError(t, UpdateRebateSettingField("payout_interval_hours", "1"))
	require.NoError(t, UpdateRebateSettingField("inviter_eligible_money_limit_micros", "0"))
	require.Error(t, UpdateRebateSettingField("payout_mode", "weekly"))
	bad := GetRebateSetting()
	bad.PayoutIntervalHours = 0
	require.Error(t, UpdateRebateSetting(bad))
}
