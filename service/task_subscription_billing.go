package service

import (
	"context"
	"fmt"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

func settleSubscriptionTaskQuota(ctx context.Context, task *model.Task, actual int, refund bool, reason string, clamps ...*common.QuotaClamp) {
	applied, delta, fundingApplied, err := settleTaskQuota(task, actual, refund)
	if err != nil {
		logger.LogError(ctx, fmt.Sprintf("订阅任务结算失败 task %s: %v", task.TaskID, err))
		return
	}
	if applied {
		recordSubscriptionTaskBilling(ctx, task, actual, delta, refund, fundingApplied, reason, clamps...)
	}
}

func recordSubscriptionTaskBilling(ctx context.Context, task *model.Task, actual, delta int, refund, fundingApplied bool, reason string, clamps ...*common.QuotaClamp) {
	if !fundingApplied {
		logger.LogInfo(ctx, fmt.Sprintf("任务 %s 属于已结束的订阅额度周期，不向新周期退还额度", task.TaskID))
	}
	if delta == 0 {
		return
	}
	logType, quota := model.LogTypeConsume, delta
	if delta < 0 {
		logType, quota = model.LogTypeRefund, -delta
	} else {
		model.UpdateUserUsedQuotaAndRequestCount(task.UserId, delta)
		model.UpdateChannelUsedQuota(task.ChannelId, delta)
	}
	other := taskBillingOther(task)
	other["task_id"], other["actual_quota"], other["pre_consumed_quota"] = task.TaskID, actual, actual-delta
	if refund {
		other["reason"] = reason
	}
	if !fundingApplied {
		other["subscription_period_expired"] = true
	}
	for _, clamp := range clamps {
		if clamp != nil {
			attachQuotaSaturationToOther(other, clamp)
			break
		}
	}
	model.RecordTaskBillingLog(model.RecordTaskBillingLogParams{UserId: task.UserId, LogType: logType, Content: reason,
		ChannelId: task.ChannelId, ModelName: taskModelName(task), Quota: quota, TokenId: task.PrivateData.TokenId, Group: task.Group, Other: other})
}

// NotifySubmittedSubscriptionTask preserves the submit-time subscription quota
// reminder after the task and its funding/token alignment have committed.
func NotifySubmittedSubscriptionTask(task *model.Task, info *relaycommon.RelayInfo) {
	if task == nil || info == nil || !taskIsSubscription(task) || task.PrivateData.BillingKey == "" {
		return
	}
	var sub model.UserSubscription
	if err := model.DB.Where("id = ? AND user_id = ?", task.PrivateData.SubscriptionId, task.UserId).First(&sub).Error; err != nil {
		common.SysError("read submitted subscription quota: " + err.Error())
		return
	}
	snapshot := *info
	snapshot.SubscriptionId = sub.Id
	snapshot.SubscriptionAmountTotal = sub.AmountTotal
	snapshot.SubscriptionAmountUsedAfterPreConsume = sub.AmountUsed
	snapshot.SubscriptionPostDelta = 0
	checkAndSendSubscriptionQuotaNotify(&snapshot)
}
