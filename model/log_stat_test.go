package model

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSumUsedQuotaKeepsQuotaRangeAndRecentRpmTpm(t *testing.T) {
	truncateTables(t)

	now := time.Now().Unix()
	require.NoError(t, LOG_DB.Create(&Log{
		CreatedAt:        now - 180,
		Type:             LogTypeConsume,
		Username:         "stat-user",
		TokenName:        "stat-token",
		ModelName:        "stat-model",
		Quota:            100,
		PromptTokens:     40,
		CompletionTokens: 20,
	}).Error)
	require.NoError(t, LOG_DB.Create(&Log{
		CreatedAt:        now - 5,
		Type:             LogTypeConsume,
		Username:         "stat-user",
		TokenName:        "stat-token",
		ModelName:        "stat-model",
		Quota:            200,
		PromptTokens:     7,
		CompletionTokens: 3,
	}).Error)
	require.NoError(t, LOG_DB.Create(&Log{
		CreatedAt:        now - 5,
		Type:             LogTypeError,
		Username:         "stat-user",
		TokenName:        "stat-token",
		ModelName:        "stat-model",
		Quota:            300,
		PromptTokens:     100,
		CompletionTokens: 100,
	}).Error)

	stat, err := SumUsedQuota(
		LogTypeConsume,
		now-240,
		now-120,
		"stat-model",
		"stat-user",
		"stat-token",
		0,
		"",
	)

	require.NoError(t, err)
	require.Equal(t, 100, stat.Quota)
	require.Equal(t, 1, stat.Rpm)
	require.Equal(t, 10, stat.Tpm)
}
