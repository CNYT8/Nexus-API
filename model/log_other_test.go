package model

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLogOtherRoleProjection(t *testing.T) {
	const raw = `{"counter":9007199254740993,"nested":{"quota":9223372036854775807},"channel_id":7,"channel_name":"secret","use_channel":[7],"upstream_model_name":"private","reject_reason":"blocked","root_info":{"secret":"root"},"admin_info":{"debug":true},"audit_info":{"actor":1},"stream_status":{"errors":["private"]}}`
	for _, role := range []string{"user", "admin", "root", "restricted"} {
		t.Run(role, func(t *testing.T) {
			logs := []*Log{nil, {Id: 500, ChannelId: 7, ChannelName: "secret", Type: LogTypeManage, Other: raw}}
			switch role {
			case "user":
				formatUserLogs(logs, 10)
				require.Zero(t, logs[1].ChannelId)
				require.Empty(t, logs[1].ChannelName)
				require.Equal(t, 12, logs[1].Id)
			case "admin":
				stripNonConsumeLogClients(logs)
				FormatAdminLogs(logs)
			case "root":
				stripNonConsumeLogClients(logs)
				FormatRootLogs(logs)
			case "restricted":
				FormatAdminLogs(logs)
				StripChannelRestrictedAdminLogFields(logs)
				require.Zero(t, logs[1].ChannelId)
			}
			fields := decodeLogOther(logs[1].Other)
			require.Equal(t, "9007199254740993", string(fields["counter"]))
			require.JSONEq(t, `{"quota":9223372036854775807}`, string(fields["nested"]))
			require.NotContains(t, fields, "reject_reason")
			if role != "root" {
				require.NotContains(t, fields, "root_info")
			}
			if role == "user" {
				for _, key := range []string{"admin_info", "audit_info", "channel_id", "channel_name", "use_channel", "upstream_model_name", "stream_status"} {
					require.NotContains(t, fields, key)
				}
			} else {
				admin := decodeLogOther(string(fields["admin_info"]))
				require.Equal(t, `"blocked"`, string(admin["reject_reason"]))
			}
		})
	}
}

func TestLogOtherMalformedFailsClosed(t *testing.T) {
	for _, raw := range []string{`{"root_info":`, `"secret"`, `["secret"]`, `null`} {
		logs := []*Log{{Other: raw}}
		FormatAdminLogs(logs)
		require.JSONEq(t, `{}`, logs[0].Other)
		require.JSONEq(t, `{}`, formatUserLogOther(raw))
	}
}

func TestLogRejectReasonCompatibilityAndCompensation(t *testing.T) {
	for _, raw := range []string{
		`{"reject_reason":"legacy","admin_info":null}`,
		`{"reject_reason":"legacy","admin_info":{"reject_reason":"current"}}`,
		`{"admin_info":{"reject_reason":"current"}}`,
	} {
		require.True(t, isExcludedEmptyResponseLog(raw))
		logs := []*Log{{Other: raw}}
		FormatAdminLogs(logs)
		require.True(t, isExcludedEmptyResponseLog(logs[0].Other))
		if raw == `{"reject_reason":"legacy","admin_info":{"reject_reason":"current"}}` {
			admin := decodeLogOther(string(decodeLogOther(logs[0].Other)["admin_info"]))
			require.Equal(t, `"current"`, string(admin["reject_reason"]))
		}
	}
	other := map[string]interface{}{"admin_info": map[string]interface{}{"keep": 1}}
	SetLogOtherAdminField(other, "reject_reason", "policy")
	b, err := json.Marshal(other)
	require.NoError(t, err)
	require.JSONEq(t, `{"admin_info":{"keep":1,"reject_reason":"policy"}}`, string(b))
	require.True(t, isExcludedEmptyResponseLog(string(b)))
}
