package router

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newRelayRouterTestContext(method string, target string) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(method, target, nil)
	return ctx, recorder
}

func TestRejectGeminiCountTokens(t *testing.T) {
	tests := []struct {
		name         string
		method       string
		path         string
		wantRejected bool
	}{
		{
			name:         "countTokens is rejected before auth",
			method:       http.MethodPost,
			path:         "/v1beta/models/gemini-2.5-pro:countTokens",
			wantRejected: true,
		},
		{
			name:         "generateContent is not intercepted",
			method:       http.MethodPost,
			path:         "/v1beta/models/gemini-2.5-pro:generateContent",
			wantRejected: false,
		},
		{
			name:         "streamGenerateContent is not intercepted",
			method:       http.MethodPost,
			path:         "/v1beta/models/gemini-2.5-pro:streamGenerateContent",
			wantRejected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, recorder := newRelayRouterTestContext(tt.method, tt.path)
			// No Authorization header on purpose: the guard must answer 404
			// instead of letting the auth middleware return 401.
			require.Empty(t, ctx.Request.Header.Get("Authorization"))

			rejectGeminiCountTokens(ctx)

			if !tt.wantRejected {
				assert.False(t, ctx.IsAborted())
				assert.Empty(t, recorder.Body.String())
				return
			}

			require.True(t, ctx.IsAborted())
			require.Equal(t, http.StatusNotFound, recorder.Code)

			var payload struct {
				Error struct {
					Message string `json:"message"`
					Type    string `json:"type"`
					Param   string `json:"param"`
					Code    any    `json:"code"`
				} `json:"error"`
			}
			require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &payload))
			assert.Equal(t, "Invalid URL (POST "+tt.path+")", payload.Error.Message)
			assert.Equal(t, "invalid_request_error", payload.Error.Type)
			assert.Empty(t, payload.Error.Param)
			assert.Equal(t, "", payload.Error.Code)
		})
	}
}
