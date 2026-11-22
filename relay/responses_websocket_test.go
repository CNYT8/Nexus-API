package relay

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func responsesWSTestContext() (*gin.Context, *relaycommon.RelayInfo) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	common.SetContextKey(c, constant.ContextKeyOriginalModel, "responses-ws-test-model")
	common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeOpenAI)
	common.SetContextKey(c, constant.ContextKeyChannelKey, "upstream-key")
	common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, "https://upstream.example")
	info := relaycommon.GenRelayInfoResponses(c, &dto.OpenAIResponsesRequest{Model: "responses-ws-test-model"})
	info.InitChannelMeta(c)
	info.StreamStatus = relaycommon.NewStreamStatus()
	info.SetEstimatePromptTokens(12)
	return c, info
}

func TestResponsesWSNormalizeCreate(t *testing.T) {
	for _, data := range []string{
		`{"type":"response.create","stream_id":"stream-1","generate":false,"model":"test","input":"hi","stream":true,"background":true}`,
		`{"type":"response.create","response":{"stream_id":"stream-1","generate":false,"model":"test","input":"hi","stream":true,"background":true}}`,
	} {
		event, streamID, err := parseResponsesWSEnvelope([]byte(data))
		require.NoError(t, err)
		create, err := normalizeResponsesWSCreateEvent([]byte(data), event, streamID)
		require.NoError(t, err)
		require.Equal(t, "test", create.Request.Model)
		require.Equal(t, "stream-1", create.StreamID)
		for _, name := range []string{"stream_id", "generate", "type", "stream", "background"} {
			require.False(t, gjson.GetBytes(create.Body, name).Exists(), name)
		}
		payload, err := buildResponsesWSCreateEvent(create.Body, create.Generate, create.StreamID)
		require.NoError(t, err)
		require.Equal(t, "response.create", gjson.GetBytes(payload, "type").String())
		require.Equal(t, "stream-1", gjson.GetBytes(payload, "stream_id").String())
		require.Equal(t, "false", gjson.GetBytes(payload, "generate").Raw)
	}
	for _, stream := range []string{`null`, `""`, `12`, `"has space"`, `"非ASCII"`, `"` + strings.Repeat("a", 257) + `"`} {
		_, _, err := parseResponsesWSEnvelope([]byte(`{"type":"response.create","stream_id":` + stream + `}`))
		require.Error(t, err)
	}
	for _, data := range []string{
		`{"type":"response.create","generate":"false"}`,
		`{"type":"response.create","response":null}`,
		`{"type":"response.create","max_output_tokens":1073741824}`,
	} {
		event, stream, err := parseResponsesWSEnvelope([]byte(data))
		require.NoError(t, err)
		_, err = normalizeResponsesWSCreateEvent([]byte(data), event, stream)
		require.Error(t, err)
	}
}

func TestResponsesWSControlErrorCorrelation(t *testing.T) {
	control := []byte(`{"type":"response.cancel","event_id":"cancel-1","stream_id":"stream","response_id":"other"}`)
	cases := []struct {
		name      string
		event     responsesWSErrorEvent
		control   []byte
		terminal  bool
		ambiguous bool
		isControl bool
	}{
		{name: "cancel event id", event: responsesWSErrorEvent{EventID: "cancel-1"}, control: control, isControl: true},
		{name: "cancel different response", event: responsesWSErrorEvent{ResponseID: "other"}, control: control, isControl: true},
		{name: "cancel code", event: responsesWSErrorEvent{Error: &types.OpenAIError{Type: "invalid_request_error", Code: "response_not_active"}}, control: control, isControl: true},
		{name: "different stream", event: responsesWSErrorEvent{StreamID: "unrelated"}, control: control},
		{name: "different response", event: responsesWSErrorEvent{ResponseID: "unrelated"}},
		{name: "ambiguous after cancel", event: responsesWSErrorEvent{}, control: control, terminal: true, ambiguous: true},
		{name: "active request error", event: responsesWSErrorEvent{ResponseID: "active"}, control: control, terminal: true},
		{name: "initial rejection", event: responsesWSErrorEvent{}, terminal: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			terminal, ambiguous, isControl := responsesWSErrorEndsRequest(tc.event, "stream", "active", tc.control)
			require.Equal(t, tc.terminal, terminal)
			require.Equal(t, tc.ambiguous, ambiguous)
			require.Equal(t, tc.isControl, isControl)
		})
	}
}

func TestResponsesWSFinalizationExactlyOnce(t *testing.T) {
	for _, tc := range []struct {
		name       string
		dispatched bool
		rejected   bool
		settles    int
		refunds    int
	}{
		{name: "preflight failure", refunds: 1},
		{name: "explicit initial rejection", dispatched: true, rejected: true, refunds: 1},
		{name: "completed", dispatched: true, settles: 1},
		{name: "ambiguous write or disconnect", dispatched: true, settles: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			settles, refunds := 0, 0
			f := &responsesWSFinalizer{dispatched: tc.dispatched, rejected: tc.rejected,
				settle: func() { settles++ }, refund: func() { refunds++ }}
			f.finish()
			f.finish()
			require.Equal(t, tc.settles, settles)
			require.Equal(t, tc.refunds, refunds)
		})
	}
	refunds := 0
	f := &responsesWSFinalizer{dispatched: true, settle: func() { panic("settlement persistence failed") }, refund: func() { refunds++ }}
	require.Panics(t, f.finish)
	require.NotPanics(t, f.finish)
	require.Zero(t, refunds)
}

type responsesWSTestBilling struct {
	relaycommon.BillingSettler
	calls int
	err   error
}

func (b *responsesWSTestBilling) Settle(int) error {
	b.calls++
	return b.err
}

func TestResponsesWSBillingObserverCapturesPersistenceFailure(t *testing.T) {
	billing := &responsesWSTestBilling{err: errors.New("commit failed")}
	observer := &responsesWSBillingObserver{BillingSettler: billing}
	require.ErrorIs(t, observer.Settle(25), billing.err)
	require.True(t, observer.called)
	require.ErrorIs(t, observer.err, billing.err)
	require.Equal(t, 1, billing.calls)
}

func responsesWSTestObserve(t *testing.T, c *gin.Context, usage *responsesWSUsage, data string) {
	t.Helper()
	var event dto.ResponsesStreamResponse
	require.NoError(t, json.Unmarshal([]byte(data), &event))
	usage.observe(c, &event, []byte(data))
}

func TestResponsesWSUsageSnapshotsAndIsolation(t *testing.T) {
	c, info := responsesWSTestContext()
	usage := newResponsesWSUsage(info)
	responsesWSTestObserve(t, c, usage, `{"type":"response.output_text.delta","delta":"hello"}`)
	responsesWSTestObserve(t, c, usage, `{"type":"response.output_item.done","item":{"id":"tool","type":"web_search_call"}}`)
	responsesWSTestObserve(t, c, usage, `{"type":"response.output_item.done","item":{"id":"tool","type":"web_search_call"}}`)
	terminal := `{"type":"response.completed","response":{"id":"one","usage":{"input_tokens":10,"output_tokens":3,"total_tokens":13,"input_tokens_details":{"cached_tokens":4},"output_tokens_details":{"reasoning_tokens":2}},"output":[{"id":"tool","type":"web_search_call"}]}}`
	responsesWSTestObserve(t, c, usage, terminal)
	responsesWSTestObserve(t, c, usage, terminal)
	result := usage.finish(c)
	require.Equal(t, 10, result.PromptTokens)
	require.Equal(t, 3, result.CompletionTokens)
	require.Equal(t, 13, result.TotalTokens)
	require.Equal(t, 4, result.PromptTokensDetails.CachedTokens)
	require.Equal(t, 2, result.CompletionTokenDetails.ReasoningTokens)
	require.Empty(t, result.UsageSource)
	require.Equal(t, map[string]int{dto.BuildInToolWebSearchPreview: 1}, result.ToolUsage)
	require.Equal(t, 1, info.ResponsesUsageInfo.BuiltInTools[dto.BuildInToolWebSearchPreview].CallCount)

	c2, info2 := responsesWSTestContext()
	usage2 := newResponsesWSUsage(info2)
	responsesWSTestObserve(t, c2, usage2, `{"type":"response.completed","response":{"usage":{"input_tokens":7,"output_tokens":0}}}`)
	result2 := usage2.finish(c2)
	require.Equal(t, 7, result2.PromptTokens)
	require.Zero(t, result2.CompletionTokens)
	require.Empty(t, result2.UsageSource)
	require.Empty(t, info2.ResponsesUsageInfo.BuiltInTools)
}

func TestResponsesWSToolUsageUnionsPartialSnapshots(t *testing.T) {
	c, info := responsesWSTestContext()
	usage := newResponsesWSUsage(info)
	responsesWSTestObserve(t, c, usage, `{"type":"response.output_item.added","output_index":0,"item":{"type":"web_search_call"}}`)
	responsesWSTestObserve(t, c, usage, `{"type":"response.output_item.done","output_index":0,"item":{"id":"search-a","type":"web_search_call"}}`)
	responsesWSTestObserve(t, c, usage, `{"type":"response.output_item.done","output_index":1,"item":{"id":"files-a","type":"file_search_call"}}`)
	responsesWSTestObserve(t, c, usage, `{"type":"response.completed","response":{"output":[{"id":"search-a","type":"web_search_call"},{"id":"files-a","type":"file_search_call"},{"id":"search-b","type":"web_search_call"}],"usage":{"input_tokens":1,"output_tokens":1}}}`)
	require.Equal(t, map[string]int{dto.BuildInToolWebSearchPreview: 2, dto.BuildInToolFileSearch: 1}, usage.finish(c).ToolUsage)
}

func TestResponsesWSToolUsageIncludesImageWithSearch(t *testing.T) {
	c, info := responsesWSTestContext()
	usage := newResponsesWSUsage(info)
	responsesWSTestObserve(t, c, usage, `{"type":"response.output_item.done","output_index":0,"item":{"id":"image-a","type":"image_generation_call","status":"completed","result":"image-data","quality":"high","size":"1024x1024"}}`)
	responsesWSTestObserve(t, c, usage, `{"type":"response.output_item.done","output_index":1,"item":{"id":"code-a","type":"code_interpreter_call"}}`)
	responsesWSTestObserve(t, c, usage, `{"type":"response.completed","response":{"output":[{"id":"image-a","type":"image_generation_call"},{"id":"code-a","type":"code_interpreter_call"},{"id":"search-a","type":"web_search_call"}],"usage":{"input_tokens":1,"output_tokens":1}}}`)
	require.Equal(t, map[string]int{"image_generation": 1, "code_interpreter": 1, dto.BuildInToolWebSearchPreview: 1}, usage.finish(c).ToolUsage)
	require.Equal(t, "high", c.GetString("image_generation_call_quality"))
	require.Equal(t, "1024x1024", c.GetString("image_generation_call_size"))
}

func TestResponsesWSToolUsageCompletedEvidenceAndFunctions(t *testing.T) {
	prices := config.GlobalConfig.Get("tool_price_setting").(*operation_setting.ToolPriceSetting)
	previous := prices.Prices
	prices.Prices = map[string]float64{"lookup:responses-ws-test-model": 6, "web_search": 999}
	operation_setting.RebuildToolPriceIndex()
	t.Cleanup(func() {
		prices.Prices = previous
		operation_setting.RebuildToolPriceIndex()
	})
	for _, status := range []string{"completed", "failed", "incomplete", "cancelled", "canceled"} {
		t.Run(status, func(t *testing.T) {
			c, info := responsesWSTestContext()
			usage := newResponsesWSUsage(info)
			responsesWSTestObserve(t, c, usage, `{"type":"response.output_item.added","output_index":2,"item":{"type":"image_generation_call","id":"added","status":"completed","result":"only-added"}}`)
			image := `{"type":"response.output_item.done","output_index":0,"item":{"type":"image_generation_call","id":"image","status":"completed","result":"image-data"}}`
			function := `{"type":"response.output_item.done","output_index":1,"item":{"type":"function_call","name":"lookup","call_id":"call","status":"completed","arguments":"{}"}}`
			for _, event := range []string{image, function, image, function} {
				responsesWSTestObserve(t, c, usage, event)
			}
			terminal := `{"type":"response.` + status + `","response":{"status":"` + status + `","usage":{"input_tokens":1,"output_tokens":1},"output":[{"type":"image_generation_call","id":"image","status":"completed","result":"image-data"},{"type":"function_call","id":"function","name":"lookup","call_id":"call","status":"completed","arguments":"{}"},{"type":"image_generation_call","id":"failed","status":"failed","result":"failed-data"},{"type":"image_generation_call","id":"empty","status":"completed"},{"type":"function_call","id":"unpriced","name":"unpriced","status":"completed"},{"type":"function_call","id":"reserved","name":"web_search","status":"completed"}]}}`
			responsesWSTestObserve(t, c, usage, terminal)
			responsesWSTestObserve(t, c, usage, terminal)
			want := map[string]int{"image_generation": 1, "lookup": 1}
			require.Equal(t, want, usage.finish(c).ToolUsage)
			require.Equal(t, want, usage.finish(c).ToolUsage)
			require.Equal(t, 1, info.BuiltInTools["lookup"].CallCount)
			require.Equal(t, 1, c.GetInt("image_generation_call_count"))
		})
	}
}

func TestResponsesWSToolUsageEmptySuppressesLegacyFallback(t *testing.T) {
	c, info := responsesWSTestContext()
	c.Set("image_generation_call", true)
	c.Set("image_generation_call_count", 9)
	usage := newResponsesWSUsage(info)
	responsesWSTestObserve(t, c, usage, `{"type":"response.output_item.added","output_index":0,"item":{"type":"image_generation_call","id":"added","result":"only-added"}}`)
	responsesWSTestObserve(t, c, usage, `{"type":"response.failed","response":{"status":"failed","output":[{"type":"image_generation_call","id":"unknown","result":"no-completion"},{"type":"image_generation_call","id":"empty","status":"completed"},{"type":"image_generation_call","id":"partial","status":"partial","result":"partial-data"}]}}`)
	counts := usage.finish(c).ToolUsage
	require.NotNil(t, counts)
	require.Empty(t, counts)
	require.False(t, c.GetBool("image_generation_call"))
	require.Zero(t, c.GetInt("image_generation_call_count"))
}

func TestResponsesWSUsageInterruptedIsConservative(t *testing.T) {
	c, info := responsesWSTestContext()
	usage := newResponsesWSUsage(info)
	responsesWSTestObserve(t, c, usage, `{"type":"response.reasoning_text.delta","delta":"private reasoning uses tokens"}`)
	result := usage.finish(c)
	require.Equal(t, 12, result.PromptTokens)
	require.Positive(t, result.CompletionTokens)
	require.Equal(t, "responses_estimated", result.UsageSource)
}

func TestResponsesWSOutputPolicyPreservesMetering(t *testing.T) {
	c, info := responsesWSTestContext()
	info.ThinkingProcessStrip = true
	output := responsesWSOutput{context: c, info: info}
	usage := newResponsesWSUsage(info)
	data := `{"type":"response.completed","response":{"usage":{"input_tokens":2,"output_tokens":5},"output":[{"type":"reasoning","summary":[{"type":"summary_text","text":"secret reasoning"}]},{"type":"message","content":[{"type":"output_text","text":"visible answer"}]}]}}`
	responsesWSTestObserve(t, c, usage, data)
	frames, err := output.filter([]byte(data))
	require.NoError(t, err)
	require.NotEmpty(t, frames)
	for _, frame := range frames {
		require.NotContains(t, string(frame.body), "secret reasoning")
	}
	require.Contains(t, string(frames[len(frames)-1].body), "visible answer")
	require.Equal(t, 5, usage.finish(c).CompletionTokens)

	c, info = responsesWSTestContext()
	info.OutputSensitiveConfig = setting.OutputSensitiveConfig{Enabled: true, Action: "error", MatchPercent: 100, Patterns: []string{"blocked"}}
	output = responsesWSOutput{context: c, info: info}
	_, err = output.filter([]byte(`{"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"bloc"}`))
	require.NoError(t, err)
	frames, err = output.filter([]byte(`{"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"ked"}`))
	require.Error(t, err)
	require.Empty(t, frames)
	require.Equal(t, "output_sensitive", common.GetContextKeyString(c, constant.ContextKeyAdminRejectReason))
}

func TestResponsesWSChannelCapabilityAndKeyRevocation(t *testing.T) {
	enabled := `{"responses_websocket_enabled":true}`
	disabled := `{}`
	ch := &model.Channel{Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled, Key: "key", Setting: &enabled}
	require.True(t, responsesWSChannelSupported(ch))
	ch.Setting = &disabled
	require.False(t, responsesWSChannelSupported(ch))
	ch.Setting = &enabled
	ch.Type = constant.ChannelTypeAnthropic
	require.False(t, responsesWSChannelSupported(ch))
	ch.Type = constant.ChannelTypeAdvancedCustom
	ch.OtherSettings = `{"advanced_custom":{"advanced_routes":[{"incoming_path":"/v1/responses","upstream_path":"/native","converter":"none"}]}}`
	require.True(t, responsesWSChannelSupported(ch))
	ch.OtherSettings = strings.ReplaceAll(ch.OtherSettings, `"none"`, `"openai"`)
	require.False(t, responsesWSChannelSupported(ch))
	require.True(t, responsesWSKeyEnabled(ch, "key", 0))
	require.False(t, responsesWSKeyEnabled(ch, "rotated", 0))
	ch.ChannelInfo.IsMultiKey = true
	ch.Key = "first\nsecond"
	ch.ChannelInfo.MultiKeyStatusList = map[int]int{1: common.ChannelStatusManuallyDisabled}
	require.True(t, responsesWSKeyEnabled(ch, "first", 0))
	require.False(t, responsesWSKeyEnabled(ch, "second", 1))
	require.False(t, responsesWSKeyEnabled(ch, "first", 1))
}

func TestResponsesWSHandshakePreservesDownstreamIdentity(t *testing.T) {
	c, info := responsesWSTestContext()
	c.Request.Header.Set("Authorization", "Bearer downstream-key")
	c.Request.Header.Set("Sec-WebSocket-Protocol", "openai-insecure-api-key.downstream-key")
	target, headers, err := responsesWSHandshake(c, info)
	require.NoError(t, err)
	require.Equal(t, "wss://upstream.example/v1/responses", target)
	require.Equal(t, "Bearer upstream-key", headers.Get("Authorization"))
	require.Empty(t, headers.Get("Sec-WebSocket-Protocol"))
	require.Equal(t, "Bearer downstream-key", c.Request.Header.Get("Authorization"))
	require.Equal(t, "responses_websockets=2026-02-06", headers.Get("OpenAI-Beta"))
}

// Local sockets are created only when the main integration owner runs tests.
func responsesWSTestSocketPair(t *testing.T) (*websocket.Conn, *websocket.Conn) {
	t.Helper()
	connections := make(chan *websocket.Conn, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err == nil {
			connections <- conn
		}
	}))
	t.Cleanup(server.Close)
	client, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	require.NoError(t, err)
	var peer *websocket.Conn
	select {
	case peer = <-connections:
	case <-time.After(3 * time.Second):
		t.Fatal("websocket upgrade did not complete")
	}
	t.Cleanup(func() { _ = client.Close(); _ = peer.Close() })
	return peer, client
}

func TestResponsesWSTerminalWaitsForRunnerAndReleasesAdmission(t *testing.T) {
	peer, client := responsesWSTestSocketPair(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	state := &responsesWSCallState{done: make(chan struct{})}
	entered, release := make(chan struct{}), make(chan struct{})
	s := &responsesWSSession{ctx: ctx, cancel: cancel, client: peer, current: state, request: httptest.NewRequest(http.MethodGet, "/v1/responses", nil)}
	s.runner = func(*http.Request, string, func(*gin.Context) *types.NewAPIError) *types.NewAPIError {
		state.terminal = []responsesWSMessage{{kind: websocket.TextMessage, body: []byte(`{"type":"response.completed"}`)}}
		close(entered)
		<-release // models settlement followed by middleware completion
		return nil
	}
	data := []byte(`{"type":"response.create","model":"test"}`)
	event, streamID, err := parseResponsesWSEnvelope(data)
	require.NoError(t, err)
	go s.runRequest(state, data, event, streamID, "request-one")
	<-entered
	require.False(t, s.tryReserveCurrent(&responsesWSCallState{}))
	select {
	case <-state.done:
		t.Fatal("request admitted a successor before settlement completed")
	default:
	}
	close(release)
	require.NoError(t, client.SetReadDeadline(time.Now().Add(3*time.Second)))
	_, terminal, err := client.ReadMessage()
	require.NoError(t, err)
	require.Equal(t, "response.completed", gjson.GetBytes(terminal, "type").String())
	require.True(t, s.tryReserveCurrent(&responsesWSCallState{}))
	<-state.done
}

func TestResponsesWSConsumeRejectsBeforeAcceptance(t *testing.T) {
	c, info := responsesWSTestContext()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &responsesWSSession{ctx: ctx, cancel: cancel}
	state := &responsesWSCallState{inbox: make(chan responsesWSMessage, 1), controls: make(chan responsesWSControl, 1)}
	state.inbox <- responsesWSMessage{kind: websocket.TextMessage, body: []byte(`{"type":"error","status":400,"error":{"type":"invalid_request_error","message":"rejected"}}`)}
	settles, refunds := 0, 0
	finalizer := &responsesWSFinalizer{dispatched: true, settle: func() { settles++ }, refund: func() { refunds++ }}
	apiErr := s.consume(c, state, responsesWSCreateRequest{}, info, newResponsesWSUsage(info), finalizer)
	require.NotNil(t, apiErr)
	require.True(t, finalizer.rejected)
	finalizer.finish()
	finalizer.finish()
	require.Equal(t, 1, refunds)
	require.Zero(t, settles)
}

func TestResponsesWSConsumeIgnoresPreviousTerminal(t *testing.T) {
	c, info := responsesWSTestContext()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &responsesWSSession{ctx: ctx, cancel: cancel, lastResponseID: "previous"}
	state := &responsesWSCallState{inbox: make(chan responsesWSMessage, 2), controls: make(chan responsesWSControl, 1)}
	state.inbox <- responsesWSMessage{kind: websocket.TextMessage, body: []byte(`{"type":"response.completed","response":{"id":"previous","usage":{"input_tokens":99,"output_tokens":99}}}`)}
	state.inbox <- responsesWSMessage{kind: websocket.TextMessage, body: []byte(`{"type":"response.completed","response":{"id":"current","usage":{"input_tokens":2,"output_tokens":1}}}`)}
	usage := newResponsesWSUsage(info)
	require.Nil(t, s.consume(c, state, responsesWSCreateRequest{}, info, usage, &responsesWSFinalizer{}))
	require.Equal(t, "current", s.lastResponseID)
	require.Len(t, state.terminal, 1)
	require.Equal(t, 3, usage.finish(c).TotalTokens)
}
