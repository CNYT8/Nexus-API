package model

import (
	"errors"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func clearCreditSourceBaseline(t *testing.T) {
	t.Helper()
	require.NoError(t, DB.Exec("DELETE FROM rebate_system_states").Error)
}

func enableCreditSourceBaseline(t *testing.T) {
	t.Helper()
	require.NoError(t, DB.Create(&RebateSystemState{Key: walletCreditBaselineStateKey, Value: "initialized:test", UpdatedAt: time.Now().UTC()}).Error)
}

func creditSourceTestUser(t *testing.T, username string, quota int) User {
	t.Helper()
	user := User{
		Username: username,
		Password: "password",
		AffCode:  common.GetUUID(),
		Group:    "default",
		Status:   common.UserStatusEnabled,
		Quota:    quota,
	}
	require.NoError(t, DB.Create(&user).Error)
	return user
}

func firstCreditSourceLot(t *testing.T, userID int) WalletCreditLot {
	t.Helper()
	var lot WalletCreditLot
	require.NoError(t, DB.Where("user_id = ?", userID).Order("id").First(&lot).Error)
	return lot
}

func TestCreditWalletSourceTxLegacyKeepsQuotaOnly(t *testing.T) {
	db := newRebateTestDB(t)
	user := createRebateTestUser(t, db, 100)

	err := db.Transaction(func(tx *gorm.DB) error {
		applied, err := CreditWalletSourceTx(tx, user.Id, WalletCreditSourceTrial, "registration_trial", "registration", walletCreditReferenceID(user.Id), 25)
		require.True(t, applied)
		return err
	})
	require.NoError(t, err)

	var refreshed User
	require.NoError(t, db.Select("quota").First(&refreshed, user.Id).Error)
	assert.Equal(t, 125, refreshed.Quota)

	var count int64
	require.NoError(t, db.Model(&WalletCreditLot{}).Where("user_id = ?", user.Id).Count(&count).Error)
	assert.Zero(t, count)
}

func TestCreditWalletSourceTxActiveWritesEligibleLotAndRollsBack(t *testing.T) {
	originalQuotaPerUnit := common.QuotaPerUnit
	t.Cleanup(func() { common.QuotaPerUnit = originalQuotaPerUnit })
	common.QuotaPerUnit = 500_000

	db := newRebateTestDB(t)
	require.NoError(t, db.Create(&RebateSystemState{Key: walletCreditBaselineStateKey, Value: "initialized:test", UpdatedAt: time.Now().UTC()}).Error)
	user := createRebateTestUser(t, db, 0)

	var firstApplied bool
	err := db.Transaction(func(tx *gorm.DB) error {
		var err error
		firstApplied, err = CreditWalletSourceTx(tx, user.Id, WalletCreditSourceRedemption, "redeemed", "redemption", "code-7", 100)
		return err
	})
	require.NoError(t, err)
	require.True(t, firstApplied)

	// Retrying the same durable identity must not credit the wallet twice.
	var secondApplied bool
	err = db.Transaction(func(tx *gorm.DB) error {
		var err error
		secondApplied, err = CreditWalletSourceTx(tx, user.Id, WalletCreditSourceRedemption, "redeemed", "redemption", "code-7", 100)
		return err
	})
	require.NoError(t, err)
	require.False(t, secondApplied)

	var refreshed User
	require.NoError(t, db.Select("quota").First(&refreshed, user.Id).Error)
	assert.Equal(t, 100, refreshed.Quota)

	var lot WalletCreditLot
	require.NoError(t, db.Where("user_id = ?", user.Id).First(&lot).Error)
	assert.Equal(t, WalletCreditSourceRedemption, lot.Source)
	assert.Equal(t, "redeemed", lot.Component)
	assert.True(t, lot.Eligible)
	assert.EqualValues(t, 200, lot.EligibleMoneyMicros)
	assert.EqualValues(t, 200, lot.EligibleMoneyRemaining)
	assert.EqualValues(t, 500_000*1_000_000, lot.QuotaPerUnitMicros)

	// A failed transaction must roll the quota and the lot back together.
	err = db.Transaction(func(tx *gorm.DB) error {
		if _, err := CreditWalletSourceTx(tx, user.Id, WalletCreditSourceTrial, "registration_trial", "registration", "rollback-1", 25); err != nil {
			return err
		}
		return errors.New("forced rollback")
	})
	require.Error(t, err)
	require.NoError(t, db.Select("quota").First(&refreshed, user.Id).Error)
	assert.Equal(t, 100, refreshed.Quota)
	var count int64
	require.NoError(t, db.Model(&WalletCreditLot{}).Where("user_id = ? AND reference_id = ?", user.Id, "rollback-1").Count(&count).Error)
	assert.Zero(t, count)
}

func TestRedeemWritesEligibleLotAfterBaseline(t *testing.T) {
	truncateTables(t)
	enableCreditSourceBaseline(t)
	user := creditSourceTestUser(t, "redeem-lot-user", 0)
	code := &Redemption{Key: "credit-source-redeem-1", Name: "test", Quota: 50, Status: common.RedemptionCodeStatusEnabled}
	require.NoError(t, DB.Create(code).Error)

	quota, err := Redeem(code.Key, user.Id)
	require.NoError(t, err)
	assert.Equal(t, 50, quota)

	var refreshed User
	require.NoError(t, DB.Select("quota").First(&refreshed, user.Id).Error)
	assert.Equal(t, 50, refreshed.Quota)

	var stored Redemption
	require.NoError(t, DB.First(&stored, code.Id).Error)
	assert.Equal(t, common.RedemptionCodeStatusUsed, stored.Status)
	assert.Equal(t, user.Id, stored.UsedUserId)

	lot := firstCreditSourceLot(t, user.Id)
	assert.Equal(t, WalletCreditSourceRedemption, lot.Source)
	assert.Equal(t, "redeemed", lot.Component)
	assert.True(t, lot.Eligible)
	assert.Equal(t, "redemption", lot.ReferenceType)
	assert.Equal(t, walletCreditReferenceID(code.Id), lot.ReferenceID)
	assert.EqualValues(t, 50, lot.QuotaTotal)
}

func TestRedeemLegacyWithoutBaselineAddsQuotaOnly(t *testing.T) {
	truncateTables(t)
	clearCreditSourceBaseline(t)
	user := creditSourceTestUser(t, "redeem-legacy-user", 0)
	code := &Redemption{Key: "credit-source-redeem-2", Name: "test", Quota: 40, Status: common.RedemptionCodeStatusEnabled}
	require.NoError(t, DB.Create(code).Error)

	quota, err := Redeem(code.Key, user.Id)
	require.NoError(t, err)
	assert.Equal(t, 40, quota)

	var refreshed User
	require.NoError(t, DB.Select("quota").First(&refreshed, user.Id).Error)
	assert.Equal(t, 40, refreshed.Quota)

	var count int64
	require.NoError(t, DB.Model(&WalletCreditLot{}).Where("user_id = ?", user.Id).Count(&count).Error)
	assert.Zero(t, count)
}

func TestInsertWritesRegistrationTrialLotAfterBaseline(t *testing.T) {
	truncateTables(t)
	enableCreditSourceBaseline(t)
	originalNewUser := common.QuotaForNewUser
	t.Cleanup(func() { common.QuotaForNewUser = originalNewUser })
	common.QuotaForNewUser = 120

	user := User{Username: "registration-lot-user", Password: "password", Status: common.UserStatusEnabled}
	require.NoError(t, user.Insert(0))
	require.NotZero(t, user.Id)
	assert.Equal(t, 120, user.Quota)

	var refreshed User
	require.NoError(t, DB.Select("quota").First(&refreshed, user.Id).Error)
	assert.Equal(t, 120, refreshed.Quota)

	lot := firstCreditSourceLot(t, user.Id)
	assert.Equal(t, WalletCreditSourceTrial, lot.Source)
	assert.Equal(t, "registration_trial", lot.Component)
	assert.False(t, lot.Eligible)
	assert.Equal(t, "registration", lot.ReferenceType)
	assert.Equal(t, walletCreditReferenceID(user.Id), lot.ReferenceID)
}

func TestInsertLegacyWithoutBaselineAddsQuotaOnly(t *testing.T) {
	truncateTables(t)
	clearCreditSourceBaseline(t)
	originalNewUser := common.QuotaForNewUser
	t.Cleanup(func() { common.QuotaForNewUser = originalNewUser })
	common.QuotaForNewUser = 90

	user := User{Username: "registration-legacy-user", Password: "password", Status: common.UserStatusEnabled}
	require.NoError(t, user.Insert(0))
	assert.Equal(t, 90, user.Quota)

	var refreshed User
	require.NoError(t, DB.Select("quota").First(&refreshed, user.Id).Error)
	assert.Equal(t, 90, refreshed.Quota)

	var count int64
	require.NoError(t, DB.Model(&WalletCreditLot{}).Where("user_id = ?", user.Id).Count(&count).Error)
	assert.Zero(t, count)
}

func TestRegistrationInviteCreditsAreAtomicWithUserCreation(t *testing.T) {
	truncateTables(t)
	enableCreditSourceBaseline(t)
	originalNewUser, originalInvitee, originalInviter := common.QuotaForNewUser, common.QuotaForInvitee, common.QuotaForInviter
	t.Cleanup(func() {
		common.QuotaForNewUser, common.QuotaForInvitee, common.QuotaForInviter = originalNewUser, originalInvitee, originalInviter
	})
	common.QuotaForNewUser = 100
	common.QuotaForInvitee = 30
	common.QuotaForInviter = 20

	paymentSetting := operation_setting.GetPaymentSetting()
	originalConfirmed, originalVersion := paymentSetting.ComplianceConfirmed, paymentSetting.ComplianceTermsVersion
	t.Cleanup(func() {
		paymentSetting.ComplianceConfirmed, paymentSetting.ComplianceTermsVersion = originalConfirmed, originalVersion
	})
	paymentSetting.ComplianceConfirmed = true
	paymentSetting.ComplianceTermsVersion = operation_setting.CurrentComplianceTermsVersion

	inviter := creditSourceTestUser(t, "invite-atomic-inviter", 0)
	invitee := User{Username: "invite-atomic-invitee", Password: "password", Status: common.UserStatusEnabled}
	require.NoError(t, invitee.Insert(inviter.Id))
	assert.Equal(t, 130, invitee.Quota)

	var refreshed User
	require.NoError(t, DB.Select("quota").First(&refreshed, invitee.Id).Error)
	assert.Equal(t, 130, refreshed.Quota)

	var lots []WalletCreditLot
	require.NoError(t, DB.Where("user_id = ?", invitee.Id).Order("id").Find(&lots).Error)
	require.Len(t, lots, 2)
	assert.Equal(t, "registration_trial", lots[0].Component)
	assert.Equal(t, WalletCreditSourcePromotion, lots[1].Source)
	assert.Equal(t, "invitee", lots[1].Component)
	assert.False(t, lots[1].Eligible)

	var inviterRow User
	require.NoError(t, DB.Select("aff_count", "aff_quota", "aff_history").First(&inviterRow, inviter.Id).Error)
	assert.Equal(t, 1, inviterRow.AffCount)
	assert.Equal(t, 20, inviterRow.AffQuota)
	assert.Equal(t, 20, inviterRow.AffHistoryQuota)
}

func TestIncreaseUserQuotaWithMembershipGrantWritesEligibleLot(t *testing.T) {
	truncateTables(t)
	enableCreditSourceBaseline(t)
	originalQuotaPerUnit := common.QuotaPerUnit
	t.Cleanup(func() { common.QuotaPerUnit = originalQuotaPerUnit })
	common.QuotaPerUnit = 500_000
	user := creditSourceTestUser(t, "admin-ordinary-user", 0)

	require.NoError(t, IncreaseUserQuotaWithMembershipGrant(user.Id, 500_000, 7))

	var refreshed User
	require.NoError(t, DB.Select("quota").First(&refreshed, user.Id).Error)
	assert.Equal(t, 500_000, refreshed.Quota)

	var grant MembershipQuotaGrant
	require.NoError(t, DB.Where("user_id = ?", user.Id).First(&grant).Error)
	assert.Equal(t, 7, grant.OperatorId)
	assert.Equal(t, 500_000, grant.Quota)

	lot := firstCreditSourceLot(t, user.Id)
	assert.Equal(t, WalletCreditSourceAdmin, lot.Source)
	assert.Equal(t, "ordinary", lot.Component)
	assert.True(t, lot.Eligible)
	assert.Equal(t, "membership_grant", lot.ReferenceType)
	assert.Equal(t, walletCreditReferenceID(grant.Id), lot.ReferenceID)
	assert.EqualValues(t, 1_000_000, lot.EligibleMoneyMicros)

	cumulative, err := GetUserCumulativeTopUpAmount(user.Id)
	require.NoError(t, err)
	assert.Greater(t, cumulative, 0.0)
}

func TestIncreaseUserQuotaAsGiftWritesNonEligibleLot(t *testing.T) {
	truncateTables(t)
	enableCreditSourceBaseline(t)
	user := creditSourceTestUser(t, "admin-gift-user", 0)

	require.NoError(t, IncreaseUserQuotaAsGift(user.Id, 60, 9))

	var refreshed User
	require.NoError(t, DB.Select("quota").First(&refreshed, user.Id).Error)
	assert.Equal(t, 60, refreshed.Quota)

	var grant MembershipQuotaGrant
	require.NoError(t, DB.Where("user_id = ?", user.Id).First(&grant).Error)
	assert.Zero(t, grant.Amount)

	lot := firstCreditSourceLot(t, user.Id)
	assert.Equal(t, WalletCreditSourceAdmin, lot.Source)
	assert.Equal(t, "gift", lot.Component)
	assert.False(t, lot.Eligible)
	assert.Zero(t, lot.EligibleMoneyMicros)

	cumulative, err := GetUserCumulativeTopUpAmount(user.Id)
	require.NoError(t, err)
	assert.Zero(t, cumulative)
}

func TestUserCheckinWritesNonEligibleLotAfterBaseline(t *testing.T) {
	truncateTables(t)
	require.NoError(t, DB.AutoMigrate(&Checkin{}))
	require.NoError(t, DB.Exec("DELETE FROM checkins").Error)
	enableCreditSourceBaseline(t)

	checkinSetting := operation_setting.GetCheckinSetting()
	originalSetting := *checkinSetting
	t.Cleanup(func() { *checkinSetting = originalSetting })
	checkinSetting.Enabled = true
	checkinSetting.ConditionEnabled = false
	checkinSetting.MinQuota = 50
	checkinSetting.MaxQuota = 50
	checkinSetting.StageRules = nil

	user := creditSourceTestUser(t, "checkin-lot-user", 0)
	checkin, err := UserCheckin(user.Id)
	require.NoError(t, err)
	require.NotNil(t, checkin)
	assert.Equal(t, 50, checkin.QuotaAwarded)

	var refreshed User
	require.NoError(t, DB.Select("quota").First(&refreshed, user.Id).Error)
	assert.Equal(t, 50, refreshed.Quota)

	lot := firstCreditSourceLot(t, user.Id)
	assert.Equal(t, WalletCreditSourcePromotion, lot.Source)
	assert.Equal(t, "checkin", lot.Component)
	assert.False(t, lot.Eligible)
	assert.Equal(t, "checkin", lot.ReferenceType)
	assert.Equal(t, walletCreditReferenceID(checkin.Id), lot.ReferenceID)

	_, err = UserCheckin(user.Id)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "今日已签到")
}

func TestClaimEmptyResponseWritesPerRecordLotsAfterBaseline(t *testing.T) {
	setupEmptyResponseTest(t)
	enableCreditSourceBaseline(t)
	user := creditSourceTestUser(t, "empty-response-lot-user", 0)
	now := time.Now().Unix()
	createEmptyResponseLog(t, user.Id, "credit-source-empty-a", now-60, 100)
	createEmptyResponseLog(t, user.Id, "credit-source-empty-b", now-120, 50)

	created, refundTotal, err := ClaimEmptyResponseCompensation(user.Id, now)
	require.NoError(t, err)
	assert.Equal(t, 2, created)
	assert.Equal(t, 70+35, refundTotal)

	var refreshed User
	require.NoError(t, DB.Select("quota").First(&refreshed, user.Id).Error)
	assert.Equal(t, refundTotal, refreshed.Quota)

	var records []EmptyResponseRecord
	require.NoError(t, DB.Where("user_id = ? AND status = ?", user.Id, "refunded").Find(&records).Error)
	require.Len(t, records, 2)
	expectedRefs := make([]string, 0, len(records))
	for i := range records {
		expectedRefs = append(expectedRefs, records[i].RequestId)
	}

	var lots []WalletCreditLot
	require.NoError(t, DB.Where("user_id = ?", user.Id).Order("id").Find(&lots).Error)
	require.Len(t, lots, 2)
	actualRefs := make([]string, 0, len(lots))
	for i := range lots {
		actualRefs = append(actualRefs, lots[i].ReferenceID)
		assert.Equal(t, WalletCreditSourcePromotion, lots[i].Source)
		assert.Equal(t, "empty_response_refund", lots[i].Component)
		assert.False(t, lots[i].Eligible)
	}
	assert.ElementsMatch(t, expectedRefs, actualRefs)
}

func TestTransferAffQuotaToQuotaWritesAffiliateLotsAfterBaseline(t *testing.T) {
	truncateTables(t)
	enableCreditSourceBaseline(t)
	originalQuotaPerUnit := common.QuotaPerUnit
	t.Cleanup(func() { common.QuotaPerUnit = originalQuotaPerUnit })
	common.QuotaPerUnit = 500_000

	user := creditSourceTestUser(t, "aff-transfer-user", 0)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", user.Id).
		Updates(map[string]interface{}{"aff_quota": 1_100_000, "aff_history": 1_100_000}).Error)

	require.NoError(t, user.TransferAffQuotaToQuota(500_000))
	require.NoError(t, user.TransferAffQuotaToQuota(500_000))

	var refreshed User
	require.NoError(t, DB.Select("quota", "aff_quota", "aff_history").First(&refreshed, user.Id).Error)
	assert.Equal(t, 1_000_000, refreshed.Quota)
	assert.Equal(t, 100_000, refreshed.AffQuota)
	assert.Equal(t, 1_100_000, refreshed.AffHistoryQuota)

	var lots []WalletCreditLot
	require.NoError(t, DB.Where("user_id = ?", user.Id).Order("id").Find(&lots).Error)
	require.Len(t, lots, 2)
	assert.Contains(t, lots[0].ReferenceID, ":0:500000")
	assert.Contains(t, lots[1].ReferenceID, ":500000:1000000")
	for i := range lots {
		assert.Equal(t, WalletCreditSourceAffiliate, lots[i].Source)
		assert.Equal(t, "reward_transfer", lots[i].Component)
		assert.Equal(t, "affiliate_transfer", lots[i].ReferenceType)
		assert.False(t, lots[i].Eligible)
	}
}

func TestTransferAffQuotaToQuotaLegacyWithoutBaselineAddsQuotaOnly(t *testing.T) {
	truncateTables(t)
	clearCreditSourceBaseline(t)
	user := creditSourceTestUser(t, "aff-legacy-user", 0)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", user.Id).
		Updates(map[string]interface{}{"aff_quota": 600_000, "aff_history": 600_000}).Error)

	require.NoError(t, user.TransferAffQuotaToQuota(500_000))

	var refreshed User
	require.NoError(t, DB.Select("quota", "aff_quota").First(&refreshed, user.Id).Error)
	assert.Equal(t, 500_000, refreshed.Quota)
	assert.Equal(t, 100_000, refreshed.AffQuota)

	var count int64
	require.NoError(t, DB.Model(&WalletCreditLot{}).Where("user_id = ?", user.Id).Count(&count).Error)
	assert.Zero(t, count)
}

func TestGenericQuotaMutationMigratesToSourceLedger(t *testing.T) {
	truncateTables(t)
	enableCreditSourceBaseline(t)
	user := creditSourceTestUser(t, "generic-ledger-user", 0)

	require.NoError(t, IncreaseUserQuota(user.Id, 10, true))
	var refreshed User
	require.NoError(t, DB.Select("quota").First(&refreshed, user.Id).Error)
	assert.Equal(t, 10, refreshed.Quota)

	giftLot := firstCreditSourceLot(t, user.Id)
	assert.Equal(t, WalletCreditSourcePromotion, giftLot.Source)
	assert.Equal(t, "compensation", giftLot.Component)
	assert.False(t, giftLot.Eligible)
	assert.EqualValues(t, 10, giftLot.QuotaRemaining)

	require.NoError(t, DecreaseUserQuota(user.Id, 4, true))
	require.NoError(t, DB.Select("quota").First(&refreshed, user.Id).Error)
	assert.Equal(t, 6, refreshed.Quota)
	require.NoError(t, DB.First(&giftLot, giftLot.ID).Error)
	assert.EqualValues(t, 6, giftLot.QuotaRemaining)

	var adjustment WalletConsumptionSettlement
	require.NoError(t, DB.Where("user_id = ? AND accounting_kind = ?", user.Id, "adjustment").First(&adjustment).Error)
	assert.EqualValues(t, 4, adjustment.WalletQuota)

	// A queued batch delta from a pre-migration writer must still not bypass
	// the ledger, while usage statistics remain writable.
	updateUserQuotaUsedQuotaAndRequestCount(user.Id, 25, 3, 1)
	require.NoError(t, DB.Select("quota", "used_quota", "request_count").First(&refreshed, user.Id).Error)
	assert.Equal(t, 6, refreshed.Quota)
	assert.Equal(t, 3, refreshed.UsedQuota)
	assert.Equal(t, 1, refreshed.RequestCount)
}

func TestGenericQuotaMutationLegacyStillWorks(t *testing.T) {
	truncateTables(t)
	clearCreditSourceBaseline(t)
	user := creditSourceTestUser(t, "generic-legacy-user", 0)

	require.NoError(t, IncreaseUserQuota(user.Id, 10, true))

	var refreshed User
	require.NoError(t, DB.Select("quota").First(&refreshed, user.Id).Error)
	assert.Equal(t, 10, refreshed.Quota)
}

func TestAdminGrantConsumptionProducesRebate(t *testing.T) {
	truncateTables(t)
	enableCreditSourceBaseline(t)
	useRebateRewardSetting(t, rebateRewardSetting())
	originalQuotaPerUnit := common.QuotaPerUnit
	t.Cleanup(func() { common.QuotaPerUnit = originalQuotaPerUnit })
	common.QuotaPerUnit = 100

	inviter := creditSourceTestUser(t, "admin-grant-inviter", 0)
	invitee := creditSourceTestUser(t, "admin-grant-invitee", 0)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", invitee.Id).Update("inviter_id", invitee.Id).Error)

	require.NoError(t, IncreaseUserQuotaWithMembershipGrant(invitee.Id, 100, 7))

	settlementKey, err := NewWalletFinalSettlementKey(invitee.Id, "relay", "admin-grant-consume")
	require.NoError(t, err)
	result, err := FinalizeWalletConsumption(DB, settlementKey, invitee.Id, 0, 60)
	require.NoError(t, err)
	assert.EqualValues(t, 60, result.EligibleQuotaConsumed)
	assert.EqualValues(t, 600_000, result.EligibleMoneyConsumedMicros)

	var refreshed User
	require.NoError(t, DB.Select("quota").First(&refreshed, invitee.Id).Error)
	assert.Equal(t, 40, refreshed.Quota)

	var rebateCount int64
	require.NoError(t, DB.Model(&RebateRecord{}).Count(&rebateCount).Error)
	assert.EqualValues(t, 1, rebateCount)
}

func TestSetUserQuotaAbsoluteMigratesDeltaToLots(t *testing.T) {
	truncateTables(t)
	enableCreditSourceBaseline(t)
	originalQuotaPerUnit := common.QuotaPerUnit
	t.Cleanup(func() { common.QuotaPerUnit = originalQuotaPerUnit })
	common.QuotaPerUnit = 10
	user := creditSourceTestUser(t, "admin-override-user", 0)

	require.NoError(t, SetUserQuotaAbsolute(user.Id, 30))

	var refreshed User
	require.NoError(t, DB.Select("quota").First(&refreshed, user.Id).Error)
	assert.Equal(t, 30, refreshed.Quota)

	var lot WalletCreditLot
	require.NoError(t, DB.Where("user_id = ?", user.Id).First(&lot).Error)
	assert.Equal(t, WalletCreditSourceAdmin, lot.Source)
	assert.Equal(t, "ordinary", lot.Component)
	assert.True(t, lot.Eligible)
	assert.EqualValues(t, 3_000_000, lot.EligibleMoneyMicros)

	require.NoError(t, SetUserQuotaAbsolute(user.Id, 12))
	require.NoError(t, DB.Select("quota").First(&refreshed, user.Id).Error)
	assert.Equal(t, 12, refreshed.Quota)
	require.NoError(t, DB.First(&lot, lot.ID).Error)
	assert.EqualValues(t, 12, lot.QuotaRemaining)

	var adjustment WalletConsumptionSettlement
	require.NoError(t, DB.Where("user_id = ? AND accounting_kind = ?", user.Id, "adjustment").First(&adjustment).Error)
	assert.EqualValues(t, 18, adjustment.WalletQuota)

	// Overriding to the current balance must not create another mutation.
	require.NoError(t, SetUserQuotaAbsolute(user.Id, 12))
	var adjustmentCount int64
	require.NoError(t, DB.Model(&WalletConsumptionSettlement{}).Where("user_id = ? AND accounting_kind = ?", user.Id, "adjustment").Count(&adjustmentCount).Error)
	assert.EqualValues(t, 1, adjustmentCount)
}

func TestSetUserQuotaAbsoluteLegacyKeepsRawUpdate(t *testing.T) {
	truncateTables(t)
	clearCreditSourceBaseline(t)
	user := creditSourceTestUser(t, "admin-override-legacy", 25)

	require.NoError(t, SetUserQuotaAbsolute(user.Id, 25))
	require.NoError(t, SetUserQuotaAbsolute(user.Id, 40))

	var refreshed User
	require.NoError(t, DB.Select("quota").First(&refreshed, user.Id).Error)
	assert.Equal(t, 40, refreshed.Quota)

	var count int64
	require.NoError(t, DB.Model(&WalletCreditLot{}).Where("user_id = ?", user.Id).Count(&count).Error)
	assert.Zero(t, count)
}

func TestRedeemNegativeQuotaRetiresLotsWithoutRebateBase(t *testing.T) {
	truncateTables(t)
	enableCreditSourceBaseline(t)
	user := creditSourceTestUser(t, "redeem-negative-user", 0)
	require.NoError(t, IncreaseUserQuotaWithMembershipGrant(user.Id, 50, 7))
	code := &Redemption{Key: "credit-source-redeem-negative", Name: "test", Quota: -20, Status: common.RedemptionCodeStatusEnabled}
	require.NoError(t, DB.Create(code).Error)

	quota, err := Redeem(code.Key, user.Id)
	require.NoError(t, err)
	assert.Equal(t, -20, quota)

	var refreshed User
	require.NoError(t, DB.Select("quota").First(&refreshed, user.Id).Error)
	assert.Equal(t, 30, refreshed.Quota)

	var adjustment WalletConsumptionSettlement
	require.NoError(t, DB.Where("user_id = ? AND accounting_kind = ?", user.Id, "adjustment").First(&adjustment).Error)
	assert.EqualValues(t, 20, adjustment.WalletQuota)
}

func TestInsertWithTxKeepsInMemoryQuotaAfterCommit(t *testing.T) {
	truncateTables(t)
	enableCreditSourceBaseline(t)
	originalNewUser := common.QuotaForNewUser
	t.Cleanup(func() { common.QuotaForNewUser = originalNewUser })
	common.QuotaForNewUser = 77

	user := User{Username: "oauth-lot-user", Password: "password", Status: common.UserStatusEnabled}
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error { return user.InsertWithTx(tx, 0) }))
	assert.Equal(t, 77, user.Quota)

	var refreshed User
	require.NoError(t, DB.Select("quota").First(&refreshed, user.Id).Error)
	assert.Equal(t, 77, refreshed.Quota)
}
