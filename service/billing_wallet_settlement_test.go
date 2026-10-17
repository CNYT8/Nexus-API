package service

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupWalletSettlementTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	originalDB := model.DB
	originalLogDB := model.LOG_DB
	originalUsingSQLite := common.UsingSQLite
	originalRedisEnabled := common.RedisEnabled
	common.UsingSQLite = true
	common.RedisEnabled = false

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)

	model.DB = db
	model.LOG_DB = db

	require.NoError(t, db.AutoMigrate(
		&model.User{},
		&model.RebateSystemState{},
		&model.WalletCreditLot{},
		&model.WalletConsumptionSettlement{},
		&model.WalletConsumptionAllocation{},
		&model.WalletReservation{},
		&model.WalletLotHold{},
		&model.WalletDebtRepayment{},
		&model.RebateRecord{},
		&model.RebateCapBucket{},
	))

	t.Cleanup(func() {
		model.DB = originalDB
		model.LOG_DB = originalLogDB
		common.UsingSQLite = originalUsingSQLite
		common.RedisEnabled = originalRedisEnabled
		sqlDB, err := db.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	})

	return db
}

func TestWalletFundingSettleUsageKeepsLegacyPathWithoutBaseline(t *testing.T) {
	db := setupWalletSettlementTestDB(t)
	user := &model.User{Username: "wallet-legacy", Quota: 60}
	require.NoError(t, db.Create(user).Error)

	key, err := model.NewWalletFinalSettlementKey(user.Id, "relay", "req-legacy")
	require.NoError(t, err)
	funding := &WalletFunding{userId: user.Id, consumed: 40, settlementKey: key}
	require.NoError(t, funding.SettleUsage(40, 70))

	var updated model.User
	require.NoError(t, db.First(&updated, user.Id).Error)
	require.Equal(t, 30, updated.Quota)

	var settlementCount, lotCount int64
	require.NoError(t, db.Model(&model.WalletConsumptionSettlement{}).Count(&settlementCount).Error)
	require.Zero(t, settlementCount)
	require.NoError(t, db.Model(&model.WalletCreditLot{}).Count(&lotCount).Error)
	require.Zero(t, lotCount)
}

func TestWalletFundingSettleUsageConsumesLotsWhenLedgerActive(t *testing.T) {
	originalQuotaPerUnit := common.QuotaPerUnit
	t.Cleanup(func() { common.QuotaPerUnit = originalQuotaPerUnit })
	common.QuotaPerUnit = 500_000

	db := setupWalletSettlementTestDB(t)
	require.NoError(t, db.Create(&model.RebateSystemState{
		Key:       "wallet_credit_baseline_v1",
		Value:     "initialized:test",
		UpdatedAt: time.Now().UTC(),
	}).Error)

	user := &model.User{Username: "wallet-ledger", Quota: 0}
	require.NoError(t, db.Create(user).Error)

	// A successful top-up creates an eligible paid lot of 100 quota.
	credited, err := model.CreditTopUpTx(db, user.Id, "trade-settle", 100)
	require.NoError(t, err)
	require.True(t, credited)

	// Simulate the reservation of 60 quota (wallet already debited) and a
	// final usage of 90 quota.
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", user.Id).Update("quota", 40).Error)

	key, err := model.NewWalletFinalSettlementKey(user.Id, "relay", "req-ledger")
	require.NoError(t, err)
	funding := &WalletFunding{userId: user.Id, consumed: 60, settlementKey: key}
	require.NoError(t, funding.SettleUsage(60, 90))

	var updated model.User
	require.NoError(t, db.First(&updated, user.Id).Error)
	require.Equal(t, 10, updated.Quota)

	var lot model.WalletCreditLot
	require.NoError(t, db.Where("user_id = ?", user.Id).First(&lot).Error)
	require.EqualValues(t, 10, lot.QuotaRemaining)
	require.EqualValues(t, 20, lot.EligibleMoneyRemaining)

	var settlement model.WalletConsumptionSettlement
	require.NoError(t, db.Where("final_settlement_key = ?", key).First(&settlement).Error)
	require.EqualValues(t, 60, settlement.PreConsumedQuota)
	require.EqualValues(t, 90, settlement.WalletQuota)
	require.EqualValues(t, 90, settlement.EligibleQuotaConsumed)
	require.Zero(t, settlement.UnallocatedQuota)

	var allocationCount int64
	require.NoError(t, db.Model(&model.WalletConsumptionAllocation{}).
		Where("final_settlement_key = ?", key).Count(&allocationCount).Error)
	require.EqualValues(t, 1, allocationCount)

	// Second request: the reservation equals the final usage, so the wallet
	// delta is zero, but the remaining lot must still be consumed.
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", user.Id).Update("quota", 0).Error)
	secondKey, err := model.NewWalletFinalSettlementKey(user.Id, "relay", "req-ledger-2")
	require.NoError(t, err)
	secondFunding := &WalletFunding{userId: user.Id, consumed: 10, settlementKey: secondKey}
	require.NoError(t, secondFunding.SettleUsage(10, 10))

	require.NoError(t, db.First(&updated, user.Id).Error)
	require.Zero(t, updated.Quota)
	require.NoError(t, db.First(&lot, lot.ID).Error)
	require.Zero(t, lot.QuotaRemaining)
	require.Zero(t, lot.EligibleMoneyRemaining)

	// Identical retries must be idempotent: no extra debit and no second lot
	// consumption.
	require.NoError(t, secondFunding.SettleUsage(10, 10))
	require.NoError(t, db.First(&updated, user.Id).Error)
	require.Zero(t, updated.Quota)
	require.NoError(t, db.First(&lot, lot.ID).Error)
	require.Zero(t, lot.QuotaRemaining)
}

type recordingUsageFunding struct {
	preConsumed int
	actual      int
	calls       int
}

func (r *recordingUsageFunding) Source() string              { return "recording" }
func (r *recordingUsageFunding) PreConsume(amount int) error { r.preConsumed = amount; return nil }
func (r *recordingUsageFunding) Settle(delta int) error      { return nil }
func (r *recordingUsageFunding) Refund() error               { return nil }
func (r *recordingUsageFunding) SettleUsage(preConsumed, actual int) error {
	r.calls++
	r.preConsumed = preConsumed
	r.actual = actual
	return nil
}

func TestBillingSessionSettleDispatchesZeroDeltaToUsageSettler(t *testing.T) {
	funding := &recordingUsageFunding{}
	session := &BillingSession{
		relayInfo:        &relaycommon.RelayInfo{},
		funding:          funding,
		preConsumedQuota: 25,
	}

	require.NoError(t, session.Settle(25))
	require.Equal(t, 1, funding.calls)
	require.Equal(t, 25, funding.preConsumed)
	require.Equal(t, 25, funding.actual)
	require.True(t, session.fundingSettled)

	// Re-entry must stay idempotent and never call funding twice.
	require.NoError(t, session.Settle(25))
	require.Equal(t, 1, funding.calls)
}
