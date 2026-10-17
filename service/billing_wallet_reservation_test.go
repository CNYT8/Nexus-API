package service

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
)

// prepareWalletReservationTestDB initializes the ledger baseline and returns a
// test database where a wallet reservation flow can be exercised end to end.
func prepareWalletReservationTestDB(t *testing.T) (*model.User, func(key string) *WalletFunding) {
	t.Helper()

	originalQuotaPerUnit := common.QuotaPerUnit
	t.Cleanup(func() { common.QuotaPerUnit = originalQuotaPerUnit })
	common.QuotaPerUnit = 500_000

	db := setupWalletSettlementTestDB(t)
	require.NoError(t, db.Create(&model.RebateSystemState{
		Key:       "wallet_credit_baseline_v1",
		Value:     "initialized:test",
		UpdatedAt: time.Now().UTC(),
	}).Error)

	user := &model.User{Username: "wallet-reservation", Quota: 0}
	require.NoError(t, db.Create(user).Error)

	credited, err := model.CreditTopUpTx(db, user.Id, "trade-reservation", 100)
	require.NoError(t, err)
	require.True(t, credited)

	newFunding := func(key string) *WalletFunding {
		return &WalletFunding{userId: user.Id, settlementKey: key}
	}
	return user, newFunding
}

func reservationTestKey(t *testing.T, userID int, suffix string) string {
	t.Helper()
	key, err := model.NewWalletFinalSettlementKey(userID, "relay", suffix)
	require.NoError(t, err)
	return key
}

func userQuota(t *testing.T, userID int) int {
	t.Helper()
	var user model.User
	require.NoError(t, model.DB.First(&user, userID).Error)
	return user.Quota
}

func TestWalletFundingReservationLifecycleAndSettlement(t *testing.T) {
	user, newFunding := prepareWalletReservationTestDB(t)
	key := reservationTestKey(t, user.Id, "reserve-settle")
	funding := newFunding(key)

	// The full pre-consume must debit the wallet and create durable holds.
	require.NoError(t, funding.PreConsume(60))
	require.True(t, funding.reservationActive)
	require.True(t, funding.CanRetrySettlement())
	require.Equal(t, 40, userQuota(t, user.Id))

	var reservation model.WalletReservation
	require.NoError(t, model.DB.Where("reservation_key = ?", key).First(&reservation).Error)
	require.EqualValues(t, 60, reservation.ReservedQuota)
	require.Equal(t, model.WalletReservationReserved, reservation.State)

	var held int64
	require.NoError(t, model.DB.Model(&model.WalletLotHold{}).Where("reservation_key = ?", key).Select("COALESCE(SUM(quota_held),0)").Scan(&held).Error)
	require.EqualValues(t, 60, held)

	// Mid-request input growth reserves more; a rolled back token reserve
	// releases back to an exact CAS-checked target.
	require.NoError(t, funding.ReserveMore(20))
	require.Equal(t, 20, userQuota(t, user.Id))
	require.NoError(t, funding.ReleaseReserved(30))
	require.Equal(t, 50, userQuota(t, user.Id))
	require.Equal(t, 50, funding.consumed)

	require.NoError(t, funding.SettleUsage(50, 70))
	require.False(t, funding.reservationActive)
	require.Equal(t, 30, userQuota(t, user.Id))

	require.NoError(t, model.DB.Where("reservation_key = ?", key).First(&reservation).Error)
	require.Equal(t, model.WalletReservationFinalized, reservation.State)
	require.EqualValues(t, 50, reservation.ReservedQuota)
	require.EqualValues(t, 70, reservation.FinalQuota)

	var lot model.WalletCreditLot
	require.NoError(t, model.DB.Where("user_id = ?", user.Id).First(&lot).Error)
	require.EqualValues(t, 30, lot.QuotaRemaining)

	// Identical retry must neither debit nor consume a second time.
	require.NoError(t, funding.SettleUsage(50, 70))
	require.Equal(t, 30, userQuota(t, user.Id))
	require.NoError(t, model.DB.First(&lot, lot.ID).Error)
	require.EqualValues(t, 30, lot.QuotaRemaining)
}

func TestWalletFundingReservationRefundIsExactAndIdempotent(t *testing.T) {
	user, newFunding := prepareWalletReservationTestDB(t)
	key := reservationTestKey(t, user.Id, "reserve-refund")
	funding := newFunding(key)

	require.NoError(t, funding.PreConsume(40))
	require.Equal(t, 60, userQuota(t, user.Id))

	require.NoError(t, funding.Refund())
	require.Equal(t, 100, userQuota(t, user.Id))
	require.Zero(t, funding.consumed)
	require.False(t, funding.reservationActive)

	// A repeated refund is a no-op, and the finalized identity can never be
	// refunded after a successful settlement.
	require.NoError(t, funding.Refund())
	require.Equal(t, 100, userQuota(t, user.Id))

	funding2 := newFunding(reservationTestKey(t, user.Id, "reserve-refund-2"))
	require.NoError(t, funding2.PreConsume(25))
	require.Equal(t, 75, userQuota(t, user.Id))
	require.NoError(t, funding2.SettleUsage(25, 25))
	require.Equal(t, 75, userQuota(t, user.Id))
	// A successful settlement is final: cleanup refunds must be no-ops.
	require.NoError(t, funding2.Refund())
	require.Equal(t, 75, userQuota(t, user.Id))
}

func TestWalletFundingReservationInsufficientQuotaUsesLegacySentinel(t *testing.T) {
	user, newFunding := prepareWalletReservationTestDB(t)
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", user.Id).Update("quota", 10).Error)

	funding := newFunding(reservationTestKey(t, user.Id, "reserve-insufficient"))
	err := funding.PreConsume(50)
	require.ErrorIs(t, err, model.ErrInsufficientQuota)
	require.False(t, funding.reservationActive)

	var count int64
	require.NoError(t, model.DB.Model(&model.WalletReservation{}).Count(&count).Error)
	require.Zero(t, count)
	require.Equal(t, 10, userQuota(t, user.Id))
}

func TestWalletFundingLegacyPathWhenLedgerInactive(t *testing.T) {
	originalQuotaPerUnit := common.QuotaPerUnit
	t.Cleanup(func() { common.QuotaPerUnit = originalQuotaPerUnit })
	common.QuotaPerUnit = 500_000

	db := setupWalletSettlementTestDB(t)
	user := &model.User{Username: "wallet-not-initialized", Quota: 100}
	require.NoError(t, db.Create(user).Error)

	funding := &WalletFunding{userId: user.Id, settlementKey: reservationTestKey(t, user.Id, "pre-baseline")}
	require.NoError(t, funding.PreConsume(30))
	require.False(t, funding.reservationActive)
	require.False(t, funding.CanRetrySettlement())
	require.Equal(t, 70, userQuota(t, user.Id))

	var count int64
	require.NoError(t, db.Model(&model.WalletReservation{}).Count(&count).Error)
	require.Zero(t, count)

	// Settling before activation keeps the difference-only debit and never
	// fabricates a settlement row.
	require.NoError(t, funding.SettleUsage(30, 45))
	require.Equal(t, 55, userQuota(t, user.Id))
	require.NoError(t, db.Model(&model.WalletConsumptionSettlement{}).Count(&count).Error)
	require.Zero(t, count)

	// A refund after a successful settlement must not restore quota.
	require.NoError(t, funding.Refund())
	require.Equal(t, 55, userQuota(t, user.Id))
}
