package middleware

import (
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
)

const RouteTagKey = "route_tag"

func RouteTag(tag string) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set(RouteTagKey, tag)
		c.Next()
	}
}

func SetUpLogger(server *gin.Engine) {
	server.Use(gin.LoggerWithConfig(gin.LoggerConfig{
		Output: common.LogWriter,
		Formatter: func(param gin.LogFormatterParams) string {
			var requestID string
			if param.Keys != nil {
				requestID, _ = param.Keys[common.RequestIdKey].(string)
			}
			tag, _ := param.Keys[RouteTagKey].(string)
			if tag == "" {
				tag = "web"
			}
			return fmt.Sprintf("[GIN] %s | %s | %s | %3d | %13v | %15s | %7s %s\n",
				param.TimeStamp.Format("2006/01/02 - 15:04:05"),
				tag,
				requestID,
				param.StatusCode,
				param.Latency,
				param.ClientIP,
				param.Method,
				logPath(param),
			)
		},
	}))
}

// sanitizeLogPath strips the query from OAuth callback paths so one-time
// codes and state values never reach the access log.
func sanitizeLogPath(path string) string {
	if strings.HasPrefix(path, "/api/oauth/") || strings.HasPrefix(path, "/oauth/") {
		path, _, _ = strings.Cut(path, "?")
	}
	return path
}

// logPath returns the request path for access log lines.
// OAuth callbacks have their query stripped to avoid logging one-time codes.
// Other paths have sensitive query values redacted via SanitizeURLForLog.
func logPath(param gin.LogFormatterParams) string {
	path := param.Path
	if strings.HasPrefix(path, "/api/oauth/") || strings.HasPrefix(path, "/oauth/") {
		return sanitizeLogPath(path)
	}
	if param.Request == nil || param.Request.URL == nil {
		return path
	}
	if param.Request.URL.RawQuery != "" {
		query := param.Request.URL.Query().Encode()
		return param.Request.URL.EscapedPath() + relaycommon.SanitizeURLForLog("?"+query)
	}
	// param.Path keeps the query the request arrived with, including a
	// credential a handler took out of it (e.g. Gemini ?key=), so log the
	// current URL path instead.
	return param.Request.URL.Path
}
