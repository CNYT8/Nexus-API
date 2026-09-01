package model

import (
	"fmt"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupEmptyResponseTest(t *testing.T) operation_setting.EmptyResponseSetting {
	t.Helper()
	truncateTables(t)
	oldSetting := operation_setting.GetEmptyResponseSetting()
	t.Cleanup(func() {
		require.NoError(t, operation_setting.SetEmptyResponseSetting(oldSetting))
	})
	require.NoError(t, operation_setting.SetEmptyResponseSetting(operation_setting.EmptyResponseSetting{
		Enabled:       true,
		PeriodDays:    3,
		RefundPercent: 70,
	}))
	return oldSetting
}

func createEmptyResponseLog(t *testing.T, userId int, requestId string, createdAt int64, quota int) {
	t.Helper()
	require.NoError(t, LOG_DB.Create(&Log{
		UserId:           userId,
		CreatedAt:        createdAt,
		Type:             LogTypeConsume,
		RequestId:        requestId,
		ModelName:        "gpt-4o",
		PromptTokens:     100,
		CompletionTokens: 0,
		Quota:            quota,
		Content:          "usage: prompt=100 completion=0",
	}).Error)
}

func TestClaimEmptyResponseCompensationIdempotent(t *testing.T) {
	setupEmptyResponseTest(t)
	user := User{Username: "empty-response-user", Password: "password", Status: common.UserStatusEnabled, Quota: 100}
	require.NoError(t, DB.Create(&user).Error)
	now := time.Now().Unix()
	createEmptyResponseLog(t, user.Id, "empty-request-1", now-3600, 100)

	created, refundQuota, err := ClaimEmptyResponseCompensation(user.Id, now)
	require.NoError(t, err)
	assert.Equal(t, 1, created)
	assert.Equal(t, 70, refundQuota)

	created, refundQuota, err = ClaimEmptyResponseCompensation(user.Id, now)
	require.NoError(t, err)
	assert.Equal(t, 0, created)
	assert.Equal(t, 0, refundQuota)

	var got User
	require.NoError(t, DB.First(&got, user.Id).Error)
	assert.Equal(t, 170, got.Quota)

	var records []EmptyResponseRecord
	require.NoError(t, DB.Find(&records).Error)
	require.Len(t, records, 1)
	assert.Equal(t, 70, records[0].RefundPercent)
	assert.Equal(t, 70, records[0].RefundQuota)
}

func TestEmptyResponseCycleExpiresOldRecords(t *testing.T) {
	setupEmptyResponseTest(t)
	require.NoError(t, DB.Exec("DELETE FROM empty_response_records").Error)
	user := User{Username: "empty-response-cycle-user", Password: "password", Status: common.UserStatusEnabled, Quota: 0}
	require.NoError(t, DB.Create(&user).Error)
	now := time.Now().Unix()
	oldTime := now - int64(4*24*3600)
	createEmptyResponseLog(t, user.Id, "old-empty-request", now-3600, 100)

	created, refundQuota, err := ClaimEmptyResponseCompensation(user.Id, now)
	require.NoError(t, err)
	require.Equal(t, 1, created)
	require.Equal(t, 70, refundQuota)
	var firstRecord EmptyResponseRecord
	require.NoError(t, DB.Where("user_id = ?", user.Id).First(&firstRecord).Error)
	require.NoError(t, DB.Model(&EmptyResponseRecord{}).Where("id = ?", firstRecord.Id).Updates(map[string]interface{}{
		"log_created_at": oldTime,
		"status":         "pending",
	}).Error)

	created, refundQuota, err = ClaimEmptyResponseCompensation(user.Id, now)
	require.NoError(t, err)
	assert.Equal(t, 0, created)
	assert.Equal(t, 0, refundQuota)

	var count int64
	require.NoError(t, DB.Model(&EmptyResponseRecord{}).Count(&count).Error)
	assert.Equal(t, int64(1), count)

	var record EmptyResponseRecord
	require.NoError(t, DB.First(&record, firstRecord.Id).Error)
	assert.Equal(t, "expired", record.Status)
	assert.Equal(t, 70, record.RefundQuota)

	_, _, _, expiredCount, err := GetEmptyResponseStatus(user.Id, now)
	require.NoError(t, err)
	assert.Equal(t, 1, expiredCount)

	var got User
	require.NoError(t, DB.First(&got, user.Id).Error)
	assert.Equal(t, 70, got.Quota)
}

func TestEmptyResponseFeatureDisabled(t *testing.T) {
	setupEmptyResponseTest(t)
	require.NoError(t, operation_setting.SetEmptyResponseSetting(operation_setting.EmptyResponseSetting{
		Enabled:       false,
		PeriodDays:    3,
		RefundPercent: 70,
	}))
	user := User{Username: "empty-response-disabled", Password: "password", Status: common.UserStatusEnabled}
	require.NoError(t, DB.Create(&user).Error)
	createEmptyResponseLog(t, user.Id, "disabled-empty-request", time.Now().Unix(), 100)
	_, _, err := ClaimEmptyResponseCompensation(user.Id, time.Now().Unix())
	require.ErrorIs(t, err, ErrEmptyResponseFeatureDisabled)
}

func TestGetEmptyResponseStatusDiscoversEligibleLogs(t *testing.T) {
	setupEmptyResponseTest(t)
	user := User{Username: "empty-response-discovery", Password: "password", Status: common.UserStatusEnabled}
	require.NoError(t, DB.Create(&user).Error)
	now := time.Now().Unix()
	// Historical logs can lack a request ID. Discovery must still use the server-side log ID safely.
	createEmptyResponseLog(t, user.Id, "", now-60, 100)

	pendingCount, pendingQuota, refundedCount, _, err := GetEmptyResponseStatus(user.Id, now)
	require.NoError(t, err)
	assert.Equal(t, int64(1), pendingCount)
	assert.Equal(t, 70, pendingQuota)
	assert.Equal(t, 0, refundedCount)

	records, total, err := ListEmptyResponses(user.Id, "pending", nil)
	require.NoError(t, err)
	require.Equal(t, int64(1), total)
	require.Len(t, records, 1)
	assert.NotEmpty(t, records[0].RequestId)
	assert.Equal(t, "pending", records[0].Status)

	created, refundQuota, err := ClaimEmptyResponseCompensation(user.Id, now)
	require.NoError(t, err)
	assert.Equal(t, 1, created)
	assert.Equal(t, 70, refundQuota)
}

func TestEmptyResponseUsesLogQuotaSnapshot(t *testing.T) {
	setupEmptyResponseTest(t)
	user := User{Username: "empty-response-snapshot", Password: "password", Status: common.UserStatusEnabled, Quota: 0}
	require.NoError(t, DB.Create(&user).Error)
	now := time.Now().Unix()
	createEmptyResponseLog(t, user.Id, "snapshot-empty-request", now-60, 137)

	_, refundQuota, err := ClaimEmptyResponseCompensation(user.Id, now)
	require.NoError(t, err)
	assert.Equal(t, operation_setting.CalcEmptyResponseRefundQuota(137, 70), refundQuota)
	assert.Equal(t, 96, refundQuota)
}

func TestEmptyResponseScanIncludesSameSecondAndExcludesNonTextLogs(t *testing.T) {
	setupEmptyResponseTest(t)
	user := User{Username: "empty-response-boundary", Password: "password", Status: common.UserStatusEnabled}
	require.NoError(t, DB.Create(&user).Error)
	now := time.Now().Unix()
	createEmptyResponseLog(t, user.Id, "same-second-empty", now, 100)
	require.NoError(t, LOG_DB.Create(&Log{
		UserId:           user.Id,
		CreatedAt:        now,
		Type:             LogTypeConsume,
		RequestId:        "image-response",
		ModelName:        "gpt-image-1",
		PromptTokens:     100,
		CompletionTokens: 0,
		Quota:            100,
		Other:            common.MapToJsonStr(map[string]interface{}{"image": true}),
	}).Error)
	require.NoError(t, LOG_DB.Create(&Log{
		UserId:           user.Id,
		CreatedAt:        now,
		Type:             LogTypeConsume,
		RequestId:        "error-stream",
		ModelName:        "gpt-4o",
		PromptTokens:     100,
		CompletionTokens: 0,
		Quota:            100,
		Other: common.MapToJsonStr(map[string]interface{}{"stream_status": map[string]interface{}{
			"status": "error",
		}}),
	}).Error)
	require.NoError(t, LOG_DB.Create(&Log{
		UserId:           user.Id,
		CreatedAt:        now,
		Type:             LogTypeConsume,
		RequestId:        "embedding-response",
		ModelName:        "text-embedding-3-small",
		PromptTokens:     100,
		CompletionTokens: 0,
		Quota:            100,
		Other:            common.MapToJsonStr(map[string]interface{}{"request_path": "/v1/embeddings"}),
	}).Error)

	pendingCount, pendingQuota, _, _, err := GetEmptyResponseStatus(user.Id, now)
	require.NoError(t, err)
	assert.Equal(t, int64(1), pendingCount)
	assert.Equal(t, 70, pendingQuota)
}

func TestEmptyResponseScanUsesOtherInputTokensAndContinuesPastExcludedBatch(t *testing.T) {
	setupEmptyResponseTest(t)
	user := User{Username: "empty-response-batch", Password: "password", Status: common.UserStatusEnabled}
	require.NoError(t, DB.Create(&user).Error)
	now := time.Now().Unix()
	for i := 0; i < emptyResponseMaxBatchSize; i++ {
		require.NoError(t, LOG_DB.Create(&Log{
			UserId:           user.Id,
			CreatedAt:        now,
			Type:             LogTypeConsume,
			RequestId:        fmt.Sprintf("embedding-%d", i),
			ModelName:        "text-embedding-3-small",
			CompletionTokens: 0,
			Quota:            100,
			Other:            common.MapToJsonStr(map[string]interface{}{"request_path": "/v1/embeddings"}),
		}).Error)
	}
	require.NoError(t, LOG_DB.Create(&Log{
		UserId:           user.Id,
		CreatedAt:        now,
		Type:             LogTypeConsume,
		RequestId:        "other-input-token-empty",
		ModelName:        "gpt-4o",
		CompletionTokens: 0,
		Quota:            100,
		Other:            common.MapToJsonStr(map[string]interface{}{"input_tokens_total": 100}),
	}).Error)

	pendingCount, pendingQuota, _, _, err := GetEmptyResponseStatus(user.Id, now)
	require.NoError(t, err)
	assert.Equal(t, int64(1), pendingCount)
	assert.Equal(t, 70, pendingQuota)
}
