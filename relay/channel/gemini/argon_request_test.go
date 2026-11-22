package gemini

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func enableThinkingAdapterForRequestTest(t *testing.T) {
	t.Helper()
	settings := model_setting.GetGeminiSettings()
	previous := *settings
	settings.ThinkingAdapterEnabled = true
	settings.ThinkingAdapterBudgetTokensPercentage = 0.6
	t.Cleanup(func() { *settings = previous })
}

func geminiRequestTestInfo(model string) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		OriginModelName: model,
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: model,
			ChannelType:       constant.ChannelTypeGemini,
			ChannelBaseUrl:    "https://example.invalid",
		},
	}
}

func convertGeminiChatRequestForTest(t *testing.T, info *relaycommon.RelayInfo, extraBody string) *dto.GeminiChatRequest {
	t.Helper()
	var request dto.GeneralOpenAIRequest
	require.NoError(t, common.Unmarshal([]byte(`{"messages":[{"role":"user","content":"hello"}],"temperature":0.2,"max_tokens":10000}`), &request))
	request.Model = info.UpstreamModelName
	if extraBody != "" {
		request.ExtraBody = json.RawMessage(extraBody)
	}
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	converted, err := CovertOpenAI2Gemini(ctx, request, info)
	require.NoError(t, err)
	return converted
}

func requireGeminiThinkingJSON(t *testing.T, request *dto.GeminiChatRequest, expected string) {
	t.Helper()
	encoded, err := common.Marshal(request)
	require.NoError(t, err)
	var out map[string]json.RawMessage
	require.NoError(t, common.Unmarshal(encoded, &out))
	var generation map[string]json.RawMessage
	require.NoError(t, common.Unmarshal(out["generationConfig"], &generation))
	if expected == "" {
		require.NotContains(t, generation, "thinkingConfig")
	} else {
		require.JSONEq(t, expected, string(generation["thinkingConfig"]))
	}
}

func TestGeminiThinkingSuffixRequestConversion(t *testing.T) {
	enableThinkingAdapterForRequestTest(t)
	for _, tc := range []struct {
		model     string
		baseModel string
		config    string
	}{
		{"gemini-4-argon-thinking-4096", "gemini-4-argon", `{"includeThoughts":true,"thinkingLevel":"medium"}`},
		{"gemini-4-argon-thinking-0", "gemini-4-argon", `{"includeThoughts":true,"thinkingLevel":"low"}`},
		{"gemini-4-argon-thinking-1", "gemini-4-argon", `{"includeThoughts":true,"thinkingLevel":"low"}`},
		{"gemini-4-argon-thinking-1024", "gemini-4-argon", `{"includeThoughts":true,"thinkingLevel":"low"}`},
		{"gemini-4-argon-thinking-1025", "gemini-4-argon", `{"includeThoughts":true,"thinkingLevel":"medium"}`},
		{"gemini-4-argon-thinking-8192", "gemini-4-argon", `{"includeThoughts":true,"thinkingLevel":"medium"}`},
		{"gemini-4-argon-thinking-8193", "gemini-4-argon", `{"includeThoughts":true,"thinkingLevel":"high"}`},
		{"gemini-4-argon-thinking--1", "gemini-4-argon", `{"includeThoughts":true,"thinkingLevel":"high"}`},
		{"gemini-4-argon-nothinking", "gemini-4-argon", `{"includeThoughts":false,"thinkingLevel":"low"}`},
		{"gemini-4-argon-none", "gemini-4-argon", `{"includeThoughts":false,"thinkingLevel":"low"}`},
		{"gemini-4-argon-minimal", "gemini-4-argon", `{"includeThoughts":true,"thinkingLevel":"low"}`},
		{"gemini-4-argon-low", "gemini-4-argon", `{"includeThoughts":true,"thinkingLevel":"low"}`},
		{"gemini-4-argon-medium", "gemini-4-argon", `{"includeThoughts":true,"thinkingLevel":"medium"}`},
		{"gemini-4-argon-high", "gemini-4-argon", `{"includeThoughts":true,"thinkingLevel":"high"}`},
		{"gemini-4-argon-xhigh", "gemini-4-argon", `{"includeThoughts":true,"thinkingLevel":"high"}`},
		{"gemini-4-argon-max", "gemini-4-argon", `{"includeThoughts":true,"thinkingLevel":"high"}`},
		{"gemini-4-argon-thinking", "gemini-4-argon", `{"includeThoughts":true}`},
		{"gemini-4-argon-cyber-permissive-thinking", "gemini-4-argon-cyber-permissive", `{"includeThoughts":true}`},
		{"gemini-4-argon", "gemini-4-argon", ""},
		{"gemini-4-argon-thinking-invalid", "gemini-4-argon-thinking-invalid", ""},
		{"gemini-4-argon-thinking-999999999999999999999999", "gemini-4-argon-thinking-999999999999999999999999", ""},
		{"gemini-2.0-flash-thinking-4096", "gemini-2.0-flash", `{"includeThoughts":true,"thinkingBudget":4096}`},
		{"gemini-2.5-flash-thinking-4096", "gemini-2.5-flash", `{"includeThoughts":true,"thinkingBudget":4096}`},
		{"gemini-2.5-flash-thinking", "gemini-2.5-flash", `{"includeThoughts":true,"thinkingBudget":6000}`},
		{"gemini-2.5-flash-nothinking", "gemini-2.5-flash", `{"thinkingBudget":0}`},
		{"gemini-2.5-pro-thinking-1", "gemini-2.5-pro", `{"includeThoughts":true,"thinkingBudget":128}`},
		{"gemini-2.5-pro-thinking-40000", "gemini-2.5-pro", `{"includeThoughts":true,"thinkingBudget":32768}`},
		{"gemini-2.5-pro-nothinking", "gemini-2.5-pro", ""},
		{"gemini-2.5-flash-lite-thinking-1", "gemini-2.5-flash-lite", `{"includeThoughts":true,"thinkingBudget":512}`},
		{"gemini-2.5-pro-preview-03-25-thinking", "gemini-2.5-pro-preview-03-25", `{"includeThoughts":true}`},
		{"gemini-3.1-pro-high", "gemini-3.1-pro", `{"includeThoughts":true,"thinkingLevel":"high"}`},
		{"gemini-3-flash-minimal", "gemini-3-flash", `{"includeThoughts":true,"thinkingLevel":"minimal"}`},
		{"gemini-3-flash-thinking-4096", "gemini-3-flash", `{"includeThoughts":true,"thinkingBudget":4096}`},
		{"gemini-3-flash-preview", "gemini-3-flash-preview", ""},
	} {
		t.Run(tc.model, func(t *testing.T) {
			for _, native := range []bool{false, true} {
				t.Run(fmt.Sprintf("native=%t", native), func(t *testing.T) {
					info := geminiRequestTestInfo(tc.model)
					info.IsStream = native
					var request *dto.GeminiChatRequest
					if native {
						request = &dto.GeminiChatRequest{}
						require.NoError(t, common.Unmarshal([]byte(`{"contents":[{"role":"user","parts":[{"text":"hello"}]}],"generationConfig":{"temperature":0.2,"maxOutputTokens":10000}}`), request))
						ThinkingAdaptor(request, info)
						_, err := (&Adaptor{}).ConvertGeminiRequest(nil, info, request)
						require.NoError(t, err)
					} else {
						request = convertGeminiChatRequestForTest(t, info, "")
					}
					requireGeminiThinkingJSON(t, request, tc.config)
					switch tc.model {
					case "gemini-4-argon-thinking":
						require.Equal(t, "medium", info.ReasoningEffort)
					case "gemini-4-argon-cyber-permissive-thinking":
						require.Equal(t, "high", info.ReasoningEffort)
					}
					require.Len(t, request.Contents, 1)
					require.Equal(t, "user", request.Contents[0].Role)
					require.Len(t, request.Contents[0].Parts, 1)
					require.Equal(t, "hello", request.Contents[0].Parts[0].Text)
					require.Equal(t, 0.2, *request.GenerationConfig.Temperature)
					require.Equal(t, uint(10000), *request.GenerationConfig.MaxOutputTokens)
					url, err := (&Adaptor{}).GetRequestURL(info)
					require.NoError(t, err)
					action := "generateContent"
					if native {
						action = "streamGenerateContent?alt=sse"
					}
					require.Equal(t, tc.baseModel, info.UpstreamModelName)
					require.Equal(t, "https://example.invalid/"+model_setting.GetGeminiVersionSetting(tc.baseModel)+"/models/"+tc.baseModel+":"+action, url)
				})
			}
		})
	}
}

func TestArgonThinkingAdapterRespectsSwitchAndPreservedSuffix(t *testing.T) {
	for _, preserve := range []bool{false, true} {
		t.Run(fmt.Sprintf("preserve=%t", preserve), func(t *testing.T) {
			enableThinkingAdapterForRequestTest(t)
			model := "gemini-4-argon-thinking-4096"
			model_setting.GetGeminiSettings().ThinkingAdapterEnabled = preserve
			if preserve {
				settings := model_setting.GetGlobalSettings()
				previous := settings.ThinkingModelBlacklist
				settings.ThinkingModelBlacklist = []string{model}
				t.Cleanup(func() { settings.ThinkingModelBlacklist = previous })
			}
			info := geminiRequestTestInfo(model)
			request := convertGeminiChatRequestForTest(t, info, "")
			requireGeminiThinkingJSON(t, request, "")
			url, err := (&Adaptor{}).GetRequestURL(info)
			require.NoError(t, err)
			require.Contains(t, url, "/models/"+model+":generateContent")
			require.Equal(t, model, info.UpstreamModelName)
		})
	}
}

func TestGeminiNativeThinkingConfigPrecedesSuffix(t *testing.T) {
	enableThinkingAdapterForRequestTest(t)
	for _, model := range []string{"gemini-4-argon-thinking-4096", "gemini-4-argon-nothinking", "gemini-4-argon-minimal", "gemini-2.5-flash-thinking", "gemini-3-flash-high"} {
		t.Run(model, func(t *testing.T) {
			for _, config := range []string{`{"thinkingLevel":"high"}`, `{"includeThoughts":true,"thinkingBudget":777}`, `{}`} {
				var request dto.GeminiChatRequest
				raw := fmt.Sprintf(`{"contents":[{"role":"user","parts":[{"text":"hello"}]}],"generationConfig":{"thinkingConfig":%s}}`, config)
				require.NoError(t, common.Unmarshal([]byte(raw), &request))
				info := geminiRequestTestInfo(model)
				ThinkingAdaptor(&request, info)
				_, err := (&Adaptor{}).ConvertGeminiRequest(nil, info, &request)
				require.NoError(t, err)
				requireGeminiThinkingJSON(t, &request, config)
			}
		})
	}
}

func TestGeminiExtraBodyThinkingConfigPrecedesSuffix(t *testing.T) {
	enableThinkingAdapterForRequestTest(t)
	for _, tc := range []struct {
		name   string
		model  string
		extra  string
		config string
		image  string
	}{
		{"level overrides nothinking", "gemini-4-argon-nothinking", `{"google":{"thinking_config":{"thinking_level":"high","include_thoughts":true}}}`, `{"includeThoughts":true,"thinkingLevel":"high"}`, ""},
		{"budget overrides alias", "gemini-4-argon-thinking-4096", `{"google":{"thinking_config":{"thinking_budget":777,"include_thoughts":false}}}`, `{"includeThoughts":false,"thinkingBudget":777}`, ""},
		{"zero remains explicit", "gemini-4-argon-high", `{"google":{"thinking_config":{"thinking_budget":0}}}`, `{"includeThoughts":false,"thinkingBudget":0}`, ""},
		{"empty config suppresses alias", "gemini-4-argon-high", `{"google":{"thinking_config":{}}}`, "", ""},
		{"image config allows alias", "gemini-4-argon-thinking-4096", `{"google":{"image_config":{"aspect_ratio":"16:9","image_size":"2K"}}}`, `{"includeThoughts":true,"thinkingLevel":"medium"}`, `{"aspectRatio":"16:9","imageSize":"2K"}`},
		{"unrelated google options allow alias", "gemini-4-argon-minimal", `{"google":{"future_option":true}}`, `{"includeThoughts":true,"thinkingLevel":"low"}`, ""},
		{"legacy explicit config", "gemini-2.5-flash-nothinking", `{"google":{"thinking_config":{"thinking_budget":1234}}}`, `{"includeThoughts":true,"thinkingBudget":1234}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := convertGeminiChatRequestForTest(t, geminiRequestTestInfo(tc.model), tc.extra)
			requireGeminiThinkingJSON(t, request, tc.config)
			if tc.image != "" {
				require.JSONEq(t, tc.image, string(request.GenerationConfig.ImageConfig))
			}
		})
	}
}

func TestGeminiExtraBodyThinkingConfigValidationWithNoThinkingSuffix(t *testing.T) {
	enableThinkingAdapterForRequestTest(t)
	for _, tc := range []struct {
		extra string
		error string
	}{
		{`{"google":{"thinkingConfig":{}}}`, "extra_body.google.thinkingConfig is not supported"},
		{`{"google":{"thinking_config":{"thinkingBudget":4096}}}`, "extra_body.google.thinking_config.thinkingBudget is not supported"},
		{`{"google":{"thinking_config":{"thinking_budget":"4096"}}}`, "thinking_budget must be an integer"},
		{`{"google":{"thinking_config":{"include_thoughts":"false"}}}`, "include_thoughts must be a boolean"},
		{`{"google":{"thinking_config":{"thinking_level":1}}}`, "thinking_level must be a string"},
	} {
		t.Run(tc.error, func(t *testing.T) {
			var request dto.GeneralOpenAIRequest
			raw := `{"model":"gemini-4-argon-nothinking","messages":[{"role":"user","content":"hello"}],"extra_body":` + tc.extra + `}`
			require.NoError(t, common.Unmarshal([]byte(raw), &request))
			_, err := CovertOpenAI2Gemini(nil, request, geminiRequestTestInfo(request.Model))
			require.ErrorContains(t, err, tc.error)
		})
	}
}
