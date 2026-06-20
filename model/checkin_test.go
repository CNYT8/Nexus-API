package model

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/require"
)

func TestCheckinConditionStatusRequiresPreviousDayUsage(t *testing.T) {
	truncateTables(t)
	userId := 1001
	setting := &operation_setting.CheckinSetting{
		ConditionEnabled: true,
		RequestThreshold: 1,
		TokenThreshold:   100,
	}

	createYesterdayConsumeLogs(t, userId, 60)

	status, err := GetCheckinConditionStatus(userId, setting)
	require.NoError(t, err)
	require.False(t, status.Eligible)
	require.Equal(t, int64(1), status.RequestCount)
	require.Equal(t, int64(60), status.TokenCount)
}

func TestCheckinConditionStatusAllowsQualifiedUsage(t *testing.T) {
	truncateTables(t)
	userId := 1002
	setting := &operation_setting.CheckinSetting{
		ConditionEnabled: true,
		RequestThreshold: 2,
		TokenThreshold:   100,
	}

	createYesterdayConsumeLogs(t, userId, 40, 45, 50)

	status, err := GetCheckinConditionStatus(userId, setting)
	require.NoError(t, err)
	require.True(t, status.Eligible)
	require.Equal(t, int64(3), status.RequestCount)
	require.Equal(t, int64(135), status.TokenCount)
}

func TestCheckinConditionStatusRejectsEqualThreshold(t *testing.T) {
	truncateTables(t)
	userId := 1003
	setting := &operation_setting.CheckinSetting{
		ConditionEnabled: true,
		RequestThreshold: 2,
		TokenThreshold:   100,
	}

	createYesterdayConsumeLogs(t, userId, 50, 50)

	status, err := GetCheckinConditionStatus(userId, setting)
	require.NoError(t, err)
	require.False(t, status.Eligible)
	require.Equal(t, "request_count", status.Reason)
	require.Equal(t, int64(2), status.RequestCount)
	require.Equal(t, int64(100), status.TokenCount)
}

func TestCheckinConditionStatusRejectsEqualTokenThreshold(t *testing.T) {
	truncateTables(t)
	userId := 1004
	setting := &operation_setting.CheckinSetting{
		ConditionEnabled: true,
		RequestThreshold: 1,
		TokenThreshold:   100,
	}

	createYesterdayConsumeLogs(t, userId, 50, 50)

	status, err := GetCheckinConditionStatus(userId, setting)
	require.NoError(t, err)
	require.False(t, status.Eligible)
	require.Equal(t, "token_count", status.Reason)
	require.Equal(t, int64(2), status.RequestCount)
	require.Equal(t, int64(100), status.TokenCount)
}

func createYesterdayConsumeLogs(t *testing.T, userId int, tokenCounts ...int) {
	t.Helper()
	yesterday := time.Now().AddDate(0, 0, -1)
	for index, tokens := range tokenCounts {
		require.NoError(t, LOG_DB.Create(&Log{
			UserId:           userId,
			CreatedAt:        time.Date(yesterday.Year(), yesterday.Month(), yesterday.Day(), 12, index, 0, 0, time.Local).Unix(),
			Type:             LogTypeConsume,
			PromptTokens:     tokens,
			CompletionTokens: 0,
		}).Error)
	}
}
