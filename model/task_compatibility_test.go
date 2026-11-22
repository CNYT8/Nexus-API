package model

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestTaskPrivateDataPreservesUnknownJSON(t *testing.T) {
	raw := ` {"key":"secret","execution":{"task_plugin":{"key":"upstream","generation":9007199254740993}},"plugin_state":[1,{"cursor":9007199254740993}],"billing_context":{"model_price":2,"tiered_snapshot":{"expression":"x","unknown":[true,null]}}} `
	var data TaskPrivateData
	require.NoError(t, data.Scan(raw))
	value, err := data.Value()
	require.NoError(t, err)
	require.Equal(t, raw, value)
	require.True(t, (&Task{PrivateData: data}).HasForeignExecutionEvidence())
	data.PollFailures = 99
	data.BillingContext.ModelPrice = 100
	value, err = data.Value()
	require.NoError(t, err)
	require.Equal(t, raw, value, "foreign runtime data must remain frozen")

	// Unknown fields inside understood objects survive a genuine local update.
	native := `{"billing_context":{"model_price":2,"future":{"n":9007199254740993}},"result_metadata":{"text":"old","artifacts":[{"id":"a","kind":"video","future":true}]},"future_root":[1,2]}`
	require.NoError(t, data.Scan(native))
	data.BillingContext.ModelPrice = 3
	data.ResultMetadata.Text = "new"
	data.ResultMetadata.Artifacts[0].URL = "https://example.com/video"
	value, err = data.Value()
	require.NoError(t, err)
	require.JSONEq(t, `{"billing_context":{"model_price":3,"future":{"n":9007199254740993}},"result_metadata":{"text":"new","artifacts":[{"id":"a","kind":"video","url":"https://example.com/video","future":true}]},"future_root":[1,2]}`, value.(string))
	// JSONEq decodes numbers; also verify the exact integer bytes survived.
	require.Contains(t, value.(string), "9007199254740993")
}

func TestTaskForeignEvidenceDoesNotInferFromPlatform(t *testing.T) {
	for _, raw := range []string{
		`{}`, `{"execution":{"request_id":"old","path":"/video"}}`,
		`{"script_plugin_id":7,"script_plugin_digest":"revision","script_plugin_state":"{}","billing_context":{"usage_snapshot":{"plugin_id":8}}}`,
	} {
		var data TaskPrivateData
		require.NoError(t, json.Unmarshal([]byte(raw), &data))
		for _, platform := range []constant.TaskPlatform{"55", "suno", "jsplugin"} {
			require.False(t, (&Task{Platform: platform, PrivateData: data}).HasForeignExecutionEvidence())
		}
	}
	for _, raw := range []string{`{"execution":{"task_plugin":{"key":"p"}}}`, `{"plugin_state":false}`, `{"plugin_state":null}`, `{"plugin_state":[1,2]}`, `{"billing_context":{"tiered_snapshot":{}}}`} {
		var data TaskPrivateData
		require.NoError(t, data.Scan(raw))
		require.True(t, (&Task{Platform: "55", PrivateData: data}).HasForeignExecutionEvidence())
	}
}

func TestForeignTaskRejectsStaleCASAndPollFailure(t *testing.T) {
	truncateTables(t)
	task := &Task{TaskID: "foreign-stale", Status: TaskStatusInProgress, Progress: "20%", Quota: 100}
	insertTask(t, task)
	stale := *task
	raw := `{"execution":{"task_plugin":{"key":"foreign"}},"poll_failures":4}`
	require.NoError(t, DB.Model(task).UpdateColumn("private_data", raw).Error)
	stale.Status, stale.Progress = TaskStatusFailure, "100%"
	won, err := stale.UpdateWithStatus(TaskStatusInProgress)
	require.ErrorIs(t, err, ErrTaskExecutionProtected)
	require.False(t, won)
	terminal, err := RecordTaskPollFailure(task, "transport", 1)
	require.ErrorIs(t, err, ErrTaskExecutionProtected)
	require.False(t, terminal)
	var stored Task
	require.NoError(t, DB.First(&stored, task.ID).Error)
	require.Equal(t, TaskStatus(TaskStatusInProgress), stored.Status)
	require.Equal(t, 4, stored.PrivateData.PollFailures)
	require.Equal(t, 100, stored.Quota)
	var saved string
	require.NoError(t, DB.Model(task).Select("private_data").Scan(&saved).Error)
	require.Equal(t, raw, saved)
}

func TestTaskDirectWritesProtectLatestForeignRow(t *testing.T) {
	for _, action := range []struct {
		name string
		run  func(*Task) error
	}{{"update", (*Task).Update}, {"quota", (*Task).UpdateQuota}} {
		t.Run(action.name, func(t *testing.T) {
			truncateTables(t)
			task := &Task{TaskID: "direct-foreign", Status: TaskStatusInProgress, Progress: "20%", Quota: 100}
			insertTask(t, task)
			raw := ` {"execution":{"task_plugin":{"key":"foreign"}},"plugin_state":[9007199254740993],"key":"keep"} `
			require.NoError(t, DB.Model(&Task{}).Where("id = ?", task.ID).UpdateColumn("private_data", raw).Error)
			// The caller still looks native; only the latest locked row has evidence.
			require.False(t, task.HasForeignExecutionEvidence())
			task.Status, task.Progress, task.Quota = TaskStatusFailure, "100%", 0
			require.ErrorIs(t, action.run(task), ErrTaskExecutionProtected)
			var stored Task
			require.NoError(t, DB.First(&stored, task.ID).Error)
			require.Equal(t, TaskStatus(TaskStatusInProgress), stored.Status)
			require.Equal(t, "20%", stored.Progress)
			require.Equal(t, 100, stored.Quota)
			var saved string
			require.NoError(t, DB.Model(task).Select("private_data").Scan(&saved).Error)
			require.Equal(t, raw, saved)
		})
	}
}

func TestTaskDirectWritesPreserveNativePrivateData(t *testing.T) {
	truncateTables(t)
	task := &Task{TaskID: "native-direct", Status: TaskStatusInProgress, Quota: 100}
	insertTask(t, task)
	raw := `{"key":"keep","billing_context":{"model_price":2,"future":{"n":9007199254740993}},"future_root":true}`
	require.NoError(t, DB.Model(&Task{}).Where("id = ?", task.ID).UpdateColumn("private_data", raw).Error)
	require.Equal(t, TaskPrivateData{}, task.PrivateData)
	// An empty private snapshot must not clear data added since the caller read.
	task.Progress, task.Quota = "40%", 200
	require.NoError(t, task.Update())
	var saved string
	require.NoError(t, DB.Model(task).Select("private_data").Scan(&saved).Error)
	require.JSONEq(t, raw, saved)
	task.PrivateData = TaskPrivateData{ResultURL: "https://example.com/result", BillingContext: &TaskBillingContext{ModelPrice: 3}}
	require.NoError(t, task.Update())
	task.Quota = 300
	require.NoError(t, task.UpdateQuota())
	var stored Task
	require.NoError(t, DB.First(&stored, task.ID).Error)
	require.Equal(t, 300, stored.Quota)
	require.Equal(t, "40%", stored.Progress)
	require.Equal(t, "keep", stored.PrivateData.Key)
	require.Equal(t, "https://example.com/result", stored.PrivateData.ResultURL)
	require.Equal(t, float64(3), stored.PrivateData.BillingContext.ModelPrice)
	require.NoError(t, DB.Model(task).Select("private_data").Scan(&saved).Error)
	require.Contains(t, saved, "9007199254740993")
	require.Contains(t, saved, `"future_root":true`)

	created := &Task{UserId: task.UserId, TaskID: "native-create", Status: TaskStatusQueued}
	require.NoError(t, created.Update())
	require.Positive(t, created.ID)
	missing := &Task{ID: created.ID + 1000, UserId: task.UserId, TaskID: "native-explicit-id"}
	require.NoError(t, missing.Update())
	missing.ID++
	require.NoError(t, missing.UpdateQuota(), "missing quota rows remain no-ops")
	require.ErrorIs(t, (&Task{}).UpdateQuota(), gorm.ErrMissingWhereClause)
}

func TestTaskBulkWritesSkipForeignAndPreserveNativePrivateData(t *testing.T) {
	for _, action := range []struct {
		name string
		run  func([]*Task, map[string]any) error
	}{
		{"upstream_id", func(tasks []*Task, params map[string]any) error {
			ids := []string{"missing"}
			for _, task := range tasks {
				ids = append(ids, task.TaskID)
			}
			return TaskBulkUpdate(ids, params)
		}},
		{"primary_id", func(tasks []*Task, params map[string]any) error {
			ids := []int64{-1}
			for _, task := range tasks {
				ids = append(ids, task.ID)
			}
			return TaskBulkUpdateByID(ids, params)
		}},
	} {
		t.Run(action.name, func(t *testing.T) {
			truncateTables(t)
			native := &Task{TaskID: "bulk-shared", Status: TaskStatusInProgress, Quota: 100}
			insertTask(t, native)
			foreign := &Task{TaskID: "bulk-shared", UserId: native.UserId, Status: TaskStatusInProgress, Quota: 200}
			insertTask(t, foreign)
			outside := &Task{TaskID: "outside", Status: TaskStatusInProgress, Quota: 300}
			insertTask(t, outside)
			nativeRaw := `{"key":"keep","billing_context":{"model_price":2,"future":{"n":9007199254740993}}}`
			foreignRaw := ` {"plugin_state":null,"key":"foreign"} `
			require.NoError(t, DB.Model(native).UpdateColumn("private_data", nativeRaw).Error)
			require.NoError(t, DB.Model(foreign).UpdateColumn("private_data", foreignRaw).Error)
			params := map[string]any{"quota": 400, "progress": "50%", "private_data": `{"billing_context":{"model_price":3}}`}
			require.NoError(t, action.run([]*Task{foreign, native}, params))
			require.Equal(t, `{"billing_context":{"model_price":3}}`, params["private_data"], "caller params must not be changed")
			// A nil private_data patch must also preserve the latest native data.
			require.NoError(t, action.run([]*Task{native, foreign}, map[string]any{"private_data": nil}))
			// Use a distinct upstream ID to exercise an entirely protected batch.
			require.NoError(t, DB.Model(foreign).UpdateColumn("task_id", "foreign-only").Error)
			foreign.TaskID = "foreign-only"
			require.NoError(t, action.run([]*Task{foreign}, map[string]any{"quota": 999, "private_data": nil}))
			var gotNative, gotForeign, gotOutside Task
			require.NoError(t, DB.First(&gotNative, native.ID).Error)
			require.NoError(t, DB.First(&gotForeign, foreign.ID).Error)
			require.NoError(t, DB.First(&gotOutside, outside.ID).Error)
			require.Equal(t, 400, gotNative.Quota)
			require.Equal(t, "50%", gotNative.Progress)
			require.Equal(t, "keep", gotNative.PrivateData.Key)
			require.Equal(t, float64(3), gotNative.PrivateData.BillingContext.ModelPrice)
			require.Equal(t, 200, gotForeign.Quota)
			require.Empty(t, gotForeign.Progress)
			require.Equal(t, 300, gotOutside.Quota)
			var saved string
			require.NoError(t, DB.Model(foreign).Select("private_data").Scan(&saved).Error)
			require.Equal(t, foreignRaw, saved)
			require.NoError(t, DB.Model(native).Select("private_data").Scan(&saved).Error)
			require.Contains(t, saved, "9007199254740993")
		})
	}
}

func TestTaskPollingPaginationSkipsForeignAndUnknown(t *testing.T) {
	truncateTables(t)
	for i := 0; i < 5; i++ {
		task := &Task{TaskID: "pagination", Status: TaskStatusInProgress, Progress: "1%", SubmitTime: 10, Platform: "unsupported"}
		if i < 2 {
			require.NoError(t, task.PrivateData.Scan(`{"plugin_state":{"cursor":1}}`))
		}
		if i == 4 {
			task.Platform = "suno"
		}
		insertTask(t, task)
	}
	accept := func(task *Task) bool { return task.Platform == "suno" }
	for _, tasks := range [][]*Task{GetAllUnFinishSyncTasks(2, accept), GetTimedOutUnfinishedTasks(20, 2, accept)} {
		require.Len(t, tasks, 1)
		require.Equal(t, constant.TaskPlatform("suno"), tasks[0].Platform)
	}
}

func TestSubscriptionAdminProtectsOnlyRelatedForeignPendingTask(t *testing.T) {
	for _, action := range []struct {
		name string
		run  func(int) (string, error)
	}{{"cancel", AdminInvalidateUserSubscription}, {"delete", AdminDeleteUserSubscription}} {
		t.Run(action.name, func(t *testing.T) {
			truncateTables(t)
			task := &Task{TaskID: "subscription-foreign", Status: TaskStatusInProgress}
			insertTask(t, task)
			sub := &UserSubscription{UserId: task.UserId, Status: "active"}
			other := &UserSubscription{UserId: task.UserId, Status: "active"}
			require.NoError(t, DB.Create(sub).Error)
			require.NoError(t, DB.Create(other).Error)
			require.NoError(t, task.PrivateData.Scan(fmt.Sprintf(`{"execution":{"task_plugin":{"key":"p"}},"billing_source":"subscription","subscription_id":%d}`, sub.Id)))
			require.NoError(t, DB.Model(task).UpdateColumn("private_data", task.PrivateData).Error)
			_, err := action.run(sub.Id)
			require.ErrorIs(t, err, ErrSubscriptionForeignTaskPending)
			_, err = action.run(other.Id)
			require.NoError(t, err)
			for _, status := range []TaskStatus{TaskStatusSuccess, TaskStatusFailure} {
				require.NoError(t, DB.Model(task).Updates(map[string]any{"status": status, "billing_state": TaskBillingPending}).Error)
				_, err = action.run(sub.Id)
				require.ErrorIs(t, err, ErrSubscriptionForeignTaskPending)
				var preserved UserSubscription
				require.NoError(t, DB.First(&preserved, sub.Id).Error)
				require.Equal(t, "active", preserved.Status)
			}
			require.NoError(t, DB.Model(task).UpdateColumn("billing_state", TaskBillingDone).Error)
			_, err = action.run(sub.Id)
			require.NoError(t, err)
		})
	}
}
