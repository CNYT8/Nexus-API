package openai

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type imageCancelWriter struct {
	gin.ResponseWriter
	cancel context.CancelFunc
}

func (w *imageCancelWriter) Write(p []byte) (int, error) {
	n, err := w.ResponseWriter.Write(p)
	if strings.Contains(string(p), "completed") {
		w.cancel()
	}
	return n, err
}

func TestImageResponseQuantitySettlement(t *testing.T) {
	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = oldTimeout })
	for _, tc := range []struct {
		name, body    string
		stream, abort bool
		want          int
	}{
		{"JSON actual", `{"data":[{"b64_json":"a"},{"b64_json":"b"}]}`, false, false, 2},
		{"JSON empty", `{"data":[]}`, false, false, 3},
		{"JSON wrapped SSE", `{"data":[{"b64_json":"a"}]}`, true, false, 1},
		{"SSE done", "data: {\"type\":\"image_generation.completed\"}\n\ndata: [DONE]\n\n", true, false, 1},
		{"SSE EOF", "data: {\"type\":\"image_edit.completed\"}\n\n", true, false, 1},
		{"SSE abort", "data: {\"type\":\"image_generation.completed\"}\n\n", true, true, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			contentType := "application/json"
			if strings.HasPrefix(tc.body, "data:") {
				contentType = "text/event-stream"
			}
			c, _, resp, info := newImageTestContext(t, tc.body, contentType, tc.stream)
			info.ImageRequestCount = 3
			info.PriceData.UsePrice = true
			info.PriceData.AddOtherRatio("n", 3)
			if tc.abort {
				ctx, cancel := context.WithCancel(c.Request.Context())
				defer cancel()
				c.Request = c.Request.WithContext(ctx)
				c.Writer = &imageCancelWriter{ResponseWriter: c.Writer, cancel: cancel}
				reader, writer := io.Pipe()
				resp.Body = reader
				defer writer.Close()
				go func() { _, _ = io.WriteString(writer, tc.body) }()
			}
			if tc.stream {
				_, apiErr := OpenaiImageStreamHandler(c, info, resp)
				require.Nil(t, apiErr)
			} else {
				_, apiErr := OpenaiImageHandler(c, info, resp)
				require.Nil(t, apiErr)
			}
			require.Equal(t, float64(tc.want), info.PriceData.OtherRatios["n"])
			require.Equal(t, 3, info.RequestedImageCount())
			if tc.abort {
				require.Contains(t, []relaycommon.StreamEndReason{relaycommon.StreamEndReasonClientGone, relaycommon.StreamEndReasonHandlerStop}, info.StreamStatus.EndReason)
			}
		})
	}
}

func TestImageTokenExpressionDoesNotAcquireQuantityMultiplier(t *testing.T) {
	c, _, resp, info := newImageTestContext(t, `{"data":[{"b64_json":"a"}]}`, "application/json", false)
	info.TieredBillingSnapshot = &billingexpr.BillingSnapshot{}
	info.ImageRequestCount = 3
	_, apiErr := OpenaiImageHandler(c, info, resp)
	require.Nil(t, apiErr)
	require.Empty(t, info.PriceData.OtherRatios)
}
