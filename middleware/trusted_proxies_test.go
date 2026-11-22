package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestResolveTrustedProxiesPolicy(t *testing.T) {
	defaults, usedDefaults, err := ResolveTrustedProxies(" \t")
	require.NoError(t, err)
	require.True(t, usedDefaults)
	require.Equal(t, []string{"127.0.0.0/8", "::1", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "fc00::/7"}, defaults)
	defaults[0] = "changed"
	again, _, err := ResolveTrustedProxies("")
	require.NoError(t, err)
	require.Equal(t, "127.0.0.0/8", again[0])

	for _, raw := range []string{"none", " NONE "} {
		proxies, usedDefaults, err := ResolveTrustedProxies(raw)
		require.NoError(t, err)
		require.False(t, usedDefaults)
		require.Nil(t, proxies)
	}
	proxies, usedDefaults, err := ResolveTrustedProxies(" 203.0.113.8, ,2001:db8::/32 ")
	require.NoError(t, err)
	require.False(t, usedDefaults)
	require.Equal(t, []string{"203.0.113.8", "2001:db8::/32"}, proxies)
	for _, raw := range []string{", ,", "none,10.0.0.1", "proxy.example.com", "10.0.0.1/99"} {
		_, _, err := ResolveTrustedProxies(raw)
		require.Error(t, err, raw)
	}
}

func TestConfiguredTrustedProxiesClientIP(t *testing.T) {
	cases := []struct {
		name, config, remote, forwarded, want string
	}{
		{"default public peer", "", "198.51.100.8:1234", "203.0.113.9", "198.51.100.8"},
		{"default private proxy", "", "10.0.0.8:1234", "203.0.113.9", "203.0.113.9"},
		{"none disables forwarding", "none", "10.0.0.8:1234", "203.0.113.9", "10.0.0.8"},
		{"explicit trusted peer", "198.51.100.8", "198.51.100.8:1234", "203.0.113.9", "203.0.113.9"},
		{"explicit replaces defaults", "198.51.100.8", "10.0.0.8:1234", "203.0.113.9", "10.0.0.8"},
		{"untrusted chain hop", "10.0.0.0/8", "10.0.0.8:1234", "192.0.2.1, 203.0.113.9", "203.0.113.9"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("TRUSTED_PROXIES", tc.config)
			router := gin.New()
			require.NoError(t, ConfigureTrustedProxies(router))
			router.GET("/", func(c *gin.Context) { c.String(http.StatusOK, c.ClientIP()) })
			request := httptest.NewRequest(http.MethodGet, "/", nil)
			request.RemoteAddr = tc.remote
			request.Header.Set("X-Forwarded-For", tc.forwarded)
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, request)
			require.Equal(t, tc.want, recorder.Body.String())
		})
	}
	t.Setenv("TRUSTED_PROXIES", "broken")
	require.Error(t, ConfigureTrustedProxies(gin.New()))
}
