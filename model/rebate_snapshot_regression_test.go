package model

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// These tests inspect generated SQL and inject query results without connecting
// to a server. They are regression checks, not evidence of database isolation.
func newRebateSnapshotDryRunDB(t *testing.T, dialect string) *gorm.DB {
	t.Helper()
	var dialector gorm.Dialector
	switch dialect {
	case "mysql":
		dialector = mysql.New(mysql.Config{
			DSN:                       "user:password@tcp(127.0.0.1:3306)/nexus",
			SkipInitializeWithVersion: true,
		})
	case "postgres":
		dialector = postgres.New(postgres.Config{
			DSN:                  "host=127.0.0.1 user=nexus password=nexus dbname=nexus port=5432 sslmode=disable",
			PreferSimpleProtocol: true,
		})
	default:
		t.Fatalf("unsupported dialect %q", dialect)
	}
	db, err := gorm.Open(dialector, &gorm.Config{
		DryRun: true, DisableAutomaticPing: true, SkipDefaultTransaction: true,
	})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	return db
}

func TestRebateConfigSnapshotQueryUsesShareOnlyOnMySQL(t *testing.T) {
	for _, dialect := range []string{"mysql", "postgres"} {
		for _, locking := range []bool{false, true} {
			name := dialect + "/snapshot"
			if locking {
				name = dialect + "/accounting"
			}
			t.Run(name, func(t *testing.T) {
				db := newRebateSnapshotDryRunDB(t, dialect)
				var queries []string
				require.NoError(t, db.Callback().Query().After("gorm:query").Register("test:rebate-snapshot-sql", func(tx *gorm.DB) {
					queries = append(queries, tx.Statement.SQL.String())
				}))
				_, err := readRebateSetting(db, locking)
				require.NoError(t, err)
				require.Len(t, queries, 1, "all persisted fields must share one statement snapshot")
				if dialect == "mysql" && locking {
					require.Contains(t, queries[0], "FOR SHARE")
				} else {
					require.NotContains(t, queries[0], "FOR SHARE")
					require.NotContains(t, queries[0], "FOR UPDATE")
				}
				if dialect == "mysql" {
					require.Contains(t, queries[0], "`key` LIKE ?")
				} else {
					require.Contains(t, queries[0], `"key" LIKE $1`)
				}
			})
		}
	}
}

func TestWalletCreditUsesGateStateBeforeUserLock(t *testing.T) {
	for _, entry := range []string{"topup", "lot", "admin", "override", "redemption", "gift"} {
		for _, state := range []string{"initialized:committed", "", "running:interrupted"} {
			// Source legacy credits consult global quota metadata; keep this
			// offline check confined to activation and fail-closed behavior.
			if state == "" && entry != "topup" && entry != "lot" {
				continue
			}
			t.Run(entry+"/"+state, func(t *testing.T) {
				db := newRebateSnapshotDryRunDB(t, "mysql")
				lot, err := newTopUpWalletLot(17, "snapshot-regression", 100)
				require.NoError(t, err)
				switch entry {
				case "admin", "override":
					lot.Source, lot.Component = WalletCreditSourceAdmin, "ordinary"
					lot.ReferenceType = "membership_grant"
					if entry == "override" {
						lot.ReferenceType = "server_generated"
					}
				case "redemption":
					lot.Source, lot.Component = WalletCreditSourceRedemption, "redeemed"
					lot.ReferenceType = "redemption"
				case "gift":
					lot.Source, lot.Component = WalletCreditSourceAdmin, "gift"
					lot.ReferenceType, lot.Eligible = "membership_gift", false
					lot.EligibleMoneyMicros, lot.EligibleMoneyRemaining, lot.QuotaPerUnitMicros = 0, 0, 0
				}
				lot.CreditEventKey, err = NewWalletCreditEventKey(lot.UserID, lot.Source, lot.ReferenceType, lot.ReferenceID, lot.Component)
				require.NoError(t, err)
				stopAtUser := errors.New("stop before legacy quota mutation")
				gateHeld, userLocked := false, false
				currentReads, lotReads := 0, 0
				observeGate := func(tx *gorm.DB) {
					if tx.Statement.Table != "rebate_system_states" {
						return
					}
					require.False(t, userLocked && !gateHeld, "must not acquire the baseline gate after a user lock")
					gateHeld = true
				}
				require.NoError(t, db.Callback().Create().After("gorm:create").Register("test:rebate-gate-create", observeGate))
				require.NoError(t, db.Callback().Update().After("gorm:update").Register("test:rebate-gate-update", observeGate))
				require.NoError(t, db.Callback().Query().After("gorm:query").Register("test:rebate-stale-snapshot", func(tx *gorm.DB) {
					switch dest := tx.Statement.Dest.(type) {
					case *RebateSystemState:
						if _, locking := tx.Statement.Clauses["FOR"]; !locking {
							// Simulate a repeatable-read snapshot taken before activation.
							tx.AddError(gorm.ErrRecordNotFound)
							return
						}
						require.True(t, gateHeld)
						currentReads++
						if state == "" {
							tx.AddError(gorm.ErrRecordNotFound)
							return
						}
						*dest = RebateSystemState{Key: walletCreditBaselineStateKey, Value: state}
						tx.RowsAffected = 1
					case *User:
						require.True(t, gateHeld)
						require.Positive(t, currentReads, "mode must be resolved before locking the user")
						userLocked = true
						if state != "initialized:committed" {
							tx.AddError(stopAtUser)
							return
						}
						dest.Id, dest.Quota = lot.UserID, int(lot.QuotaTotal)
						tx.RowsAffected = 1
					case *WalletCreditLot:
						require.True(t, userLocked)
						lotReads++
						// A matching replay verifies the ledger path without mutations.
						*dest = lot
						tx.RowsAffected = 1
					default:
						t.Fatalf("unexpected query destination %T", dest)
					}
				}))
				var applied bool
				switch entry {
				case "topup":
					applied, err = CreditTopUpTx(db, lot.UserID, lot.ReferenceID, int(lot.QuotaTotal))
				case "lot":
					applied, err = AddWalletCreditLotTx(db, lot)
				default:
					applied, err = CreditWalletSourceTx(db, lot.UserID, lot.Source, lot.Component, lot.ReferenceType, lot.ReferenceID, int(lot.QuotaTotal))
				}
				require.False(t, applied)
				switch state {
				case "initialized:committed":
					require.NoError(t, err)
					require.Equal(t, 1, lotReads, "current active mode must reach idempotent source credit")
				case "":
					if entry == "topup" {
						require.ErrorIs(t, err, stopAtUser)
					} else {
						require.ErrorContains(t, err, "wallet credit baseline is not initialized")
						require.False(t, userLocked)
					}
				default:
					require.ErrorContains(t, err, "wallet credit baseline initialization is incomplete")
					require.False(t, userLocked)
				}
			})
		}
	}
}
