package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/stretchr/testify/require"
)

type compatibilityNoIOAdaptor struct {
	calls int
}

func (*compatibilityNoIOAdaptor) Init(*relaycommon.RelayInfo) {}
func (a *compatibilityNoIOAdaptor) FetchTask(string, string, map[string]any, string) (*http.Response, error) {
	a.calls++
	return nil, errors.New("unexpected provider query")
}
func (*compatibilityNoIOAdaptor) ParseTaskResult([]byte) (*relaycommon.TaskInfo, error) {
	return nil, errors.New("unexpected provider response")
}
func (*compatibilityNoIOAdaptor) AdjustBillingOnComplete(*model.Task, *relaycommon.TaskInfo) int {
	return 0
}

func TestForeignTaskPollingDoesNotQueryOrCountFailure(t *testing.T) {
	db, task := setupTaskLedgerTest(t)
	require.NoError(t, task.PrivateData.Scan(`{"execution":{"task_plugin":{"key":"p"}},"poll_failures":3}`))
	require.NoError(t, db.Model(task).UpdateColumn("private_data", task.PrivateData).Error)
	adaptor := &compatibilityNoIOAdaptor{}
	require.NoError(t, updateVideoSingleTask(context.Background(), adaptor, &model.Channel{}, task.TaskID, map[string]*model.Task{task.TaskID: task}))
	recordPollFailure(context.Background(), task, "transport", 500)
	require.Zero(t, adaptor.calls)
	var stored model.Task
	require.NoError(t, db.First(&stored, task.ID).Error)
	require.Equal(t, 3, stored.PrivateData.PollFailures)
	require.Equal(t, model.TaskStatus(model.TaskStatusInProgress), stored.Status)
}

func TestForeignTaskBillingSkipsStaleRefundSettlementAndRecovery(t *testing.T) {
	db, task := setupTaskLedgerTest(t)
	task.Quota, task.Status = 1000, model.TaskStatusFailure
	task.BillingState, task.BillingQuota = model.TaskBillingPending, 0
	raw := fmt.Sprintf(` {"execution":{"task_plugin":{"key":"upstream"}},"plugin_state":[9007199254740993],"billing_source":"wallet","token_id":%d} `, task.PrivateData.TokenId)
	require.NoError(t, db.Model(&model.Task{}).Where("id = ?", task.ID).Updates(map[string]any{
		"quota": task.Quota, "status": task.Status, "billing_state": task.BillingState,
		"billing_quota": 0, "private_data": raw,
	}).Error)

	// The caller predates the foreign evidence. Both transaction phases must
	// reload the row before touching funds, token quota or a billing marker.
	require.False(t, task.HasForeignExecutionEvidence())
	RefundTaskQuota(context.Background(), task, "must skip")
	applied, delta, funded, err := settleTaskQuota(task, 2000, false)
	require.NoError(t, err)
	require.False(t, applied)
	require.Zero(t, delta)
	require.False(t, funded)
	processed, err := RecoverTaskBilling(context.Background(), 1)
	require.NoError(t, err)
	require.Zero(t, processed)
	require.Equal(t, 10000, getUserQuota(t, task.UserId))
	var token model.Token
	require.NoError(t, db.First(&token, task.PrivateData.TokenId).Error)
	require.Equal(t, 10000, token.RemainQuota)
	require.Zero(t, taskLedgerCount(t, db, &model.WalletReservation{}))
	require.Zero(t, taskLedgerCount(t, db, &model.Log{}))
	var stored model.Task
	require.NoError(t, db.First(&stored, task.ID).Error)
	require.Equal(t, 1000, stored.Quota)
	require.Equal(t, model.TaskBillingPending, stored.BillingState)
	var saved string
	require.NoError(t, db.Model(task).Select("private_data").Scan(&saved).Error)
	require.Equal(t, raw, saved)
}

func TestTimeoutSweepSkipsForeignAndUnknownWithoutStarvingNative(t *testing.T) {
	db, task := setupTaskLedgerTest(t)
	originalFactory, originalTimeout := GetTaskAdaptorFunc, constant.TaskTimeoutMinutes
	constant.TaskTimeoutMinutes = 1
	GetTaskAdaptorFunc = func(platform constant.TaskPlatform) TaskPollingAdaptor {
		if platform == "suno" {
			return &compatibilityNoIOAdaptor{}
		}
		return nil
	}
	t.Cleanup(func() {
		GetTaskAdaptorFunc, constant.TaskTimeoutMinutes = originalFactory, originalTimeout
	})
	for i := 0; i < 101; i++ {
		foreign := &model.Task{TaskID: fmt.Sprintf("foreign-%d", i), UserId: task.UserId,
			Status: model.TaskStatusInProgress, Progress: "10%", SubmitTime: 1, Platform: "suno"}
		require.NoError(t, foreign.PrivateData.Scan(`{"plugin_state":false}`))
		require.NoError(t, db.Create(foreign).Error)
	}
	unknown := &model.Task{TaskID: "unknown", UserId: task.UserId, Status: model.TaskStatusInProgress, SubmitTime: 1, Platform: "upstream-key"}
	native := &model.Task{TaskID: "native", UserId: task.UserId, Status: model.TaskStatusInProgress, SubmitTime: 1, Platform: "suno"}
	require.NoError(t, db.Create(unknown).Error)
	require.NoError(t, db.Create(native).Error)
	sweepTimedOutTasks(context.Background())
	var stored model.Task
	require.NoError(t, db.First(&stored, native.ID).Error)
	require.Equal(t, model.TaskStatus(model.TaskStatusFailure), stored.Status)
	require.Equal(t, model.TaskBillingSkipped, stored.BillingState)
	var count int64
	require.NoError(t, db.Model(&model.Task{}).Where("id <> ? AND status = ?", native.ID, model.TaskStatusFailure).Count(&count).Error)
	require.Zero(t, count)
}
