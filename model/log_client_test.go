package model

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func newLogClientTestContext(headers map[string]string) *gin.Context {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req, _ := http.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	c.Request = req
	return c
}

func TestResolveLogClientPrefersExplicitClientHeader(t *testing.T) {
	c := newLogClientTestContext(map[string]string{
		"X-Client-Name": "Cherry Studio",
		"User-Agent":    "OpenAI/Python 1.0.0",
	})

	require.Equal(t, "Cherry Studio", ResolveLogClient(c))
}

func TestResolveLogClientFallsBackToStainlessHeaders(t *testing.T) {
	c := newLogClientTestContext(map[string]string{
		"X-Stainless-Lang":            "python",
		"X-Stainless-Package-Version": "1.2.3",
		"User-Agent":                  "OpenAI/Python 1.2.3",
	})

	require.Equal(t, "OpenAI SDK python 1.2.3", ResolveLogClient(c))
}

func TestResolveLogClientTruncatesLongUserAgent(t *testing.T) {
	c := newLogClientTestContext(map[string]string{
		"User-Agent": strings.Repeat("a", maxLogClientLength+10),
	})

	require.Len(t, []rune(ResolveLogClient(c)), maxLogClientLength)
}
