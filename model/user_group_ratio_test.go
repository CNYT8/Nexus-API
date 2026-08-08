package model

import (
	"math"
	"testing"

	"github.com/QuantumNous/new-api/common"
	membership_setting "github.com/QuantumNous/new-api/setting/membership_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestUserGroupRatioOverrideIsFinalAfterMembership(t *testing.T) {
	truncateTables(t)
	resetMembershipForTest(t)
	common.QuotaPerUnit = 100
	setting := membership_setting.GetMembershipSetting()
	setting.Enabled = true
	require.NoError(t, membership_setting.UpdateTiersByJSONString(`[
		{"id":"gold","name":"Gold","threshold_amount":0,"auto_upgrade_enabled":true,"enabled":true,"sort_order":1,"discount_all_groups":true,"all_group_discount":0.7,"all_group_stack_ratio":true,"group_discounts":[]}
	]`))

	user := &User{Username: "user_ratio_override", Status: common.UserStatusEnabled, Group: "default"}
	require.NoError(t, DB.Create(user).Error)
	require.NoError(t, SetUserMembershipTier(user.Id, "gold", MembershipSourceManual))
	require.NoError(t, SetUserGroupRatioOverride(user.Id, "default", 0.5))

	ratio, membershipInfo, overridden := ResolveUserGroupRatio(user.Id, "default", 0.8)

	assert.True(t, membershipInfo.Applied)
	assert.True(t, overridden)
	assert.Equal(t, 0.5, ratio)

	require.NoError(t, DeleteUserGroupRatioOverride(user.Id, "default"))
	ratio, membershipInfo, overridden = ResolveUserGroupRatio(user.Id, "default", 0.8)
	assert.True(t, membershipInfo.Applied)
	assert.False(t, overridden)
	assert.Equal(t, 0.56, ratio)
}

func TestUserGroupRatioOverrideSupportsZeroAndInvalidatesCache(t *testing.T) {
	truncateTables(t)
	resetMembershipForTest(t)

	user := &User{Username: "user_ratio_zero", Status: common.UserStatusEnabled, Group: "default"}
	require.NoError(t, DB.Create(user).Error)

	_, _, overridden := ResolveUserGroupRatio(user.Id, "default", 1)
	assert.False(t, overridden)
	require.NoError(t, SetUserGroupRatioOverride(user.Id, "default", 0))

	ratio, _, overridden := ResolveUserGroupRatio(user.Id, "default", 1)
	assert.True(t, overridden)
	assert.Zero(t, ratio)

	require.NoError(t, SetUserGroupRatioOverride(user.Id, "default", 1.25))
	ratio, _, overridden = ResolveUserGroupRatio(user.Id, "default", 1)
	assert.True(t, overridden)
	assert.Equal(t, 1.25, ratio)
}

func TestValidateUserGroupRatioRejectsUnsafeValues(t *testing.T) {
	assert.Error(t, ValidateUserGroupRatio("", 1))
	assert.Error(t, ValidateUserGroupRatio("default", -0.1))
	assert.Error(t, ValidateUserGroupRatio("default", math.NaN()))
	assert.Error(t, ValidateUserGroupRatio("default", math.Inf(1)))
	assert.Error(t, ValidateUserGroupRatio("default", membership_setting.MaxMembershipMultiplier+1))
	assert.NoError(t, ValidateUserGroupRatio("default", membership_setting.MaxMembershipMultiplier))
}

func TestUserGroupRatioOverrideUpsertSQLAcrossSupportedDialects(t *testing.T) {
	tests := []struct {
		name     string
		open     func() (*gorm.DB, error)
		contains []string
	}{
		{
			name: "mysql",
			open: func() (*gorm.DB, error) {
				return gorm.Open(mysql.New(mysql.Config{
					DSN:                       "user:password@tcp(127.0.0.1:3306)/nexus",
					SkipInitializeWithVersion: true,
				}), &gorm.Config{
					DryRun:                 true,
					DisableAutomaticPing:   true,
					SkipDefaultTransaction: true,
				})
			},
			contains: []string{"`group`", "ON DUPLICATE KEY UPDATE"},
		},
		{
			name: "postgres",
			open: func() (*gorm.DB, error) {
				return gorm.Open(postgres.New(postgres.Config{
					DSN:                  "host=127.0.0.1 user=nexus password=nexus dbname=nexus port=5432 sslmode=disable",
					PreferSimpleProtocol: true,
				}), &gorm.Config{
					DryRun:                 true,
					DisableAutomaticPing:   true,
					SkipDefaultTransaction: true,
				})
			},
			contains: []string{"\"group\"", "ON CONFLICT (\"user_id\",\"group\") DO UPDATE"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db, err := test.open()
			require.NoError(t, err)

			result := upsertUserGroupRatioOverride(db, &UserGroupRatioOverride{
				UserId:    1,
				Group:     "default",
				Ratio:     0.5,
				CreatedAt: 1,
				UpdatedAt: 1,
			})
			require.NoError(t, result.Error)
			for _, expected := range test.contains {
				assert.Contains(t, result.Statement.SQL.String(), expected)
			}
		})
	}
}
