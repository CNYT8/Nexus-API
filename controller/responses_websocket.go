package controller

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

type responsesWSRequestContextKey struct{}

type responsesWSIdentityVersion struct {
	user  int64
	token int64
}

type responsesWSRequestState struct {
	requestID string
	handle    func(*gin.Context) *types.NewAPIError
	apiError  *types.NewAPIError
	called    bool
	userID    int
	tokenID   int
	version   responsesWSIdentityVersion
}

// Each create gets a new Gin context and completes the normal auth/rate-limit
// middleware before its terminal frame is published. No pooled context survives
// a request, and channel credentials never replace the client's credentials.
var responsesWSRequestEngine = sync.OnceValue(func() *gin.Engine {
	engine := gin.New()
	engine.ForwardedByClientIP = false
	_ = engine.SetTrustedProxies(nil)
	engine.POST("/v1/responses", func(c *gin.Context) {
		state := c.Request.Context().Value(responsesWSRequestContextKey{}).(*responsesWSRequestState)
		c.Set(common.RequestIdKey, state.requestID)
		common.SetContextKey(c, constant.ContextKeyRequestStartTime, time.Now())
		c.Set(relay.ResponsesWebSocketContextKey, true)
		c.Next()
	}, middleware.BodyStorageCleanup(), middleware.TokenAuth(), func(c *gin.Context) {
		state := c.Request.Context().Value(responsesWSRequestContextKey{}).(*responsesWSRequestState)
		if state.apiError = revalidateResponsesWSIdentity(c, state.userID, state.tokenID, &state.version); state.apiError != nil {
			c.AbortWithStatus(state.apiError.StatusCode)
			return
		}
		c.Next()
	}, middleware.ModelRequestRateLimit(), func(c *gin.Context) {
		state := c.Request.Context().Value(responsesWSRequestContextKey{}).(*responsesWSRequestState)
		state.called = true
		state.apiError = state.handle(c)
		logErr := state.apiError
		if logErr == nil {
			if value, ok := c.Get(relay.ResponsesWebSocketErrorContextKey); ok {
				logErr, _ = value.(*types.NewAPIError)
			}
		}
		if logErr != nil {
			if value, ok := c.Get(relay.ResponsesWebSocketInfoContextKey); ok {
				if info, ok := value.(*relaycommon.RelayInfo); ok {
					recordRelayErrorLog(c, info, nil, logErr)
				}
			}
		}
		if state.apiError != nil {
			service.ApplyErrorMask(c, state.apiError)
			status := state.apiError.StatusCode
			if status < http.StatusBadRequest {
				status = http.StatusInternalServerError
			}
			c.Status(status)
		}
	})
	return engine
})

// Cached TokenAuth remains the common middleware, but persistent connections
// must also observe revocation, key rotation, quota and permission edits in DB.
func revalidateResponsesWSIdentity(c *gin.Context, userID, tokenID int, version *responsesWSIdentityVersion) *types.NewAPIError {
	deny := func(message string, status int) *types.NewAPIError {
		return types.NewErrorWithStatusCode(errors.New(message), types.ErrorCodeAccessDenied, status, types.ErrOptionWithSkipRetry())
	}
	key := strings.TrimSpace(c.GetHeader("Authorization"))
	if len(key) >= 7 && strings.EqualFold(key[:7], "Bearer ") {
		key = strings.TrimSpace(key[7:])
	}
	parts := strings.Split(strings.TrimPrefix(key, "sk-"), "-")
	if parts[0] == "" || userID <= 0 || tokenID <= 0 {
		return deny("authentication is required", http.StatusUnauthorized)
	}
	token, err := model.GetTokenByKey(parts[0], true)
	if err != nil || token == nil || token.Id != tokenID || token.UserId != userID || token.Status != common.TokenStatusEnabled ||
		(token.ExpiredTime != -1 && token.ExpiredTime < time.Now().Unix()) || (!token.UnlimitedQuota && token.RemainQuota <= 0) {
		return deny("token is no longer valid", http.StatusUnauthorized)
	}
	user, err := model.GetUserById(userID, true)
	if err != nil || user == nil || user.Status != common.UserStatusEnabled {
		return deny("user is no longer enabled", http.StatusForbidden)
	}
	if version != nil && (version.user != user.AuthVersion || version.token != token.AuthVersion) {
		return deny("authentication changed; reconnect to continue", http.StatusUnauthorized)
	}
	c.Set("responses_ws_user_auth_version", user.AuthVersion)
	c.Set("responses_ws_token_auth_version", token.AuthVersion)
	if len(parts) > 1 && user.Role < common.RoleAdminUser {
		return deny("channel pin requires an administrator", http.StatusForbidden)
	}
	if ips := token.GetIpLimits(); len(ips) > 0 {
		ip := net.ParseIP(c.ClientIP())
		if ip == nil || !common.IsIpInCIDRList(ip, ips) {
			return deny("client IP is no longer allowed", http.StatusForbidden)
		}
	}
	group := user.Group
	if token.Group != "" {
		if _, ok := service.GetUserUsableGroups(group)[token.Group]; !ok {
			return deny("token group is no longer allowed", http.StatusForbidden)
		}
		if token.Group != "auto" && !ratio_setting.ContainsGroupRatio(token.Group) {
			return deny("token group is no longer available", http.StatusForbidden)
		}
		group = token.Group
	}
	user.ToBaseUser().WriteContext(c)
	common.SetContextKey(c, constant.ContextKeyUsingGroup, group)
	if err := middleware.SetupContextForToken(c, token); err != nil {
		return deny("token context could not be refreshed", http.StatusUnauthorized)
	}
	if len(parts) > 1 {
		common.SetContextKey(c, constant.ContextKeyTokenSpecificChannelId, parts[1])
	}
	return nil
}

type responsesWSResponseWriter struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (w *responsesWSResponseWriter) Header() http.Header { return w.header }
func (w *responsesWSResponseWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}
func (w *responsesWSResponseWriter) Write(data []byte) (int, error) {
	w.WriteHeader(http.StatusOK)
	return w.body.Write(data)
}

func newResponsesWSRequestRunner(c *gin.Context) relay.ResponsesWSRequestRunner {
	headers := c.Request.Header.Clone()
	for _, name := range []string{"Connection", "Upgrade", "Sec-WebSocket-Key", "Sec-WebSocket-Version", "Sec-WebSocket-Extensions", "Sec-WebSocket-Protocol", "Content-Length", "Content-Encoding"} {
		headers.Del(name)
	}
	remoteAddr := net.JoinHostPort(c.ClientIP(), "0")
	userID, tokenID := c.GetInt("id"), c.GetInt("token_id")
	version := responsesWSIdentityVersion{user: c.GetInt64("responses_ws_user_auth_version"), token: c.GetInt64("responses_ws_token_auth_version")}
	return func(request *http.Request, requestID string, handle func(*gin.Context) *types.NewAPIError) *types.NewAPIError {
		state := &responsesWSRequestState{requestID: requestID, handle: handle, userID: userID, tokenID: tokenID, version: version}
		ctx := context.WithValue(request.Context(), responsesWSRequestContextKey{}, state)
		ctx = context.WithValue(ctx, common.RequestIdKey, requestID)
		request = request.Clone(ctx)
		request.Method = http.MethodPost
		request.URL.Path, request.URL.RawPath, request.URL.RawQuery = "/v1/responses", "", ""
		request.Header = headers.Clone()
		request.Header.Set("Content-Type", "application/json")
		request.RemoteAddr = remoteAddr
		response := &responsesWSResponseWriter{header: make(http.Header)}
		responsesWSRequestEngine().ServeHTTP(response, request)
		if state.apiError != nil {
			return state.apiError
		}
		if state.called && response.status < http.StatusBadRequest {
			return nil
		}
		status := response.status
		if status < http.StatusBadRequest {
			status = http.StatusUnauthorized
		}
		var body struct {
			Error *types.OpenAIError `json:"error"`
		}
		if common.Unmarshal(response.body.Bytes(), &body) == nil && body.Error != nil {
			return types.WithOpenAIError(*body.Error, status, types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog())
		}
		return types.NewErrorWithStatusCode(errors.New(http.StatusText(status)), types.ErrorCodeAccessDenied, status, types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog())
	}
}

// Register GET /v1/responses behind TokenAuth, without the HTTP Distribute or
// model rate-limit middleware: those run for each create, after its model exists.
func ResponsesWebSocket(c *gin.Context) {
	if c.GetInt("id") <= 0 || c.GetInt("token_id") <= 0 || c.GetString("token_key") == "" {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": gin.H{"message": "authentication is required", "type": "invalid_request_error"}})
		return
	}
	drain := common.ShutdownRequests
	stopping, done, ok := drain.Register()
	if !ok {
		c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"message": "server is shutting down", "type": "server_error"}})
		return
	}
	// The helper returns only after its readers, request workers and billing
	// finalizers have stopped. Upgrading or closing the socket is not completion.
	defer done()
	if apiErr := revalidateResponsesWSIdentity(c, c.GetInt("id"), c.GetInt("token_id"), nil); apiErr != nil {
		c.AbortWithStatusJSON(apiErr.StatusCode, gin.H{"error": apiErr.ToOpenAIError()})
		return
	}
	runner := newResponsesWSRequestRunner(c)
	wsUpgrader := websocket.Upgrader{Subprotocols: []string{"responses"}, CheckOrigin: func(r *http.Request) bool { return true }}
	ws, err := wsUpgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	defer ws.Close()
	_ = relay.ResponsesWebSocketHelper(c, ws, runner, drain, stopping)
}
