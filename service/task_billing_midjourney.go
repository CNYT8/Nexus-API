package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ReservePendingMidjourneyQuota persists the token identity with the wallet
// reservation. A submit crash can therefore refund both holds atomically.
func ReservePendingMidjourneyQuota(userID, tokenID, quota int) (string, error) {
	if userID <= 0 || quota < 0 || tokenID < 0 {
		return "", ErrWalletBillingIdentityRequired
	}
	key, err := model.NewWalletFinalSettlementKey(userID, "midjourney-submit", common.GetUUID())
	if err != nil {
		return "", err
	}
	err = model.DB.Transaction(func(tx *gorm.DB) error {
		if err := model.ReserveWalletQuotaTx(tx, key, userID, int64(quota)); err != nil {
			return err
		}
		if err := model.AdjustTaskTokenTx(tx, userID, tokenID, quota, true); err != nil {
			return err
		}
		if tokenID == 0 {
			return nil
		}
		r := tx.Model(&model.WalletReservation{}).Where("reservation_key = ? AND user_id = ? AND token_id = ?", key, userID, 0).
			UpdateColumn("token_id", tokenID)
		if r.Error != nil {
			return r.Error
		}
		if r.RowsAffected != 1 {
			return model.ErrWalletReservationConflict
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	invalidateTaskBillingCache(&model.Task{UserId: userID, PrivateData: model.TaskPrivateData{TokenId: tokenID}})
	return key, nil
}

func RefundPendingMidjourneyQuota(key string, userID int) error {
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		return model.RefundPendingTaskReservationTx(tx, key, userID)
	})
	if err == nil {
		_ = model.InvalidateUserCache(userID)
		_ = model.InvalidateUserTokensCache(userID)
	}
	return err
}

// SettleMidjourneyTaskBilling is shared by notification, polling and recovery.
// The stored terminal outcome and quota are authoritative; stale callbacks
// cannot refund a success or apply a second token refund.
func SettleMidjourneyTaskBilling(ctx context.Context, task *model.Midjourney) (bool, error) {
	if task == nil || task.Id <= 0 || task.UserId <= 0 {
		return false, ErrWalletBillingIdentityRequired
	}
	applied, delta, inviterID := false, 0, 0
	var committed model.Midjourney
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		if err := model.LockTaskBillingUserTx(tx, task.UserId); err != nil {
			return err
		}
		q := tx.Where("id = ? AND user_id = ?", task.Id, task.UserId)
		if tx.Dialector.Name() != "sqlite" {
			q = q.Clauses(clause.Locking{Strength: "UPDATE"})
		}
		var stored model.Midjourney
		if err := q.First(&stored).Error; err != nil {
			return err
		}
		if stored.BillingSource == BillingSourceSubscription || stored.Quota < 0 {
			return ErrWalletBillingIdentityRequired
		}
		if stored.Status != "SUCCESS" && stored.Status != "FAILURE" {
			return errors.New("midjourney status does not permit billing")
		}
		committed = stored
		if stored.BillingState == model.TaskBillingDone || stored.BillingState == model.TaskBillingSkipped {
			return nil
		}
		actual := stored.Quota
		refund := stored.Status == "FAILURE"
		if refund {
			actual = 0
		}
		if stored.BillingState == model.TaskBillingPending && stored.BillingQuota != actual {
			return model.ErrWalletReservationConflict
		}
		key := stored.BillingKey
		if key == "" {
			var err error
			key, err = model.NewWalletFinalSettlementKey(stored.UserId, "midjourney", strconv.Itoa(stored.Id))
			if err != nil {
				return err
			}
		} else {
			var reservation model.WalletReservation
			if err := tx.Where("reservation_key = ? AND user_id = ?", key, stored.UserId).First(&reservation).Error; err != nil {
				return err
			}
			// Older workers could refund only the wallet before ownership and
			// token metadata existed. The persisted failed task is the token
			// checkpoint for this case; new atomic refunds must not enter it.
			legacyWalletRefund := refund && reservation.State == model.WalletReservationRefunded &&
				reservation.FundingMode == "" && reservation.TokenID == 0 && reservation.TaskKind == "" && reservation.TaskID == 0
			if !legacyWalletRefund {
				if err := model.BindWalletReservationTaskTx(tx, key, stored.UserId, "midjourney", int64(stored.Id)); err != nil {
					return err
				}
			}
		}
		funds := &model.Task{UserId: stored.UserId, Quota: stored.Quota,
			PrivateData: model.TaskPrivateData{BillingKey: stored.BillingKey, TokenId: stored.TokenId}}
		var err error
		delta, inviterID, err = settleTaskFundsTx(tx, key, funds, actual, refund)
		if err != nil {
			return err
		}
		r := tx.Model(&stored).Where("quota = ? AND billing_state = ?", stored.Quota, stored.BillingState).
			Updates(map[string]interface{}{"quota": actual, "billing_quota": actual, "billing_state": model.TaskBillingDone})
		if r.Error != nil {
			return r.Error
		}
		if r.RowsAffected != 1 {
			return model.ErrWalletReservationConflict
		}
		committed.Quota, committed.BillingQuota, committed.BillingState = actual, actual, model.TaskBillingDone
		applied = true
		return nil
	})
	if err != nil {
		return false, err
	}
	*task = committed
	invalidateTaskBillingCache(&model.Task{UserId: task.UserId, PrivateData: model.TaskPrivateData{TokenId: task.TokenId}})
	if inviterID > 0 {
		_ = model.InvalidateUserCache(inviterID)
	}
	if applied && delta < 0 {
		model.RecordTaskBillingLog(model.RecordTaskBillingLogParams{UserId: task.UserId, LogType: model.LogTypeRefund,
			ChannelId: task.ChannelId, ModelName: CovertMjpActionToModelName(task.Action), Quota: -delta, TokenId: task.TokenId,
			Other: map[string]interface{}{"task_id": task.MjId, "reason": "构图失败"}})
	}
	if applied && task.Status == "SUCCESS" {
		NotifyTaskQuotaConsumed(ctx, task.UserId, task.Quota)
	}
	return applied, nil
}

func recoverMidjourneyTaskBilling(ctx context.Context, limit int) (int, error) {
	if limit <= 0 {
		return 0, nil
	}
	processed, afterID := 0, 0
	var failures []error
	for !rebateMaintenancePaused.Load() {
		var tasks []model.Midjourney
		err := model.DB.Where("id > ? AND status IN ? AND (billing_state = ? OR (billing_state = ? AND billing_key <> ?))",
			afterID, []string{"SUCCESS", "FAILURE"}, model.TaskBillingPending, "", "").Order("id").Limit(limit).Find(&tasks).Error
		if err != nil {
			return processed, err
		}
		for i := range tasks {
			afterID = tasks[i].Id
			applied, err := SettleMidjourneyTaskBilling(ctx, &tasks[i])
			if err != nil {
				failures = append(failures, fmt.Errorf("recover midjourney %d: %w", tasks[i].Id, err))
			} else if applied {
				processed++
			}
		}
		if len(tasks) < limit {
			break
		}
	}
	return processed, errors.Join(failures...)
}
