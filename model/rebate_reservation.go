package model

import (
	"errors"
	"fmt"
	"math"
	"math/big"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	WalletReservationReserved   = "reserved"
	WalletReservationFinalizing = "finalizing"
	WalletReservationFinalized  = "finalized"
	WalletReservationRefunded   = "refunded"
)

var (
	ErrWalletReservationConflict = errors.New("wallet reservation identity, amount or state conflict")
	ErrWalletInsufficientQuota   = errors.New("insufficient unreserved wallet quota")
)

// WalletReservation permanently binds a server-owned billing key to one user.
// ReservedQuota is an absolute total, not a delta. It remains available for
// auditing after termination. Finalizing is a durable, non-refundable barrier.
type WalletReservation struct {
	ID             int64     `gorm:"primaryKey"`
	ReservationKey string    `gorm:"type:char(64);not null;uniqueIndex"`
	UserID         int       `gorm:"not null;index"`
	ReservedQuota  int64     `gorm:"not null;default:0"`
	State          string    `gorm:"type:varchar(24);not null;index:idx_wallet_reservation_recovery,priority:1"`
	FinalQuota     int64     `gorm:"not null;default:0"`
	CreatedAt      time.Time `gorm:"not null"`
	UpdatedAt      time.Time `gorm:"not null;index:idx_wallet_reservation_recovery,priority:2"`
}

// WalletLotHold is live only while QuotaHeld > 0. Terminal rows are retained.
// All hold and lot mutations serialize on the same non-deleted users row.
type WalletLotHold struct {
	ID             int64     `gorm:"primaryKey"`
	ReservationKey string    `gorm:"type:char(64);not null;uniqueIndex:ux_wallet_hold_key_lot,priority:1"`
	LotID          int64     `gorm:"not null;uniqueIndex:ux_wallet_hold_key_lot,priority:2;index"`
	UserID         int       `gorm:"not null;index"`
	QuotaHeld      int64     `gorm:"not null"`
	CreatedAt      time.Time `gorm:"not null"`
	UpdatedAt      time.Time `gorm:"not null"`
}

func walletLockUserTx(tx *gorm.DB, userID int) (*User, error) {
	if tx == nil || userID <= 0 {
		return nil, errors.New("invalid wallet user lock")
	}
	// SQLite has no row locks. Acquire its write lock before any accounting
	// reads (including zero-delta operations), rather than upgrading a snapshot.
	if tx.Dialector.Name() == "sqlite" {
		r := tx.Model(&User{}).Where("id = ?", userID).UpdateColumn("quota", gorm.Expr("quota"))
		if r.Error != nil {
			return nil, r.Error
		}
		if r.RowsAffected != 1 {
			return nil, fmt.Errorf("user %d not found while settling wallet quota: %w", userID, ErrTopUpUserNotFound)
		}
	}
	var user User
	q := tx.Select("id", "COALESCE(quota, 0) AS quota").Where("id = ?", userID)
	if tx.Dialector.Name() != "sqlite" {
		q = q.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	if err := q.First(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("user %d not found while settling wallet quota: %w", userID, ErrTopUpUserNotFound)
		}
		return nil, fmt.Errorf("lock wallet user %d: %w", userID, err)
	}
	return &user, nil
}

// walletQuotaBoundsTx reads metadata through the supplied transaction, never
// through DB/the outer pool (which would deadlock a one-connection pool).
func walletQuotaBoundsTx(tx *gorm.DB) (int64, int64, error) {
	var typ string
	switch tx.Dialector.Name() {
	case "sqlite":
		// SQLite INTEGER storage is signed 64-bit regardless of the INT type
		// spelling in GORM. Do not impose the old per-top-up quote limit here.
		return math.MinInt64, math.MaxInt64, nil
	case "mysql":
		err := tx.Raw("SELECT COLUMN_TYPE FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ? AND COLUMN_NAME = ?", "users", "quota").Scan(&typ).Error
		if err != nil {
			return 0, 0, err
		}
	case "postgres":
		err := tx.Raw("SELECT format_type(a.atttypid, a.atttypmod) FROM pg_attribute a WHERE a.attrelid = to_regclass(?) AND a.attname = ? AND NOT a.attisdropped", "users", "quota").Scan(&typ).Error
		if err != nil {
			return 0, 0, err
		}
	default:
		return 0, 0, errors.New("unsupported wallet database dialect")
	}
	max, err := quotaLimitForColumnType(tx.Dialector.Name(), typ, typ)
	if err != nil {
		return 0, 0, err
	}
	if strings.Contains(strings.ToLower(typ), "unsigned") {
		// INTEGER is a MySQL INT synonym; the shared legacy parser treats
		// that spelling as signed, so correct its unsigned bound here.
		if strings.HasPrefix(strings.ToLower(typ), "integer") {
			max = math.MaxUint32
		}
		return 0, max, nil
	}
	return -max - 1, max, nil
}

func walletCheckedAdd(a, b int64) (int64, error) {
	if (b > 0 && a > math.MaxInt64-b) || (b < 0 && a < math.MinInt64-b) {
		return 0, ErrTopUpQuotaOutOfRange
	}
	return a + b, nil
}

func walletFloorProduct(a, b, denominator int64) (int64, error) {
	if a < 0 || b < 0 || denominator <= 0 {
		return 0, ErrTopUpQuotaOutOfRange
	}
	v := new(big.Int).Mul(big.NewInt(a), big.NewInt(b))
	v.Quo(v, big.NewInt(denominator))
	if !v.IsInt64() {
		return 0, ErrTopUpQuotaOutOfRange
	}
	return v.Int64(), nil
}

// Caller holds the user lock. The equality guard additionally detects a writer
// that has not yet migrated. Never let SQL promote overflowing integers to REAL.
// Legacy rows may store NULL quota; it is treated as zero on both sides of the
// CAS so an admin grant still makes progress without weakening the guard.
func walletChangeQuotaTx(tx *gorm.DB, user *User, delta int64, requireNonnegative bool) error {
	min, max, err := walletQuotaBoundsTx(tx)
	if err != nil {
		return err
	}
	old := int64(user.Quota)
	next, err := walletCheckedAdd(old, delta)
	if err != nil {
		return err
	}
	if old < min || old > max || next < min || next > max {
		return ErrTopUpQuotaLimitExceeded
	}
	if requireNonnegative && next < 0 {
		return ErrWalletInsufficientQuota
	}
	if delta == 0 {
		return nil
	} // user has already been locked and validated
	r := tx.Model(&User{}).Where("id = ? AND COALESCE(quota, 0) = ?", user.Id, old).UpdateColumn("quota", next)
	if r.Error != nil {
		return r.Error
	}
	if r.RowsAffected != 1 {
		return errors.New("wallet quota changed during accounting")
	}
	user.Quota = int(next)
	return nil
}

func walletAfterCommit(db *gorm.DB, userID int, err error) error {
	if err == nil && db == DB {
		// A committed mutation must not become a retryable debit on cache error.
		if cacheErr := InvalidateUserCache(userID); cacheErr != nil {
			common.SysError(fmt.Sprintf("wallet cache invalidation for user %d: %v", userID, cacheErr))
		}
	}
	return err
}

// Locking reads see committed latest rows even when an outer MySQL transaction
// already established a repeatable-read snapshot before entering accounting.
func walletCurrentTx(tx *gorm.DB) *gorm.DB {
	if tx.Dialector.Name() == "sqlite" {
		return tx
	}
	return tx.Clauses(clause.Locking{Strength: "UPDATE"})
}

func walletReservationTx(tx *gorm.DB, key string, userID int) (*WalletReservation, error) {
	var r WalletReservation
	if err := walletCurrentTx(tx).Where("reservation_key = ?", key).First(&r).Error; err != nil {
		return nil, err
	}
	if r.UserID != userID {
		return nil, ErrWalletReservationConflict
	}
	return &r, nil
}

func validateWalletReservationInput(tx *gorm.DB, key string, userID int) error {
	if tx == nil || userID <= 0 || !isCanonicalSHA256(key) {
		return errors.New("invalid wallet reservation input")
	}
	return nil
}

func ReserveWalletQuota(db *gorm.DB, key string, userID int, targetTotal int64) error {
	if db == nil {
		return errors.New("wallet reservation requires a database")
	}
	err := db.Transaction(func(tx *gorm.DB) error { return ReserveWalletQuotaTx(tx, key, userID, targetTotal) })
	return walletAfterCommit(db, userID, err)
}

// ReserveWalletQuotaTx requires an enclosing transaction. Repeated absolute
// targets do not debit twice. A smaller target must use the explicit CAS release.
func ReserveWalletQuotaTx(tx *gorm.DB, key string, userID int, targetTotal int64) error {
	if err := validateWalletReservationInput(tx, key, userID); err != nil {
		return err
	}
	if targetTotal < 0 {
		return ErrInvalidTopUpQuota
	}
	user, err := walletLockUserTx(tx, userID)
	if err != nil {
		return err
	}
	active, err := walletCreditBaselineState(tx)
	if err != nil {
		return err
	}
	if !active {
		return errors.New("wallet credit baseline is not initialized")
	}
	r, err := walletReservationTx(tx, key, userID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		// Never resurrect a key previously used by the legacy settlement API.
		var settled WalletConsumptionSettlement
		err := walletCurrentTx(tx).Where("final_settlement_key = ?", key).First(&settled).Error
		if err == nil {
			return ErrWalletReservationConflict
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		r = &WalletReservation{ReservationKey: key, UserID: userID, State: WalletReservationReserved, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
		res := tx.Create(r)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 {
			return ErrWalletReservationConflict
		}
	} else if err != nil {
		return err
	}
	if r.State != WalletReservationReserved || targetTotal < r.ReservedQuota {
		return ErrWalletReservationConflict
	}
	delta := targetTotal - r.ReservedQuota
	if err := walletChangeQuotaTx(tx, user, -delta, delta > 0); err != nil {
		return err
	}
	if delta == 0 {
		return nil
	}
	if err := walletAcquireHoldsTx(tx, key, userID, delta); err != nil {
		return err
	}
	res := tx.Model(&WalletReservation{}).Where("id = ? AND state = ? AND reserved_quota = ?", r.ID, r.State, r.ReservedQuota).
		Updates(map[string]interface{}{"reserved_quota": targetTotal, "updated_at": time.Now().UTC()})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected != 1 {
		return ErrWalletReservationConflict
	}
	return nil
}

// Bind even maintenance/legacy terminal operations in the same unique key
// namespace, so racing reserve/finalize calls for different users cannot both
// succeed merely because the records otherwise live in different tables.
func walletBindTerminalKeyTx(tx *gorm.DB, key string, userID int, pre, actual int64) error {
	r := WalletReservation{ReservationKey: key, UserID: userID, ReservedQuota: pre, FinalQuota: actual, State: WalletReservationFinalized, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	created := tx.Create(&r)
	if created.Error != nil {
		return created.Error
	}
	if created.RowsAffected != 1 {
		return ErrWalletReservationConflict
	}
	return nil
}

func walletLotsAndHoldsTx(tx *gorm.DB, userID int) ([]WalletCreditLot, map[int64]int64, error) {
	var lots []WalletCreditLot
	// Compatibility ordering: non-eligible first, then created_at and id.
	// This is deliberately NOT global FIFO across eligibility classes.
	if err := walletCurrentTx(tx).Where("user_id = ? AND quota_remaining > 0", userID).Order("eligible ASC, created_at ASC, id ASC").Find(&lots).Error; err != nil {
		return nil, nil, err
	}
	var holds []WalletLotHold
	if err := walletCurrentTx(tx).Where("user_id = ? AND quota_held > 0", userID).Find(&holds).Error; err != nil {
		return nil, nil, err
	}
	totals := make(map[int64]int64)
	for _, h := range holds {
		total, err := walletCheckedAdd(totals[h.LotID], h.QuotaHeld)
		if err != nil {
			return nil, nil, err
		}
		totals[h.LotID] = total
	}
	matched := make(map[int64]struct{}, len(lots))
	for _, lot := range lots {
		if lot.QuotaRemaining < totals[lot.ID] || lot.EligibleMoneyRemaining < 0 {
			return nil, nil, errors.New("wallet hold exceeds lot balance")
		}
		matched[lot.ID] = struct{}{}
	}
	for lotID := range totals {
		if _, ok := matched[lotID]; !ok {
			return nil, nil, errors.New("wallet hold references exhausted or missing lot")
		}
	}
	return lots, totals, nil
}

func walletAcquireHoldsTx(tx *gorm.DB, key string, userID int, quota int64) error {
	lots, totals, err := walletLotsAndHoldsTx(tx, userID)
	if err != nil {
		return err
	}
	for _, lot := range lots {
		if quota == 0 {
			break
		}
		n := lot.QuotaRemaining - totals[lot.ID]
		if n > quota {
			n = quota
		}
		if n == 0 {
			continue
		}
		var hold WalletLotHold
		err := walletCurrentTx(tx).Where("reservation_key = ? AND lot_id = ?", key, lot.ID).First(&hold).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			hold = WalletLotHold{ReservationKey: key, UserID: userID, LotID: lot.ID, QuotaHeld: n, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
			r := tx.Create(&hold)
			if r.Error != nil {
				return r.Error
			}
			if r.RowsAffected != 1 {
				return ErrWalletReservationConflict
			}
		} else if err != nil {
			return err
		} else {
			next, err := walletCheckedAdd(hold.QuotaHeld, n)
			if err != nil {
				return err
			}
			r := tx.Model(&WalletLotHold{}).Where("id = ? AND user_id = ? AND quota_held = ?", hold.ID, userID, hold.QuotaHeld).Updates(map[string]interface{}{"quota_held": next, "updated_at": time.Now().UTC()})
			if r.Error != nil {
				return r.Error
			}
			if r.RowsAffected != 1 {
				return ErrWalletReservationConflict
			}
		}
		quota -= n
	}
	if quota != 0 {
		return ErrWalletInsufficientQuota
	}
	return nil
}

// Release from the newest acquired holds first, leaving other reservations
// untouched. The exact sum must agree with the durable reservation header.
func walletReleaseHoldsTx(tx *gorm.DB, r *WalletReservation, release int64) error {
	var holds []WalletLotHold
	if err := walletCurrentTx(tx).Where("reservation_key = ? AND user_id = ? AND quota_held > 0", r.ReservationKey, r.UserID).Order("id DESC").Find(&holds).Error; err != nil {
		return err
	}
	var sum int64
	for _, h := range holds {
		next, err := walletCheckedAdd(sum, h.QuotaHeld)
		if err != nil {
			return err
		}
		sum = next
	}
	if sum != r.ReservedQuota || release < 0 || release > sum {
		return ErrWalletReservationConflict
	}
	for _, h := range holds {
		if release == 0 {
			break
		}
		n := h.QuotaHeld
		if n > release {
			n = release
		}
		res := tx.Model(&WalletLotHold{}).Where("id = ? AND user_id = ? AND quota_held = ?", h.ID, r.UserID, h.QuotaHeld).Updates(map[string]interface{}{"quota_held": h.QuotaHeld - n, "updated_at": time.Now().UTC()})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 {
			return ErrWalletReservationConflict
		}
		release -= n
	}
	return nil
}

func ReleaseWalletReservationTo(db *gorm.DB, key string, userID int, expectedTotal, targetTotal int64) error {
	if db == nil {
		return errors.New("wallet release requires a database")
	}
	err := db.Transaction(func(tx *gorm.DB) error {
		return ReleaseWalletReservationToTx(tx, key, userID, expectedTotal, targetTotal)
	})
	return walletAfterCommit(db, userID, err)
}

func ReleaseWalletReservationToTx(tx *gorm.DB, key string, userID int, expectedTotal, targetTotal int64) error {
	if err := validateWalletReservationInput(tx, key, userID); err != nil {
		return err
	}
	if targetTotal < 0 || expectedTotal < targetTotal {
		return ErrWalletReservationConflict
	}
	user, err := walletLockUserTx(tx, userID)
	if err != nil {
		return err
	}
	r, err := walletReservationTx(tx, key, userID)
	if err != nil {
		return err
	}
	if r.State != WalletReservationReserved || r.ReservedQuota != expectedTotal {
		return ErrWalletReservationConflict
	}
	if err := walletReleaseHoldsTx(tx, r, expectedTotal-targetTotal); err != nil {
		return err
	}
	if err := walletChangeQuotaTx(tx, user, expectedTotal-targetTotal, false); err != nil {
		return err
	}
	if expectedTotal == targetTotal {
		return nil
	}
	res := tx.Model(&WalletReservation{}).Where("id = ? AND state = ? AND reserved_quota = ?", r.ID, r.State, expectedTotal).Updates(map[string]interface{}{"reserved_quota": targetTotal, "updated_at": time.Now().UTC()})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected != 1 {
		return ErrWalletReservationConflict
	}
	return nil
}

func RefundWalletReservation(db *gorm.DB, key string, userID int) error {
	if db == nil {
		return errors.New("wallet refund requires a database")
	}
	err := db.Transaction(func(tx *gorm.DB) error { return RefundWalletReservationTx(tx, key, userID) })
	return walletAfterCommit(db, userID, err)
}

func RefundWalletReservationTx(tx *gorm.DB, key string, userID int) error {
	if err := validateWalletReservationInput(tx, key, userID); err != nil {
		return err
	}
	user, err := walletLockUserTx(tx, userID)
	if err != nil {
		return err
	}
	r, err := walletReservationTx(tx, key, userID)
	if err != nil {
		return err
	}
	if r.State == WalletReservationRefunded {
		return nil
	}
	if r.State != WalletReservationReserved {
		return ErrWalletReservationConflict
	}
	if err := walletReleaseHoldsTx(tx, r, r.ReservedQuota); err != nil {
		return err
	}
	if err := walletChangeQuotaTx(tx, user, r.ReservedQuota, false); err != nil {
		return err
	}
	res := tx.Model(&WalletReservation{}).Where("id = ? AND state = ?", r.ID, r.State).Updates(map[string]interface{}{"state": WalletReservationRefunded, "updated_at": time.Now().UTC()})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected != 1 {
		return ErrWalletReservationConflict
	}
	return nil
}

// RecoverFinalizingWalletSettlements replays only durable successful-usage
// intents. It never refunds, mutates tokens, or guesses actual usage. Each
// entry commits independently; errors remain retryable and are returned even
// if other entries succeeded. The count is successfully handled candidates
// (another worker may have already finalized one; that replay is harmless).
func RecoverFinalizingWalletSettlements(db *gorm.DB, limit int) (int, error) {
	if db == nil {
		return 0, errors.New("wallet recovery requires a database")
	}
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	var pending []WalletReservation
	if err := db.Where("state = ?", WalletReservationFinalizing).Order("updated_at ASC, id ASC").Limit(limit).Find(&pending).Error; err != nil {
		return 0, err
	}
	processed := 0
	var failures []error
	for _, r := range pending {
		if _, err := FinalizeWalletConsumption(db, r.ReservationKey, r.UserID, r.ReservedQuota, r.FinalQuota); err != nil {
			failures = append(failures, fmt.Errorf("recover wallet reservation %d: %w", r.ID, err))
			continue
		}
		processed++
	}
	return processed, errors.Join(failures...)
}

// Prepare must commit separately BEFORE attempting final settlement. Recovery
// can replay Finalize using ReservedQuota and FinalQuota without refunding usage.
func PrepareWalletFinalization(db *gorm.DB, key string, userID int, pre, actual int64) error {
	if db == nil {
		return errors.New("wallet finalization requires a database")
	}
	err := db.Transaction(func(tx *gorm.DB) error { return PrepareWalletFinalizationTx(tx, key, userID, pre, actual) })
	return walletAfterCommit(db, userID, err)
}

func PrepareWalletFinalizationTx(tx *gorm.DB, key string, userID int, pre, actual int64) error {
	if err := validateWalletReservationInput(tx, key, userID); err != nil {
		return err
	}
	if pre < 0 || actual < 0 {
		return ErrWalletReservationConflict
	}
	if _, err := walletLockUserTx(tx, userID); err != nil {
		return err
	}
	r, err := walletReservationTx(tx, key, userID)
	if err != nil {
		return err
	}
	if r.ReservedQuota != pre {
		return ErrWalletReservationConflict
	}
	if r.State == WalletReservationFinalizing || r.State == WalletReservationFinalized {
		if r.FinalQuota != actual {
			return ErrWalletReservationConflict
		}
		return nil
	}
	if r.State != WalletReservationReserved {
		return ErrWalletReservationConflict
	}
	res := tx.Model(&WalletReservation{}).Where("id = ? AND state = ? AND reserved_quota = ?", r.ID, r.State, pre).Updates(map[string]interface{}{"state": WalletReservationFinalizing, "final_quota": actual, "updated_at": time.Now().UTC()})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected != 1 {
		return ErrWalletReservationConflict
	}
	return nil
}
