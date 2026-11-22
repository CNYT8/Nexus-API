package model

import (
	"time"

	"gorm.io/gorm"
)

// RecordTaskPollFailure serializes the counter with terminal transitions and
// money operations. A stale worker cannot overwrite billing identities or
// increment a task that has already moved to another status.
func RecordTaskPollFailure(task *Task, class string, maxFailures int) (bool, error) {
	if task == nil {
		return false, nil
	}
	if task.HasForeignExecutionEvidence() {
		return false, ErrTaskExecutionProtected
	}
	terminal := false
	var updated *Task
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := LockTaskBillingUserTx(tx, task.UserId); err != nil {
			return err
		}
		var stored Task
		if err := walletCurrentTx(tx).First(&stored, task.ID).Error; err != nil {
			return err
		}
		if stored.HasForeignExecutionEvidence() {
			return ErrTaskExecutionProtected
		}
		if stored.UserId != task.UserId || stored.Status != task.Status || stored.Status == TaskStatusSuccess || stored.Status == TaskStatusFailure {
			return nil
		}
		if stored.PrivateData.PollFailures < 2147483647 {
			stored.PrivateData.PollFailures++
		}
		stored.PrivateData.LastPollClass = class
		if maxFailures > 0 && stored.PrivateData.PollFailures >= maxFailures {
			stored.Status, stored.Progress = TaskStatusFailure, "100%"
			stored.FailReason = "连续查询上游任务状态失败"
			stored.FinishTime = time.Now().Unix()
			stored.BillingState, stored.BillingQuota = TaskBillingPending, 0
			terminal = true
		}
		result := tx.Model(&Task{}).Where("id = ? AND status = ?", stored.ID, task.Status).Select("private_data", "status", "progress", "fail_reason", "finish_time", "billing_state", "billing_quota", "updated_at").Updates(&stored)
		if result.Error != nil {
			return result.Error
		}
		terminal = terminal && result.RowsAffected == 1
		if result.RowsAffected == 1 {
			updated = &stored
		}
		return nil
	})
	if err == nil && updated != nil {
		*task = *updated
	}
	return terminal && err == nil, err
}
