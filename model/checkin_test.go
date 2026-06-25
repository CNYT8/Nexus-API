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

func TestCheckinStageStatusUsesFirstMatchedRule(t *testing.T) {
	truncateTables(t)
	userId := 1005
	setting := &operation_setting.CheckinSetting{
		ConditionEnabled: true,
		StageRules: []operation_setting.CheckinStageRule{
			{
				RequestThreshold: 12,
				TokenThreshold:   300000,
				AllowCheckin:     true,
				MinQuota:         50000,
				MaxQuota:         50000,
			},
			{
				RequestThreshold: 5,
				AllowCheckin:     true,
				MinQuota:         25000,
				MaxQuota:         50000,
			},
		},
	}

	createYesterdayConsumeLogs(t, userId, 100000, 100000, 100000, 100000)
	for i := 0; i < 9; i++ {
		createYesterdayConsumeLogs(t, userId, 1)
	}

	status, minQuota, maxQuota, err := getCheckinConditionStatusWithQuota(userId, setting)
	require.NoError(t, err)
	require.True(t, status.Eligible)
	require.Equal(t, 0, status.MatchedStage)
	require.Equal(t, 50000, minQuota)
	require.Equal(t, 50000, maxQuota)
	require.Equal(t, int64(13), status.RequestCount)
	require.Equal(t, int64(400009), status.TokenCount)
}

func TestCheckinStageStatusCanDenyLowUsage(t *testing.T) {
	truncateTables(t)
	userId := 1006
	setting := &operation_setting.CheckinSetting{
		ConditionEnabled: true,
		StageRules: []operation_setting.CheckinStageRule{
			{
				RequestThreshold: 5,
				AllowCheckin:     true,
				MinQuota:         25000,
				MaxQuota:         50000,
			},
			{
				AllowCheckin: false,
			},
		},
	}

	createYesterdayConsumeLogs(t, userId, 100, 100, 100, 100, 100)

	status, minQuota, maxQuota, err := getCheckinConditionStatusWithQuota(userId, setting)
	require.NoError(t, err)
	require.False(t, status.Eligible)
	require.Equal(t, "stage_disabled", status.Reason)
	require.Equal(t, 1, status.MatchedStage)
	require.Equal(t, 0, minQuota)
	require.Equal(t, 0, maxQuota)
}

func TestCheckinStageStatusNoMatchRejects(t *testing.T) {
	truncateTables(t)
	userId := 1007
	setting := &operation_setting.CheckinSetting{
		ConditionEnabled: true,
		StageRules: []operation_setting.CheckinStageRule{
			{
				RequestThreshold: 5,
				AllowCheckin:     true,
				MinQuota:         25000,
				MaxQuota:         50000,
			},
		},
	}

	createYesterdayConsumeLogs(t, userId, 100, 100, 100, 100, 100)

	status, _, _, err := getCheckinConditionStatusWithQuota(userId, setting)
	require.NoError(t, err)
	require.False(t, status.Eligible)
	require.Equal(t, "stage_no_match", status.Reason)
	require.Equal(t, -1, status.MatchedStage)
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
