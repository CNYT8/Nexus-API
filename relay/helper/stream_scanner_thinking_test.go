package helper

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func thinkingWriterTest(t *testing.T, format types.RelayFormat) (*gin.Context, *httptest.ResponseRecorder, *relaycommon.RelayInfo) {
	t.Helper()
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	info := &relaycommon.RelayInfo{
		RelayFormat:          format,
		ThinkingProcessStrip: true,
		StreamStatus:         relaycommon.NewStreamStatus(),
	}
	ensureOutputSensitiveStreamBlockState(c)
	EnsureStreamWriteBudget(c)
	installThinkingStreamWriter(c, info)
	return c, recorder, info
}

func thinkingWriterEvents(t *testing.T, data string) []gjson.Result {
	t.Helper()
	var result []gjson.Result
	for _, line := range strings.Split(data, "\n") {
		payload, ok := strings.CutPrefix(line, "data: ")
		if !ok || payload == "[DONE]" {
			continue
		}
		require.True(t, json.Valid([]byte(payload)), payload)
		result = append(result, gjson.Parse(payload))
	}
	return result
}

func TestThinkingStripWriterOriginalEnvelopeAndEOF(t *testing.T) {
	c, recorder, _ := thinkingWriterTest(t, types.RelayFormatOpenAIResponses)
	frame := "event: response.output_text.delta\nid: original-id\nretry: 123\ndata: " +
		`{"type":"response.output_text.delta","output_index":0,"content_index":0,"sequence_number":9007199254740993,"delta":"visible<thi","extension":{"thinking":"metadata"}}` + "\n\n"
	// 包含 data 及 JSON 内部切分，模拟 CustomEvent 的多次 Write。
	for _, part := range []string{frame[:11], frame[11:77], frame[77 : len(frame)-1], frame[len(frame)-1:]} {
		_, err := c.Writer.Write([]byte(part))
		require.NoError(t, err)
	}
	require.Empty(t, recorder.Body.String(), "普通前缀未确定前保留原事件")
	FinalizeThinkingStream(c)
	events := thinkingWriterEvents(t, recorder.Body.String())
	require.Len(t, events, 1)
	require.Equal(t, "visible<thi", events[0].Get("delta").String())
	require.Equal(t, "9007199254740993", events[0].Get("sequence_number").Raw)
	require.Equal(t, "metadata", events[0].Get("extension.thinking").String())
	require.Contains(t, recorder.Body.String(), "event: response.output_text.delta\nid: original-id\nretry: 123\n")
}

func TestThinkingStripWriterClaudeLifecycleAndTools(t *testing.T) {
	c, recorder, _ := thinkingWriterTest(t, types.RelayFormatClaude)
	for _, data := range []string{
		`{"type":"message_start","message":{"id":"m1","content":[],"usage":{"input_tokens":11}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"HIDDEN"}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"HIDDEN_SIGNATURE"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}`,
		`{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"visible<thi"}}`,
		`{"type":"content_block_stop","index":1}`,
		`{"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"call1","name":"lookup","input":{},"signature":"tool-signature"}}`,
		`{"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"{\"thinking\":\"keep\",\"n\":9007199254740993}"}}`,
		`{"type":"content_block_stop","index":2}`,
		`{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":7}}`,
		`{"type":"message_stop"}`,
	} {
		require.NoError(t, StringData(c, data))
	}
	FinalizeThinkingStream(c)
	output := recorder.Body.String()
	require.NotContains(t, output, "HIDDEN")
	events := thinkingWriterEvents(t, output)
	var sequence []string
	for _, event := range events {
		kind := event.Get("type").String()
		if strings.HasPrefix(kind, "content_block_") {
			sequence = append(sequence, kind+":"+event.Get("index").Raw)
		}
	}
	require.Equal(t, []string{"content_block_start:0", "content_block_delta:0", "content_block_stop:0", "content_block_start:1", "content_block_delta:1", "content_block_stop:1"}, sequence)
	require.Equal(t, "visible<thi", events[2].Get("delta.text").String())
	require.Equal(t, "tool-signature", events[4].Get("content_block.signature").String())
	require.Equal(t, `{"thinking":"keep","n":9007199254740993}`, events[5].Get("delta.partial_json").String())
	require.Equal(t, int64(7), events[len(events)-2].Get("usage.output_tokens").Int())
	require.Equal(t, "message_stop", events[len(events)-1].Get("type").String())
}

func TestThinkingStripWriterResponsesIndexesAndSnapshot(t *testing.T) {
	c, recorder, _ := thinkingWriterTest(t, types.RelayFormatOpenAIResponses)
	for _, data := range []string{
		`{"type":"response.output_item.added","output_index":0,"item":{"id":"r1","type":"reasoning"}}`,
		`{"type":"response.reasoning_summary_text.delta","output_index":0,"delta":"HIDDEN"}`,
		`{"type":"response.output_item.done","output_index":0,"item":{"id":"r1","type":"reasoning"}}`,
		`{"type":"response.output_item.added","output_index":1,"item":{"id":"m1","type":"message","content":[]}}`,
		`{"type":"response.content_part.added","output_index":1,"content_index":0,"part":{"type":"reasoning_text","text":"HIDDEN"}}`,
		`{"type":"response.content_part.done","output_index":1,"content_index":0,"part":{"type":"reasoning_text","text":"HIDDEN"}}`,
		`{"type":"response.content_part.added","output_index":1,"content_index":1,"part":{"type":"output_text","text":""}}`,
		`{"type":"response.output_text.delta","item_id":"m1","output_index":1,"content_index":1,"sequence_number":7,"delta":"visible<thi"}`,
		`{"type":"response.output_text.done","item_id":"m1","output_index":1,"content_index":1,"sequence_number":8,"text":"visible<thi"}`,
		`{"type":"response.output_item.added","output_index":2,"item":{"id":"f1","type":"function_call","call_id":"call1","name":"lookup","arguments":"","signature":"tool-signature"}}`,
		`{"type":"response.function_call_arguments.delta","output_index":2,"item_id":"f1","sequence_number":10,"delta":"{\"thinking\":\"keep\",\"n\":9007199254740993}"}`,
		`{"type":"response.completed","sequence_number":11,"response":{"status":"completed","output":[{"id":"r1","type":"reasoning","summary":[{"text":"HIDDEN"}]},{"id":"m1","type":"message","content":[{"type":"reasoning_text","text":"HIDDEN"},{"type":"output_text","text":"visible<thi"}]},{"id":"f1","type":"function_call","arguments":"{\"thinking\":\"keep\"}","signature":"tool-signature"}],"usage":{"input_tokens":11,"output_tokens":7,"total_tokens":18}}}`,
	} {
		require.NoError(t, StringData(c, data))
	}
	FinalizeThinkingStream(c)
	output := recorder.Body.String()
	require.NotContains(t, output, "HIDDEN")
	events := thinkingWriterEvents(t, output)
	for _, event := range events {
		if event.Get("item_id").String() == "m1" || event.Get("part").Exists() {
			require.Equal(t, int64(0), event.Get("output_index").Int())
			require.Equal(t, int64(0), event.Get("content_index").Int())
		}
		if event.Get("sequence_number").Int() == 7 {
			require.Equal(t, "visible<thi", event.Get("delta").String())
		}
		if event.Get("item_id").String() == "f1" {
			require.Equal(t, int64(1), event.Get("output_index").Int())
			require.Equal(t, `{"thinking":"keep","n":9007199254740993}`, event.Get("delta").String())
		}
	}
	terminal := events[len(events)-1]
	require.Equal(t, int64(11), terminal.Get("sequence_number").Int())
	require.Len(t, terminal.Get("response.output").Array(), 2)
	require.Len(t, terminal.Get("response.output.0.content").Array(), 1)
	require.Equal(t, "tool-signature", terminal.Get("response.output.1.signature").String())
	require.Equal(t, int64(18), terminal.Get("response.usage.total_tokens").Int())
}

func TestThinkingStripWriterChoicesAndSensitiveTail(t *testing.T) {
	c, recorder, _ := thinkingWriterTest(t, types.RelayFormatOpenAI)
	for _, data := range []string{
		`{"choices":[{"index":0,"delta":{"content":"<thi"}},{"index":1,"delta":{"content":"other"}}]}`,
		`{"choices":[{"index":1,"delta":{"content":" visible"}},{"index":0,"delta":{"content":"nk>HIDDEN"}}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"},{"index":1,"delta":{},"finish_reason":"stop"}],"usage":{"total_tokens":18}}`,
		"[DONE]",
	} {
		require.NoError(t, StringData(c, data))
	}
	FinalizeThinkingStream(c)
	text := map[int64]string{}
	for _, event := range thinkingWriterEvents(t, recorder.Body.String()) {
		for _, choice := range event.Get("choices").Array() {
			text[choice.Get("index").Int()] += choice.Get("delta.content").String()
		}
	}
	require.Equal(t, "", text[0])
	require.Equal(t, "other visible", text[1])
	require.Contains(t, recorder.Body.String(), `"total_tokens":18`)

	// 扫描器已正常结束后，适配器最后一帧或 EOF 普通前缀仍应触发策略。
	c, recorder, info := thinkingWriterTest(t, types.RelayFormatOpenAI)
	info.OutputSensitiveConfig = setting.OutputSensitiveConfig{Enabled: true, Action: "error", MatchPercent: 100, Patterns: []string{"visible<thi"}}
	writer := c.Writer.(*thinkingStreamWriter)
	writer.matchers = make(map[string]*relaycommon.OutputSensitiveMatcher)
	require.NoError(t, StringData(c, `{"choices":[{"index":0,"delta":{"content":"visible<thi"}}]}`))
	info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonEOF, nil)
	FinalizeThinkingStream(c)
	Done(c)
	require.Equal(t, relaycommon.StreamEndReasonOutputSensitive, info.StreamStatus.EndReason)
	require.NotContains(t, recorder.Body.String(), "visible")
	require.Equal(t, 1, strings.Count(recorder.Body.String(), "sensitive_words_detected"))
	require.NotContains(t, recorder.Body.String(), "[DONE]")
}

func TestThinkingStripWriterDisconnectAndInvalidFrame(t *testing.T) {
	t.Run("disconnect", func(t *testing.T) {
		c, recorder, info := thinkingWriterTest(t, types.RelayFormatOpenAI)
		ctx, cancel := context.WithCancel(c.Request.Context())
		c.Request = c.Request.WithContext(ctx)
		require.NoError(t, StringData(c, `{"choices":[{"index":0,"delta":{"content":"visible<thi"}}]}`))
		cancel()
		info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonClientGone, context.Canceled)
		FinalizeThinkingStream(c)
		require.Empty(t, recorder.Body.String())
		require.Equal(t, relaycommon.StreamEndReasonClientGone, info.StreamStatus.EndReason)
	})
	t.Run("invalid JSON", func(t *testing.T) {
		c, recorder, _ := thinkingWriterTest(t, types.RelayFormatOpenAI)
		_, err := c.Writer.Write([]byte("data: {invalid}\n\n"))
		require.Error(t, err)
		require.Empty(t, recorder.Body.String())
	})
}
