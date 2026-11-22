package model

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// Offline SQL/result-injection regressions, not a substitute for a real MySQL
// REPEATABLE READ concurrency test. Ordinary reads represent the old snapshot;
// only locking reads receive the projection committed before the user lock.
func TestRebateReversalExposureUsesCurrentProjection(t *testing.T) {
	for _, dialect := range []string{"mysql", "postgres"} {
		for _, quarantine := range []bool{false, true} {
			name := dialect + "/target"
			if quarantine {
				name = dialect + "/quarantine"
			}
			t.Run(name, func(t *testing.T) {
				db := newRebateSnapshotDryRunDB(t, dialect)
				queries := 0
				require.NoError(t, db.Callback().Query().After("gorm:query").Register("test:reversal-current-exposure", func(tx *gorm.DB) {
					queries++
					sql := tx.Statement.SQL.String()
					if !strings.Contains(sql, "FOR UPDATE") {
						return // stale snapshot has no projection
					}
					switch dest := tx.Statement.Dest.(type) {
					case *[]RebateSourceReversal:
						*dest = []RebateSourceReversal{{AllocationID: 11, RecordID: 7, ReversedMoneyMicros: 500_000, QuarantinedMoneyMicros: 1_000_000}}
					case *WalletConsumptionAllocation:
						require.NotContains(t, sql, "record_id", "a previous query must not leak its WHERE into provenance")
						*dest = WalletConsumptionAllocation{ID: 11, FinalSettlementKey: "original", UserID: 17}
					case *[]struct {
						ID    int64
						Money int64
						Ratio int64
					}:
						require.NotContains(t, sql, "record_id")
						require.Contains(t, sql, "JOIN wallet_credit_lots")
						*dest = []struct {
							ID    int64
							Money int64
							Ratio int64
						}{
							{11, 1_000_000, 100_000_000},
							{12, 1_000_000, 200_000_000},
						}
					default:
						t.Fatalf("unexpected query destination %T", dest)
					}
					tx.RowsAffected = 1
				}))
				record := RebateRecord{ID: 7, InviteeID: 17, RateBasisPoints: 1000, RewardMoneyMicros: 200_000, RewardQuota: 30}
				got, err := rebateSourceExposureQuotaTx(db, &record, quarantine)
				require.NoError(t, err)
				want := int64(5)
				if quarantine {
					want = 10
				}
				require.Equal(t, want, got, "the unrelated mixed-source reward must remain untouched")
				require.Equal(t, 3, queries)
				require.EqualValues(t, 30, record.RewardQuota)
				require.EqualValues(t, 200_000, record.RewardMoneyMicros)
			})
		}
	}
}

// Uses the existing destructive dialect fixture: TEST_MYSQL_DSN must name a
// disposable test database. The two transactions deliberately commit in order,
// without sleeps: old snapshot -> completed projection -> payout user lock.
func TestMySQLRebatePayoutAfterCommittedReversalSnapshot(t *testing.T) {
	dsn := os.Getenv("TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("TEST_MYSQL_DSN is not set; skipping real MySQL reversal snapshot test")
	}
	db := openWalletDialectDB(t, "mysql", dsn)
	requireDialectBaseline(t, db)
	inviter := createRebateTestUser(t, db, 0)
	invitee := createRebateTestUser(t, db, 0)
	key, err := NewWalletFinalSettlementKey(invitee.Id, "relay", "reversal-old-snapshot")
	require.NoError(t, err)
	lotKey, err := NewWalletCreditEventKey(invitee.Id, WalletCreditSourceTopUp, "topup", key, "paid")
	require.NoError(t, err)
	lot := WalletCreditLot{CreditEventKey: lotKey, UserID: invitee.Id, Source: WalletCreditSourceTopUp, Component: "paid", Eligible: true, ReferenceType: "topup", ReferenceID: key, QuotaTotal: 100, QuotaRemaining: 100, EligibleMoneyMicros: 1_000_000, EligibleMoneyRemaining: 1_000_000, QuotaPerUnitMicros: 100_000_000}
	require.NoError(t, db.Create(&lot).Error)
	require.NoError(t, db.Model(&lot).Updates(map[string]interface{}{"quota_remaining": 0, "eligible_money_remaining": 0}).Error)
	allocation := WalletConsumptionAllocation{FinalSettlementKey: key, UserID: invitee.Id, LotID: lot.ID, QuotaConsumed: 100, EligibleQuotaConsumed: 100, EligibleMoneyConsumedMicros: 1_000_000}
	require.NoError(t, db.Create(&allocation).Error)
	recordKey, err := NewRebateIdempotencyKey("rebate-record-v1", key)
	require.NoError(t, err)
	payoutKey, err := NewRebateIdempotencyKey("rebate-payout-v1", recordKey)
	require.NoError(t, err)
	now := time.Now().UTC()
	record := RebateRecord{SettlementKey: recordKey, PayoutKey: payoutKey, InviterID: inviter.Id, InviteeID: invitee.Id, EligibleQuota: 100, EligibleMoneyMicros: 1_000_000, RateBasisPoints: 1000, RewardMoneyMicros: 100_000, RewardQuota: 10, Status: RebateStatusPending, AvailableAt: now}
	require.NoError(t, db.Create(&record).Error)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	payout := db.WithContext(ctx).Begin(&sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	require.NoError(t, payout.Error)
	defer payout.Rollback()
	var hint RebateRecord
	require.NoError(t, payout.First(&hint, record.ID).Error) // establish old snapshot
	require.NoError(t, db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := walletLockUserTx(tx, invitee.Id); err != nil {
			return err
		}
		// Completed source accounting: no pending flag and no payout lot yet.
		source := WalletSourceReversal{SourceKey: key, DebtKey: key, UserID: invitee.Id, LotID: lot.ID, OriginalQuota: 100, ReversedQuota: 100, TargetQuota: 100, QuarantineQuota: 100}
		if err := tx.Create(&source).Error; err != nil {
			return err
		}
		return tx.Create(&RebateSourceReversal{AllocationID: allocation.ID, SourceKey: key, RecordID: record.ID, ReversedQuota: 100, ReversedMoneyMicros: 1_000_000, QuarantinedQuota: 100, QuarantinedMoneyMicros: 1_000_000}).Error
	}))
	var stale []RebateSourceReversal
	require.NoError(t, payout.Where("record_id = ?", record.ID).Find(&stale).Error)
	require.Empty(t, stale, "fixture must actually retain the old RR snapshot")
	paid, err := payWalletRebateTx(payout, record.ID, now)
	require.NoError(t, err)
	require.True(t, paid)
	require.NoError(t, payout.Commit().Error)
	require.Equal(t, 0, topUpReversalTestQuota(t, db, inviter.Id), "payout and clawback must commit together")
	require.NoError(t, db.First(&record, record.ID).Error)
	require.Equal(t, RebateStatusPaid, record.Status)
	require.EqualValues(t, 10, record.RewardQuota)
	require.EqualValues(t, 100_000, record.RewardMoneyMicros)
	require.NotNil(t, record.PayoutLotID)
	var reward WalletCreditLot
	require.NoError(t, db.First(&reward, *record.PayoutLotID).Error)
	require.EqualValues(t, 10, reward.QuotaTotal)
	require.Zero(t, reward.QuotaRemaining)
}

func TestRebateReversalSafetyReadsAreCurrent(t *testing.T) {
	for _, dialect := range []string{"mysql", "postgres"} {
		for _, check := range []string{"held_lot", "invalidated_repayment", "repayment_provenance"} {
			t.Run(dialect+"/"+check, func(t *testing.T) {
				db := newRebateSnapshotDryRunDB(t, dialect)
				key, err := NewRebateIdempotencyKey("snapshot-reversal", "source")
				require.NoError(t, err)
				checked := false
				require.NoError(t, db.Callback().Query().After("gorm:query").Register("test:reversal-current-safety", func(tx *gorm.DB) {
					sql := tx.Statement.SQL.String()
					assertCurrent := func() {
						require.Contains(t, sql, "FOR UPDATE")
						require.NotContains(t, strings.ToLower(sql), "count(", "lock rows, not aggregates, for PostgreSQL compatibility")
						checked = true
					}
					switch dest := tx.Statement.Dest.(type) {
					case *RebateSystemState:
						*dest = RebateSystemState{Key: walletCreditBaselineStateKey, Value: "initialized:test"}
					case *User:
						dest.Id, dest.Quota = 17, 100
					case *WalletSourceReversal:
						*dest = WalletSourceReversal{ID: 1, SourceKey: key, UserID: 17, LotID: 9, OriginalQuota: 100}
					case *WalletCreditLot:
						*dest = WalletCreditLot{ID: 9, UserID: 17, QuotaTotal: 100, QuotaRemaining: 100}
					case *[]WalletLotHold:
						assertCurrent()
						*dest = []WalletLotHold{{}}
					case *WalletConsumptionSettlement:
						*dest = WalletConsumptionSettlement{UserID: 17, AccountingKind: "payment_reversal", UnallocatedQuota: 100, DebtRepaidQuota: 50}
					case *[]WalletDebtRepayment:
						assertCurrent()
						*dest = []WalletDebtRepayment{{LotID: 9, QuotaRepaid: 50}}
					case *[]WalletSourceReversal:
						assertCurrent()
						*dest = []WalletSourceReversal{{LotID: 9, ReversedQuota: 1}}
					case *[]WalletConsumptionAllocation:
						*dest = nil
					default:
						t.Fatalf("unexpected query destination %T", dest)
					}
					tx.RowsAffected = 1
				}))
				source := WalletSourceReversal{SourceKey: key, DebtKey: key, UserID: 17, LotID: 9, OriginalQuota: 100}
				switch check {
				case "held_lot":
					err = reverseWalletSourceTx(db, key, 17, 9, 100, 50, 50, map[int]bool{})
					require.ErrorIs(t, err, ErrTopUpReversalHeld)
				case "invalidated_repayment":
					err = setSourceReversalDebtTx(db, &source, 0)
					require.ErrorContains(t, err, "reversal repayment source was itself reversed; reconciliation required")
				case "repayment_provenance":
					err = setSourceAllocationReversalsTx(db, &source, 50, 50, map[int]bool{}, false)
					require.NoError(t, err)
				}
				require.True(t, checked)
			})
		}
	}
}
