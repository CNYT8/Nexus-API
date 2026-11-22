package gemini

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func geminiToolDeclarationContext() *gin.Context {
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	return ctx
}

func TestGeminiFunctionDeclarationOptionalStrict(t *testing.T) {
	for _, tc := range []struct {
		name, strictField string
		explicitFalse     bool
	}{
		{name: "omitted"},
		{name: "false", strictField: `,"strict":false`, explicitFalse: true},
		{name: "null", strictField: `,"strict":null`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var request dto.GeneralOpenAIRequest
			require.NoError(t, json.Unmarshal([]byte(fmt.Sprintf(`{
				"model":"gemini-2.0-flash",
				"messages":[{"role":"user","content":"read a record"}],
				"tools":[{"type":"function","function":{
					"name":"read_record","description":"Read one record",
					"arguments":"declarations must not carry call arguments"%s,
					"parameters":{
						"type":"object","additionalProperties":false,
						"properties":{"strict":{"type":"boolean"},"arguments":{"type":"string"}},
						"required":["strict","arguments"]
					}
				}}]
			}`, tc.strictField)), &request))
			if tc.explicitFalse {
				require.NotNil(t, request.Tools[0].Function.Strict)
				require.False(t, *request.Tools[0].Function.Strict)
			} else {
				require.Nil(t, request.Tools[0].Function.Strict)
			}
			before, err := json.Marshal(request)
			require.NoError(t, err)
			converted, err := CovertOpenAI2Gemini(geminiToolDeclarationContext(), request, geminiRequestTestInfo(request.Model))
			require.NoError(t, err)
			require.NotNil(t, converted)
			var groups []struct {
				Declarations []map[string]json.RawMessage `json:"functionDeclarations"`
			}
			require.NoError(t, json.Unmarshal(converted.Tools, &groups))
			require.Len(t, groups, 1)
			require.Len(t, groups[0].Declarations, 1)
			declaration := groups[0].Declarations[0]
			require.Len(t, declaration, 3)
			require.NotContains(t, declaration, "strict")
			require.NotContains(t, declaration, "arguments")
			require.JSONEq(t, `"read_record"`, string(declaration["name"]))
			require.JSONEq(t, `"Read one record"`, string(declaration["description"]))
			require.JSONEq(t, `{
				"type":"OBJECT",
				"properties":{"strict":{"type":"BOOLEAN"},"arguments":{"type":"STRING"}},
				"required":["strict","arguments"]
			}`, string(declaration["parameters"]), "schema property names must not be mistaken for declaration fields")
			after, err := json.Marshal(request)
			require.NoError(t, err)
			require.JSONEq(t, string(before), string(after), "conversion must not mutate the source strict flag or schema")
		})
	}
}

func TestGeminiFunctionDeclarationStrictError(t *testing.T) {
	for _, name := range []string{"read_record", "googleSearch", "codeExecution", "urlContext"} {
		t.Run(name, func(t *testing.T) {
			var request dto.GeneralOpenAIRequest
			require.NoError(t, json.Unmarshal([]byte(fmt.Sprintf(`{
				"model":"gemini-2.0-flash",
				"messages":[{"role":"user","content":"hello"}],
				"tools":[
					{"type":"function","function":{"name":"earlier_tool","strict":false}},
					{"type":"function","function":{"name":%q,"strict":true}}
				]
			}`, name)), &request))
			converted, err := CovertOpenAI2Gemini(geminiToolDeclarationContext(), request, geminiRequestTestInfo(request.Model))
			require.Nil(t, converted, "do not emit a partially converted request")
			var apiErr *types.NewAPIError
			require.ErrorAs(t, err, &apiErr)
			require.Equal(t, http.StatusBadRequest, apiErr.StatusCode)
			require.True(t, types.IsSkipRetryError(apiErr))
			require.Equal(t, types.ErrorCode("unsupported_function_strict"), apiErr.GetErrorCode())

			// The relay's conversion-error wrapper must preserve the client error contract.
			wrapped := types.NewError(fmt.Errorf("conversion failed: %w", err), types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
			require.Same(t, apiErr, wrapped)
			body := wrapped.ToOpenAIError()
			require.Equal(t, "invalid_request_error", body.Type)
			require.Equal(t, "unsupported_function_strict", body.Code)
			require.Equal(t, "tools[1].function.strict", body.Param)
			require.Contains(t, body.Message, "set strict to false or omit it")
			require.True(t, *request.Tools[1].Function.Strict)
		})
	}
}

func TestGeminiFunctionDeclarationsPreserveBuiltins(t *testing.T) {
	for _, strictField := range []string{"", `,"strict":false`} {
		name := "omitted"
		if strictField != "" {
			name = "false"
		}
		t.Run(name, func(t *testing.T) {
			var request dto.GeneralOpenAIRequest
			require.NoError(t, json.Unmarshal([]byte(fmt.Sprintf(`{
				"model":"gemini-2.0-flash","messages":[{"role":"user","content":"hello"}],
				"tools":[
					{"type":"function","function":{"name":"googleSearch"%[1]s}},
					{"type":"function","function":{"name":"codeExecution"%[1]s}},
					{"type":"function","function":{"name":"urlContext"%[1]s}},
					{"type":"function","function":{"name":"read_record"%[1]s}}
				]
			}`, strictField)), &request))
			converted, err := CovertOpenAI2Gemini(geminiToolDeclarationContext(), request, geminiRequestTestInfo(request.Model))
			require.NoError(t, err)
			var groups []map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(converted.Tools, &groups))
			require.Len(t, groups, 4)
			seen := make(map[string]bool)
			for _, group := range groups {
				require.Len(t, group, 1)
				for key, value := range group {
					require.False(t, seen[key], "tool groups must not be duplicated")
					seen[key] = true
					if key == "functionDeclarations" {
						require.JSONEq(t, `[{"name":"read_record"}]`, string(value))
					} else {
						require.JSONEq(t, `{}`, string(value))
					}
				}
			}
			require.Equal(t, map[string]bool{"googleSearch": true, "codeExecution": true, "urlContext": true, "functionDeclarations": true}, seen)
		})
	}
}

func TestGeminiFunctionDeclarationsKeepHistory(t *testing.T) {
	strict := false
	ids := []string{"调用-\"first\"-\\-🙂", "调用-\x00-\u2028-second"}
	const arguments = `{"count":0,"enabled":false,"missing":null}`
	request := dto.GeneralOpenAIRequest{
		Model:    "gemini-2.0-flash",
		Messages: []dto.Message{{Role: "user", Content: "read records"}},
		Tools: []dto.ToolCallRequest{{
			Type:     "function",
			Function: dto.FunctionRequest{Name: "read_record", Strict: &strict},
		}},
	}
	assistant := dto.Message{Role: "assistant"}
	var calls []dto.ToolCallRequest
	for _, id := range ids {
		calls = append(calls, dto.ToolCallRequest{
			ID: id, Type: "function",
			Function: dto.FunctionRequest{Name: "read_record", Arguments: arguments},
		})
	}
	assistant.SetToolCalls(calls)
	request.Messages = append(request.Messages, assistant)
	for _, id := range ids {
		request.Messages = append(request.Messages, dto.Message{Role: "tool", ToolCallId: id, Content: `{"ok":false,"count":0,"value":null}`})
	}
	wire, err := json.Marshal(request)
	require.NoError(t, err)
	var decoded dto.GeneralOpenAIRequest
	require.NoError(t, json.Unmarshal(wire, &decoded))
	converted, err := CovertOpenAI2Gemini(geminiToolDeclarationContext(), decoded, geminiRequestTestInfo(request.Model))
	require.NoError(t, err)
	wire, err = json.Marshal(converted)
	require.NoError(t, err)
	var roundTrip dto.GeminiChatRequest
	require.NoError(t, json.Unmarshal(wire, &roundTrip))
	require.JSONEq(t, `[{"functionDeclarations":[{"name":"read_record"}]}]`, string(roundTrip.Tools))
	var callIDs, responseIDs []string
	for _, content := range roundTrip.Contents {
		for _, part := range content.Parts {
			if part.FunctionCall != nil {
				callIDs = append(callIDs, part.FunctionCall.ID)
				require.Equal(t, "read_record", part.FunctionCall.FunctionName)
				encoded, err := json.Marshal(part.FunctionCall.Arguments)
				require.NoError(t, err)
				require.JSONEq(t, arguments, string(encoded))
			}
			if part.FunctionResponse != nil {
				var id string
				require.NoError(t, json.Unmarshal(part.FunctionResponse.ID, &id))
				responseIDs = append(responseIDs, id)
				require.Equal(t, "read_record", part.FunctionResponse.Name)
			}
		}
	}
	require.Equal(t, ids, callIDs)
	require.Equal(t, ids, responseIDs)
	require.NotNil(t, decoded.Tools[0].Function.Strict)
	require.False(t, *decoded.Tools[0].Function.Strict)
}

func TestGeminiNativeToolDeclarationsRemainOpaque(t *testing.T) {
	const tools = `[{"functionDeclarations":[{
		"name":"read_record","parametersJsonSchema":{"type":"object","additionalProperties":false},
		"futureNativeOption":{"count":9007199254740993,"enabled":false}
	}],"futureTool":{"value":null}}]`
	request := &dto.GeminiChatRequest{
		Contents: []dto.GeminiChatContent{{Parts: []dto.GeminiPart{{Text: "hello"}}}},
		Tools:    json.RawMessage(tools),
	}
	converted, err := (&Adaptor{}).ConvertGeminiRequest(geminiToolDeclarationContext(), geminiRequestTestInfo("gemini-2.0-flash"), request)
	require.NoError(t, err)
	wire, err := json.Marshal(converted)
	require.NoError(t, err)
	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(wire, &fields))
	require.JSONEq(t, tools, string(fields["tools"]))
	require.Contains(t, string(fields["tools"]), "9007199254740993")
	require.Equal(t, tools, string(request.Tools))
}
