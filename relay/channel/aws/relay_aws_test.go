package aws

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestDoAwsClientRequest_AppliesRuntimeHeaderOverrideToAnthropicBeta(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	info := &relaycommon.RelayInfo{
		OriginModelName:           "claude-3-5-sonnet-20240620",
		IsStream:                  false,
		UseRuntimeHeadersOverride: true,
		RuntimeHeadersOverride: map[string]any{
			"anthropic-beta": "computer-use-2025-01-24",
		},
		ChannelMeta: &relaycommon.ChannelMeta{
			ApiKey:            "access-key|secret-key|us-east-1",
			UpstreamModelName: "claude-3-5-sonnet-20240620",
		},
	}

	requestBody := bytes.NewBufferString(`{"messages":[{"role":"user","content":"hello"}],"max_tokens":128}`)
	adaptor := &Adaptor{}

	_, err := doAwsClientRequest(ctx, info, adaptor, requestBody)
	require.NoError(t, err)

	awsReq, ok := adaptor.AwsReq.(*bedrockruntime.InvokeModelInput)
	require.True(t, ok)

	var payload map[string]any
	require.NoError(t, common.Unmarshal(awsReq.Body, &payload))

	anthropicBeta, exists := payload["anthropic_beta"]
	require.True(t, exists)

	values, ok := anthropicBeta.([]any)
	require.True(t, ok)
	require.Equal(t, []any{"computer-use-2025-01-24"}, values)
}

func TestNewAwsInvokeContextInheritsClientCancellation(t *testing.T) {
	parent, cancelParent := context.WithCancel(context.Background())
	invokeContext, cancelInvoke := newAwsInvokeContext(parent)
	t.Cleanup(cancelInvoke)

	cancelParent()
	require.ErrorIs(t, invokeContext.Err(), context.Canceled)
}

func TestNewAwsInvokeContextUsesRelayTimeoutWithClientContext(t *testing.T) {
	originalRelayTimeout := common.RelayTimeout
	common.RelayTimeout = 1
	t.Cleanup(func() { common.RelayTimeout = originalRelayTimeout })

	parent := context.Background()
	invokeContext, cancelInvoke := newAwsInvokeContext(parent)
	t.Cleanup(cancelInvoke)

	_, hasDeadline := invokeContext.Deadline()
	require.True(t, hasDeadline)
	select {
	case <-invokeContext.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("AWS invoke context did not respect relay timeout")
	}
	require.ErrorIs(t, invokeContext.Err(), context.DeadlineExceeded)
}
