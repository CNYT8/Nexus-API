package aws

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestClaudeSafeguardsSurviveAWSRequestConversion(t *testing.T) {
	for _, tc := range []struct {
		name       string
		safeguards json.RawMessage
	}{
		{name: "ordinary request"},
		{name: "opaque extensions", safeguards: json.RawMessage(`{"mode":"standard","future_option":{"enabled":false,"limit":0,"sequence":9007199254740993,"rules":["a",{"b":true}]}}`)},
		{name: "explicit null", safeguards: json.RawMessage(`null`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fields := map[string]any{
				"model":      "claude-sonnet-4-5",
				"max_tokens": 128,
				"messages":   []any{map[string]any{"role": "user", "content": "hello"}},
				"system":     "Be concise.",
			}
			if tc.safeguards != nil {
				fields["safeguards"] = tc.safeguards
			}
			raw, err := common.Marshal(fields)
			require.NoError(t, err)
			var incoming dto.ClaudeRequest
			require.NoError(t, common.Unmarshal(raw, &incoming))
			request, err := common.DeepCopy(&incoming)
			require.NoError(t, err)

			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(raw))
			converted, err := (&Adaptor{}).ConvertClaudeRequest(ctx, &relaycommon.RelayInfo{}, request)
			require.NoError(t, err)
			body, err := common.Marshal(converted)
			require.NoError(t, err)
			headers := http.Header{}
			headers.Set("anthropic-beta", " first-beta, , second-beta ")
			awsRequest, err := formatRequest(bytes.NewReader(body), headers)
			require.NoError(t, err)
			encoded, err := common.Marshal(awsRequest)
			require.NoError(t, err)
			var out map[string]json.RawMessage
			require.NoError(t, common.Unmarshal(encoded, &out))

			require.JSONEq(t, `"bedrock-2023-05-31"`, string(out["anthropic_version"]))
			require.JSONEq(t, `["first-beta","second-beta"]`, string(out["anthropic_beta"]))
			require.JSONEq(t, `[{"role":"user","content":"hello"}]`, string(out["messages"]))
			require.JSONEq(t, `128`, string(out["max_tokens"]))
			require.JSONEq(t, `"Be concise."`, string(out["system"]))
			if tc.safeguards == nil {
				require.NotContains(t, out, "safeguards")
			} else {
				require.JSONEq(t, string(tc.safeguards), string(out["safeguards"]))
				if tc.name == "opaque extensions" {
					require.Contains(t, string(out["safeguards"]), "9007199254740993")
				}
			}
		})
	}
}
