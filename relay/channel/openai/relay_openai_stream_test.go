package openai

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type streamCaptureWriter struct {
	header  http.Header
	mu      sync.Mutex
	body    bytes.Buffer
	status  int
	flushed chan struct{}
}

func newStreamCaptureWriter() *streamCaptureWriter {
	return &streamCaptureWriter{
		header:  make(http.Header),
		flushed: make(chan struct{}, 8),
	}
}

func (w *streamCaptureWriter) Header() http.Header {
	return w.header
}

func (w *streamCaptureWriter) WriteHeader(statusCode int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.status == 0 {
		w.status = statusCode
	}
}

func (w *streamCaptureWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.body.Write(data)
}

func (w *streamCaptureWriter) Flush() {
	select {
	case w.flushed <- struct{}{}:
	default:
	}
}

func (w *streamCaptureWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.body.String()
}

func newOpenAIStreamTestContext(w http.ResponseWriter, includeUsage bool) (*gin.Context, *relaycommon.RelayInfo) {
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	info := &relaycommon.RelayInfo{
		StartTime:          time.Now(),
		RelayMode:          relayconstant.RelayModeChatCompletions,
		RelayFormat:        types.RelayFormatOpenAI,
		ShouldIncludeUsage: includeUsage,
		DisablePing:        true,
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "gpt-4o-mini",
		},
	}
	return c, info
}

func TestShouldHoldOpenAIUsageChunk(t *testing.T) {
	t.Parallel()

	baseInfo := &relaycommon.RelayInfo{RelayFormat: types.RelayFormatOpenAI}
	tests := []struct {
		name string
		info *relaycommon.RelayInfo
		data string
		want bool
	}{
		{name: "ordinary content", info: baseInfo, data: `{"choices":[{"delta":{"content":"hello"}}]}`, want: false},
		{name: "null usage", info: baseInfo, data: `{"choices":[],"usage":null}`, want: false},
		{name: "usage only", info: baseInfo, data: `{"choices":[],"usage":{"prompt_tokens":2,"completion_tokens":1}}`, want: true},
		{name: "content with usage", info: baseInfo, data: `{"choices":[{"delta":{"content":"hello"}}],"usage":{"prompt_tokens":2}}`, want: false},
		{name: "usage requested", info: &relaycommon.RelayInfo{RelayFormat: types.RelayFormatOpenAI, ShouldIncludeUsage: true}, data: `{"choices":[],"usage":{"prompt_tokens":2}}`, want: false},
		{name: "converted stream", info: &relaycommon.RelayInfo{RelayFormat: types.RelayFormatClaude}, data: `{"choices":[],"usage":{"prompt_tokens":2}}`, want: false},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, test.want, shouldHoldOpenAIUsageChunk(test.info, test.data))
		})
	}
}

func TestOaiStreamHandlerFlushesFirstChunkAndSuppressesUnrequestedUsage(t *testing.T) {
	oldStreamingTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() {
		constant.StreamingTimeout = oldStreamingTimeout
	})

	writer := newStreamCaptureWriter()
	c, info := newOpenAIStreamTestContext(writer, false)
	pipeReader, pipeWriter := io.Pipe()
	t.Cleanup(func() {
		_ = pipeWriter.Close()
		_ = pipeReader.Close()
	})

	resp := &http.Response{Body: pipeReader}
	firstFrame := `{"id":"chatcmpl-test","created":1,"model":"gpt-4o-mini","choices":[{"index":0,"delta":{"role":"assistant","content":"hello"},"finish_reason":null}]}`
	usageFrame := `{"id":"chatcmpl-test","created":1,"model":"gpt-4o-mini","choices":[],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`

	done := make(chan struct{})
	var handlerErr *types.NewAPIError
	go func() {
		_, handlerErr = OaiStreamHandler(c, info, resp)
		close(done)
	}()

	_, err := io.WriteString(pipeWriter, "data: "+firstFrame+"\n\n")
	require.NoError(t, err)

	select {
	case <-writer.flushed:
		require.Contains(t, writer.String(), firstFrame)
	case <-time.After(3 * time.Second):
		t.Fatal("first content chunk was not flushed before the next upstream chunk")
	}

	_, err = io.WriteString(pipeWriter, "data: "+usageFrame+"\n\n")
	require.NoError(t, err)
	_, err = io.WriteString(pipeWriter, "data: [DONE]\n\n")
	require.NoError(t, err)
	require.NoError(t, pipeWriter.Close())

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("stream handler did not finish")
	}

	require.Nil(t, handlerErr)
	output := writer.String()
	require.Equal(t, 1, strings.Count(output, firstFrame), "the final normal chunk must not be duplicated")
	require.NotContains(t, output, usageFrame, "usage-only chunk must stay hidden when include_usage is false")
	require.Contains(t, output, "data: [DONE]")
}
