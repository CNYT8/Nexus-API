package model

import (
	"errors"
	"math"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestTopUpProviderMinorAmountExact(t *testing.T) {
	for _, tc := range []struct {
		amount string
		places int32
		want   int64
	}{
		{"12.34", 2, 1234}, {"12", 2, 1200}, {"0.001", 3, 1},
		{"92233720368547758.07", 2, math.MaxInt64}, {"001.2", 2, 120},
	} {
		got, err := TopUpProviderMinorAmount(tc.amount, tc.places)
		require.NoError(t, err, tc.amount)
		require.Equal(t, tc.want, got)
	}
	for _, amount := range []string{"", "0", "-1", "+1", "1e2", " 1", "1.", ".1", "1.001", "1.2.3", "92233720368547758.08"} {
		_, err := TopUpProviderMinorAmount(amount, 2)
		require.Error(t, err, amount)
	}
}

func TestTopUpReversalStateOrderingAndOverlap(t *testing.T) {
	state := TopUpReversalState{ID: 1, Kind: "refund_total", AmountMinor: 600, OccurredAt: 20, Terminal: true}
	next, err := nextTopUpReversalState(state, VerifiedTopUpReversal{Kind: "refund_total", AmountMinor: 200, OccurredAt: 10, Terminal: true})
	require.NoError(t, err)
	require.EqualValues(t, 600, next.AmountMinor)
	next, err = nextTopUpReversalState(TopUpReversalState{}, VerifiedTopUpReversal{Kind: "dispute", AmountMinor: 400, OccurredAt: 30, Terminal: true, Released: true})
	require.NoError(t, err)
	next.ID = 1
	next, err = nextTopUpReversalState(next, VerifiedTopUpReversal{Kind: "dispute", AmountMinor: 400, OccurredAt: 10})
	require.NoError(t, err)
	require.True(t, next.Released)
	_, err = nextTopUpReversalState(next, VerifiedTopUpReversal{Kind: "dispute", AmountMinor: 400, OccurredAt: 40, Terminal: true})
	require.ErrorIs(t, err, ErrTopUpReversalConflict)
	amount, err := topUpReversedMinor([]TopUpReversalState{{Kind: "refund_total", AmountMinor: 300}, {Kind: "dispute", AmountMinor: 200}}, 1000)
	require.NoError(t, err)
	require.EqualValues(t, 300, amount)
	amount, err = topUpReversedMinor([]TopUpReversalState{{Kind: "refund_total", AmountMinor: 1000}, {Kind: "dispute", AmountMinor: 200}}, 1000)
	require.NoError(t, err)
	require.EqualValues(t, 1000, amount)
	_, err = topUpReversedMinor([]TopUpReversalState{{Kind: "refund_total", AmountMinor: math.MaxInt64}, {Kind: "refund_total", AmountMinor: 1}}, math.MaxInt64)
	require.Error(t, err)
}

func newTopUpReversalFixture(t *testing.T, quota, paid int64) (*gorm.DB, *User, *WalletCreditLot, TopUpPaymentBinding) {
	t.Helper()
	db := newRebateTestDB(t)
	require.NoError(t, db.AutoMigrate(&TopUp{}))
	require.NoError(t, db.Create(&RebateSystemState{Key: walletCreditBaselineStateKey, Value: "initialized:test", UpdatedAt: time.Now().UTC()}).Error)
	user := createRebateTestUser(t, db, 0)
	trade := "reversal-" + common.GetUUID()
	key, err := NewWalletCreditEventKey(user.Id, WalletCreditSourceTopUp, "topup", trade, "paid")
	require.NoError(t, err)
	lot := creditRebateTestLot(t, db, WalletCreditLot{CreditEventKey: key, UserID: user.Id, Source: WalletCreditSourceTopUp, Component: "paid", Eligible: true, ReferenceType: "topup", ReferenceID: trade, QuotaTotal: quota, QuotaRemaining: quota, EligibleMoneyMicros: quota * 10_000, EligibleMoneyRemaining: quota * 10_000, QuotaPerUnitMicros: 100_000_000})
	order := TopUp{UserId: user.Id, TradeNo: trade, PaymentProvider: PaymentProviderStripe, Status: common.TopUpStatusSuccess}
	require.NoError(t, db.Create(&order).Error)
	checkoutKey, err := topUpPaymentKey(PaymentProviderStripe, "cs_"+trade)
	require.NoError(t, err)
	binding := TopUpPaymentBinding{TradeNo: trade, Provider: PaymentProviderStripe, CheckoutID: "cs_" + trade, CheckoutKey: checkoutKey, UserID: user.Id}
	require.NoError(t, db.Create(&binding).Error)
	identity := TopUpPaymentIdentity{Provider: PaymentProviderStripe, CheckoutID: binding.CheckoutID, PaymentID: "pi_" + trade, Currency: "usd", PaidMinor: paid}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return BindTopUpPaymentTx(tx, &order, identity, quota) }))
	require.NoError(t, db.First(&binding, binding.ID).Error)
	return db, user, lot, binding
}

func topUpReversalTestEvent(binding TopUpPaymentBinding, id string, amount int64) VerifiedTopUpReversal {
	return VerifiedTopUpReversal{Provider: binding.Provider, PaymentID: binding.PaymentID, EventID: id, ObjectID: "ch_original", Kind: "refund_total", Currency: "usd", AmountMinor: amount, OriginalMinor: binding.PaidMinor, OccurredAt: 100, Terminal: true}
}

func topUpReversalTestQuota(t *testing.T, db *gorm.DB, userID int) int {
	t.Helper()
	var user User
	require.NoError(t, db.First(&user, userID).Error)
	return user.Quota
}

func TestTopUpReversalCumulativeRefundAndImmutableSnapshot(t *testing.T) {
	db, user, lot, binding := newTopUpReversalFixture(t, 100, 1000)
	event := topUpReversalTestEvent(binding, "evt_partial", 300)
	require.NoError(t, ApplyVerifiedTopUpReversal(db, event))
	require.NoError(t, ApplyVerifiedTopUpReversal(db, event))
	require.Equal(t, 70, topUpReversalTestQuota(t, db, user.Id))
	older := topUpReversalTestEvent(binding, "evt_older", 100)
	older.OccurredAt = 50
	require.NoError(t, ApplyVerifiedTopUpReversal(db, older))
	require.Equal(t, 70, topUpReversalTestQuota(t, db, user.Id))
	conflict := event
	conflict.AmountMinor = 400
	require.ErrorIs(t, ApplyVerifiedTopUpReversal(db, conflict), ErrTopUpReversalConflict)
	require.NoError(t, ApplyVerifiedTopUpReversal(db, topUpReversalTestEvent(binding, "evt_full", 1000)))
	require.Zero(t, topUpReversalTestQuota(t, db, user.Id))
	require.NoError(t, db.First(lot, lot.ID).Error)
	require.EqualValues(t, 100, lot.QuotaTotal)
	require.EqualValues(t, 1_000_000, lot.EligibleMoneyMicros)
	require.EqualValues(t, 100_000_000, lot.QuotaPerUnitMicros)
	require.Zero(t, lot.QuotaRemaining)
	require.Zero(t, lot.EligibleMoneyRemaining)
	net, err := WalletNetEligibleMoneyTx(db, user.Id)
	require.NoError(t, err)
	require.Zero(t, net)
	var receipts int64
	require.NoError(t, db.Model(&TopUpReversalEvent{}).Count(&receipts).Error)
	require.EqualValues(t, 3, receipts)
}

func TestTopUpReversalHoldPersistsQuarantineAndRetries(t *testing.T) {
	db, user, lot, binding := newTopUpReversalFixture(t, 100, 1000)
	key, err := NewWalletFinalSettlementKey(user.Id, "relay", "held-reversal")
	require.NoError(t, err)
	require.NoError(t, ReserveWalletQuota(db, key, user.Id, 40))
	event := topUpReversalTestEvent(binding, "evt_held", 600)
	require.ErrorIs(t, ApplyVerifiedTopUpReversal(db, event), ErrTopUpReversalHeld)
	require.ErrorIs(t, ApplyVerifiedTopUpReversal(db, event), ErrTopUpReversalHeld)
	require.Equal(t, 60, topUpReversalTestQuota(t, db, user.Id))
	require.NoError(t, db.First(lot, lot.ID).Error)
	require.EqualValues(t, 100, lot.QuotaRemaining)
	for _, table := range []interface{}{&TopUpReversalEvent{}, &TopUpReversalState{}, &WalletSourceReversal{}} {
		var count int64
		require.NoError(t, db.Model(table).Count(&count).Error)
		require.EqualValues(t, 1, count)
	}
	require.NoError(t, db.First(&binding, binding.ID).Error)
	require.True(t, binding.ReversalPending)
	var source WalletSourceReversal
	require.NoError(t, db.Where("lot_id = ?", lot.ID).First(&source).Error)
	require.True(t, source.Pending)
	require.EqualValues(t, 60, source.TargetQuota)
	require.Zero(t, source.ReversedQuota)
	net, err := WalletNetEligibleMoneyTx(db, user.Id)
	require.NoError(t, err)
	require.EqualValues(t, 400_000, net)
	newKey, err := NewWalletFinalSettlementKey(user.Id, "relay", "quarantine-blocks-new-hold")
	require.NoError(t, err)
	require.Error(t, ReserveWalletQuota(db, newKey, user.Id, 1))
	require.NoError(t, RefundWalletReservation(db, key, user.Id))
	// The durable binding is sufficient for recovery; no new webhook is needed.
	require.NoError(t, ResumePendingTopUpReversal(db, binding.ID))
	require.NoError(t, ApplyVerifiedTopUpReversal(db, event))
	require.Equal(t, 40, topUpReversalTestQuota(t, db, user.Id))
	require.NoError(t, db.First(&binding, binding.ID).Error)
	require.False(t, binding.ReversalPending)
	require.NoError(t, db.First(&source, source.ID).Error)
	require.False(t, source.Pending)
	require.EqualValues(t, 60, source.ReversedQuota)
}

func TestTopUpReversalConsumedDebtAndWonDispute(t *testing.T) {
	db, user, lot, binding := newTopUpReversalFixture(t, 100, 1000)
	key, err := NewWalletFinalSettlementKey(user.Id, "relay", "consumed-reversal")
	require.NoError(t, err)
	_, err = FinalizeWalletConsumption(db, key, user.Id, 0, 80)
	require.NoError(t, err)
	event := topUpReversalTestEvent(binding, "evt_dispute", 500)
	event.Kind, event.ObjectID, event.Terminal = "dispute", "dp_original", false
	require.NoError(t, ApplyVerifiedTopUpReversal(db, event))
	require.Equal(t, -30, topUpReversalTestQuota(t, db, user.Id))
	var debt WalletConsumptionSettlement
	require.NoError(t, db.Where("accounting_kind = ?", "payment_reversal").First(&debt).Error)
	require.EqualValues(t, 30, debt.UnallocatedQuota)
	giftKey, err := NewWalletCreditEventKey(user.Id, WalletCreditSourceTopUp, "topup", "unrelated-gift", "gift")
	require.NoError(t, err)
	gift := creditRebateTestLot(t, db, WalletCreditLot{CreditEventKey: giftKey, UserID: user.Id, Source: WalletCreditSourceTopUp, Component: "gift", ReferenceType: "topup", ReferenceID: "unrelated-gift", QuotaTotal: 20, QuotaRemaining: 20})
	require.Zero(t, gift.QuotaRemaining)
	require.Equal(t, -10, topUpReversalTestQuota(t, db, user.Id))
	won := event
	won.EventID, won.OccurredAt, won.Terminal, won.Released = "evt_won", 200, true, true
	require.NoError(t, ApplyVerifiedTopUpReversal(db, won))
	require.NoError(t, ApplyVerifiedTopUpReversal(db, won))
	require.Equal(t, 40, topUpReversalTestQuota(t, db, user.Id))
	require.NoError(t, db.First(lot, lot.ID).Error)
	require.EqualValues(t, 20, lot.QuotaRemaining)
	require.NoError(t, db.First(gift, gift.ID).Error)
	require.EqualValues(t, 20, gift.QuotaRemaining)
	require.False(t, gift.Eligible)
	require.NoError(t, db.First(&debt, debt.ID).Error)
	require.Zero(t, debt.UnallocatedQuota)
	require.Zero(t, debt.DebtRepaidQuota)
	late := event
	late.EventID = "evt_late_open"
	require.NoError(t, ApplyVerifiedTopUpReversal(db, late))
	require.Equal(t, 40, topUpReversalTestQuota(t, db, user.Id))
}

func TestTopUpReversalRejectsUnboundIdentityAndCurrency(t *testing.T) {
	db, user, _, binding := newTopUpReversalFixture(t, 100, 1000)
	event := topUpReversalTestEvent(binding, "evt_wrong", 100)
	event.PaymentID = "pi_metadata_only"
	require.ErrorIs(t, ApplyVerifiedTopUpReversal(db, event), ErrTopUpPaymentUnbound)
	event.PaymentID, event.Currency = binding.PaymentID, "EUR"
	require.ErrorIs(t, ApplyVerifiedTopUpReversal(db, event), ErrPaymentCurrencyMismatch)
	var order TopUp
	require.NoError(t, db.Where("trade_no = ?", binding.TradeNo).First(&order).Error)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { return bindTopUpPaymentIfProvidedTx(tx, &order, nil, 0) }), ErrTopUpPaymentUnbound)
	identity := TopUpPaymentIdentity{Provider: binding.Provider, CheckoutID: "cs_attacker", PaymentID: binding.PaymentID, Currency: binding.Currency, PaidMinor: binding.PaidMinor}
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { return BindTopUpPaymentTx(tx, &order, identity, 0) }), ErrTopUpReversalConflict)
	require.Equal(t, 100, topUpReversalTestQuota(t, db, user.Id))
}

func TestTopUpReversalReceiptFailureRollsBackDebit(t *testing.T) {
	db, user, lot, binding := newTopUpReversalFixture(t, 100, 1000)
	callback := "test:reject_reversal_receipt"
	require.NoError(t, db.Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
		if _, ok := tx.Statement.Dest.(*TopUpReversalEvent); ok {
			tx.AddError(errors.New("injected reversal receipt failure"))
		}
	}))
	event := topUpReversalTestEvent(binding, "evt_fault", 500)
	require.ErrorContains(t, ApplyVerifiedTopUpReversal(db, event), "injected reversal receipt failure")
	require.Equal(t, 100, topUpReversalTestQuota(t, db, user.Id))
	require.NoError(t, db.First(lot, lot.ID).Error)
	require.EqualValues(t, 100, lot.QuotaRemaining)
	for _, table := range []interface{}{&TopUpReversalEvent{}, &TopUpReversalState{}, &WalletSourceReversal{}} {
		var count int64
		require.NoError(t, db.Model(table).Count(&count).Error)
		require.Zero(t, count)
	}
	require.NoError(t, db.Callback().Create().Remove(callback))
	require.NoError(t, ApplyVerifiedTopUpReversal(db, event))
	require.Equal(t, 50, topUpReversalTestQuota(t, db, user.Id))
}

func TestTopUpReversalRefundItemsImmutableAndDeduplicated(t *testing.T) {
	db, user, _, binding := newTopUpReversalFixture(t, 100, 1000)
	first := topUpReversalTestEvent(binding, "refund:rf_1", 200)
	first.Kind, first.ObjectID = "refund_item", "rf_1"
	require.NoError(t, ApplyVerifiedTopUpReversal(db, first))
	require.NoError(t, ApplyVerifiedTopUpReversal(db, first))
	redelivery := first
	redelivery.EventID, redelivery.OccurredAt = "refund:rf_1:redelivery", 50
	require.NoError(t, ApplyVerifiedTopUpReversal(db, redelivery))
	require.Equal(t, 80, topUpReversalTestQuota(t, db, user.Id))
	conflict := first
	conflict.EventID, conflict.AmountMinor = "refund:rf_1:changed", 201
	require.ErrorIs(t, ApplyVerifiedTopUpReversal(db, conflict), ErrTopUpReversalConflict)
	second := first
	second.EventID, second.ObjectID, second.AmountMinor = "refund:rf_2", "rf_2", 300
	require.NoError(t, ApplyVerifiedTopUpReversal(db, second))
	require.Equal(t, 50, topUpReversalTestQuota(t, db, user.Id))
	// The same successful refunds represented as a cumulative total are not added.
	require.NoError(t, ApplyVerifiedTopUpReversal(db, topUpReversalTestEvent(binding, "evt_total", 500)))
	require.Equal(t, 50, topUpReversalTestQuota(t, db, user.Id))
	dispute := topUpReversalTestEvent(binding, "evt_overlap", 600)
	dispute.Kind, dispute.ObjectID, dispute.Terminal = "dispute", "dp_overlap", false
	require.NoError(t, ApplyVerifiedTopUpReversal(db, dispute))
	require.Equal(t, 40, topUpReversalTestQuota(t, db, user.Id))
	dispute.EventID, dispute.OccurredAt, dispute.Terminal, dispute.Released = "evt_overlap_won", 200, true, true
	require.NoError(t, ApplyVerifiedTopUpReversal(db, dispute))
	require.Equal(t, 50, topUpReversalTestQuota(t, db, user.Id))
	var states int64
	require.NoError(t, db.Model(&TopUpReversalState{}).Where("kind = ?", "refund_item").Count(&states).Error)
	require.EqualValues(t, 2, states)
	var receipts int64
	require.NoError(t, db.Model(&TopUpReversalEvent{}).Where("event_key = ?", mustTopUpEventKey(t, conflict)).Count(&receipts).Error)
	require.Zero(t, receipts)
}

func TestTopUpReversalDisputeGroupsPreserveActiveRounds(t *testing.T) {
	db, user, _, binding := newTopUpReversalFixture(t, 100, 1000)
	first := topUpReversalTestEvent(binding, "evt_first_round", 400)
	first.Kind, first.ObjectID, first.GroupID, first.Terminal = "dispute", "cb_first", "cb_first", false
	require.NoError(t, ApplyVerifiedTopUpReversal(db, first))
	second := first
	second.EventID, second.ObjectID = "evt_second_round", "cb_second"
	require.NoError(t, ApplyVerifiedTopUpReversal(db, second))
	require.Equal(t, 60, topUpReversalTestQuota(t, db, user.Id))
	// Winning the second round cannot restore an effective first round.
	second.EventID, second.OccurredAt, second.Terminal, second.Released = "evt_second_won", 200, true, true
	require.NoError(t, ApplyVerifiedTopUpReversal(db, second))
	require.Equal(t, 60, topUpReversalTestQuota(t, db, user.Id))
	independent := first
	independent.EventID, independent.ObjectID, independent.GroupID, independent.AmountMinor = "evt_independent", "cb_other", "cb_other", 200
	require.NoError(t, ApplyVerifiedTopUpReversal(db, independent))
	require.Equal(t, 40, topUpReversalTestQuota(t, db, user.Id))
	first.EventID, first.OccurredAt, first.Terminal, first.Released = "evt_first_won", 300, true, true
	require.NoError(t, ApplyVerifiedTopUpReversal(db, first))
	require.Equal(t, 80, topUpReversalTestQuota(t, db, user.Id))
	// An object's root is immutable, even with a new event identity.
	second.EventID, second.GroupID = "evt_second_regrouped", "cb_other"
	require.ErrorIs(t, ApplyVerifiedTopUpReversal(db, second), ErrTopUpReversalConflict)
	require.Equal(t, 80, topUpReversalTestQuota(t, db, user.Id))
}

func TestTopUpReversalDisputeSecondRoundDebitsAfterFirstWon(t *testing.T) {
	db, user, _, binding := newTopUpReversalFixture(t, 100, 1000)
	first := topUpReversalTestEvent(binding, "evt_first_open", 400)
	first.Kind, first.ObjectID, first.GroupID, first.Terminal = "dispute", "cb_first", "cb_first", false
	require.NoError(t, ApplyVerifiedTopUpReversal(db, first))
	first.EventID, first.OccurredAt, first.Terminal, first.Released = "evt_first_restored", 200, true, true
	require.NoError(t, ApplyVerifiedTopUpReversal(db, first))
	require.Equal(t, 100, topUpReversalTestQuota(t, db, user.Id))
	second := first
	second.EventID, second.ObjectID, second.OccurredAt, second.Terminal, second.Released = "evt_second_open", "cb_second", 300, false, false
	require.NoError(t, ApplyVerifiedTopUpReversal(db, second))
	require.NoError(t, ApplyVerifiedTopUpReversal(db, first))
	require.Equal(t, 60, topUpReversalTestQuota(t, db, user.Id))
}

func TestTopUpReversalDisputeChildFirstBindsRootToPayment(t *testing.T) {
	db, user, _, binding := newTopUpReversalFixture(t, 100, 1000)
	child := topUpReversalTestEvent(binding, "evt_child_first", 400)
	child.Kind, child.ObjectID, child.GroupID, child.Terminal = "dispute", "cb_child", "cb_root", false
	require.NoError(t, ApplyVerifiedTopUpReversal(db, child))
	rootKey, err := NewRebateIdempotencyKey("topup-reversal-object-v1", binding.Provider, "dispute", child.GroupID)
	require.NoError(t, err)
	var root TopUpReversalState
	require.NoError(t, db.Where("object_key = ?", rootKey).First(&root).Error)
	require.Equal(t, binding.ID, root.BindingID)
	require.Zero(t, root.AmountMinor)
	require.Zero(t, root.OccurredAt)
	// Use another genuine persisted payment/lot in the same database.
	otherUser := createRebateTestUser(t, db, 0)
	trade := "second-payment-" + common.GetUUID()
	lotKey, err := NewWalletCreditEventKey(otherUser.Id, WalletCreditSourceTopUp, "topup", trade, "paid")
	require.NoError(t, err)
	creditRebateTestLot(t, db, WalletCreditLot{CreditEventKey: lotKey, UserID: otherUser.Id, Source: WalletCreditSourceTopUp, Component: "paid", Eligible: true, ReferenceType: "topup", ReferenceID: trade, QuotaTotal: 100, QuotaRemaining: 100, EligibleMoneyMicros: 1_000_000, EligibleMoneyRemaining: 1_000_000, QuotaPerUnitMicros: 100_000_000})
	order := TopUp{UserId: otherUser.Id, TradeNo: trade, PaymentProvider: binding.Provider, Status: common.TopUpStatusSuccess}
	require.NoError(t, db.Create(&order).Error)
	checkoutKey, err := topUpPaymentKey(binding.Provider, "cs_"+trade)
	require.NoError(t, err)
	other := TopUpPaymentBinding{TradeNo: trade, Provider: binding.Provider, CheckoutID: "cs_" + trade, CheckoutKey: checkoutKey, UserID: otherUser.Id}
	require.NoError(t, db.Create(&other).Error)
	identity := TopUpPaymentIdentity{Provider: other.Provider, CheckoutID: other.CheckoutID, PaymentID: "pi_" + trade, Currency: "USD", PaidMinor: 1000}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return BindTopUpPaymentTx(tx, &order, identity, 100) }))
	wrong := child
	wrong.EventID, wrong.ObjectID, wrong.PaymentID = "evt_cross_payment_child", "cb_cross_payment", identity.PaymentID
	require.ErrorIs(t, ApplyVerifiedTopUpReversal(db, wrong), ErrTopUpReversalConflict)
	wrong.EventID, wrong.ObjectID = "evt_cross_payment_root", child.GroupID
	require.ErrorIs(t, ApplyVerifiedTopUpReversal(db, wrong), ErrTopUpReversalConflict)
	require.Equal(t, 100, topUpReversalTestQuota(t, db, otherUser.Id))
	// The real root may arrive late and fill only its own reserved snapshot.
	first := child
	first.EventID, first.ObjectID, first.OccurredAt = "evt_late_root", child.GroupID, 50
	require.NoError(t, ApplyVerifiedTopUpReversal(db, first))
	require.Equal(t, 60, topUpReversalTestQuota(t, db, user.Id))
	child.EventID, child.OccurredAt, child.Terminal, child.Released = "evt_child_won", 200, true, true
	require.NoError(t, ApplyVerifiedTopUpReversal(db, child))
	require.Equal(t, 60, topUpReversalTestQuota(t, db, user.Id))
}

func TestTopUpReversalDisputeGroupArithmeticAndLegacyRoot(t *testing.T) {
	amount, err := topUpReversedMinor([]TopUpReversalState{
		{Kind: "dispute", GroupID: "root", AmountMinor: 300},
		{Kind: "dispute", GroupID: "root", AmountMinor: 400},
		{Kind: "dispute", GroupID: "other", AmountMinor: 200},
		{Kind: "refund_item", AmountMinor: 500, Terminal: true},
	}, 1000)
	require.NoError(t, err)
	require.EqualValues(t, 600, amount)
	_, err = topUpReversedMinor([]TopUpReversalState{
		{Kind: "dispute", GroupID: "one", AmountMinor: math.MaxInt64},
		{Kind: "dispute", GroupID: "two", AmountMinor: 1},
	}, math.MaxInt64)
	require.Error(t, err)
	db, user, _, binding := newTopUpReversalFixture(t, 100, 1000)
	first := topUpReversalTestEvent(binding, "evt_legacy_root", 400)
	first.Kind, first.ObjectID, first.Terminal = "dispute", "cb_legacy_root", false
	require.NoError(t, ApplyVerifiedTopUpReversal(db, first))
	require.NoError(t, db.Model(&TopUpReversalState{}).Where("binding_id = ?", binding.ID).Update("group_id", "").Error)
	child := first
	child.EventID, child.ObjectID, child.GroupID = "evt_legacy_child", "cb_legacy_child", first.ObjectID
	require.NoError(t, ApplyVerifiedTopUpReversal(db, child))
	require.NoError(t, ApplyVerifiedTopUpReversal(db, first))
	require.Equal(t, 60, topUpReversalTestQuota(t, db, user.Id))
}

func mustTopUpEventKey(t *testing.T, event VerifiedTopUpReversal) string {
	t.Helper()
	key, err := NewRebateIdempotencyKey("topup-reversal-event-v1", event.Provider, event.EventID)
	require.NoError(t, err)
	return key
}

func TestTopUpRefundItemRejectsInvalidStateAndOverflow(t *testing.T) {
	for _, event := range []VerifiedTopUpReversal{
		{Kind: "refund_item", AmountMinor: 0, OccurredAt: 1, Terminal: true},
		{Kind: "refund_item", AmountMinor: 1, OccurredAt: 1},
		{Kind: "refund_item", AmountMinor: 1, OccurredAt: 1, Terminal: true, Released: true},
	} {
		_, err := nextTopUpReversalState(TopUpReversalState{}, event)
		require.ErrorIs(t, err, ErrTopUpReversalConflict)
	}
	_, err := topUpReversedMinor([]TopUpReversalState{
		{Kind: "refund_item", AmountMinor: math.MaxInt64, Terminal: true},
		{Kind: "refund_item", AmountMinor: 1, Terminal: true},
	}, math.MaxInt64)
	require.Error(t, err)
	_, err = topUpReversedMinor([]TopUpReversalState{{Kind: "refund_item", AmountMinor: 1001, Terminal: true}}, 1000)
	require.ErrorIs(t, err, ErrTopUpReversalConflict)
}

func TestTopUpLegacySuccessReplayDoesNotBindOrCredit(t *testing.T) {
	db := newRebateTestDB(t)
	require.NoError(t, db.AutoMigrate(&TopUp{}))
	user := createRebateTestUser(t, db, 37)
	order := TopUp{UserId: user.Id, TradeNo: "legacy-" + common.GetUUID(), PaymentProvider: PaymentProviderStripe, Status: common.TopUpStatusSuccess}
	require.NoError(t, db.Create(&order).Error)
	identity := TopUpPaymentIdentity{Provider: PaymentProviderStripe, CheckoutID: "cs_legacy", PaymentID: "pi_legacy", Currency: "USD", PaidMinor: 1000}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return BindTopUpPaymentTx(tx, &order, identity, 0) }))
	require.Equal(t, 37, topUpReversalTestQuota(t, db, user.Id))
	var count int64
	require.NoError(t, db.Model(&TopUpPaymentBinding{}).Count(&count).Error)
	require.Zero(t, count)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { return BindTopUpPaymentTx(tx, &order, identity, 100) }), ErrTopUpPaymentUnbound)
	order.Status = common.TopUpStatusPending
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { return BindTopUpPaymentTx(tx, &order, identity, 0) }), ErrTopUpPaymentUnbound)
}

func TestTopUpReversalTinyRefundStillReducesQualification(t *testing.T) {
	db, user, _, binding := newTopUpReversalFixture(t, 1, 1000)
	require.NoError(t, ApplyVerifiedTopUpReversal(db, topUpReversalTestEvent(binding, "evt_tiny", 1)))
	require.Equal(t, 1, topUpReversalTestQuota(t, db, user.Id))
	net, err := WalletNetEligibleMoneyTx(db, user.Id)
	require.NoError(t, err)
	require.EqualValues(t, 9990, net)
}
