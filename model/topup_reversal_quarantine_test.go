package model

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestTopUpReversalExposureBounds(t *testing.T) {
	for _, tc := range []struct {
		name              string
		paid              int64
		states            []TopUpReversalState
		certain, possible int64
	}{
		{"total and items overlap", 1000, []TopUpReversalState{
			{Kind: "refund_total", AmountMinor: 300},
			{Kind: "refund_item", AmountMinor: 200, Terminal: true},
			{Kind: "refund_item", AmountMinor: 100, Terminal: true},
		}, 300, 300},
		{"grouped disputes and refund", 1000, []TopUpReversalState{
			{Kind: "refund_total", AmountMinor: 300},
			{Kind: "refund_item", AmountMinor: 300, Terminal: true},
			{Kind: "dispute", GroupID: "root", AmountMinor: 200},
			{Kind: "dispute", GroupID: "root", AmountMinor: 400},
			{Kind: "dispute", GroupID: "other", AmountMinor: 100},
			{Kind: "dispute", GroupID: "released", AmountMinor: 900, Released: true},
		}, 500, 800},
		{"possible capped without overflow", math.MaxInt64, []TopUpReversalState{
			{Kind: "refund_total", AmountMinor: math.MaxInt64 - 1},
			{Kind: "dispute", GroupID: "root", AmountMinor: 2},
		}, math.MaxInt64 - 1, math.MaxInt64},
	} {
		t.Run(tc.name, func(t *testing.T) {
			certain, possible, err := topUpReversalExposure(tc.states, tc.paid)
			require.NoError(t, err)
			require.Equal(t, tc.certain, certain)
			require.Equal(t, tc.possible, possible)
		})
	}
}

func TestTopUpReversalQuarantineRejectsBelowTargetAndSupportsLegacyZero(t *testing.T) {
	db, user, lot, _ := newTopUpReversalFixture(t, 100, 1000)
	key, err := NewRebateIdempotencyKey("test-reversal-bounds", "source")
	require.NoError(t, err)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		return stageWalletSourceReversalTx(tx, key, user.Id, lot.ID, 100, 30, 20, map[int]bool{})
	}), ErrTopUpReversalConflict)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		return reverseWalletSourceTx(tx, key, user.Id, lot.ID, 100, 30, 20, map[int]bool{})
	}), ErrTopUpReversalConflict)
	legacy := WalletSourceReversal{SourceKey: key, UserID: user.Id, LotID: lot.ID, OriginalQuota: 100, TargetQuota: 30, Pending: true, DebtKey: key}
	require.NoError(t, db.Create(&legacy).Error)
	byLot, total, err := walletPendingReversalQuotasTx(db, user.Id)
	require.NoError(t, err)
	require.EqualValues(t, 30, total)
	require.EqualValues(t, 30, byLot[lot.ID])
}

func TestTopUpReversalAmbiguitySurvivesResume(t *testing.T) {
	db, user, lot, binding := newTopUpReversalFixture(t, 100, 1000)
	hold, err := NewWalletFinalSettlementKey(user.Id, "relay", "ambiguous-held")
	require.NoError(t, err)
	require.NoError(t, ReserveWalletQuota(db, hold, user.Id, 10))
	require.ErrorIs(t, ApplyVerifiedTopUpReversal(db, topUpReversalTestEvent(binding, "evt_quarantine_refund", 300)), ErrTopUpReversalHeld)
	dispute := topUpReversalTestEvent(binding, "evt_quarantine_dispute", 200)
	dispute.Kind, dispute.ObjectID, dispute.Terminal = "dispute", "dp_quarantine", false
	require.ErrorIs(t, ApplyVerifiedTopUpReversal(db, dispute), ErrTopUpReversalHeld)
	require.NoError(t, RefundWalletReservation(db, hold, user.Id))
	require.NoError(t, ResumePendingTopUpReversal(db, binding.ID))
	require.NoError(t, ResumePendingTopUpReversal(db, binding.ID))
	require.Equal(t, 70, topUpReversalTestQuota(t, db, user.Id))
	var source WalletSourceReversal
	require.NoError(t, db.Where("lot_id = ?", lot.ID).First(&source).Error)
	require.False(t, source.Pending)
	require.EqualValues(t, 30, source.ReversedQuota)
	require.EqualValues(t, 50, source.QuarantineQuota)
	require.NoError(t, db.First(&binding, binding.ID).Error)
	require.False(t, binding.ReversalPending)
	require.True(t, binding.ReversalAmbiguous)
	byLot, pending, err := walletPendingReversalQuotasTx(db, user.Id)
	require.NoError(t, err)
	require.EqualValues(t, 20, pending)
	require.EqualValues(t, 20, byLot[lot.ID])
	net, err := WalletNetEligibleMoneyTx(db, user.Id)
	require.NoError(t, err)
	require.EqualValues(t, 500_000, net)
	spend, err := NewWalletFinalSettlementKey(user.Id, "relay", "ambiguous-spend")
	require.NoError(t, err)
	require.Error(t, ReserveWalletQuota(db, spend, user.Id, 51))
	require.NoError(t, ReserveWalletQuota(db, spend, user.Id, 50))
	require.NoError(t, RefundWalletReservation(db, spend, user.Id))
	dispute.EventID, dispute.OccurredAt, dispute.Terminal, dispute.Released = "evt_quarantine_won", 200, true, true
	require.NoError(t, ApplyVerifiedTopUpReversal(db, dispute))
	require.Equal(t, 70, topUpReversalTestQuota(t, db, user.Id))
	_, pending, err = walletPendingReversalQuotasTx(db, user.Id)
	require.NoError(t, err)
	require.Zero(t, pending)
	net, err = WalletNetEligibleMoneyTx(db, user.Id)
	require.NoError(t, err)
	require.EqualValues(t, 700_000, net)
}

func TestRebateReversalAmbiguityProjectsRewardAndGatesPayout(t *testing.T) {
	db, invitee, inviter, binding, payout, record := newMixedRebateReversalFixture(t)
	require.NoError(t, ApplyVerifiedTopUpReversal(db, topUpReversalTestEvent(binding, "evt_reward_refund", 300)))
	dispute := topUpReversalTestEvent(binding, "evt_reward_overlap", 200)
	dispute.Kind, dispute.ObjectID, dispute.Terminal = "dispute", "dp_reward_overlap", false
	require.NoError(t, ApplyVerifiedTopUpReversal(db, dispute))
	require.Equal(t, -30, topUpReversalTestQuota(t, db, invitee.Id))
	require.Equal(t, 27, topUpReversalTestQuota(t, db, inviter.Id))
	var overlay RebateSourceReversal
	require.NoError(t, db.Where("record_id = ?", record.ID).First(&overlay).Error)
	require.EqualValues(t, 30, overlay.ReversedQuota)
	require.EqualValues(t, 50, overlay.QuarantinedQuota)
	require.EqualValues(t, 300_000, overlay.ReversedMoneyMicros)
	require.EqualValues(t, 500_000, overlay.QuarantinedMoneyMicros)
	var source WalletSourceReversal
	require.NoError(t, db.Where("lot_id = ?", payout.ID).First(&source).Error)
	require.False(t, source.Pending)
	require.EqualValues(t, 3, source.ReversedQuota)
	require.EqualValues(t, 5, source.QuarantineQuota)
	byLot, pending, err := walletPendingReversalQuotasTx(db, inviter.Id)
	require.NoError(t, err)
	require.EqualValues(t, 2, pending)
	require.EqualValues(t, 2, byLot[payout.ID])
	var allocation WalletConsumptionAllocation
	require.NoError(t, db.First(&allocation, overlay.AllocationID).Error)
	blocked, err := walletSettlementReversalPendingTx(db, allocation.FinalSettlementKey, invitee.Id)
	require.NoError(t, err)
	require.True(t, blocked)
	blocked, err = rebateRecordReversalPendingTx(db, &record)
	require.NoError(t, err)
	require.True(t, blocked)
	spend, err := NewWalletFinalSettlementKey(inviter.Id, "relay", "quarantined-reward-spend")
	require.NoError(t, err)
	require.Error(t, ReserveWalletQuota(db, spend, inviter.Id, 26))
	require.NoError(t, ReserveWalletQuota(db, spend, inviter.Id, 25))
	require.NoError(t, RefundWalletReservation(db, spend, inviter.Id))
	dispute.EventID, dispute.OccurredAt, dispute.Terminal, dispute.Released = "evt_reward_overlap_won", 200, true, true
	require.NoError(t, ApplyVerifiedTopUpReversal(db, dispute))
	require.Equal(t, 27, topUpReversalTestQuota(t, db, inviter.Id))
	_, pending, err = walletPendingReversalQuotasTx(db, inviter.Id)
	require.NoError(t, err)
	require.Zero(t, pending)
	blocked, err = rebateRecordReversalPendingTx(db, &record)
	require.NoError(t, err)
	require.False(t, blocked)
	require.NoError(t, db.First(&record, record.ID).Error)
	require.EqualValues(t, 30, record.RewardQuota)
	require.EqualValues(t, 200_000, record.RewardMoneyMicros)
}
