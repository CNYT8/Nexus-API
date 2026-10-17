package controller

import (
	"context"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
)

// UpdateVideoTaskAll keeps the legacy controller entry point on the same CAS,
// funding-source and token-accounting path as the background polling service.
// Never duplicate wallet-only debits/refunds here: they bypass reservations and
// can incorrectly refund subscriptions or create reward-bearing adjustments.
func UpdateVideoTaskAll(ctx context.Context, platform constant.TaskPlatform, taskChannelM map[int][]string, taskM map[string]*model.Task) error {
	return service.UpdateVideoTasks(ctx, platform, taskChannelM, taskM)
}
