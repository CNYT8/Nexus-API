package service

import (
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm"
)

// Caller holds User -> task. The wallet side can already be terminal because
// an older recovery worker finalized it alone. Task quota remains the token
// adjustment checkpoint; the done marker and both quotas commit together.
func settleTaskFundsTx(tx *gorm.DB, key string, task *model.Task, actual int, refund bool) (int, int, error) {
	if task.HasForeignExecutionEvidence() {
		return 0, 0, model.ErrTaskExecutionProtected
	}
	delta := actual - task.Quota
	var reservation model.WalletReservation
	err := tx.Where("reservation_key = ? AND user_id = ?", key, task.UserId).First(&reservation).Error
	inviterID := 0
	tokenID := task.PrivateData.TokenId
	if errors.Is(err, gorm.ErrRecordNotFound) {
		if task.PrivateData.BillingKey != "" || (task.Quota == 0 && actual != 0) {
			return 0, 0, fmt.Errorf("task wallet reservation unavailable: %w", err)
		}
		if err := model.SettleLegacyTaskDeltaTx(tx, key, task.UserId, delta); err != nil {
			return 0, 0, err
		}
	} else if err != nil {
		return 0, 0, err
	} else {
		tokenID = model.BillingReservationTokenID(&reservation, tokenID)
		if reservation.ReservedQuota < 0 || reservation.ReservedQuota > int64(math.MaxInt) ||
			(task.Quota != int(reservation.ReservedQuota) && task.Quota != actual) {
			return 0, 0, model.ErrWalletReservationConflict
		}
		if refund {
			err = model.RefundWalletReservationTx(tx, key, task.UserId)
		} else {
			inviterID, _, err = model.FinalizeBillingFundingTx(tx, key, task.UserId, reservation.ReservedQuota, int64(actual))
		}
		if err != nil {
			return 0, 0, err
		}
	}
	if err := model.AdjustTaskTokenTx(tx, task.UserId, tokenID, delta, false); err != nil {
		return 0, 0, err
	}
	return delta, inviterID, nil
}

// RecoverTaskBilling walks by primary key, so unrelated legacy rows and a
// permanently failing item cannot starve later terminal tasks. Only durable
// targets or an existing reservation authorize automatic replay.
func RecoverTaskBilling(ctx context.Context, limit int) (int, error) {
	if limit <= 0 {
		return 0, nil
	}
	processed := 0
	var failures []error
	var afterID int64
	for !rebateMaintenancePaused.Load() {
		var tasks []model.Task
		err := model.DB.Where("id > ? AND status IN ? AND billing_state IN ?", afterID,
			[]string{model.TaskStatusSuccess, model.TaskStatusFailure}, []string{"", model.TaskBillingPending}).
			Order("id").Limit(limit).Find(&tasks).Error
		if err != nil {
			return processed, err
		}
		for i := range tasks {
			task := &tasks[i]
			afterID = task.ID
			if task.HasForeignExecutionEvidence() {
				continue
			}
			// Old subscription tasks without a reservation cannot prove which
			// part of a split adjustment committed. Never guess their replay.
			if taskIsSubscription(task) && task.PrivateData.BillingKey == "" {
				continue
			}
			actual := task.BillingQuota
			if task.BillingState == "" {
				key, err := TaskWalletBillingKey(task)
				if err != nil {
					failures = append(failures, err)
					continue
				}
				var reservation model.WalletReservation
				err = model.DB.Where("reservation_key = ? AND user_id = ?", key, task.UserId).First(&reservation).Error
				if errors.Is(err, gorm.ErrRecordNotFound) {
					continue
				}
				if err != nil {
					failures = append(failures, err)
					continue
				}
				actual = task.Quota
				if reservation.State == model.WalletReservationFinalizing || reservation.State == model.WalletReservationFinalized {
					if reservation.FinalQuota < 0 || reservation.FinalQuota > int64(math.MaxInt) {
						failures = append(failures, model.ErrWalletReservationConflict)
						continue
					}
					actual = int(reservation.FinalQuota)
				}
			}
			refund := task.Status == model.TaskStatusFailure
			if refund {
				actual = 0
			}
			applied, delta, fundingApplied, err := settleTaskQuota(task, actual, refund)
			if err != nil {
				failures = append(failures, fmt.Errorf("recover task %d: %w", task.ID, err))
				continue
			}
			if applied {
				if taskIsSubscription(task) {
					recordSubscriptionTaskBilling(ctx, task, actual, delta, refund, fundingApplied, "任务结算恢复")
				} else {
					recordRecoveredTaskBilling(ctx, task, actual, delta, refund)
				}
				processed++
			}
		}
		if len(tasks) < limit {
			break
		}
	}
	return processed, errors.Join(failures...)
}

func recordRecoveredTaskBilling(ctx context.Context, task *model.Task, actual, delta int, refund bool) {
	if delta != 0 {
		logType, quota := model.LogTypeConsume, delta
		if delta < 0 {
			logType, quota = model.LogTypeRefund, -delta
		} else {
			model.UpdateUserUsedQuotaAndRequestCount(task.UserId, delta)
			model.UpdateChannelUsedQuota(task.ChannelId, delta)
		}
		other := taskBillingOther(task)
		other["task_id"], other["actual_quota"], other["pre_consumed_quota"] = task.TaskID, actual, actual-delta
		model.RecordTaskBillingLog(model.RecordTaskBillingLogParams{UserId: task.UserId, LogType: logType, Content: "任务结算恢复", ChannelId: task.ChannelId, ModelName: taskModelName(task), Quota: quota, TokenId: task.PrivateData.TokenId, Group: task.Group, Other: other})
	}
	if !refund {
		NotifyTaskQuotaConsumed(ctx, task.UserId, actual)
	}
}
