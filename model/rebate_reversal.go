package model

import (
	"errors"
	"fmt"
	"math/big"
	"time"

	"gorm.io/gorm"
)

// WalletSourceReversal never edits original lot face value, unit price, or
// consumption snapshots. Remaining balances and separate debt are reversible.
type WalletSourceReversal struct {
	ID            int64  `gorm:"primaryKey"`
	SourceKey     string `gorm:"type:char(64);not null;uniqueIndex"`
	UserID        int    `gorm:"not null;index"`
	LotID         int64  `gorm:"not null;index"`
	OriginalQuota int64  `gorm:"not null"`
	ReversedQuota int64  `gorm:"not null;default:0"`
	Pending       bool   `gorm:"not null;default:false;index"`
	TargetQuota   int64  `gorm:"not null;default:0"`
	// Inclusive exposure upper bound; only its excess over ReversedQuota is frozen.
	QuarantineQuota    int64  `gorm:"not null;default:0"`
	RetiredQuota       int64  `gorm:"not null;default:0"`
	RetiredMoneyMicros int64  `gorm:"not null;default:0"`
	DebtKey            string `gorm:"type:char(64);not null"`
	UpdatedAt          time.Time
}

// RebateSourceReversal overlays ONE original allocation, not its entire mixed
// settlement. Non-reward allocations are retained too for restoration audit.
type RebateSourceReversal struct {
	ID                  int64  `gorm:"primaryKey"`
	AllocationID        int64  `gorm:"not null;uniqueIndex"`
	SourceKey           string `gorm:"type:char(64);not null;index"`
	RecordID            int64  `gorm:"not null;default:0;index"`
	ReversedQuota       int64  `gorm:"not null;default:0"`
	ReversedMoneyMicros int64  `gorm:"not null;default:0"`
	// Inclusive upper bounds projected onto this original allocation.
	QuarantinedQuota       int64 `gorm:"not null;default:0"`
	QuarantinedMoneyMicros int64 `gorm:"not null;default:0"`
	UpdatedAt              time.Time
}

// WalletNetEligibleMoneyTx preserves the historical eligible face values but
// excludes the source fraction invalidated by a verified payment reversal.
// Ordinary/override administrator credits and gifts retain their own policy.
func WalletNetEligibleMoneyTx(tx *gorm.DB, userID int) (int64, error) {
	if _, err := walletLockUserTx(tx, userID); err != nil {
		return 0, err
	}
	tx = walletCurrentTx(tx).Session(&gorm.Session{})
	var lots []WalletCreditLot
	if err := tx.Where("user_id = ? AND eligible = ?", userID, true).Find(&lots).Error; err != nil {
		return 0, err
	}
	var reversals []WalletSourceReversal
	if err := tx.Where("user_id = ? AND lot_id > 0 AND reversed_quota > 0", userID).Find(&reversals).Error; err != nil {
		return 0, err
	}
	byLot := make(map[int64]int64)
	for _, r := range reversals {
		n, err := walletCheckedAdd(byLot[r.LotID], r.ReversedQuota)
		if err != nil {
			return 0, err
		}
		byLot[r.LotID] = n
	}
	var bindings []TopUpPaymentBinding
	if err := tx.Where("user_id = ? AND lot_id > 0 AND paid_minor > 0", userID).Find(&bindings).Error; err != nil {
		return 0, err
	}
	bound := make(map[int64]TopUpPaymentBinding)
	for _, binding := range bindings {
		if _, exists := bound[binding.LotID]; exists {
			return 0, ErrTopUpReversalConflict
		}
		bound[binding.LotID] = binding
	}
	var total int64
	for _, lot := range lots {
		if lot.QuotaTotal <= 0 || byLot[lot.ID] > lot.QuotaTotal {
			return 0, ErrTopUpReversalConflict
		}
		numerator, denominator := lot.QuotaTotal-byLot[lot.ID], lot.QuotaTotal
		if binding, ok := bound[lot.ID]; ok {
			var states []TopUpReversalState
			if err := tx.Where("binding_id = ?", binding.ID).Find(&states).Error; err != nil {
				return 0, err
			}
			_, possible, err := topUpReversalExposure(states, binding.PaidMinor)
			if err != nil {
				return 0, err
			}
			numerator, denominator = binding.PaidMinor-possible, binding.PaidMinor
		}
		net, err := walletFloorProduct(lot.EligibleMoneyMicros, numerator, denominator)
		if err != nil {
			return 0, err
		}
		total, err = walletCheckedAdd(total, net)
		if err != nil {
			return 0, err
		}
	}
	return total, nil
}

// stageWalletSourceReversalTx changes NO balance, debt, or original snapshot.
// It commits the exact pending target even when a source/reward has live holds.
func stageWalletSourceReversalTx(tx *gorm.DB, key string, userID int, lotID, original, target, quarantine int64, changed map[int]bool) error {
	if target < 0 || original <= 0 || target > original || quarantine < target || quarantine > original || !isCanonicalSHA256(key) {
		return ErrTopUpReversalConflict
	}
	if _, err := walletLockUserTx(tx, userID); err != nil {
		return err
	}
	var state WalletSourceReversal
	err := walletCurrentTx(tx).Where("source_key = ?", key).First(&state).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	if state.ID == 0 {
		debtKey, err := NewRebateIdempotencyKey("source-reversal-debt-v1", key)
		if err != nil {
			return err
		}
		state = WalletSourceReversal{SourceKey: key, UserID: userID, LotID: lotID, OriginalQuota: original, DebtKey: debtKey}
	}
	if state.UserID != userID || state.LotID != lotID || state.OriginalQuota != original || state.ReversedQuota < 0 || state.ReversedQuota > original || state.RetiredQuota < 0 || state.RetiredQuota > state.ReversedQuota {
		return ErrTopUpReversalConflict
	}
	state.Pending, state.TargetQuota, state.QuarantineQuota = true, target, quarantine
	if err := tx.Save(&state).Error; err != nil {
		return err
	}
	changed[userID] = true
	return projectPendingSourceAllocationsTx(tx, &state, changed)
}

func projectPendingSourceAllocationsTx(tx *gorm.DB, state *WalletSourceReversal, changed map[int]bool) error {
	if state.TargetQuota < 0 || state.TargetQuota > state.OriginalQuota || state.QuarantineQuota < 0 || state.QuarantineQuota > state.OriginalQuota || state.ReversedQuota < 0 || state.ReversedQuota > state.OriginalQuota || state.RetiredQuota < 0 || state.RetiredQuota > state.ReversedQuota {
		return ErrTopUpReversalConflict
	}
	// Added columns are zero on legacy rows. Their certain target still applies.
	quarantine := max(state.TargetQuota, state.QuarantineQuota)
	if state.LotID == 0 {
		return nil
	}
	var lot WalletCreditLot
	if err := walletCurrentTx(tx).Where("id = ? AND user_id = ?", state.LotID, state.UserID).First(&lot).Error; err != nil {
		return err
	}
	if lot.QuotaTotal != state.OriginalQuota || lot.QuotaRemaining < 0 || lot.QuotaRemaining > lot.QuotaTotal {
		return ErrTopUpReversalConflict
	}
	if lot.Source == WalletCreditSourceRebate {
		return nil
	}
	debt := state.ReversedQuota - state.RetiredQuota
	delta := state.TargetQuota - state.ReversedQuota
	if delta >= 0 {
		debt += max(int64(0), delta-lot.QuotaRemaining)
	} else {
		debt -= min(-delta, debt)
	}
	maxDebt := state.ReversedQuota - state.RetiredQuota
	maxDelta := quarantine - state.ReversedQuota
	if maxDelta >= 0 {
		maxDebt += max(int64(0), maxDelta-lot.QuotaRemaining)
	} else {
		maxDebt -= min(-maxDelta, maxDebt)
	}
	return setSourceAllocationReversalsTx(tx, state, debt, maxDebt, changed, state.Pending)
}

func stageRebateSourceReversalTx(tx *gorm.DB, record *RebateRecord) error {
	if record.PayoutLotID == nil {
		return nil
	}
	if record.RewardQuota <= 0 {
		return ErrTopUpReversalConflict
	}
	target, quarantine, err := rebateSourceReversalExposureTx(tx, record)
	if err != nil {
		return err
	}
	key, err := NewRebateIdempotencyKey("rebate-source-reversal-v1", fmt.Sprint(record.ID))
	if err != nil {
		return err
	}
	return stageWalletSourceReversalTx(tx, key, record.InviterID, *record.PayoutLotID, record.RewardQuota, target, quarantine, map[int]bool{})
}

// Holds acquired before quarantine can finish. Refresh their original-source
// overlays in the SAME settlement transaction, after any new record exists.
func refreshPendingSourceReversalsTx(tx *gorm.DB, userID int) error {
	var states []WalletSourceReversal
	if err := walletCurrentTx(tx).Where("user_id = ? AND (pending = ? OR quarantine_quota > reversed_quota)", userID, true).Order("id ASC").Find(&states).Error; err != nil {
		return err
	}
	for i := range states {
		if err := projectPendingSourceAllocationsTx(tx, &states[i], map[int]bool{}); err != nil {
			return err
		}
	}
	return nil
}

// Only a positive, not-yet-debited delta is unavailable. A won dispute must not
// expose its restoration before the accounting transaction commits.
func walletPendingReversalQuotasTx(tx *gorm.DB, userID int) (map[int64]int64, int64, error) {
	var states []WalletSourceReversal
	if err := walletCurrentTx(tx).Where("user_id = ? AND (pending = ? OR quarantine_quota > reversed_quota)", userID, true).Find(&states).Error; err != nil {
		return nil, 0, err
	}
	lots := make(map[int64]int64)
	var total int64
	for _, state := range states {
		if state.TargetQuota < 0 || state.TargetQuota > state.OriginalQuota || state.ReversedQuota < 0 || state.ReversedQuota > state.OriginalQuota {
			return nil, 0, ErrTopUpReversalConflict
		}
		if state.QuarantineQuota < 0 || state.QuarantineQuota > state.OriginalQuota {
			return nil, 0, ErrTopUpReversalConflict
		}
		// Legacy rows default to zero; never let that erase a certain target.
		n := max(int64(0), max(state.TargetQuota, state.QuarantineQuota)-state.ReversedQuota)
		var err error
		total, err = walletCheckedAdd(total, n)
		if err != nil {
			return nil, 0, err
		}
		lots[state.LotID], err = walletCheckedAdd(lots[state.LotID], n)
		if err != nil {
			return nil, 0, err
		}
	}
	return lots, total, nil
}

func walletSettlementReversalPendingTx(tx *gorm.DB, settlementKey string, userID int) (bool, error) {
	var states []WalletSourceReversal
	if err := walletCurrentTx(tx).Where("user_id = ? AND (pending = ? OR quarantine_quota > reversed_quota) AND lot_id > 0", userID, true).Find(&states).Error; err != nil {
		return false, err
	}
	if len(states) == 0 {
		return false, nil
	}
	lots := make(map[int64]bool)
	for _, state := range states {
		lots[state.LotID] = true
	}
	var allocations []WalletConsumptionAllocation
	if err := walletCurrentTx(tx).Where("final_settlement_key = ? AND user_id = ?", settlementKey, userID).Find(&allocations).Error; err != nil {
		return false, err
	}
	for _, allocation := range allocations {
		if lots[allocation.LotID] {
			return true, nil
		}
	}
	return false, nil
}

// Payouts know the record key, while allocations use the original settlement
// key. Resolve from persisted settlements, never from user/provider metadata.
func rebateRecordReversalPendingTx(tx *gorm.DB, record *RebateRecord) (bool, error) {
	if tx == nil || record == nil || record.InviteeID <= 0 {
		return false, ErrTopUpReversalConflict
	}
	var sources []WalletSourceReversal
	if err := walletCurrentTx(tx).Where("user_id = ? AND (pending = ? OR quarantine_quota > reversed_quota) AND lot_id > 0", record.InviteeID, true).Find(&sources).Error; err != nil {
		return false, err
	}
	if len(sources) == 0 {
		return false, nil
	}
	lots := make([]int64, 0, len(sources))
	for _, source := range sources {
		lots = append(lots, source.LotID)
	}
	var allocations []WalletConsumptionAllocation
	if err := walletCurrentTx(tx).Where("user_id = ? AND lot_id IN ?", record.InviteeID, lots).Find(&allocations).Error; err != nil {
		return false, err
	}
	for _, allocation := range allocations {
		key, err := NewRebateIdempotencyKey("rebate-record-v1", allocation.FinalSettlementKey)
		if err != nil {
			return false, err
		}
		if key == record.SettlementKey {
			return true, nil
		}
	}
	return false, nil
}

func reverseWalletSourceTx(tx *gorm.DB, key string, userID int, lotID, original, target, quarantine int64, changed map[int]bool) error {
	if target < 0 || original <= 0 || target > original || quarantine < target || quarantine > original || !isCanonicalSHA256(key) {
		return ErrTopUpReversalConflict
	}
	user, err := walletLockUserTx(tx, userID)
	if err != nil {
		return err
	}
	var state WalletSourceReversal
	err = walletCurrentTx(tx).Where("source_key = ?", key).First(&state).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	if state.ID == 0 {
		debtKey, err := NewRebateIdempotencyKey("source-reversal-debt-v1", key)
		if err != nil {
			return err
		}
		state = WalletSourceReversal{SourceKey: key, UserID: userID, LotID: lotID, OriginalQuota: original, DebtKey: debtKey}
	} else if state.UserID != userID || state.LotID != lotID || state.OriginalQuota != original {
		return ErrTopUpReversalConflict
	}
	if state.ReversedQuota < 0 || state.ReversedQuota > original || state.RetiredQuota < 0 || state.RetiredQuota > state.ReversedQuota {
		return ErrTopUpReversalConflict
	}
	if state.ReversedQuota == target {
		state.Pending, state.TargetQuota, state.QuarantineQuota = false, target, quarantine
		if err := tx.Save(&state).Error; err != nil {
			return err
		}
		return projectPendingSourceAllocationsTx(tx, &state, changed)
	}
	delta := target - state.ReversedQuota
	if lotID == 0 {
		if err := walletChangeQuotaTx(tx, user, -delta, false); err != nil {
			return err
		}
		state.ReversedQuota, state.TargetQuota, state.QuarantineQuota, state.Pending = target, target, quarantine, false
		changed[userID] = true
		return tx.Save(&state).Error
	}
	var lot WalletCreditLot
	if err := walletCurrentTx(tx).Where("id = ? AND user_id = ?", lotID, userID).First(&lot).Error; err != nil {
		return err
	}
	if lot.QuotaTotal != original || lot.QuotaRemaining < 0 || lot.EligibleMoneyRemaining < 0 {
		return ErrTopUpReversalConflict
	}
	// Existence checks must also bypass an outer repeatable-read snapshot.
	// Read a row rather than locking an aggregate (unsupported on PostgreSQL).
	var held []WalletLotHold
	if err := walletCurrentTx(tx).Where("lot_id = ? AND quota_held > 0", lotID).Limit(1).Find(&held).Error; err != nil {
		return err
	}
	if len(held) > 0 {
		return ErrTopUpReversalHeld
	}
	oldDebt := state.ReversedQuota - state.RetiredQuota
	newDebt := oldDebt
	if delta > 0 {
		retire := min(delta, lot.QuotaRemaining)
		money := int64(0)
		if retire > 0 {
			money, err = walletFloorProduct(retire, lot.EligibleMoneyRemaining, lot.QuotaRemaining)
			if err != nil {
				return err
			}
		}
		lot.QuotaRemaining -= retire
		lot.EligibleMoneyRemaining -= money
		state.RetiredQuota += retire
		state.RetiredMoneyMicros += money
		newDebt += delta - retire
	} else {
		restore := -delta
		debtRestore := min(restore, oldDebt)
		newDebt -= debtRestore
		restore -= debtRestore
		if restore > 0 {
			money, err := walletFloorProduct(restore, state.RetiredMoneyMicros, state.RetiredQuota)
			if err != nil {
				return err
			}
			lot.QuotaRemaining += restore
			lot.EligibleMoneyRemaining += money
			state.RetiredQuota -= restore
			state.RetiredMoneyMicros -= money
		}
	}
	if err := tx.Model(&lot).Updates(map[string]interface{}{"quota_remaining": lot.QuotaRemaining, "eligible_money_remaining": lot.EligibleMoneyRemaining, "updated_at": time.Now().UTC()}).Error; err != nil {
		return err
	}
	// One debit for the refund. Consumed amounts become explicit debt; adding
	// a second consumption debit here would charge the original use twice.
	if err := walletChangeQuotaTx(tx, user, -delta, false); err != nil {
		return err
	}
	if err := setSourceReversalDebtTx(tx, &state, newDebt); err != nil {
		return err
	}
	state.ReversedQuota, state.TargetQuota, state.QuarantineQuota, state.Pending = target, target, quarantine, false
	if err := tx.Save(&state).Error; err != nil {
		return err
	}
	changed[userID] = true
	if lot.Source != WalletCreditSourceRebate {
		return projectPendingSourceAllocationsTx(tx, &state, changed)
	}
	return nil // non-eligible reward credits never generate recursive rebates
}

func setSourceReversalDebtTx(tx *gorm.DB, state *WalletSourceReversal, target int64) error {
	var debt WalletConsumptionSettlement
	err := walletCurrentTx(tx).Where("final_settlement_key = ?", state.DebtKey).First(&debt).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		if target == 0 {
			return nil
		}
		debt = WalletConsumptionSettlement{FinalSettlementKey: state.DebtKey, UserID: state.UserID, WalletQuota: target, UnallocatedQuota: target, AccountingKind: "payment_reversal", FinalizedAt: time.Now().UTC()}
		return tx.Create(&debt).Error
	}
	if err != nil {
		return err
	}
	if debt.UserID != state.UserID || debt.AccountingKind != "payment_reversal" || debt.DebtRepaidQuota > debt.UnallocatedQuota {
		return ErrTopUpReversalConflict
	}
	// A won dispute restores prior debt repayments to their EXACT funding
	// sources, not to this already-consumed top-up and never at today's price.
	if target < debt.DebtRepaidQuota {
		restore := debt.DebtRepaidQuota - target
		var repayments []WalletDebtRepayment
		if err := walletCurrentTx(tx).Where("final_settlement_key = ? AND quota_repaid > 0", state.DebtKey).Order("id DESC").Find(&repayments).Error; err != nil {
			return err
		}
		for _, repayment := range repayments {
			if restore == 0 {
				break
			}
			n := min(restore, repayment.QuotaRepaid)
			var lot WalletCreditLot
			if err := walletCurrentTx(tx).Where("id = ? AND user_id = ?", repayment.LotID, state.UserID).First(&lot).Error; err != nil {
				return err
			}
			var invalidated []WalletSourceReversal
			if err := walletCurrentTx(tx).Where("lot_id = ? AND (reversed_quota > 0 OR (pending = ? AND target_quota > 0))", lot.ID, true).Limit(1).Find(&invalidated).Error; err != nil {
				return err
			}
			if len(invalidated) > 0 {
				return errors.New("reversal repayment source was itself reversed; reconciliation required")
			}
			money, err := walletFloorProduct(n, repayment.MoneyRetiredMicros, repayment.QuotaRepaid)
			if err != nil {
				return err
			}
			if lot.QuotaRemaining > lot.QuotaTotal-n || lot.EligibleMoneyRemaining > lot.EligibleMoneyMicros-money {
				return ErrTopUpReversalConflict
			}
			if err := tx.Model(&lot).Updates(map[string]interface{}{"quota_remaining": lot.QuotaRemaining + n, "eligible_money_remaining": lot.EligibleMoneyRemaining + money, "updated_at": time.Now().UTC()}).Error; err != nil {
				return err
			}
			if err := tx.Model(&repayment).Updates(map[string]interface{}{"quota_repaid": repayment.QuotaRepaid - n, "money_retired_micros": repayment.MoneyRetiredMicros - money}).Error; err != nil {
				return err
			}
			restore -= n
		}
		if restore != 0 {
			return ErrTopUpReversalConflict
		}
		debt.DebtRepaidQuota = target
	}
	return tx.Model(&debt).Updates(map[string]interface{}{"wallet_quota": target, "unallocated_quota": target, "debt_repaid_quota": debt.DebtRepaidQuota}).Error
}

func setSourceAllocationReversalsTx(tx *gorm.DB, source *WalletSourceReversal, target, quarantine int64, changed map[int]bool, pending bool) error {
	if target < 0 || quarantine < target || quarantine > source.OriginalQuota {
		return ErrTopUpReversalConflict
	}
	var allocations []WalletConsumptionAllocation
	if err := walletCurrentTx(tx).Where("lot_id = ? AND user_id = ?", source.LotID, source.UserID).Order("id DESC").Find(&allocations).Error; err != nil {
		return err
	}
	// Retire latest original allocations first; debt repayments/adjustments
	// without an earned reward naturally carry no reward to claw back.
	remaining, possibleRemaining := target, quarantine
	recordIDs := make(map[int64]bool)
	for _, allocation := range allocations {
		n := min(remaining, allocation.QuotaConsumed)
		q := min(possibleRemaining, allocation.QuotaConsumed)
		remaining -= n
		possibleRemaining -= q
		money, err := walletFloorProduct(n, allocation.EligibleMoneyConsumedMicros, allocation.QuotaConsumed)
		if err != nil {
			return err
		}
		quarantinedMoney, err := walletFloorProduct(q, allocation.EligibleMoneyConsumedMicros, allocation.QuotaConsumed)
		if err != nil {
			return err
		}
		var row RebateSourceReversal
		err = walletCurrentTx(tx).Where("allocation_id = ?", allocation.ID).First(&row).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if row.ID == 0 && q == 0 {
			continue
		}
		if row.ID == 0 {
			row.AllocationID, row.SourceKey = allocation.ID, source.SourceKey
		}
		if row.SourceKey != source.SourceKey {
			return ErrTopUpReversalConflict
		}
		if row.RecordID == 0 {
			recordKey, err := NewRebateIdempotencyKey("rebate-record-v1", allocation.FinalSettlementKey)
			if err != nil {
				return err
			}
			var record RebateRecord
			err = walletCurrentTx(tx).Where("settlement_key = ?", recordKey).First(&record).Error
			if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			row.RecordID = record.ID
		}
		row.ReversedQuota, row.ReversedMoneyMicros = n, money
		row.QuarantinedQuota, row.QuarantinedMoneyMicros = q, quarantinedMoney
		if err := tx.Save(&row).Error; err != nil {
			return err
		}
		if row.RecordID > 0 {
			recordIDs[row.RecordID] = true
		}
	}
	// Consumed quota may have retired an earlier debt without an allocation.
	if possibleRemaining > 0 {
		var repayments []WalletDebtRepayment
		if err := walletCurrentTx(tx).Where("lot_id = ? AND user_id = ?", source.LotID, source.UserID).Find(&repayments).Error; err != nil {
			return err
		}
		for _, r := range repayments {
			remaining -= min(remaining, r.QuotaRepaid)
			possibleRemaining -= min(possibleRemaining, r.QuotaRepaid)
		}
		if remaining != 0 || possibleRemaining != 0 {
			return errors.New("source consumption provenance is incomplete")
		}
	}
	// Deterministic database order, not map iteration, for multiple records.
	ids := make([]int64, 0, len(recordIDs))
	for id := range recordIDs {
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return nil
	}
	var records []RebateRecord
	if err := walletCurrentTx(tx).Where("id IN ?", ids).Order("id ASC").Find(&records).Error; err != nil {
		return err
	}
	for i := range records {
		if pending {
			if err := stageRebateSourceReversalTx(tx, &records[i]); err != nil {
				return err
			}
		} else if err := ApplyRebateSourceReversalTx(tx, &records[i]); err != nil {
			return err
		}
		changed[records[i].InviterID] = true
	}
	return nil
}

// rebateSourceReversedQuotaTx follows original allocation order, the stored
// rate, stored reward cap, and EACH lot's ratio. No unrelated mixed source is
// voided, and current rebate settings never enter this calculation.
func rebateSourceReversedQuotaTx(tx *gorm.DB, record *RebateRecord) (int64, error) {
	return rebateSourceExposureQuotaTx(tx, record, false)
}

func rebateSourceReversalExposureTx(tx *gorm.DB, record *RebateRecord) (int64, int64, error) {
	target, err := rebateSourceReversedQuotaTx(tx, record)
	if err != nil {
		return 0, 0, err
	}
	quarantine, err := rebateSourceExposureQuotaTx(tx, record, true)
	if err != nil {
		return 0, 0, err
	}
	if quarantine < target {
		return 0, 0, ErrTopUpReversalConflict
	}
	return target, quarantine, nil
}

func rebateSourceExposureQuotaTx(tx *gorm.DB, record *RebateRecord, quarantine bool) (int64, error) {
	// The caller serializes on the invitee before locking the record. An
	// earlier payout hint (or outer caller) may already have opened an old
	// MySQL snapshot: projections AND their provenance need current reads.
	tx = walletCurrentTx(tx).Session(&gorm.Session{})
	var rows []RebateSourceReversal
	if err := tx.Where("record_id = ?", record.ID).Find(&rows).Error; err != nil {
		return 0, err
	}
	if len(rows) == 0 || record.RewardQuota == 0 {
		return 0, nil
	}
	revoked := make(map[int64]int64)
	for _, row := range rows {
		if row.ReversedMoneyMicros < 0 || row.QuarantinedMoneyMicros < 0 {
			return 0, ErrTopUpReversalConflict
		}
		revoked[row.AllocationID] = row.ReversedMoneyMicros
		if quarantine {
			revoked[row.AllocationID] = max(row.ReversedMoneyMicros, row.QuarantinedMoneyMicros)
		}
	}
	var first WalletConsumptionAllocation
	if err := tx.Where("id = ?", rows[0].AllocationID).First(&first).Error; err != nil {
		return 0, err
	}
	var parts []struct {
		ID    int64
		Money int64
		Ratio int64
	}
	if err := tx.Table("wallet_consumption_allocations AS a").Select("a.id, a.eligible_money_consumed_micros AS money, l.quota_per_unit_micros AS ratio").
		Joins("JOIN wallet_credit_lots l ON l.id = a.lot_id").Where("a.final_settlement_key = ? AND a.user_id = ? AND a.eligible_money_consumed_micros > 0", first.FinalSettlementKey, record.InviteeID).Order("a.id ASC").Find(&parts).Error; err != nil {
		return 0, err
	}
	remaining := new(big.Int).Mul(big.NewInt(record.RewardMoneyMicros), big.NewInt(10_000))
	total, removed := new(big.Rat), new(big.Rat)
	for _, part := range parts {
		if part.Money <= 0 || part.Ratio <= 0 || record.RateBasisPoints <= 0 || revoked[part.ID] < 0 || revoked[part.ID] > part.Money {
			return 0, ErrTopUpReversalConflict
		}
		award := new(big.Int).Mul(big.NewInt(part.Money), big.NewInt(record.RateBasisPoints))
		if award.Cmp(remaining) > 0 {
			award.Set(remaining)
		}
		remaining.Sub(remaining, award)
		weight := new(big.Int).Mul(award, big.NewInt(part.Ratio))
		total.Add(total, new(big.Rat).SetInt(weight))
		removed.Add(removed, new(big.Rat).SetFrac(new(big.Int).Mul(weight, big.NewInt(revoked[part.ID])), big.NewInt(part.Money)))
	}
	if remaining.Sign() != 0 || total.Sign() <= 0 {
		return 0, ErrTopUpReversalConflict
	}
	// Paid records have no precision hold: the original weighted numerator
	// must match the exact stored payout. Do not rescale a mismatched snapshot.
	denominator := big.NewInt(10_000 * MoneyMicrosPerUnit * MoneyMicrosPerUnit)
	expected := new(big.Rat).SetInt(new(big.Int).Mul(big.NewInt(record.RewardQuota), denominator))
	if total.Cmp(expected) != 0 {
		return 0, ErrTopUpReversalConflict
	}
	portion := new(big.Rat).Quo(removed, new(big.Rat).SetInt(denominator))
	value := new(big.Int).Quo(portion.Num(), portion.Denom())
	if !value.IsInt64() || value.Sign() < 0 || value.Int64() > record.RewardQuota {
		return 0, ErrTopUpReversalConflict
	}
	return value.Int64(), nil
}

// ApplyRebateSourceReversalTx must also run in scheduled payout transactions
// AFTER the original payout lot exists and PayoutLotID has been set, BEFORE
// commit. It intentionally leaves immutable RewardQuota/RewardMoney untouched.
func ApplyRebateSourceReversalTx(tx *gorm.DB, record *RebateRecord) error {
	if tx == nil || record == nil || record.ID <= 0 {
		return ErrTopUpReversalConflict
	}
	if record.PayoutLotID == nil {
		return nil
	}
	if record.RewardQuota <= 0 {
		return ErrTopUpReversalConflict
	}
	target, quarantine, err := rebateSourceReversalExposureTx(tx, record)
	if err != nil {
		return err
	}
	key, err := NewRebateIdempotencyKey("rebate-source-reversal-v1", fmt.Sprint(record.ID))
	if err != nil {
		return err
	}
	return reverseWalletSourceTx(tx, key, record.InviterID, *record.PayoutLotID, record.RewardQuota, target, quarantine, map[int]bool{})
}
