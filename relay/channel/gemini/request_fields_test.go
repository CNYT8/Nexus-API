package gemini

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGeminiRequestContinuationAndPartMediaProcessing(t *testing.T) {
	for _, tc := range []struct {
		name         string
		topFields    string
		partFields   string
		continuation string
		processing   string
	}{
		{
			name:         "camel case with opaque extensions",
			topFields:    `"continuationToken":{"token":"resume","future":{"sequence":9007199254740993,"flags":[false,0]}},`,
			partFields:   `,"mediaProcessing":{"fps":0,"future":{"enabled":false,"modes":["auto","new"]}}`,
			continuation: `{"token":"resume","future":{"sequence":9007199254740993,"flags":[false,0]}}`,
			processing:   `{"fps":0,"future":{"enabled":false,"modes":["auto","new"]}}`,
		},
		{
			name:         "snake case aliases",
			topFields:    `"continuation_token":"opaque-token",`,
			partFields:   `,"media_processing":{"future_option":[1,2,{"nested":true}]}`,
			continuation: `"opaque-token"`,
			processing:   `{"future_option":[1,2,{"nested":true}]}`,
		},
		{
			name:         "snake case takes precedence",
			topFields:    `"continuationToken":"camel","continuation_token":"snake",`,
			partFields:   `,"mediaProcessing":{"source":"camel"},"media_processing":{"source":"snake"}`,
			continuation: `"snake"`,
			processing:   `{"source":"snake"}`,
		},
		{
			name:         "explicit null aliases",
			topFields:    `"continuationToken":"camel","continuation_token":null,`,
			partFields:   `,"mediaProcessing":{"source":"camel"},"media_processing":null`,
			continuation: `null`,
			processing:   `null`,
		},
		{name: "ordinary request"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := []byte(fmt.Sprintf(`{
				%s
				"contents":[{
					"continuationToken":"legacy-content-token",
					"parts":[{"text":"hello","fileData":{"fileUri":"https://www.youtube.com/watch?v=test"}%s}]
				}],
				"generationConfig":{"mediaProcessing":{"scope":"generation","future":{"value":0}},"temperature":0},
				"system_instruction":{"parts":[{"text":"Be concise."}]},
				"cachedContent":"cachedContents/test"
			}`, tc.topFields, tc.partFields))
			var incoming dto.GeminiChatRequest
			require.NoError(t, common.Unmarshal(raw, &incoming))
			request, err := common.DeepCopy(&incoming)
			require.NoError(t, err)
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-4-argon:generateContent", bytes.NewReader(raw))
			converted, err := (&Adaptor{}).ConvertGeminiRequest(ctx, &relaycommon.RelayInfo{}, request)
			require.NoError(t, err)
			encoded, err := common.Marshal(converted)
			require.NoError(t, err)

			var out map[string]json.RawMessage
			require.NoError(t, common.Unmarshal(encoded, &out))
			var contents []map[string]json.RawMessage
			require.NoError(t, common.Unmarshal(out["contents"], &contents))
			require.Len(t, contents, 1)
			var parts []map[string]json.RawMessage
			require.NoError(t, common.Unmarshal(contents[0]["parts"], &parts))
			require.Len(t, parts, 1)
			var generation map[string]json.RawMessage
			require.NoError(t, common.Unmarshal(out["generationConfig"], &generation))

			require.JSONEq(t, `"user"`, string(contents[0]["role"]))
			require.JSONEq(t, `"hello"`, string(parts[0]["text"]))
			require.JSONEq(t, `{"mimeType":"video/webm","fileUri":"https://www.youtube.com/watch?v=test"}`, string(parts[0]["fileData"]))
			require.JSONEq(t, `"legacy-content-token"`, string(contents[0]["continuationToken"]))
			require.JSONEq(t, `{"scope":"generation","future":{"value":0}}`, string(generation["mediaProcessing"]))
			require.JSONEq(t, `0`, string(generation["temperature"]))
			require.JSONEq(t, `{"parts":[{"text":"Be concise."}]}`, string(out["systemInstruction"]))
			require.JSONEq(t, `"cachedContents/test"`, string(out["cachedContent"]))
			require.NotContains(t, out, "continuation_token")
			require.NotContains(t, out, "mediaProcessing")
			require.NotContains(t, contents[0], "mediaProcessing")
			require.NotContains(t, parts[0], "media_processing")
			require.NotContains(t, parts[0], "continuationToken")
			if tc.continuation == "" {
				require.NotContains(t, out, "continuationToken")
				require.NotContains(t, parts[0], "mediaProcessing")
			} else {
				require.JSONEq(t, tc.continuation, string(out["continuationToken"]))
				require.JSONEq(t, tc.processing, string(parts[0]["mediaProcessing"]))
				if tc.name == "camel case with opaque extensions" {
					require.Contains(t, string(out["continuationToken"]), "9007199254740993")
				}
			}
		})
	}
}
