package model

import (
	"errors"

	"gorm.io/gorm"
)

var ErrSubscriptionForeignTaskPending = errors.New("订阅仍关联未完成或待结算的上游插件任务，暂不能取消或删除")

// Resolve the owner without taking a subscription lock, then follow the task
// billing order: baseline gate -> User -> task -> subscription.
func lockSubscriptionForTaskAdminTx(tx *gorm.DB, subscriptionID int) (*UserSubscription, error) {
	var owner UserSubscription
	if err := tx.Select("id", "user_id").First(&owner, subscriptionID).Error; err != nil {
		return nil, err
	}
	if err := LockTaskBillingUserTx(tx, owner.UserId); err != nil {
		return nil, err
	}
	var afterID int64
	for {
		var tasks []Task
		if err := lockForUpdate(tx).Where("user_id = ? AND id > ?", owner.UserId, afterID).
			Where("(status NOT IN ? OR billing_state = ?)", []string{TaskStatusSuccess, TaskStatusFailure}, TaskBillingPending).
			Order("id").Limit(100).Find(&tasks).Error; err != nil {
			return nil, err
		}
		for i := range tasks {
			task := &tasks[i]
			afterID = task.ID
			if task.PrivateData.BillingSource == "subscription" && task.PrivateData.SubscriptionId == subscriptionID &&
				task.HasForeignExecutionEvidence() {
				return nil, ErrSubscriptionForeignTaskPending
			}
		}
		if len(tasks) < 100 {
			break
		}
	}
	var sub UserSubscription
	if err := lockForUpdate(tx).Where("id = ? AND user_id = ?", subscriptionID, owner.UserId).First(&sub).Error; err != nil {
		return nil, err
	}
	return &sub, nil
}
