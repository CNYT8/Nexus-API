package model

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

func insertPaymentGuardRoot(t *testing.T) int {
	t.Helper()
	const id = 900001
	insertUserForPaymentGuardTest(t, id, 0)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", id).Update("role", common.RoleRootUser).Error)
	return id
}

func TestManualCompleteTopUpChecksCurrentRoles(t *testing.T) {
	cases := []struct {
		name         string
		operatorRole int
		targetRole   int
		disabled     bool
		self         bool
		allowed      bool
	}{
		{"普通用户拒绝", common.RoleCommonUser, common.RoleCommonUser, false, false, false},
		{"管理员为普通用户补单", common.RoleAdminUser, common.RoleCommonUser, false, false, true},
		{"管理员不能给同级补单", common.RoleAdminUser, common.RoleAdminUser, false, false, false},
		{"管理员不能给超级管理员补单", common.RoleAdminUser, common.RoleRootUser, false, false, false},
		{"管理员不能给自己补单", common.RoleAdminUser, common.RoleAdminUser, false, true, false},
		{"超级管理员可补单", common.RoleRootUser, common.RoleAdminUser, false, false, true},
		{"禁用管理员拒绝", common.RoleRootUser, common.RoleCommonUser, true, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			truncateTables(t)
			const operatorID = 900002
			targetID := 900003
			insertUserForPaymentGuardTest(t, operatorID, 0)
			require.NoError(t, DB.Model(&User{}).Where("id = ?", operatorID).Update("role", tc.operatorRole).Error)
			if tc.disabled {
				require.NoError(t, DB.Model(&User{}).Where("id = ?", operatorID).Update("status", common.UserStatusDisabled).Error)
			}
			if tc.self {
				targetID = operatorID
			} else {
				insertUserForPaymentGuardTest(t, targetID, 0)
				require.NoError(t, DB.Model(&User{}).Where("id = ?", targetID).Update("role", tc.targetRole).Error)
			}
			const tradeNo = "manual-role-check"
			insertTopUpForPaymentGuardTest(t, tradeNo, targetID, PaymentProviderCreem)
			err := ManualCompleteTopUp(tradeNo, "127.0.0.1", operatorID)
			if !tc.allowed {
				require.Error(t, err)
				require.Zero(t, getUserQuotaForPaymentGuardTest(t, targetID))
				require.Equal(t, common.TopUpStatusPending, getTopUpStatusForPaymentGuardTest(t, tradeNo))
				return
			}
			require.NoError(t, err)
			require.Equal(t, 2, getUserQuotaForPaymentGuardTest(t, targetID))
			require.NoError(t, ManualCompleteTopUp(tradeNo, "127.0.0.1", operatorID))
			require.Equal(t, 2, getUserQuotaForPaymentGuardTest(t, targetID))
			// 已完成订单也重新校验调用者当前权限，不能沿用旧角色。
			require.NoError(t, DB.Model(&User{}).Where("id = ?", operatorID).Update("role", common.RoleCommonUser).Error)
			require.Error(t, ManualCompleteTopUp(tradeNo, "127.0.0.1", operatorID))
		})
	}
}

func TestUserLogProjectionHidesPaymentAndOperatorIP(t *testing.T) {
	for _, logType := range []int{LogTypeManage, LogTypeTopup, LogTypeConsume} {
		t.Run(fmt.Sprint(logType), func(t *testing.T) {
			logs := []*Log{{Type: logType, Ip: "192.0.2.10", Other: "{}"}}
			formatUserLogs(logs, 0)
			if logType == LogTypeConsume {
				require.Equal(t, "192.0.2.10", logs[0].Ip)
			} else {
				require.Empty(t, logs[0].Ip)
			}
			adminLogs := []*Log{{Type: logType, Ip: "192.0.2.10", Other: "{}"}}
			FormatRootLogs(adminLogs)
			require.Equal(t, "192.0.2.10", adminLogs[0].Ip)
		})
	}
}

func TestRebateClawbackReasonFitsStoredColumn(t *testing.T) {
	for _, reason := range []string{strings.Repeat("x", 119), strings.Repeat("界", 39) + "xx"} {
		t.Run(fmt.Sprint([]rune(reason)[0]), func(t *testing.T) {
			db, inviter, _, record := rebateAdminTestSetup(t, 0)
			clawed, _, err := ClawbackPaidWalletRebates(db, []int64{record.ID}, 7, reason+"x")
			require.Error(t, err)
			require.Zero(t, clawed)
			var user User
			require.NoError(t, db.First(&user, inviter.Id).Error)
			require.EqualValues(t, record.RewardQuota, user.Quota)
			var stored RebateRecord
			require.NoError(t, db.First(&stored, record.ID).Error)
			require.Equal(t, RebateStatusPaid, stored.Status)
			clawed, _, err = ClawbackPaidWalletRebates(db, []int64{record.ID}, 7, reason)
			require.NoError(t, err)
			require.Equal(t, 1, clawed)
			require.NoError(t, db.First(&stored, record.ID).Error)
			require.Equal(t, "clawback:"+reason, stored.VoidReason)
			require.Len(t, stored.VoidReason, 128)
		})
	}
}

func TestRebatePayoutScanBudgetAdvancesPastHeldRecords(t *testing.T) {
	db := newRebatePayoutDB(t)
	inviter, invitee := createRebateInvitation(t, db)
	for i := 0; i < rebatePayoutScanLimit+1; i++ {
		seedRebatePayout(t, db, inviter.Id, invitee.Id, 1, RebateStatusHeld, false)
	}
	payable := seedRebatePayout(t, db, inviter.Id, invitee.Id, 10, RebateStatusPending, false)
	now := time.Now().UTC().Add(time.Hour)
	paid, err := PayDueWalletRebates(db, now, 1)
	require.NoError(t, err)
	require.Zero(t, paid)
	var state RebateSystemState
	require.NoError(t, db.Where(map[string]interface{}{"key": rebatePayoutCursorKey}).First(&state).Error)
	cursor, highWater, err := parseRebatePayoutCursor(state.Value)
	require.NoError(t, err)
	require.EqualValues(t, rebatePayoutScanLimit, cursor)
	require.Equal(t, payable.ID, highWater)
	paid, err = PayDueWalletRebates(db, now, 1)
	require.NoError(t, err)
	require.Equal(t, 1, paid)
	var stored RebateRecord
	require.NoError(t, db.First(&stored, payable.ID).Error)
	require.Equal(t, RebateStatusPaid, stored.Status)
	paid, err = PayDueWalletRebates(db, now, 1)
	require.NoError(t, err)
	require.Zero(t, paid)
	var user User
	require.NoError(t, db.First(&user, inviter.Id).Error)
	require.Equal(t, 10, user.Quota)
}
