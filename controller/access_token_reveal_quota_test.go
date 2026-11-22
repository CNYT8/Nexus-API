package controller

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAuthenticationControllerRevealIndependentScopeAndOwnership(t *testing.T) {
	db, user := authenticationControllerDB(t)
	owned := seedToken(t, db, user.Id, "own", "owned-relay-key")
	foreign := seedToken(t, db, user.Id+1, "foreign", "foreign-relay-key")
	for _, batch := range []bool{false, true} {
		for _, kind := range []string{"session", "legacy", "read", "write", "read-write", "reveal", "unresolved"} {
			t.Run(fmt.Sprintf("batch-%t/%s", batch, kind), func(t *testing.T) {
				target := fmt.Sprintf("/api/token/%d/key", owned.Id)
				var body any
				if batch {
					target, body = "/api/token/batch/keys", gin.H{"ids": []int{owned.Id, foreign.Id}}
				}
				c, recorder := newAuthenticatedContext(t, http.MethodPost, target, body, user.Id)
				c.Params = gin.Params{{Key: "id", Value: fmt.Sprint(owned.Id)}}
				if kind != "session" {
					c.Set("use_access_token", true)
					var token *model.UserAccessToken
					scopes := map[string]string{"read": `["token:read"]`, "write": `["token:write"]`,
						"read-write": `["token:read","token:write"]`, "reveal": `["token:reveal"]`}
					if encoded, ok := scopes[kind]; ok {
						token = &model.UserAccessToken{Scopes: encoded}
					}
					if kind != "unresolved" {
						c.Set("access_token", token)
					}
				}
				if batch {
					GetTokenKeysBatch(c)
				} else {
					GetTokenKey(c)
				}
				require.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))
				require.NotContains(t, recorder.Body.String(), foreign.Key)
				if kind == "session" || kind == "reveal" {
					require.Equal(t, http.StatusOK, recorder.Code)
					require.True(t, decodeAPIResponse(t, recorder).Success)
					require.Contains(t, recorder.Body.String(), owned.Key)
				} else {
					require.Equal(t, http.StatusForbidden, recorder.Code)
					require.Contains(t, recorder.Body.String(), "TOKEN_REVEAL_REQUIRED")
					require.NotContains(t, recorder.Body.String(), owned.Key)
				}
			})
		}
	}
	c, recorder := newAuthenticatedContext(t, http.MethodPost, "/api/token/foreign/key", nil, user.Id)
	c.Params = gin.Params{{Key: "id", Value: fmt.Sprint(foreign.Id)}}
	c.Set("access_token", &model.UserAccessToken{Scopes: `["token:reveal"]`})
	GetTokenKey(c)
	require.False(t, decodeAPIResponse(t, recorder).Success)
	require.NotContains(t, recorder.Body.String(), foreign.Key)
}

func TestAuthenticationControllerUserQuotaObjectContract(t *testing.T) {
	db, user := authenticationControllerDB(t)
	require.NoError(t, db.Model(user).Updates(map[string]any{"quota": 12345, "used_quota": 678}).Error)
	token := seedToken(t, db, user.Id, "quota", "quota-relay-key")
	require.NoError(t, db.Model(token).Updates(map[string]any{"remain_quota": 90, "used_quota": 10}).Error)
	for _, allowed := range []bool{false, true} {
		require.NoError(t, db.Model(token).Update("allow_user_quota_query", allowed).Error)
		c, recorder := newAuthenticatedContext(t, http.MethodGet, "/api/usage/token", nil, user.Id)
		c.Set("token_id", token.Id)
		// A second Authorization parser would read a different identity; the
		// controller must use only the identity established by middleware.
		c.Request.Header.Set("Authorization", "Bearer unrelated-key")
		GetTokenUsage(c)
		var response struct {
			Code bool           `json:"code"`
			Data map[string]any `json:"data"`
		}
		require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
		require.True(t, response.Code)
		require.Equal(t, allowed, response.Data["allow_user_quota_query"])
		require.Equal(t, float64(90), response.Data["total_available"])
		require.Equal(t, float64(10), response.Data["total_used"])
		if allowed {
			require.Equal(t, map[string]any{"total_available": float64(12345), "total_used": float64(678)}, response.Data["user_quota"])
		} else {
			require.NotContains(t, response.Data, "user_quota", "disabled means omitted, not null or a scalar")
		}
	}
	c, recorder := newAuthenticatedContext(t, http.MethodGet, "/api/usage/token", nil, user.Id+1)
	c.Set("token_id", token.Id)
	GetTokenUsage(c)
	require.False(t, decodeAPIResponse(t, recorder).Success)
	require.NotContains(t, recorder.Body.String(), "user_quota")
}
