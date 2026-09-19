package model

import (
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 保护契约:PostgreSQL 走 simple protocol(PrepareStmt 关闭)时,driver.Valuer
// 返回 []byte 会被 pgx 按 bytea 十六进制字面量编码,写入 json 列触发
// SQLSTATE 22P02。所有 json 列的 Value() 必须返回 string(或 nil)。
func TestJSONColumnValuersReturnString(t *testing.T) {
	testCases := []struct {
		name   string
		valuer driver.Valuer
		want   string
	}{
		{
			name:   "ChannelInfo",
			valuer: ChannelInfo{IsMultiKey: true, MultiKeySize: 2},
			want:   `{"is_multi_key":true,"multi_key_size":2,"multi_key_status_list":null,"multi_key_polling_index":0,"multi_key_mode":""}`,
		},
		{
			name:   "Properties",
			valuer: Properties{Input: "hello"},
			want:   `{"input":"hello"}`,
		},
		{
			name:   "TaskPrivateData",
			valuer: TaskPrivateData{Key: "k"},
			want:   `{"key":"k"}`,
		},
		{
			name:   "JSONValue",
			valuer: JSONValue(`[{"k":"v"}]`),
			want:   `[{"k":"v"}]`,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			value, err := testCase.valuer.Value()
			require.NoError(t, err)
			str, ok := value.(string)
			require.True(t, ok, "Value() must return string, got %T", value)
			assert.JSONEq(t, testCase.want, str)
		})
	}
}

// 空值仍返回 nil,保持列的 NULL 语义。
func TestJSONColumnValuersZeroValueIsNil(t *testing.T) {
	for name, valuer := range map[string]driver.Valuer{
		"Properties":      Properties{},
		"TaskPrivateData": TaskPrivateData{},
		"JSONValue":       JSONValue(nil),
		"JSONValue_empty": JSONValue{},
	} {
		t.Run(name, func(t *testing.T) {
			value, err := valuer.Value()
			require.NoError(t, err)
			assert.Nil(t, value)
		})
	}
}

// 保护契约:json 列的 Scan 必须同时接受 []byte 与 string——不同驱动/协议
// 模式返回类型不同,静默丢弃 string 会把已有数据清零。
func TestJSONColumnScannersAcceptStringAndBytes(t *testing.T) {
	toInput := func(kind string, payload string) interface{} {
		if kind == "bytes" {
			return []byte(payload)
		}
		return payload
	}

	for _, kind := range []string{"bytes", "string"} {
		t.Run(kind, func(t *testing.T) {
			var info ChannelInfo
			require.NoError(t, info.Scan(toInput(kind, `{"is_multi_key":true,"multi_key_size":2}`)))
			assert.True(t, info.IsMultiKey)
			assert.Equal(t, 2, info.MultiKeySize)

			var props Properties
			require.NoError(t, props.Scan(toInput(kind, `{"input":"hello"}`)))
			assert.Equal(t, "hello", props.Input)

			var private TaskPrivateData
			require.NoError(t, private.Scan(toInput(kind, `{"key":"k"}`)))
			assert.Equal(t, "k", private.Key)

			var items JSONValue
			require.NoError(t, items.Scan(toInput(kind, `["a","b"]`)))
			assert.JSONEq(t, `["a","b"]`, string(items))
		})
	}
}

func TestJSONColumnScannersReplaceAndRejectInvalidTypes(t *testing.T) {
	tests := []struct {
		name string
		seed func() sql.Scanner
		zero sql.Scanner
	}{
		{
			name: "ChannelInfo",
			seed: func() sql.Scanner {
				return &ChannelInfo{IsMultiKey: true, MultiKeySize: 2, MultiKeyStatusList: map[int]int{0: 3}, MultiKeyDisabledReason: map[int]string{0: "old"}}
			},
			zero: &ChannelInfo{},
		},
		{
			name: "Properties",
			seed: func() sql.Scanner { return &Properties{Input: "old", UpstreamModelName: "private-model"} },
			zero: &Properties{},
		},
		{
			name: "TaskPrivateData",
			seed: func() sql.Scanner {
				return &TaskPrivateData{Key: "old-secret", UpstreamTaskID: "private-id", TokenId: 7, BillingContext: &TaskBillingContext{GroupRatio: 1}}
			},
			zero: &TaskPrivateData{},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			for _, input := range []any{nil, []byte(nil), []byte{}, "", "null", []byte("null"), "{}", []byte("{}")} {
				scanner := test.seed()
				require.NoError(t, scanner.Scan(input), "input %T: %v", input, input)
				assert.Equal(t, test.zero, scanner, "old fields must not survive a new row")
			}
			for _, input := range []any{123, true, map[string]string{"key": "secret-value"}, "{invalid", []byte("{invalid")} {
				scanner := test.seed()
				err := scanner.Scan(input)
				require.Error(t, err)
				assert.NotContains(t, err.Error(), "secret-value")
				assert.Equal(t, test.seed(), scanner, "failed scans must not silently clear or partially mutate data")
			}
		})
	}
}

// SQLite round-trip:Value() 现在写 string,读回仍要完整;同时手工写入旧格式
// 的 BLOB,证明历史 []byte 数据仍可被 Scan 读出。
func TestJSONColumnSQLiteRoundTripAndLegacyBlob(t *testing.T) {
	truncateTables(t)
	// TestMain 的共享测试库未迁移 PrefillGroup,这里补上;清理逻辑也放在本测试内。
	require.NoError(t, DB.AutoMigrate(&PrefillGroup{}))
	t.Cleanup(func() { DB.Exec("DELETE FROM prefill_groups") })

	channel := Channel{
		Name:   "json-column-roundtrip",
		Key:    "key-a\nkey-b",
		Status: common.ChannelStatusEnabled,
		ChannelInfo: ChannelInfo{
			IsMultiKey:           true,
			MultiKeySize:         2,
			MultiKeyMode:         constant.MultiKeyModePolling,
			MultiKeyPollingIndex: 1,
		},
	}
	require.NoError(t, DB.Create(&channel).Error)

	task := Task{
		TaskID:     "json-column-roundtrip-task",
		Platform:   constant.TaskPlatformSuno,
		UserId:     1,
		Status:     TaskStatusInProgress,
		Properties: Properties{Input: "hello"},
		PrivateData: TaskPrivateData{
			Key: "k",
		},
		Data: json.RawMessage(`{"raw":"data"}`),
	}
	require.NoError(t, DB.Create(&task).Error)

	group := PrefillGroup{
		Name:  "json-column-roundtrip",
		Type:  "model",
		Items: JSONValue(`["gpt-4o","gpt-3.5-turbo"]`),
	}
	require.NoError(t, DB.Create(&group).Error)

	var storedChannel Channel
	require.NoError(t, DB.First(&storedChannel, channel.Id).Error)
	assert.True(t, storedChannel.ChannelInfo.IsMultiKey)
	assert.Equal(t, 2, storedChannel.ChannelInfo.MultiKeySize)
	assert.Equal(t, 1, storedChannel.ChannelInfo.MultiKeyPollingIndex)

	var storedTask Task
	require.NoError(t, DB.First(&storedTask, task.ID).Error)
	assert.Equal(t, "hello", storedTask.Properties.Input)
	assert.Equal(t, "k", storedTask.PrivateData.Key)
	assert.JSONEq(t, `{"raw":"data"}`, string(storedTask.Data))

	var storedGroup PrefillGroup
	require.NoError(t, DB.First(&storedGroup, group.Id).Error)
	assert.JSONEq(t, `["gpt-4o","gpt-3.5-turbo"]`, string(storedGroup.Items))

	// 新的 Value() 返回 string,SQLite 中应为 text 而不是 BLOB。
	var channelInfoType string
	require.NoError(t, DB.Raw("SELECT typeof(channel_info) FROM channels WHERE id = ?", channel.Id).Scan(&channelInfoType).Error)
	assert.Equal(t, "text", channelInfoType)

	// 手工写入旧驱动产生的 BLOB,验证仍可读出。
	require.NoError(t, DB.Exec("UPDATE channels SET channel_info = ? WHERE id = ?",
		[]byte(`{"is_multi_key":true,"multi_key_size":3,"multi_key_polling_index":2}`), channel.Id).Error)
	require.NoError(t, DB.Exec("UPDATE tasks SET properties = ?, private_data = ? WHERE id = ?",
		[]byte(`{"input":"legacy-blob"}`), []byte(`{"key":"legacy-secret"}`), task.ID).Error)
	require.NoError(t, DB.Exec("UPDATE prefill_groups SET items = ? WHERE id = ?",
		[]byte(`["legacy-item"]`), group.Id).Error)

	var legacyChannel Channel
	require.NoError(t, DB.First(&legacyChannel, channel.Id).Error)
	assert.True(t, legacyChannel.ChannelInfo.IsMultiKey)
	assert.Equal(t, 3, legacyChannel.ChannelInfo.MultiKeySize)
	assert.Equal(t, 2, legacyChannel.ChannelInfo.MultiKeyPollingIndex)

	var legacyTask Task
	require.NoError(t, DB.First(&legacyTask, task.ID).Error)
	assert.Equal(t, "legacy-blob", legacyTask.Properties.Input)
	assert.Equal(t, "legacy-secret", legacyTask.PrivateData.Key)

	var legacyGroup PrefillGroup
	require.NoError(t, DB.First(&legacyGroup, group.Id).Error)
	assert.JSONEq(t, `["legacy-item"]`, string(legacyGroup.Items))
}
