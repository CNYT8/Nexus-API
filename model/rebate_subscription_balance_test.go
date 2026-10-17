package model

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

// TestPurchaseSubscriptionWithBalanceRetiresLotsWithoutRebate guards the
// non-consumption debit path: buying a subscription with wallet balance must
// retire source lots (so provenance and reconciliation stay exact) but must
// never create an eligible consumption or a rebate record.
func TestPurchaseSubscriptionWithBalanceRetiresLotsWithoutRebate(t *testing.T) {
	originalQuotaPerUnit := common.QuotaPerUnit
	t.Cleanup(func() { common.QuotaPerUnit = originalQuotaPerUnit })
	common.QuotaPerUnit = 500_000

	originalDB := DB
	originalLogDB := LOG_DB
	db := newRebateTestDB(t)
	DB = db
	LOG_DB = db
	t.Cleanup(func() {
		DB = originalDB
		LOG_DB = originalLogDB
	})
	require.NoError(t, db.AutoMigrate(&SubscriptionPlan{}, &SubscriptionOrder{}, &UserSubscription{}, &Log{}))
	require.NoError(t, db.Create(&RebateSystemState{Key: walletCreditBaselineStateKey, Value: "initialized:test", UpdatedAt: time.Now().UTC()}).Error)

	user := createRebateTestUser(t, db, 0)
	credited, err := CreditTopUpTx(db, user.Id, "trade-sub-balance", 100)
	require.NoError(t, err)
	require.True(t, credited)

	allowBalancePay := true
	plan := &SubscriptionPlan{
		Id:              424242, // avoid the process-wide subscription plan cache from other tests
		Title:           "balance-plan",
		PriceAmount:     0.0001, // ceil(0.0001 * 500000) = 50 quota
		Currency:        "USD",
		DurationUnit:    "month",
		DurationValue:   1,
		Enabled:         true,
		AllowBalancePay: &allowBalancePay,
	}
	require.NoError(t, db.Create(plan).Error)

	require.NoError(t, PurchaseSubscriptionWithBalance(user.Id, plan.Id))

	var saved User
	require.NoError(t, db.First(&saved, user.Id).Error)
	require.Equal(t, 50, saved.Quota)

	var lot WalletCreditLot
	require.NoError(t, db.Where("user_id = ?", user.Id).First(&lot).Error)
	require.EqualValues(t, 50, lot.QuotaRemaining)

	// The balance purchase is a non-rewardable debit: no settlement is marked
	// eligible and no rebate record may exist.
	var settlement WalletConsumptionSettlement
	require.NoError(t, db.Where("user_id = ? AND accounting_kind = ?", user.Id, "adjustment").First(&settlement).Error)
	require.Zero(t, settlement.EligibleMoneyConsumedMicros)
	require.Zero(t, settlement.UnallocatedQuota)
	var rebates int64
	require.NoError(t, db.Model(&RebateRecord{}).Count(&rebates).Error)
	require.Zero(t, rebates)

	mismatches, _, done, err := ReconcileWalletCreditBatch(db, 0, 100)
	require.NoError(t, err)
	require.True(t, done)
	require.Empty(t, mismatches)
}
