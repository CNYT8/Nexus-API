package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupUserUpdateIntegrityTest(t *testing.T) {
	t.Helper()
	truncateTables(t)
	oldRedisEnabled := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() {
		common.RedisEnabled = oldRedisEnabled
	})
}

func TestUserUpdateDoesNotOverwriteConcurrentAccountingOrTokenChanges(t *testing.T) {
	setupUserUpdateIntegrityTest(t)

	user := User{
		Username:        "quota-integrity-user",
		Password:        "password",
		DisplayName:     "before",
		Status:          common.UserStatusEnabled,
		Quota:           1000,
		UsedQuota:       20,
		RequestCount:    3,
		AffCount:        2,
		AffQuota:        800,
		AffHistoryQuota: 1200,
	}
	user.SetAccessToken("old-token")
	require.NoError(t, DB.Create(&user).Error)

	staleUser, err := GetUserById(user.Id, true)
	require.NoError(t, err)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", user.Id).Updates(map[string]interface{}{
		"quota":         gorm.Expr("quota - ?", 400),
		"used_quota":    gorm.Expr("used_quota + ?", 400),
		"request_count": gorm.Expr("request_count + ?", 1),
		"aff_count":     gorm.Expr("aff_count + ?", 1),
		"aff_quota":     gorm.Expr("aff_quota - ?", 500),
		"aff_history":   gorm.Expr("aff_history + ?", 500),
		"access_token":  "rotated-token",
	}).Error)

	staleUser.DisplayName = "after"
	require.NoError(t, staleUser.Update(false))

	var got User
	require.NoError(t, DB.First(&got, user.Id).Error)
	assert.Equal(t, "after", got.DisplayName)
	assert.Equal(t, 600, got.Quota)
	assert.Equal(t, 420, got.UsedQuota)
	assert.Equal(t, 4, got.RequestCount)
	assert.Equal(t, 3, got.AffCount)
	assert.Equal(t, 300, got.AffQuota)
	assert.Equal(t, 1700, got.AffHistoryQuota)
	assert.Equal(t, "rotated-token", got.GetAccessToken())
}

func TestUpdateUserAccessTokenOnlyUpdatesAccessToken(t *testing.T) {
	setupUserUpdateIntegrityTest(t)

	user := User{
		Username:        "token-integrity-user",
		Password:        "password",
		Status:          common.UserStatusEnabled,
		Quota:           1000,
		AffQuota:        800,
		AffHistoryQuota: 1200,
	}
	require.NoError(t, DB.Create(&user).Error)

	require.NoError(t, DB.Model(&User{}).Where("id = ?", user.Id).Updates(map[string]interface{}{
		"quota":        gorm.Expr("quota + ?", 500),
		"aff_quota":    gorm.Expr("aff_quota - ?", 500),
		"display_name": "concurrent-update",
	}).Error)
	require.NoError(t, UpdateUserAccessToken(user.Id, "rotated-token"))

	var got User
	require.NoError(t, DB.First(&got, user.Id).Error)
	assert.Equal(t, "rotated-token", got.GetAccessToken())
	assert.Equal(t, "concurrent-update", got.DisplayName)
	assert.Equal(t, 1500, got.Quota)
	assert.Equal(t, 300, got.AffQuota)
	assert.Equal(t, 1200, got.AffHistoryQuota)
}

func TestUpdateUserAccessTokenRejectsSoftDeletedUser(t *testing.T) {
	setupUserUpdateIntegrityTest(t)

	user := User{
		Username: "deleted-token-integrity-user",
		Password: "password",
		Status:   common.UserStatusEnabled,
	}
	user.SetAccessToken("old-token")
	require.NoError(t, DB.Create(&user).Error)
	require.NoError(t, DB.Delete(&user).Error)

	err := UpdateUserAccessToken(user.Id, "orphaned-token")
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)

	var got User
	require.NoError(t, DB.Unscoped().First(&got, user.Id).Error)
	assert.Equal(t, "old-token", got.GetAccessToken())
}
