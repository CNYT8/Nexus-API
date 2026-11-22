package model

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// The SQLite fixture checks control flow and rollback only. It does not prove
// MySQL/PostgreSQL lock waiting or snapshot behavior.
func TestClaimEmptyResponseWalletGateBeforeUserLock(t *testing.T) {
	for _, mode := range []string{"legacy", "active", "gate_error", "incomplete"} {
		t.Run(mode, func(t *testing.T) {
			setupEmptyResponseTest(t)
			db := DB
			if mode == "active" {
				enableCreditSourceBaseline(t)
			} else if mode == "incomplete" {
				require.NoError(t, db.Create(&RebateSystemState{
					Key: walletCreditBaselineStateKey, Value: "running:interrupted", UpdatedAt: time.Now().UTC(),
				}).Error)
			}
			user := creditSourceTestUser(t, "empty-response-gate-"+mode, 100)
			now := time.Now().Unix()
			createEmptyResponseLog(t, user.Id, "empty-response-gate-"+mode, now-60, 100)

			gateError := errors.New("injected empty response baseline gate failure")
			observing, gateWritten, markerRead := true, false, false
			userReads := 0
			const queryCallback = "test:empty-response-wallet-query-order"
			const updateCallback = "test:empty-response-wallet-gate-error"
			require.NoError(t, db.Callback().Update().Before("gorm:update").Register(updateCallback, func(tx *gorm.DB) {
				if !observing || tx.Statement.Table != "rebate_system_states" {
					return
				}
				// Discovery runs in its own transaction and locks the user once.
				// The claim's first gate write must precede its second user read.
				if !gateWritten {
					require.Equal(t, 1, userReads)
				}
				gateWritten = true
				if mode == "gate_error" {
					tx.AddError(gateError)
				}
			}))
			t.Cleanup(func() { require.NoError(t, db.Callback().Update().Remove(updateCallback)) })
			require.NoError(t, db.Callback().Query().Before("gorm:query").Register(queryCallback, func(tx *gorm.DB) {
				if !observing {
					return
				}
				switch tx.Statement.Dest.(type) {
				case *RebateSystemState:
					require.True(t, gateWritten)
					markerRead = true
				case *User:
					userReads++
					if userReads > 1 {
						require.True(t, markerRead, "claim must resolve the baseline before locking the user")
					}
				}
			}))
			t.Cleanup(func() { require.NoError(t, db.Callback().Query().Remove(queryCallback)) })

			count, total, err := ClaimEmptyResponseCompensation(user.Id, now)
			observing = false
			require.True(t, gateWritten)
			if mode == "gate_error" || mode == "incomplete" {
				if mode == "gate_error" {
					require.ErrorIs(t, err, gateError)
				} else {
					require.ErrorContains(t, err, "wallet credit baseline initialization is incomplete")
				}
				require.Equal(t, 1, userReads, "only the separate discovery transaction may lock the user")
				require.Zero(t, count)
				require.Zero(t, total)
			} else {
				require.NoError(t, err)
				require.Equal(t, 1, count)
				require.Equal(t, 70, total)
				require.GreaterOrEqual(t, userReads, 2)
				count, total, err = ClaimEmptyResponseCompensation(user.Id, now)
				require.NoError(t, err)
				require.Zero(t, count)
				require.Zero(t, total)
			}

			var refreshed User
			require.NoError(t, db.First(&refreshed, user.Id).Error)
			var record EmptyResponseRecord
			require.NoError(t, db.Where("user_id = ?", user.Id).First(&record).Error)
			if mode == "gate_error" || mode == "incomplete" {
				require.Equal(t, 100, refreshed.Quota)
				require.Equal(t, "pending", record.Status)
				require.Zero(t, record.RefundedAt)
			} else {
				require.Equal(t, 170, refreshed.Quota)
				require.Equal(t, "refunded", record.Status)
			}
			var lots []WalletCreditLot
			require.NoError(t, db.Where("user_id = ?", user.Id).Find(&lots).Error)
			if mode == "active" {
				require.Len(t, lots, 1)
				require.Equal(t, WalletCreditSourcePromotion, lots[0].Source)
				require.Equal(t, "empty_response_refund", lots[0].Component)
				require.Equal(t, record.RequestId, lots[0].ReferenceID)
				require.False(t, lots[0].Eligible)
				require.Zero(t, lots[0].EligibleMoneyMicros)
			} else {
				require.Empty(t, lots)
			}
		})
	}
}
