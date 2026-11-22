package service

import (
	"encoding/json"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
)

func TestTaskResultResponseMetadata(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
		url  string
		want string
	}{
		{name: "empty", want: `null`},
		{
			name: "text_only",
			raw:  `{"result_text":"first reply","key":"private-key","state":{"cursor":2},"usage":{"seconds":4},"billing_key":"private-reservation","metadata":{"url":"https://example.com/private"}}`,
			want: `{"text":"first reply"}`,
		},
		{
			name: "url_only",
			url:  "https://example.com/output.mp4?signature=output-access",
			want: `{"url":"https://example.com/output.mp4?signature=output-access","artifacts":[{"id":"1","kind":"file","url":"https://example.com/output.mp4?signature=output-access"}]}`,
		},
		{
			name: "explicit_artifacts_only",
			raw:  `{"result_text":"two outputs","url":"https://example.com/private","artifacts":[{"id":"provider-id","kind":"image","url":"https://example.com/image.png","mime_type":"image/png","content_url":"/api/task/self/another-owner/artifacts/1/content","key":"private-key"},{"kind":"image","url":"https://example.com/image.png"},{"kind":"unknown","url":"https://example.com/audio.wav","mime_type":"bad\r\nmime"}]}`,
			want: `{"text":"two outputs","url":"https://example.com/image.png","artifacts":[{"id":"1","kind":"image","url":"https://example.com/image.png","mime_type":"image/png"},{"id":"2","kind":"file","url":"https://example.com/audio.wav"}]}`,
		},
		{
			name: "invalid_primary_does_not_hide_valid_artifact",
			url:  "/invalid\r\nurl",
			raw:  `{"artifacts":[{"kind":"audio","url":"https://example.com/output.wav"}]}`,
			want: `{"url":"https://example.com/output.wav","artifacts":[{"id":"2","kind":"audio","url":"https://example.com/output.wav"}]}`,
		},
		{
			name: "unsafe_urls_are_not_delivered",
			url:  "https://private-key@example.com/result",
			raw:  `{"artifacts":[{"url":"javascript:alert(1)"},{"url":"file:///etc/passwd"},{"url":"//example.com/result"},{"url":"https://example.com/result#private"}],"content_url":"/forged-proxy"}`,
			want: `null`,
		},
		{
			name: "local_result_url_remains_compatible",
			url:  "/v1/videos/public-task/content",
			want: `{"url":"/v1/videos/public-task/content","artifacts":[{"id":"1","kind":"file","url":"/v1/videos/public-task/content"}]}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := []byte(tc.raw)
			metadata := TaskResultResponseMetadata(raw, tc.url)
			encoded, err := json.Marshal(metadata)
			require.NoError(t, err)
			require.JSONEq(t, tc.want, string(encoded))
			require.Equal(t, tc.raw, string(raw), "projection must leave internal data intact")
		})
	}
}

func TestApplyImmediateTaskResultRetentionRequiresFrozenNewTerminalTask(t *testing.T) {
	require.False(t, ApplyImmediateTaskResultRetention(nil))
	for _, tc := range []struct {
		name   string
		change func(*model.Task)
	}{
		{"persisted", func(task *model.Task) { task.ID = 1 }},
		{"asynchronous", func(task *model.Task) { task.Status = model.TaskStatusInProgress }},
		{"billing_not_frozen", func(task *model.Task) { task.BillingState = "" }},
		{"billing_done", func(task *model.Task) { task.BillingState = model.TaskBillingDone }},
		{"billing_skipped", func(task *model.Task) { task.BillingState = model.TaskBillingSkipped }},
		{"invalid_target", func(task *model.Task) { task.BillingQuota = -1 }},
		{"missing_billing_context", func(task *model.Task) { task.PrivateData.BillingContext = nil }},
		{"missing_snapshot", func(task *model.Task) { task.PrivateData.BillingContext.UsageSnapshot = nil }},
		{"default_retains", func(task *model.Task) { task.PrivateData.BillingContext.UsageSnapshot.RetainResult = nil }},
		{"explicit_retains", func(task *model.Task) { *task.PrivateData.BillingContext.UsageSnapshot.RetainResult = true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			retain := false
			task := &model.Task{
				Status: model.TaskStatusSuccess, Quota: 3000, BillingState: model.TaskBillingPending, BillingQuota: 0,
				Data: json.RawMessage(`{"result_text":"retained"}`), FailReason: "retained reason",
				PrivateData: model.TaskPrivateData{
					Key: "private-key", UpstreamTaskID: "upstream", ResultURL: "https://example.com/result",
					ResultMetadata: &model.TaskResultMetadata{Text: "retained"}, ScriptPluginState: `{"cursor":1}`,
					BillingContext: &model.TaskBillingContext{UsageSnapshot: &model.TaskUsageSnapshot{RetainResult: &retain}},
				},
			}
			tc.change(task)
			before := *task
			require.False(t, ApplyImmediateTaskResultRetention(task))
			require.Equal(t, before, *task)
		})
	}
}
