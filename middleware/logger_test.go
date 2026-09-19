package middleware

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSanitizeLogPath(t *testing.T) {
	tests := []struct {
		name string
		path string
		want string
	}{
		{name: "api oauth callback drops query", path: "/api/oauth/github?state=abc&code=def", want: "/api/oauth/github"},
		{name: "oauth callback drops query", path: "/oauth/discord?code=xyz", want: "/oauth/discord"},
		{name: "api oauth path without query is unchanged", path: "/api/oauth/oidc", want: "/api/oauth/oidc"},
		{name: "oauth path without query is unchanged", path: "/oauth/github", want: "/oauth/github"},
		{name: "non oauth path keeps its query", path: "/api/user/self?token=xyz", want: "/api/user/self?token=xyz"},
		{name: "oauth without trailing slash is unchanged", path: "/api/oauth?state=abc", want: "/api/oauth?state=abc"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, sanitizeLogPath(test.path))
		})
	}
}

// captureGinLog runs one request through a router that uses SetUpLogger and
// returns the formatted log line.
func captureGinLog(t *testing.T, method string, target string) string {
	t.Helper()
	gin.SetMode(gin.TestMode)
	oldWriter := gin.DefaultWriter
	var output bytes.Buffer
	gin.DefaultWriter = &output
	t.Cleanup(func() { gin.DefaultWriter = oldWriter })

	router := gin.New()
	SetUpLogger(router)
	router.Any("/*path", func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})
	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(method, target, nil))
	require.NotEmpty(t, output.String())
	return output.String()
}

func TestSetUpLoggerRedactsOAuthCallbackQuery(t *testing.T) {
	logged := captureGinLog(t, http.MethodGet, "/api/oauth/github?state=secret-state&code=secret-code")

	assert.Contains(t, logged, "/api/oauth/github")
	assert.NotContains(t, logged, "secret-state")
	assert.NotContains(t, logged, "secret-code")
}

func TestSetUpLoggerRedactsRootOAuthCallbackQuery(t *testing.T) {
	logged := captureGinLog(t, http.MethodGet, "/oauth/discord?code=secret-code")

	assert.Contains(t, logged, "/oauth/discord")
	assert.NotContains(t, logged, "secret-code")
}

func TestSetUpLoggerKeepsNonOAuthQuery(t *testing.T) {
	logged := captureGinLog(t, http.MethodGet, "/api/user/self?foo=bar")

	assert.Contains(t, logged, "/api/user/self?foo=bar")
}
