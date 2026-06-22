package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

func TestModelMonitorWeight(t *testing.T) {
	now := int64(1000000)

	if got := modelMonitorWeight(now, now); got != 2 {
		t.Fatalf("recent weight = %v, want 2", got)
	}

	hotEdge := modelMonitorWeight(now-modelMonitorHotSeconds, now)
	if hotEdge < 1.49 || hotEdge > 1.51 {
		t.Fatalf("hot edge weight = %v, want about 1.5", hotEdge)
	}

	if got := modelMonitorWeight(now-modelMonitorWindowSeconds, now); got != 0.2 {
		t.Fatalf("window edge weight = %v, want 0.2", got)
	}
}

func TestScoreModelMonitorBucket(t *testing.T) {
	healthy := modelMonitorBucket{
		weightedRequests: 10,
		weightedSuccess:  10,
		weightedTokens:   10000,
		weightedUseTime:  20,
	}
	if score := scoreModelMonitorBucket(healthy); score < 85 {
		t.Fatalf("healthy score = %d, want >= 85", score)
	}

	poor := modelMonitorBucket{
		weightedRequests: 10,
		weightedErrors:   10,
		weightedUseTime:  200,
	}
	if score := scoreModelMonitorBucket(poor); score >= 45 {
		t.Fatalf("poor score = %d, want < 45", score)
	}

	lowSample := modelMonitorBucket{
		weightedRequests: 1,
		weightedSuccess:  1,
		weightedTokens:   10000,
		weightedUseTime:  1,
	}
	if score := scoreModelMonitorBucket(lowSample); score > 68 {
		t.Fatalf("low sample score = %d, want <= 68", score)
	}
}

func TestGetModelMonitorSummaryAggregatesRecentLogs(t *testing.T) {
	require.NoError(t, LOG_DB.Exec("DELETE FROM logs").Error)
	t.Cleanup(func() {
		_ = LOG_DB.Exec("DELETE FROM logs").Error
	})

	now := common.GetTimestamp()
	require.NoError(t, LOG_DB.Create(&Log{
		CreatedAt:        now - 60,
		Type:             LogTypeConsume,
		ModelName:        "gpt-monitor-test",
		PromptTokens:     600,
		CompletionTokens: 400,
		UseTime:          2,
	}).Error)
	require.NoError(t, LOG_DB.Create(&Log{
		CreatedAt: now - 120,
		Type:      LogTypeError,
		ModelName: "gpt-monitor-test",
		UseTime:   12,
	}).Error)
	require.NoError(t, LOG_DB.Create(&Log{
		CreatedAt:        now - modelMonitorWindowSeconds - 60,
		Type:             LogTypeConsume,
		ModelName:        "old-monitor-test",
		PromptTokens:     100,
		CompletionTokens: 100,
		UseTime:          1,
	}).Error)

	summary, err := GetModelMonitorSummary()
	require.NoError(t, err)
	require.Equal(t, 1, summary.ModelCount)
	require.Equal(t, 1, summary.VendorCount)
	require.Len(t, summary.Vendors, 1)
	require.Equal(t, "OpenAI", summary.Vendors[0].Name)
	require.Len(t, summary.Vendors[0].Models, 1)
	require.Equal(t, "gpt-monitor-test", summary.Vendors[0].Models[0].ModelName)
	require.GreaterOrEqual(t, summary.Vendors[0].Models[0].Score, 1)
	require.LessOrEqual(t, summary.Vendors[0].Models[0].Score, 100)
}
