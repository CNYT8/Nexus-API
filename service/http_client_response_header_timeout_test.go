package service

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"

	"github.com/stretchr/testify/require"
)

func TestRelayTransportResponseHeaderTimeout(t *testing.T) {
	original := common.RelayResponseHeaderTimeout
	originalDefault := http.DefaultTransport
	clonedDefault := http.DefaultTransport.(*http.Transport).Clone()
	clonedDefault.ResponseHeaderTimeout = time.Minute
	http.DefaultTransport = clonedDefault
	t.Cleanup(func() { common.RelayResponseHeaderTimeout = original; http.DefaultTransport = originalDefault })

	tests := []struct {
		name    string
		seconds int
		want    time.Duration
	}{
		{name: "default", seconds: 1800, want: 1800 * time.Second},
		{name: "disabled", seconds: 0, want: 0},
		{name: "negative is disabled", seconds: -1800, want: 0},
		{name: "overflow is clamped", seconds: maxTimeoutSeconds + 1, want: time.Duration(maxTimeoutSeconds) * time.Second},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			common.RelayResponseHeaderTimeout = tc.seconds
			transport := newRelayHTTPTransport()
			require.Equal(t, tc.want, transport.ResponseHeaderTimeout)
		})
	}
}

func TestRelayHeaderTimeoutDoesNotLimitResponseBody(t *testing.T) {
	for _, headersFirst := range []bool{false, true} {
		t.Run(fmt.Sprintf("headers first=%v", headersFirst), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if headersFirst {
					w.WriteHeader(http.StatusOK)
					w.(http.Flusher).Flush()
				}
				select {
				case <-time.After(150 * time.Millisecond):
					_, _ = w.Write([]byte("done"))
				case <-r.Context().Done():
				}
			}))
			defer server.Close()
			transport := newRelayHTTPTransport()
			transport.ResponseHeaderTimeout = 40 * time.Millisecond
			defer transport.CloseIdleConnections()
			client := newRelayHTTPClient(transport)
			resp, err := client.Get(server.URL)
			if !headersFirst {
				require.ErrorContains(t, err, "timeout awaiting response headers")
				return
			}
			require.NoError(t, err)
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			require.Equal(t, "done", string(body))
			require.NotNil(t, client.CheckRedirect)
		})
	}
}

func TestProxyClientInheritsResponseHeaderTimeout(t *testing.T) {
	original := common.RelayResponseHeaderTimeout
	t.Cleanup(func() { common.RelayResponseHeaderTimeout = original })
	common.RelayResponseHeaderTimeout = 42

	proxyURL, err := url.Parse("http://proxy.example:8080")
	require.NoError(t, err)
	client, err := newProxyHTTPClient(proxyURL)
	require.NoError(t, err)
	transport, ok := client.Transport.(*http.Transport)
	require.True(t, ok)
	require.Equal(t, 42*time.Second, transport.ResponseHeaderTimeout)
}
