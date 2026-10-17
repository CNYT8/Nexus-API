package model

import (
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// walletDialectModels is the complete set of tables the wallet/rebate ledger
// owns. The dialect tests drop and recreate exactly these tables so a real
// PostgreSQL/MySQL server can run them repeatedly without leftover state.
var walletDialectModels = []interface{}{
	&RebateRecord{},
	&RebateCapBucket{},
	&RebateSystemState{},
	&WalletDebtRepayment{},
	&WalletLotHold{},
	&WalletReservation{},
	&WalletConsumptionAllocation{},
	&WalletConsumptionSettlement{},
	&WalletCreditLot{},
	&User{},
}

// TestPostgresWalletLedgerDialect exercises the accounting ledger against a
// real PostgreSQL server. It is gated on TEST_POSTGRES_DSN and skips with a
// clear reason when the variable is absent, so the default test run stays
// hermetic.
func TestPostgresWalletLedgerDialect(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN is not set; skipping real PostgreSQL wallet ledger test")
	}
	db := openWalletDialectDB(t, "postgres", dsn)
	testWalletLedgerDialect(t, db, "postgres")
}

// TestMySQLWalletLedgerDialect is the MySQL twin of the PostgreSQL test. It
// covers the dialect-specific FOR UPDATE, information_schema and
// ON DUPLICATE KEY paths that SQLite cannot reproduce.
func TestMySQLWalletLedgerDialect(t *testing.T) {
	dsn := os.Getenv("TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("TEST_MYSQL_DSN is not set; skipping real MySQL wallet ledger test")
	}
	db := openWalletDialectDB(t, "mysql", dsn)
	testWalletLedgerDialect(t, db, "mysql")
}

// openWalletDialectDB opens a real database, resets the ledger tables, and
// points the package globals at it. The cleanup restores every global and
// closes the pool so repeated -count runs stay isolated.
func openWalletDialectDB(t *testing.T, dialect, dsn string) *gorm.DB {
	t.Helper()

	oldDB, oldLogDB := DB, LOG_DB
	oldSQLite, oldMySQL, oldPostgres := common.UsingSQLite, common.UsingMySQL, common.UsingPostgreSQL
	oldQuotaPerUnit, oldRedisEnabled := common.QuotaPerUnit, common.RedisEnabled

	var (
		db  *gorm.DB
		err error
	)
	switch dialect {
	case "postgres":
		db, err = gorm.Open(postgres.Open(dsn), &gorm.Config{})
	case "mysql":
		db, err = gorm.Open(mysql.Open(dsn), &gorm.Config{})
	default:
		t.Fatalf("unsupported wallet dialect %q", dialect)
	}
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(16)
	sqlDB.SetMaxIdleConns(16)

	DB = db
	LOG_DB = db
	common.UsingSQLite = false
	common.UsingMySQL = dialect == "mysql"
	common.UsingPostgreSQL = dialect == "postgres"
	common.QuotaPerUnit = 500_000
	common.RedisEnabled = false
	// The quota column-type cache is keyed by connection pool identity. Reset
	// it so a reopened pool can never inherit a stale column bound.
	quotaLimitCache = sync.Map{}

	t.Cleanup(func() {
		DB, LOG_DB = oldDB, oldLogDB
		common.UsingSQLite, common.UsingMySQL, common.UsingPostgreSQL = oldSQLite, oldMySQL, oldPostgres
		common.QuotaPerUnit = oldQuotaPerUnit
		common.RedisEnabled = oldRedisEnabled
		quotaLimitCache = sync.Map{}
		require.NoError(t, sqlDB.Close())
	})

	require.NoError(t, db.Migrator().DropTable(walletDialectModels...))
	require.NoError(t, db.AutoMigrate(walletDialectModels...))
	return db
}

func creditDialectTopUp(t *testing.T, db *gorm.DB, userID int, reference string, quota int) {
	t.Helper()
	var applied bool
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		applied, err = CreditTopUpTx(tx, userID, reference, quota)
		return err
	}))
	require.True(t, applied)
}

func runWalletDialectConcurrent(t *testing.T, count int, fn func() error) {
	t.Helper()
	var wg sync.WaitGroup
	errs := make(chan error, count)
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- fn()
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
}

func requireDialectBaseline(t *testing.T, db *gorm.DB) {
	t.Helper()
	require.NoError(t, InitializeWalletCreditBaseline(db))
	active, err := WalletCreditLedgerActive(db)
	require.NoError(t, err)
	require.True(t, active)
}

func testWalletLedgerDialect(t *testing.T, db *gorm.DB, dialect string) {
	t.Helper()

	// Fail-closed first: this subtest must run before the baseline marker
	// exists, so it cannot follow any credit in the same database.
	t.Run("reserve fails closed before baseline", func(t *testing.T) {
		user := createRebateTestUser(t, db, 0)
		key, err := NewWalletFinalSettlementKey(user.Id, "task-submit", "dialect-uninitialized")
		require.NoError(t, err)

		err = ReserveWalletQuota(db, key, user.Id, 10)
		require.ErrorContains(t, err, "wallet credit baseline is not initialized")

		var reservations, lots int64
		require.NoError(t, db.Model(&WalletReservation{}).Count(&reservations).Error)
		require.NoError(t, db.Model(&WalletCreditLot{}).Count(&lots).Error)
		require.Zero(t, reservations)
		require.Zero(t, lots)
	})

	requireDialectBaseline(t, db)

	// One server-owned billing identity, two concurrent goroutines for both
	// the reservation and the finalization. Exactly one settlement, one debit
	// and one rebate reward may survive.
	t.Run("same billing identity settles exactly once", func(t *testing.T) {
		setting := rebateRewardSetting()
		setting.RateBasisPoints = 200
		useRebateRewardSetting(t, setting)

		inviter, invitee := createRebateInvitation(t, db)
		creditDialectTopUp(t, db, invitee.Id, "dialect-same-identity", 1000)
		require.Equal(t, 1000, userQuota(t, db, invitee.Id))

		key, err := NewWalletFinalSettlementKey(invitee.Id, "task-submit", "dialect-same-identity-1")
		require.NoError(t, err)
		const pre, actual = int64(300), int64(250)

		runWalletDialectConcurrent(t, 2, func() error {
			return ReserveWalletQuota(db, key, invitee.Id, pre)
		})
		require.Equal(t, 700, userQuota(t, db, invitee.Id))

		runWalletDialectConcurrent(t, 2, func() error {
			_, err := FinalizeWalletConsumption(db, key, invitee.Id, pre, actual)
			return err
		})

		// 1000 - 300 reserved + 50 refunded = 750, and the lot consumed 250.
		require.Equal(t, 750, userQuota(t, db, invitee.Id))
		var settlements int64
		require.NoError(t, db.Model(&WalletConsumptionSettlement{}).Where("final_settlement_key = ?", key).Count(&settlements).Error)
		require.EqualValues(t, 1, settlements)

		var lot WalletCreditLot
		require.NoError(t, db.Where("user_id = ? AND source = ?", invitee.Id, WalletCreditSourceTopUp).First(&lot).Error)
		require.EqualValues(t, 750, lot.QuotaRemaining)
		require.EqualValues(t, 1500, lot.EligibleMoneyRemaining)

		var records []RebateRecord
		require.NoError(t, db.Where("invitee_id = ?", invitee.Id).Find(&records).Error)
		require.Len(t, records, 1)
		require.Equal(t, inviter.Id, records[0].InviterID)
		require.Equal(t, RebateStatusPaid, records[0].Status)
		require.EqualValues(t, 500, records[0].EligibleMoneyMicros)
		require.EqualValues(t, 10, records[0].RewardMoneyMicros)
		require.EqualValues(t, 5, records[0].RewardQuota)
		require.Equal(t, 5, userQuota(t, db, inviter.Id))

		// An identical replay is a no-op, and a conflicting replay fails closed.
		result, err := FinalizeWalletConsumption(db, key, invitee.Id, pre, actual)
		require.NoError(t, err)
		require.EqualValues(t, actual, result.Settlement.WalletQuota)
		require.Equal(t, 750, userQuota(t, db, invitee.Id))
		require.Equal(t, 5, userQuota(t, db, inviter.Id))
		_, err = FinalizeWalletConsumption(db, key, invitee.Id, pre, actual+1)
		require.ErrorIs(t, err, ErrWalletReservationConflict)
		require.NoError(t, db.Model(&WalletConsumptionSettlement{}).Where("final_settlement_key = ?", key).Count(&settlements).Error)
		require.EqualValues(t, 1, settlements)
		require.NoError(t, db.Where("invitee_id = ?", invitee.Id).Find(&records).Error)
		require.Len(t, records, 1)
	})

	// Two different keys race for the same balance. The sum must never exceed
	// the wallet, quota bounds metadata must resolve, and the ledger invariant
	// must hold once both reservations are finalized.
	t.Run("two different reservations cannot oversell", func(t *testing.T) {
		user := createRebateTestUser(t, db, 0)
		creditDialectTopUp(t, db, user.Id, "dialect-two-keys", 1000)

		keyA, err := NewWalletFinalSettlementKey(user.Id, "task-submit", "dialect-two-keys-a")
		require.NoError(t, err)
		keyB, err := NewWalletFinalSettlementKey(user.Id, "task-submit", "dialect-two-keys-b")
		require.NoError(t, err)

		var wg sync.WaitGroup
		errs := make(chan error, 2)
		for _, amount := range []int64{600, 400} {
			amount := amount
			wg.Add(1)
			go func() {
				defer wg.Done()
				key := keyA
				if amount == 400 {
					key = keyB
				}
				errs <- ReserveWalletQuota(db, key, user.Id, amount)
			}()
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			require.NoError(t, err)
		}
		require.Zero(t, userQuota(t, db, user.Id))

		// The real dialect bound lookup (information_schema / pg_attribute)
		// must succeed rather than reject the reservation.
		minBound, maxBound, err := walletQuotaBoundsTx(db)
		require.NoError(t, err)
		require.Less(t, minBound, maxBound)

		overflowKey, err := NewWalletFinalSettlementKey(user.Id, "task-submit", "dialect-two-keys-overflow")
		require.NoError(t, err)
		err = ReserveWalletQuota(db, overflowKey, user.Id, 1)
		require.ErrorIs(t, err, ErrWalletInsufficientQuota)

		// Finalizing both reservations moves quota and lots together, which is
		// the state the read-only reconciliation invariant expects.
		_, err = FinalizeWalletConsumption(db, keyA, user.Id, 600, 600)
		require.NoError(t, err)
		_, err = FinalizeWalletConsumption(db, keyB, user.Id, 400, 400)
		require.NoError(t, err)
		require.Zero(t, userQuota(t, db, user.Id))

		var lotRemaining int64
		require.NoError(t, db.Model(&WalletCreditLot{}).Where("user_id = ?", user.Id).
			Select("COALESCE(SUM(quota_remaining), 0)").Scan(&lotRemaining).Error)
		require.Zero(t, lotRemaining)

		mismatches, _, done, err := ReconcileWalletCreditBatch(db, 0, 100)
		require.NoError(t, err)
		require.True(t, done)
		require.Empty(t, mismatches)
	})

	// Commit the durable finalizing barrier, then crash the settlement
	// transaction. The retry must replay the exact committed intent once and
	// never refund the already-consumed usage.
	t.Run("prepared finalization survives a rolled back attempt", func(t *testing.T) {
		user := createRebateTestUser(t, db, 0)
		creditDialectTopUp(t, db, user.Id, "dialect-recovery", 1000)

		key, err := NewWalletFinalSettlementKey(user.Id, "task-submit", "dialect-recovery-1")
		require.NoError(t, err)
		const pre, actual = int64(300), int64(250)

		require.NoError(t, ReserveWalletQuota(db, key, user.Id, pre))
		require.NoError(t, PrepareWalletFinalization(db, key, user.Id, pre, actual))
		require.Equal(t, 700, userQuota(t, db, user.Id))
		require.NoError(t, PrepareWalletFinalization(db, key, user.Id, pre, actual))

		var reservation WalletReservation
		require.NoError(t, db.Where("reservation_key = ?", key).First(&reservation).Error)
		require.Equal(t, WalletReservationFinalizing, reservation.State)

		crash := errors.New("simulated process crash")
		err = db.Transaction(func(tx *gorm.DB) error {
			if _, err := FinalizeWalletConsumptionTx(tx, key, user.Id, pre, actual); err != nil {
				return err
			}
			return crash
		})
		require.ErrorIs(t, err, crash)

		var settlements int64
		require.NoError(t, db.Model(&WalletConsumptionSettlement{}).Where("final_settlement_key = ?", key).Count(&settlements).Error)
		require.Zero(t, settlements)
		require.NoError(t, db.Where("reservation_key = ?", key).First(&reservation).Error)
		require.Equal(t, WalletReservationFinalizing, reservation.State)
		require.Equal(t, 700, userQuota(t, db, user.Id))

		result, err := FinalizeWalletConsumption(db, key, user.Id, pre, actual)
		require.NoError(t, err)
		require.EqualValues(t, actual, result.Settlement.WalletQuota)
		require.Equal(t, 750, userQuota(t, db, user.Id))
		require.NoError(t, db.Model(&WalletConsumptionSettlement{}).Where("final_settlement_key = ?", key).Count(&settlements).Error)
		require.EqualValues(t, 1, settlements)

		recovered, err := RecoverFinalizingWalletSettlements(db, 10)
		require.NoError(t, err)
		require.Zero(t, recovered)
		_, err = FinalizeWalletConsumption(db, key, user.Id, pre, actual)
		require.NoError(t, err)
		require.Equal(t, 750, userQuota(t, db, user.Id))
		require.NoError(t, db.Model(&WalletConsumptionSettlement{}).Where("final_settlement_key = ?", key).Count(&settlements).Error)
		require.EqualValues(t, 1, settlements)
	})

	// Stale reservation refunds and scheduled payout must each be exactly-once
	// under real row locking.
	t.Run("stale expiry and due payout are exactly once", func(t *testing.T) {
		expireUser := createRebateTestUser(t, db, 0)
		creditDialectTopUp(t, db, expireUser.Id, "dialect-expire", 100)
		expireKey, err := NewWalletFinalSettlementKey(expireUser.Id, "task-submit", "dialect-expire-1")
		require.NoError(t, err)
		require.NoError(t, ReserveWalletQuota(db, expireKey, expireUser.Id, 30))
		require.Equal(t, 70, userQuota(t, db, expireUser.Id))
		require.NoError(t, db.Model(&WalletReservation{}).Where("reservation_key = ?", expireKey).
			UpdateColumn("updated_at", time.Now().UTC().Add(-2*time.Hour)).Error)

		cutoff := time.Now().UTC().Add(-time.Hour)
		refunded, err := ExpireStaleWalletReservations(db, cutoff, 10)
		require.NoError(t, err)
		require.Equal(t, 1, refunded)
		require.Equal(t, 100, userQuota(t, db, expireUser.Id))
		refunded, err = ExpireStaleWalletReservations(db, cutoff, 10)
		require.NoError(t, err)
		require.Zero(t, refunded)

		setting := rebateRewardSetting()
		setting.RateBasisPoints = 250
		setting.PayoutMode = operation_setting.RebatePayoutScheduled
		useRebateRewardSetting(t, setting)

		inviter, invitee := createRebateInvitation(t, db)
		creditDialectTopUp(t, db, invitee.Id, "dialect-payout", 1000)
		payoutKey, err := NewWalletFinalSettlementKey(invitee.Id, "task-submit", "dialect-payout-1")
		require.NoError(t, err)
		require.NoError(t, ReserveWalletQuota(db, payoutKey, invitee.Id, 1000))
		_, err = FinalizeWalletConsumption(db, payoutKey, invitee.Id, 1000, 1000)
		require.NoError(t, err)

		var record RebateRecord
		require.NoError(t, db.Where("invitee_id = ?", invitee.Id).First(&record).Error)
		require.Equal(t, RebateStatusPending, record.Status)
		require.EqualValues(t, 25, record.RewardQuota)
		require.True(t, record.AvailableAt.After(time.Now().UTC()))
		require.NoError(t, db.Model(&RebateRecord{}).Where("id = ?", record.ID).
			UpdateColumn("available_at", time.Now().UTC().Add(-time.Minute)).Error)

		paid, err := PayDueWalletRebates(db, time.Now().UTC(), 10)
		require.NoError(t, err)
		require.Equal(t, 1, paid)
		paid, err = PayDueWalletRebates(db, time.Now().UTC(), 10)
		require.NoError(t, err)
		require.Zero(t, paid)

		require.NoError(t, db.First(&record, record.ID).Error)
		require.Equal(t, RebateStatusPaid, record.Status)
		require.Equal(t, 25, userQuota(t, db, inviter.Id))
		var rebateLots int64
		require.NoError(t, db.Model(&WalletCreditLot{}).Where("user_id = ? AND source = ?", inviter.Id, WalletCreditSourceRebate).Count(&rebateLots).Error)
		require.EqualValues(t, 1, rebateLots)
	})

	// The full ledger must satisfy users.quota == SUM(lots) - open debt after
	// every scenario, on the real dialect.
	t.Run("global reconciliation is clean", func(t *testing.T) {
		after := 0
		for {
			mismatches, next, done, err := ReconcileWalletCreditBatch(db, after, 100)
			require.NoError(t, err, "dialect %s reconciliation failed", dialect)
			require.Empty(t, mismatches)
			after = next
			if done {
				break
			}
		}
	})
}
