package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestModelDeleteRootPATCannotEscapeLinkedScopes(t *testing.T) {
	for _, tc := range []struct {
		name     string
		scopes   string
		channels bool
		pricing  bool
		allowed  bool
	}{
		{"metadata_only", `["model:write"]`, false, false, true},
		{"channel_missing", `["model:write"]`, true, false, false},
		{"pricing_missing", `["model:write","channel:write"]`, true, true, false},
		{"channel_read_only", `["model:write","channel:read","system:write"]`, true, true, false},
		{"all_granted", `["model:write","channel:write","system:write"]`, true, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Set("role", common.RoleRootUser)
			c.Set("use_access_token", true)
			c.Set("access_token", &model.UserAccessToken{Scopes: tc.scopes})
			require.Equal(t, tc.allowed, authorizeModelDelete(c, tc.channels, tc.pricing))
			if !tc.allowed {
				require.True(t, c.IsAborted())
				require.Equal(t, http.StatusForbidden, recorder.Code)
			}
		})
	}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set("role", common.RoleRootUser)
	require.True(t, authorizeModelDelete(c, true, true))
	c.Set("use_access_token", true)
	require.False(t, authorizeModelDelete(c, true, true))
}

func TestFastTokenPricingUsesExplicitCompletionLimit(t *testing.T) {
	legacy, explicit, zero := uint(4096), uint(128), uint(0)
	request := &dto.GeneralOpenAIRequest{MaxTokens: &legacy, MaxCompletionTokens: &explicit}
	require.Equal(t, 128, fastTokenCountMetaForPricing(request).MaxTokens)
	request.MaxCompletionTokens = &zero
	require.Zero(t, fastTokenCountMetaForPricing(request).MaxTokens)
	request.MaxCompletionTokens = nil
	require.Equal(t, 4096, fastTokenCountMetaForPricing(request).MaxTokens)
}
