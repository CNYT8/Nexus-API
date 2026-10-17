package model

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	walletCreditBaselineStateKey = "wallet_credit_baseline_v1"
	walletCreditBaselineRef      = "initial-wallet-snapshot-v1"
)

// AddWalletCreditLotTx atomically creates a source lot and credits its quota.
// A duplicate event is accepted only when its immutable identity and valuation
// match; retries never add the wallet balance twice.
func AddWalletCreditLotTx(tx *gorm.DB, lot WalletCreditLot) (bool, error) {
	if tx == nil {
		return false, errors.New("wallet credit requires a transaction")
	}
	if lot.CreatedAt.IsZero() {
		lot.CreatedAt = time.Now().UTC()
	}
	if lot.UpdatedAt.IsZero() {
		lot.UpdatedAt = lot.CreatedAt
	}
	if err := lot.BeforeCreate(tx); err != nil {
		return false, err
	}
	user, err := walletLockUserTx(tx, lot.UserID)
	if err != nil {
		return false, err
	}

	initialized, err := walletCreditBaselineState(tx)
	if err != nil {
		return false, err
	}
	if !initialized {
		return false, errors.New("wallet credit baseline is not initialized")
	}

	// Check under the user lock, not INSERT ... DO NOTHING RowsAffected:
	// MySQL clientFoundRows can report 1 for an unchanged duplicate row.
	var existing WalletCreditLot
	err = walletCurrentTx(tx).Where("credit_event_key = ?", lot.CreditEventKey).First(&existing).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		result := tx.Create(&lot)
		if result.Error != nil {
			return false, result.Error
		}
		if result.RowsAffected != 1 {
			return false, errors.New("unexpected wallet credit insert row count")
		}
		if err := walletChangeQuotaTx(tx, user, lot.QuotaTotal, false); err != nil {
			return false, err
		}
		if err := repayWalletDebtTx(tx, &lot); err != nil {
			return false, err
		}
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("verify duplicate wallet credit event: %w", err)
	}
	if existing.UserID != lot.UserID || existing.Source != lot.Source || existing.Component != lot.Component ||
		existing.Eligible != lot.Eligible || existing.ReferenceType != lot.ReferenceType || existing.ReferenceID != lot.ReferenceID ||
		existing.QuotaTotal != lot.QuotaTotal || existing.EligibleMoneyMicros != lot.EligibleMoneyMicros ||
		existing.QuotaPerUnitMicros != lot.QuotaPerUnitMicros {
		return false, errors.New("wallet credit event key reused with different immutable values")
	}
	return false, nil
}

// WalletSettlementResult is the durable result of one final wallet settlement.
// Eligible values are calculated from source lots inside the same transaction as
// the wallet debit/refund, so callers never have to reconstruct provenance.
type WalletSettlementResult struct {
	// Applied is true only for a newly created settlement. Tx callers must wait
	// for commit before performing side effects; replays return false.
	Applied                     bool
	PaidInviterID               int
	Settlement                  WalletConsumptionSettlement
	EligibleQuotaConsumed       int64
	EligibleMoneyConsumedMicros int64
	UnallocatedQuota            int64
}

// FinalizeWalletConsumption commits one successful wallet request. The caller
// supplies the server-owned final settlement key, the quota reserved before the
// request, and the actual final wallet usage. The operation is idempotent: an
// identical retry returns the original settlement without changing the wallet,
// while a key reused with different immutable values fails closed.
func FinalizeWalletConsumption(db *gorm.DB, finalSettlementKey string, userID int, preConsumedQuota, walletQuota int64) (*WalletSettlementResult, error) {
	if db == nil {
		return nil, errors.New("wallet settlement requires a database")
	}
	var result *WalletSettlementResult
	err := db.Transaction(func(tx *gorm.DB) error {
		var err error
		result, err = FinalizeWalletConsumptionTx(tx, finalSettlementKey, userID, preConsumedQuota, walletQuota)
		return err
	})
	if err != nil {
		return nil, err
	}
	if result != nil && result.PaidInviterID > 0 {
		_ = walletAfterCommit(db, result.PaidInviterID, nil)
	}
	return result, walletAfterCommit(db, userID, nil)
}

// FinalizeWalletConsumptionTx is the transaction-scoped form used by callers
// that already own an accounting transaction.
func FinalizeWalletConsumptionTx(tx *gorm.DB, finalSettlementKey string, userID int, preConsumedQuota, walletQuota int64) (*WalletSettlementResult, error) {
	if tx == nil || !isCanonicalSHA256(finalSettlementKey) || userID <= 0 || preConsumedQuota < 0 || walletQuota < 0 {
		return nil, errors.New("invalid wallet settlement input")
	}

	user, err := walletLockUserTx(tx, userID)
	if err != nil {
		return nil, err
	}
	reservation, err := walletReservationTx(tx, finalSettlementKey, userID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		reservation = nil // Maintenance/legacy only: consume free lots, never holds.
	} else if err != nil {
		return nil, err
	}
	if reservation != nil {
		if reservation.ReservedQuota != preConsumedQuota || reservation.State == WalletReservationRefunded {
			return nil, ErrWalletReservationConflict
		}
		if reservation.State != WalletReservationReserved && reservation.State != WalletReservationFinalizing && reservation.State != WalletReservationFinalized {
			return nil, ErrWalletReservationConflict
		}
		if reservation.State != WalletReservationReserved && reservation.FinalQuota != walletQuota {
			return nil, fmt.Errorf("wallet settlement key reused with different immutable values: %w", ErrWalletReservationConflict)
		}
	}

	settlement := WalletConsumptionSettlement{
		FinalSettlementKey: finalSettlementKey,
		UserID:             userID,
		PreConsumedQuota:   preConsumedQuota,
		WalletQuota:        walletQuota,
		FinalizedAt:        time.Now().UTC(),
	}
	var existing WalletConsumptionSettlement
	err = walletCurrentTx(tx).Where("final_settlement_key = ?", finalSettlementKey).First(&existing).Error
	if err == nil {
		if existing.UserID != userID || existing.PreConsumedQuota != preConsumedQuota || existing.WalletQuota != walletQuota || existing.AccountingKind != "" {
			return nil, errors.New("wallet settlement key reused with different immutable values")
		}
		if reservation == nil {
			if err := walletBindTerminalKeyTx(tx, finalSettlementKey, userID, preConsumedQuota, walletQuota); err != nil {
				return nil, err
			}
		}
		return &WalletSettlementResult{
			Settlement:                  existing,
			EligibleQuotaConsumed:       existing.EligibleQuotaConsumed,
			EligibleMoneyConsumedMicros: existing.EligibleMoneyConsumedMicros,
			UnallocatedQuota:            existing.UnallocatedQuota,
		}, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	insert := tx.Create(&settlement)
	if insert.Error != nil {
		return nil, insert.Error
	}
	if insert.RowsAffected != 1 {
		return nil, errors.New("unexpected wallet settlement insert row count")
	}

	if reservation != nil && reservation.State == WalletReservationFinalized {
		return nil, ErrWalletReservationConflict
	}
	if err := walletChangeQuotaTx(tx, user, preConsumedQuota-walletQuota, false); err != nil {
		return nil, err
	}
	allocated, eligibleQuota, eligibleMoney, err := allocateWalletLotsTx(tx, finalSettlementKey, userID, walletQuota)
	if err != nil {
		return nil, err
	}
	// Usage that no source lot can cover is recorded as unallocated debt. It
	// can never earn rebates, and a later credit must repay it before becoming
	// spendable or eligible.
	unallocated := walletQuota - allocated
	// Skip a no-op summary update: MySQL changed-rows mode returns zero for it.
	if eligibleQuota != 0 || eligibleMoney != 0 || unallocated != 0 {
		update := tx.Model(&WalletConsumptionSettlement{}).
			Where("final_settlement_key = ?", finalSettlementKey).
			Updates(map[string]interface{}{
				"eligible_quota_consumed":        eligibleQuota,
				"eligible_money_consumed_micros": eligibleMoney,
				"unallocated_quota":              unallocated,
			})
		if update.Error != nil {
			return nil, update.Error
		}
		if update.RowsAffected != 1 {
			return nil, errors.New("wallet settlement result disappeared")
		}
	}
	settlement.EligibleQuotaConsumed = eligibleQuota
	settlement.EligibleMoneyConsumedMicros = eligibleMoney
	settlement.UnallocatedQuota = unallocated
	if reservation != nil {
		if err := walletReleaseHoldsTx(tx, reservation, reservation.ReservedQuota); err != nil {
			return nil, err
		}
		res := tx.Model(&WalletReservation{}).Where("id = ? AND state = ? AND reserved_quota = ?", reservation.ID, reservation.State, preConsumedQuota).
			Updates(map[string]interface{}{"state": WalletReservationFinalized, "final_quota": walletQuota, "updated_at": time.Now().UTC()})
		if res.Error != nil {
			return nil, res.Error
		}
		if res.RowsAffected != 1 {
			return nil, ErrWalletReservationConflict
		}
	} else {
		if err := walletBindTerminalKeyTx(tx, finalSettlementKey, userID, preConsumedQuota, walletQuota); err != nil {
			return nil, err
		}
	}
	// Reward creation and any immediate payout share the settlement transaction
	// so a successful request can never be settled without its rebate entry.
	rebateRecord, err := RecordWalletRebateTx(tx, settlement)
	if err != nil {
		return nil, err
	}
	paidInviterID := 0
	if rebateRecord != nil && rebateRecord.Status == RebateStatusPaid && rebateRecord.InviterID > 0 {
		paidInviterID = rebateRecord.InviterID
	}
	return &WalletSettlementResult{
		Applied:                     true,
		PaidInviterID:               paidInviterID,
		Settlement:                  settlement,
		EligibleQuotaConsumed:       eligibleQuota,
		EligibleMoneyConsumedMicros: eligibleMoney,
		UnallocatedQuota:            unallocated,
	}, nil
}

// allocateWalletLotsTx consumes final wallet usage from source lots in a
// deterministic order (non-eligible funding first, then oldest lots) and
// returns the total allocated quota plus the eligible quota and money. Usage
// that no lot can cover is reported as unallocated debt by the caller; it never
// earns rebates.
func allocateWalletLotsTx(tx *gorm.DB, settlementKey string, userID int, walletQuota int64) (int64, int64, int64, error) {
	if walletQuota <= 0 {
		return 0, 0, 0, nil
	}
	lots, held, err := walletLotsAndHoldsTx(tx, userID)
	if err != nil {
		return 0, 0, 0, err
	}
	var ownHolds []WalletLotHold
	if err := walletCurrentTx(tx).Where("reservation_key = ? AND user_id = ? AND quota_held > 0", settlementKey, userID).Find(&ownHolds).Error; err != nil {
		return 0, 0, 0, err
	}
	own := make(map[int64]int64)
	for _, h := range ownHolds {
		own[h.LotID] = h.QuotaHeld
	}

	// Preserve non-eligible-first, created_at, id order; this is not global FIFO.
	// Only this reservation's holds plus unheld quota may be consumed.
	remaining := walletQuota
	var allocated, eligibleQuota, eligibleMoney int64
	for _, lot := range lots {
		if remaining == 0 {
			break
		}
		consume := lot.QuotaRemaining - held[lot.ID] + own[lot.ID]
		if consume <= 0 {
			continue
		}
		if consume > remaining {
			consume = remaining
		}
		money := int64(0)
		if lot.Eligible {
			if consume == lot.QuotaRemaining {
				// Exhausting a lot always moves all remaining valuation, so floor
				// remainders cannot be lost across repeated partial settlements.
				money = lot.EligibleMoneyRemaining
			} else {
				money, err = walletFloorProduct(consume, lot.EligibleMoneyRemaining, lot.QuotaRemaining)
				if err != nil {
					return 0, 0, 0, err
				}
			}
			if money < 0 || money > lot.EligibleMoneyRemaining {
				return 0, 0, 0, errors.New("wallet lot eligible valuation overflow")
			}
		}

		update := tx.Model(&WalletCreditLot{}).
			Where("id = ? AND user_id = ? AND quota_remaining = ? AND eligible_money_remaining = ?",
				lot.ID, userID, lot.QuotaRemaining, lot.EligibleMoneyRemaining).
			Updates(map[string]interface{}{
				"quota_remaining":          gorm.Expr("quota_remaining - ?", consume),
				"eligible_money_remaining": gorm.Expr("eligible_money_remaining - ?", money),
				"updated_at":               time.Now().UTC(),
			})
		if update.Error != nil {
			return 0, 0, 0, update.Error
		}
		if update.RowsAffected != 1 {
			return 0, 0, 0, errors.New("wallet lot changed during settlement")
		}

		allocation := WalletConsumptionAllocation{
			FinalSettlementKey:          settlementKey,
			LotID:                       lot.ID,
			UserID:                      userID,
			QuotaConsumed:               consume,
			EligibleMoneyConsumedMicros: money,
			CreatedAt:                   time.Now().UTC(),
		}
		if lot.Eligible {
			allocation.EligibleQuotaConsumed = consume
			eligibleQuota, err = walletCheckedAdd(eligibleQuota, consume)
			if err != nil {
				return 0, 0, 0, err
			}
			eligibleMoney, err = walletCheckedAdd(eligibleMoney, money)
			if err != nil {
				return 0, 0, 0, err
			}
		}
		created := tx.Create(&allocation)
		if created.Error != nil {
			return 0, 0, 0, created.Error
		}
		if created.RowsAffected != 1 {
			return 0, 0, 0, errors.New("wallet allocation insert failed")
		}
		remaining -= consume
		allocated += consume
	}
	return allocated, eligibleQuota, eligibleMoney, nil
}

// EligibleMoneyValuationForQuota converts a quota-denominated eligible source
// using the current site quota ratio and returns the ratio snapshot. Truncation
// is toward zero so integer micros never overstate the eligible money value.
func EligibleMoneyValuationForQuota(quota int64) (moneyMicros int64, quotaPerUnitMicros int64, err error) {
	if quota <= 0 {
		return 0, 0, ErrInvalidTopUpQuota
	}
	quotaPerUnit, err := TopUpQuotaPerUnit()
	if err != nil {
		return 0, 0, err
	}
	scale := decimal.NewFromInt(MoneyMicrosPerUnit)
	moneyValue, _ := decimal.NewFromInt(quota).Mul(scale).QuoRem(quotaPerUnit, 0)
	ratioValue := quotaPerUnit.Mul(scale)
	maxInt64 := decimal.NewFromInt(math.MaxInt64)
	if moneyValue.IsNegative() || moneyValue.GreaterThan(maxInt64) || ratioValue.LessThan(decimal.NewFromInt(1)) || ratioValue.GreaterThan(maxInt64) {
		return 0, 0, ErrTopUpQuotaOutOfRange
	}
	return moneyValue.IntPart(), ratioValue.IntPart(), nil
}

type walletBaselineUser struct {
	ID    int `gorm:"column:id"`
	Quota int `gorm:"column:quota"`
}

// InitializeWalletCreditBaseline snapshots every positive balance that
// predates source-attributed wallet accounting as non-eligible legacy lots.
// Negative balances become explicit non-eligible debt, only in this offline
// initialization. No request path automatically reconstructs lots from balances.
// The unique marker is locked inside the same transaction as the complete
// backfill, making competing initializers serialize across database nodes.
// Wallet writes from old application versions must be stopped during rollout.
func InitializeWalletCreditBaseline(db *gorm.DB) error {
	if db == nil {
		return errors.New("wallet credit baseline requires a database")
	}

	var nonceBytes [16]byte
	if _, err := rand.Read(nonceBytes[:]); err != nil {
		return fmt.Errorf("create wallet baseline lock token: %w", err)
	}
	ownerToken := "running:" + hex.EncodeToString(nonceBytes[:])

	return db.Transaction(func(tx *gorm.DB) error {
		marker := RebateSystemState{
			Key:       walletCreditBaselineStateKey,
			Value:     ownerToken,
			UpdatedAt: time.Now().UTC(),
		}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&marker).Error; err != nil {
			return fmt.Errorf("acquire wallet credit baseline marker: %w", err)
		}

		var current RebateSystemState
		// GORM quotes map keys per dialect; a raw "key = ?" predicate is invalid on
		// MySQL because `key` is a reserved word.
		if err := lockForUpdate(tx).Where(map[string]interface{}{"key": walletCreditBaselineStateKey}).First(&current).Error; err != nil {
			return fmt.Errorf("lock wallet credit baseline marker: %w", err)
		}
		if current.Value != ownerToken {
			if len(current.Value) >= len("initialized:") && current.Value[:len("initialized:")] == "initialized:" {
				return nil
			}
			return errors.New("wallet credit baseline is owned by another initializer")
		}

		const batchSize = 500
		lastID := 0
		for {
			users := make([]walletBaselineUser, 0, batchSize)
			if err := tx.Model(&User{}).
				Select("id", "quota").
				Where("id > ? AND quota <> 0", lastID).
				Order("id ASC").
				Limit(batchSize).
				Find(&users).Error; err != nil {
				return fmt.Errorf("read wallet credit baseline users: %w", err)
			}
			if len(users) == 0 {
				break
			}

			lots := make([]WalletCreditLot, 0, len(users))
			createdAt := time.Now().UTC()
			for _, user := range users {
				locked, err := walletLockUserTx(tx, user.ID)
				if err != nil {
					return err
				}
				user.Quota = locked.Quota
				if user.Quota == 0 {
					continue
				}
				if user.Quota < 0 {
					if int64(user.Quota) == math.MinInt64 {
						return ErrTopUpQuotaOutOfRange
					}
					debtKey, err := NewWalletFinalSettlementKey(user.ID, "legacy-baseline-debt", walletCreditBaselineRef)
					if err != nil {
						return err
					}
					debt := WalletConsumptionSettlement{FinalSettlementKey: debtKey, UserID: user.ID, WalletQuota: -int64(user.Quota), UnallocatedQuota: -int64(user.Quota), AccountingKind: "legacy_baseline", FinalizedAt: createdAt}
					res := tx.Create(&debt)
					if res.Error != nil {
						return res.Error
					}
					if res.RowsAffected != 1 {
						return errors.New("legacy debt insert failed")
					}
					continue
				}
				key, err := NewWalletCreditEventKey(user.ID, WalletCreditSourceLegacy, "migration", walletCreditBaselineRef, "legacy_baseline")
				if err != nil {
					return err
				}
				lots = append(lots, WalletCreditLot{
					CreditEventKey: key,
					UserID:         user.ID,
					Source:         WalletCreditSourceLegacy,
					Component:      "legacy_baseline",
					Eligible:       false,
					ReferenceType:  "migration",
					ReferenceID:    walletCreditBaselineRef,
					QuotaTotal:     int64(user.Quota),
					QuotaRemaining: int64(user.Quota),
					CreatedAt:      createdAt,
					UpdatedAt:      createdAt,
				})
			}
			if len(lots) > 0 {
				res := tx.CreateInBatches(&lots, 200)
				if res.Error != nil {
					return fmt.Errorf("write legacy wallet credit baseline: %w", res.Error)
				}
				if res.RowsAffected != int64(len(lots)) {
					return errors.New("legacy wallet baseline insert count mismatch")
				}
			}
			lastID = users[len(users)-1].ID
		}

		completed := RebateSystemState{
			Key:       walletCreditBaselineStateKey,
			Value:     "initialized:" + time.Now().UTC().Format(time.RFC3339Nano),
			UpdatedAt: time.Now().UTC(),
		}
		result := tx.Model(&RebateSystemState{}).
			Where(map[string]interface{}{"key": walletCreditBaselineStateKey, "value": ownerToken}).
			Updates(map[string]interface{}{"value": completed.Value, "updated_at": completed.UpdatedAt})
		if result.Error != nil {
			return fmt.Errorf("complete wallet credit baseline: %w", result.Error)
		}
		if result.RowsAffected != 1 {
			return errors.New("wallet credit baseline marker changed unexpectedly")
		}
		return nil
	})
}
