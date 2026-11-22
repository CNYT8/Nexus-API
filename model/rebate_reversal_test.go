package model

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func newMixedRebateReversalFixture(t *testing.T) (*gorm.DB, *User, *User, TopUpPaymentBinding, *WalletCreditLot, RebateRecord) {
	t.Helper()
	db, invitee, _, binding := newTopUpReversalFixture(t, 100, 1000)
	inviter := createRebateTestUser(t, db, 0)
	otherKey, err := NewWalletCreditEventKey(invitee.Id, WalletCreditSourceRedemption, "redemption", "unrelated-source", "redeemed")
	require.NoError(t, err)
	creditRebateTestLot(t, db, WalletCreditLot{CreditEventKey: otherKey, UserID: invitee.Id, Source: WalletCreditSourceRedemption, Component: "redeemed", Eligible: true, ReferenceType: "redemption", ReferenceID: "unrelated-source", QuotaTotal: 200, QuotaRemaining: 200, EligibleMoneyMicros: 1_000_000, EligibleMoneyRemaining: 1_000_000, QuotaPerUnitMicros: 200_000_000})
	settlementKey, err := NewWalletFinalSettlementKey(invitee.Id, "relay", "mixed-source-consumption")
	require.NoError(t, err)
	_, err = FinalizeWalletConsumption(db, settlementKey, invitee.Id, 0, 300)
	require.NoError(t, err)
	recordKey, err := NewRebateIdempotencyKey("rebate-record-v1", settlementKey)
	require.NoError(t, err)
	payoutKey, err := NewWalletCreditEventKey(inviter.Id, WalletCreditSourceRebate, "rebate", settlementKey, "payout")
	require.NoError(t, err)
	payout := creditRebateTestLot(t, db, WalletCreditLot{CreditEventKey: payoutKey, UserID: inviter.Id, Source: WalletCreditSourceRebate, Component: "payout", ReferenceType: "rebate", ReferenceID: settlementKey, QuotaTotal: 30, QuotaRemaining: 30})
	now := time.Now().UTC()
	record := RebateRecord{SettlementKey: recordKey, PayoutKey: payoutKey, InviterID: inviter.Id, InviteeID: invitee.Id, EligibleQuota: 300, EligibleMoneyMicros: 2_000_000, RateBasisPoints: 1000, RewardMoneyMicros: 200_000, RewardQuota: 30, Status: RebateStatusPaid, AvailableAt: now, PayoutLotID: &payout.ID, PaidAt: &now}
	require.NoError(t, db.Create(&record).Error)
	return db, invitee, inviter, binding, payout, record
}

func TestRebateReversalPreservesUnrelatedMixedReward(t *testing.T) {
	db, invitee, inviter, binding, payout, record := newMixedRebateReversalFixture(t)
	require.NoError(t, ApplyVerifiedTopUpReversal(db, topUpReversalTestEvent(binding, "evt_mixed", 1000)))
	require.Equal(t, -100, topUpReversalTestQuota(t, db, invitee.Id))
	require.Equal(t, 20, topUpReversalTestQuota(t, db, inviter.Id))
	require.NoError(t, db.First(payout, payout.ID).Error)
	require.EqualValues(t, 30, payout.QuotaTotal)
	require.EqualValues(t, 20, payout.QuotaRemaining)
	require.NoError(t, db.First(&record, record.ID).Error)
	require.EqualValues(t, 30, record.RewardQuota)
	require.EqualValues(t, 200_000, record.RewardMoneyMicros)
	var rows []RebateSourceReversal
	require.NoError(t, db.Where("record_id = ?", record.ID).Find(&rows).Error)
	require.Len(t, rows, 1)
	require.EqualValues(t, 100, rows[0].ReversedQuota)
	require.EqualValues(t, 1_000_000, rows[0].ReversedMoneyMicros)
	amount, err := rebateSourceReversedQuotaTx(db, &record)
	require.NoError(t, err)
	require.EqualValues(t, 10, amount)
}

func TestRebateReversalInviterHoldQuarantinesBothWallets(t *testing.T) {
	db, invitee, inviter, binding, payout, _ := newMixedRebateReversalFixture(t)
	key, err := NewWalletFinalSettlementKey(inviter.Id, "relay", "inviter-reward-hold")
	require.NoError(t, err)
	require.NoError(t, ReserveWalletQuota(db, key, inviter.Id, 5))
	event := topUpReversalTestEvent(binding, "evt_inviter_hold", 1000)
	require.ErrorIs(t, ApplyVerifiedTopUpReversal(db, event), ErrTopUpReversalHeld)
	require.Zero(t, topUpReversalTestQuota(t, db, invitee.Id))
	require.Equal(t, 25, topUpReversalTestQuota(t, db, inviter.Id))
	require.NoError(t, db.First(payout, payout.ID).Error)
	require.EqualValues(t, 30, payout.QuotaRemaining)
	for _, table := range []interface{}{&TopUpReversalEvent{}, &TopUpReversalState{}, &RebateSourceReversal{}} {
		var count int64
		require.NoError(t, db.Model(table).Count(&count).Error)
		require.EqualValues(t, 1, count)
	}
	var sources []WalletSourceReversal
	require.NoError(t, db.Order("id ASC").Find(&sources).Error)
	require.Len(t, sources, 2)
	for _, source := range sources {
		require.True(t, source.Pending)
		require.Zero(t, source.ReversedQuota)
	}
	// Only ten reward quota is quarantined; the unrelated mixed-source share
	// remains available after the existing five-quota reservation.
	newKey, err := NewWalletFinalSettlementKey(inviter.Id, "relay", "unrelated-reward-hold")
	require.NoError(t, err)
	require.Error(t, ReserveWalletQuota(db, newKey, inviter.Id, 16))
	require.NoError(t, ReserveWalletQuota(db, newKey, inviter.Id, 15))
	require.NoError(t, RefundWalletReservation(db, newKey, inviter.Id))
	var debts int64
	require.NoError(t, db.Model(&WalletConsumptionSettlement{}).Where("accounting_kind = ?", "payment_reversal").Count(&debts).Error)
	require.Zero(t, debts)
	require.NoError(t, RefundWalletReservation(db, key, inviter.Id))
	require.NoError(t, ApplyVerifiedTopUpReversal(db, event))
	require.Equal(t, -100, topUpReversalTestQuota(t, db, invitee.Id))
	require.Equal(t, 20, topUpReversalTestQuota(t, db, inviter.Id))
}

func TestRebateReversalWonBeforeAccountingClearsInviterQuarantine(t *testing.T) {
	db, invitee, inviter, binding, _, record := newMixedRebateReversalFixture(t)
	key, err := NewWalletFinalSettlementKey(inviter.Id, "relay", "won-before-accounting")
	require.NoError(t, err)
	require.NoError(t, ReserveWalletQuota(db, key, inviter.Id, 5))
	event := topUpReversalTestEvent(binding, "evt_dispute_hold", 1000)
	event.Kind, event.ObjectID, event.Terminal = "dispute", "dp_reward_hold", false
	require.ErrorIs(t, ApplyVerifiedTopUpReversal(db, event), ErrTopUpReversalHeld)
	event.EventID, event.OccurredAt, event.Terminal, event.Released = "evt_dispute_hold_won", 200, true, true
	require.NoError(t, ApplyVerifiedTopUpReversal(db, event))
	require.Zero(t, topUpReversalTestQuota(t, db, invitee.Id))
	require.Equal(t, 25, topUpReversalTestQuota(t, db, inviter.Id))
	var pending int64
	require.NoError(t, db.Model(&WalletSourceReversal{}).Where("pending = ?", true).Count(&pending).Error)
	require.Zero(t, pending)
	require.NoError(t, db.First(&binding, binding.ID).Error)
	require.False(t, binding.ReversalPending)
	amount, err := rebateSourceReversedQuotaTx(db, &record)
	require.NoError(t, err)
	require.Zero(t, amount)
	require.NoError(t, RefundWalletReservation(db, key, inviter.Id))
	require.Equal(t, 30, topUpReversalTestQuota(t, db, inviter.Id))
}

func TestRebateReversalConsumedRewardBecomesSeparateDebt(t *testing.T) {
	db, _, inviter, binding, payout, _ := newMixedRebateReversalFixture(t)
	key, err := NewWalletFinalSettlementKey(inviter.Id, "relay", "spent-reward")
	require.NoError(t, err)
	_, err = FinalizeWalletConsumption(db, key, inviter.Id, 0, 25)
	require.NoError(t, err)
	event := topUpReversalTestEvent(binding, "evt_spent_reward", 1000)
	require.NoError(t, ApplyVerifiedTopUpReversal(db, event))
	require.NoError(t, ApplyVerifiedTopUpReversal(db, event))
	require.Equal(t, -5, topUpReversalTestQuota(t, db, inviter.Id))
	require.NoError(t, db.First(payout, payout.ID).Error)
	require.Zero(t, payout.QuotaRemaining)
	var debt WalletConsumptionSettlement
	require.NoError(t, db.Where("user_id = ? AND accounting_kind = ?", inviter.Id, "payment_reversal").First(&debt).Error)
	require.EqualValues(t, 5, debt.UnallocatedQuota)
}

func TestRebateReversalRejectsMismatchedRewardSnapshot(t *testing.T) {
	db, invitee, inviter, binding, _, record := newMixedRebateReversalFixture(t)
	require.NoError(t, db.Model(&record).Update("reward_quota", 31).Error)
	require.ErrorIs(t, ApplyVerifiedTopUpReversal(db, topUpReversalTestEvent(binding, "evt_bad_snapshot", 1000)), ErrTopUpReversalConflict)
	require.Zero(t, topUpReversalTestQuota(t, db, invitee.Id))
	require.Equal(t, 30, topUpReversalTestQuota(t, db, inviter.Id))
	var receipts int64
	require.NoError(t, db.Model(&TopUpReversalEvent{}).Count(&receipts).Error)
	require.Zero(t, receipts)
}
