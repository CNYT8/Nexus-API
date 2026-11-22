package relay

import (
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/stretchr/testify/require"
)

func TestRealtimeTaskSkipsForeignGeminiAndVertex(t *testing.T) {
	for _, channelType := range []int{constant.ChannelTypeGemini, constant.ChannelTypeVertexAi} {
		t.Run(strconv.Itoa(channelType), func(t *testing.T) {
			db, task, _ := setupRealtimeTaskBillingTest(t, false)
			require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", task.ChannelId).UpdateColumn("type", channelType).Error)
			task.Platform = constant.TaskPlatform(strconv.Itoa(channelType))
			require.NoError(t, task.PrivateData.Scan(`{"execution":{"task_plugin":{"key":"upstream"}},"plugin_state":[1]}`))
			factory := func(constant.TaskPlatform) service.TaskPollingAdaptor {
				t.Fatal("foreign task must not resolve or invoke a local adaptor")
				return nil
			}
			require.Nil(t, tryRealtimeFetchWithAdaptor(task, false, factory))
			require.Equal(t, model.TaskStatus(model.TaskStatusInProgress), task.Status)
		})
	}
}

func TestRealtimeTaskSkipsUnknownPlatform(t *testing.T) {
	task := &model.Task{Platform: "upstream-plugin-key", Status: model.TaskStatusInProgress}
	calls := 0
	factory := func(platform constant.TaskPlatform) service.TaskPollingAdaptor {
		calls++
		require.Equal(t, task.Platform, platform)
		return nil
	}
	require.Nil(t, tryRealtimeFetchWithAdaptor(task, false, factory))
	require.Equal(t, 1, calls)
}
