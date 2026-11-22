package model

import (
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

func TestScriptTaskStateChangesSnapshotWithoutBecomingPublic(t *testing.T) {
	task := &Task{PrivateData: TaskPrivateData{ScriptPluginID: 7, ScriptPluginDigest: "digest", ScriptPluginState: `{"cursor":"first-secret"}`}}
	before := task.Snapshot()
	task.PrivateData.ScriptPluginState = `{"cursor":"next-secret"}`
	if before.Equal(task.Snapshot()) {
		t.Fatal("state-only poll change would not be persisted")
	}
	before = task.Snapshot()
	task.PrivateData.ScriptPluginUsage = `{"seconds":1.5}`
	if before.Equal(task.Snapshot()) {
		t.Fatal("usage-only poll change would not be persisted")
	}
	before = task.Snapshot()
	task.PrivateData.ScriptPluginUsageError = "completion_usage_invalid"
	if before.Equal(task.Snapshot()) {
		t.Fatal("usage error would not be persisted")
	}
	data, err := common.Marshal(task)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "secret") || strings.Contains(string(data), "script_plugin") || strings.Contains(string(data), "completion_usage_invalid") {
		t.Fatalf("private script state leaked: %s", data)
	}
	encoded, err := task.PrivateData.Value()
	if err != nil {
		t.Fatal(err)
	}
	var restored TaskPrivateData
	if err = restored.Scan(encoded); err != nil {
		t.Fatal(err)
	}
	if restored.ScriptPluginState != task.PrivateData.ScriptPluginState || restored.ScriptPluginID != 7 || restored.ScriptPluginUsage != task.PrivateData.ScriptPluginUsage || restored.ScriptPluginUsageError != task.PrivateData.ScriptPluginUsageError {
		t.Fatalf("private state round trip failed: %+v", restored)
	}
}

func TestScriptTaskFreezesSelectedChannelKey(t *testing.T) {
	info := &relaycommon.RelayInfo{
		ChannelMeta:   &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI, ApiKey: "selected-key"},
		TaskRelayInfo: &relaycommon.TaskRelayInfo{PublicTaskID: "task_fixture"},
	}
	task := InitTask(constant.TaskPlatform("jsplugin"), info)
	if task.PrivateData.Key != "selected-key" {
		t.Fatal("script polling lost the selected key")
	}
}
