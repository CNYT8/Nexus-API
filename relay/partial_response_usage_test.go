package relay

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestPartialResponseUsageRecognizesNonTextUsage(t *testing.T) {
	for _, tc := range []struct {
		name  string
		usage *dto.Usage
		want  bool
	}{
		{"missing", nil, false},
		{"empty", &dto.Usage{}, false},
		{"zero_tools", &dto.Usage{ToolUsage: map[string]int{"search": 0}}, false},
		{"negative_tools", &dto.Usage{ToolUsage: map[string]int{"search": -1}}, false},
		{"prompt", &dto.Usage{PromptTokens: 1}, true},
		{"responses_input", &dto.Usage{InputTokens: 1}, true},
		{"responses_output", &dto.Usage{OutputTokens: 1}, true},
		{"cache_hit", &dto.Usage{PromptCacheHitTokens: 2}, true},
		{"cache_write", &dto.Usage{PromptTokensDetails: dto.InputTokenDetails{CacheCreationInputTokens: 2}}, true},
		{"claude_cache", &dto.Usage{ClaudeCacheCreation1hTokens: 2}, true},
		{"responses_cache", &dto.Usage{InputTokensDetails: &dto.InputTokenDetails{CachedTokens: 2}}, true},
		{"audio", &dto.Usage{CompletionTokenDetails: dto.OutputTokenDetails{AudioTokens: 3}}, true},
		{"image", &dto.Usage{OutputTokensDetails: &dto.OutputTokenDetails{ImageTokens: 3}}, true},
		{"reasoning", &dto.Usage{CompletionTokenDetails: dto.OutputTokenDetails{ReasoningTokens: 4}}, true},
		{"tool_only", &dto.Usage{ToolUsage: map[string]int{"search": 1}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, hasObservedResponseUsage(tc.usage))
		})
	}
}

func TestPartialResponseCannotRetryAfterStreamOutput(t *testing.T) {
	for _, written := range []bool{false, true} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
		if written {
			_, err := c.Writer.WriteString("data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n")
			require.NoError(t, err)
		}
		upstreamErr := types.NewErrorWithStatusCode(errors.New("upstream interrupted"), types.ErrorCodeBadResponse, http.StatusBadGateway)
		settlePartialResponseUsage(c, nil, nil, upstreamErr)
		require.Equal(t, written, types.IsSkipRetryError(upstreamErr))
		require.Equal(t, http.StatusBadGateway, upstreamErr.StatusCode)
		require.Equal(t, "upstream interrupted", upstreamErr.Error())
	}
}
