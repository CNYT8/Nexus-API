package model

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func rebateAdminTestSetup(t *testing.T, holdHours int) (*gorm.DB, *User, *User, RebateRecord) {
	t.Helper()
	originalQuotaPerUnit := common.QuotaPerUnit
	t.Cleanup(func() { common.QuotaPerUnit = originalQuotaPerUnit })
	common.QuotaPerUnit = 500_000

	db := newRebateTestDB(t)
	require.NoError(t, db.Create(&RebateSystemState{Key: walletCreditBaselineStateKey, Value: "initialized:test", UpdatedAt: time.Now().UTC()}).Error)
	inviter, invitee := createRebateInvitation(t, db)

	setting := rebateRewardSetting()
	setting.PayoutHoldHours = holdHours
	useRebateRewardSetting(t, setting)

	settleRebateEligibleUsage(t, db, invitee.Id, "rebate-admin", 200, 100_000_000)
	var record RebateRecord
	require.NoError(t, db.Where("invitee_id = ?", invitee.Id).First(&record).Error)
	return db, inviter, invitee, record
}

// TestAdminListWalletRebatesFiltersAndBounds proves the operator listing can
// locate fraud records by inviter and status, and rejects malformed filters.
func TestAdminListWalletRebatesFiltersAndBounds(t *testing.T) {
	db, inviter, _, paid := rebateAdminTestSetup(t, 0)
	_, inviteeB := createRebateInvitation(t, db)
	settleRebateEligibleUsage(t, db, inviteeB.Id, "rebate-admin-list-b", 200, 100_000_000)

	all, total, err := AdminListWalletRebates(db, AdminRebateRecordFilter{}, 0, 10)
	require.NoError(t, err)
	require.EqualValues(t, 2, total)
	require.Len(t, all, 2)

	byInviter, inviterTotal, err := AdminListWalletRebates(db, AdminRebateRecordFilter{InviterID: inviter.Id}, 0, 10)
	require.NoError(t, err)
	require.EqualValues(t, 1, inviterTotal)
	require.Len(t, byInviter, 1)
	require.Equal(t, paid.ID, byInviter[0].ID)
	require.Equal(t, inviter.Id, byInviter[0].InviterID)
	require.NotEmpty(t, byInviter[0].InviterUsername)

	paidOnly, paidTotal, err := AdminListWalletRebates(db, AdminRebateRecordFilter{Status: RebateStatusPaid}, 0, 1000)
	require.NoError(t, err)
	require.EqualValues(t, 2, paidTotal)
	require.Len(t, paidOnly, 2)

	_, _, err = AdminListWalletRebates(db, AdminRebateRecordFilter{Status: "not-a-status"}, 0, 10)
	require.Error(t, err)
	_, _, err = AdminListWalletRebates(db, AdminRebateRecordFilter{InviterID: -1}, 0, 10)
	require.Error(t, err)
	_, _, err = AdminListWalletRebates(db, AdminRebateRecordFilter{}, -1, 10)
	require.Error(t, err)

	// Deep offsets return an empty page with the real total instead of scanning.
	empty, deepTotal, err := AdminListWalletRebates(db, AdminRebateRecordFilter{}, maxWalletRebateRecordOffset+1000, 10)
	require.NoError(t, err)
	require.EqualValues(t, 2, deepTotal)
	require.Empty(t, empty)
}

// TestVoidWalletRebatesStopsUnpaidPayout proves fraud response can cancel an
// unpaid obligation permanently and idempotently, before any balance is paid.
func TestVoidWalletRebatesStopsUnpaidPayout(t *testing.T) {
	db, inviter, _, record := rebateAdminTestSetup(t, 48)
	require.Equal(t, RebateStatusPending, record.Status)

	voided, skipped, err := VoidWalletRebates(db, []int64{record.ID}, 1, "confirmed fraud")
	require.NoError(t, err)
	require.Equal(t, 1, voided)
	require.Empty(t, skipped)

	var stored RebateRecord
	require.NoError(t, db.First(&stored, record.ID).Error)
	require.Equal(t, RebateStatusVoided, stored.Status)
	require.Equal(t, 1, stored.VoidedBy)
	require.Equal(t, "confirmed fraud", stored.VoidReason)

	// Even after the hold elapses the payout task must never pay a voided row.
	paid, err := PayDueWalletRebates(db, time.Now().UTC().Add(96*time.Hour), 10)
	require.NoError(t, err)
	require.Zero(t, paid)
	var inviterUser User
	require.NoError(t, db.First(&inviterUser, inviter.Id).Error)
	require.Zero(t, inviterUser.Quota)

	// Repeating the mutation is a safe no-op.
	voided, skipped, err = VoidWalletRebates(db, []int64{record.ID}, 1, "confirmed fraud")
	require.NoError(t, err)
	require.Equal(t, 1, voided)
	require.Empty(t, skipped)
}

// TestVoidWalletRebatesSkipsPaidRecords proves voiding can never silently
// rewrite a paid reward; paid rows must go through clawback instead.
func TestVoidWalletRebatesSkipsPaidRecords(t *testing.T) {
	db, inviter, _, record := rebateAdminTestSetup(t, 0)
	require.Equal(t, RebateStatusPaid, record.Status)

	voided, skipped, err := VoidWalletRebates(db, []int64{record.ID}, 1, "too late")
	require.NoError(t, err)
	require.Zero(t, voided)
	require.Equal(t, []int64{record.ID}, skipped)

	var inviterUser User
	require.NoError(t, db.First(&inviterUser, inviter.Id).Error)
	require.EqualValues(t, record.RewardQuota, int64(inviterUser.Quota))
}

// TestClawbackPaidWalletRebatesIsIdempotentAndPreservesInvariant proves a paid
// fraud reward can be reversed exactly once, with lot provenance and the
// wallet credit invariant kept intact.
func TestClawbackPaidWalletRebatesIsIdempotentAndPreservesInvariant(t *testing.T) {
	db, inviter, _, record := rebateAdminTestSetup(t, 0)
	require.Equal(t, RebateStatusPaid, record.Status)

	var inviterUser User
	require.NoError(t, db.First(&inviterUser, inviter.Id).Error)
	require.EqualValues(t, record.RewardQuota, int64(inviterUser.Quota))

	clawed, skipped, err := ClawbackPaidWalletRebates(db, []int64{record.ID}, 7, "chargeback")
	require.NoError(t, err)
	require.Equal(t, 1, clawed)
	require.Empty(t, skipped)

	require.NoError(t, db.First(&inviterUser, inviter.Id).Error)
	require.Zero(t, inviterUser.Quota)
	var stored RebateRecord
	require.NoError(t, db.First(&stored, record.ID).Error)
	require.Equal(t, RebateStatusVoided, stored.Status)
	require.Equal(t, 7, stored.VoidedBy)
	require.Equal(t, "clawback:chargeback", stored.VoidReason)

	mismatches, _, done, err := ReconcileWalletCreditBatch(db, 0, 100)
	require.NoError(t, err)
	require.True(t, done)
	require.Empty(t, mismatches)

	// A retry must never debit the inviter a second time.
	clawed, skipped, err = ClawbackPaidWalletRebates(db, []int64{record.ID}, 7, "chargeback")
	require.NoError(t, err)
	require.Equal(t, 1, clawed)
	require.Empty(t, skipped)
	require.NoError(t, db.First(&inviterUser, inviter.Id).Error)
	require.Zero(t, inviterUser.Quota)
}

// TestClawbackRequiresPaidRecordAndValidInput proves the sensitive endpoint
// fails closed on unpaid targets and malformed administrative input.
func TestClawbackRequiresPaidRecordAndValidInput(t *testing.T) {
	db, _, _, pending := rebateAdminTestSetup(t, 48)
	require.Equal(t, RebateStatusPending, pending.Status)

	clawed, skipped, err := ClawbackPaidWalletRebates(db, []int64{pending.ID}, 1, "not paid")
	require.NoError(t, err)
	require.Zero(t, clawed)
	require.Equal(t, []int64{pending.ID}, skipped)

	require.Error(t, func() error { _, _, err := VoidWalletRebates(db, nil, 1, "r"); return err }())
	require.Error(t, func() error { _, _, err := VoidWalletRebates(db, []int64{pending.ID}, 0, "r"); return err }())
	require.Error(t, func() error { _, _, err := VoidWalletRebates(db, []int64{pending.ID}, 1, "  "); return err }())
	require.Error(t, func() error {
		_, _, err := VoidWalletRebates(db, []int64{pending.ID, -1}, 1, "r")
		return err
	}())
	tooMany := make([]int64, maxRebateAdminRecords+1)
	for i := range tooMany {
		tooMany[i] = int64(i + 1)
	}
	require.Error(t, func() error { _, _, err := VoidWalletRebates(db, tooMany, 1, "r"); return err }())
}
