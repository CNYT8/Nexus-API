package model

import (
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
	require.NoError(t, DB.Model(&EmptyResponseRecord{}).Where("request_id = ?", "old-empty-request").Update("created_at", oldTime).Error)

	created, refundQuota, err = ClaimEmptyResponseCompensation(user.Id, now)
	require.NoError(t, err)
	assert.Equal(t, 0, created)
	assert.Equal(t, 0, refundQuota)

	var count int64
	require.NoError(t, DB.Model(&EmptyResponseRecord{}).Count(&count).Error)
	assert.Equal(t, int64(1), count)

	var record EmptyResponseRecord
	require.NoError(t, DB.First(&record, "request_id = ?", "old-empty-request").Error)
	assert.Equal(t, "refunded", record.Status)
	assert.Equal(t, 70, record.RefundQuota)

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
