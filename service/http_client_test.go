package service

import (
	"context"
	"crypto/tls"
	"errors"
	"net/http"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/stretchr/testify/require"
)

func TestGetHttpClientWithProxyNormalizesAndCachesLegacyURL(t *testing.T) {
	ResetProxyClientCache()
	t.Cleanup(ResetProxyClientCache)

	first, err := GetHttpClientWithProxy("HTTP://proxy.example:8080/legacy?unused=true")
	if err != nil {
		t.Fatalf("GetHttpClientWithProxy: %v", err)
	}
	second, err := GetHttpClientWithProxy("http://proxy.example:8080")
	if err != nil {
		t.Fatalf("GetHttpClientWithProxy canonical URL: %v", err)
	}
	if first != second {
		t.Fatal("legacy and canonical proxy URLs did not share the cached client")
	}
}

func TestGetHttpClientWithProxyCreatesOneClientConcurrently(t *testing.T) {
	ResetProxyClientCache()
	t.Cleanup(ResetProxyClientCache)

	const workers = 32
	clients := make([]*http.Client, workers)
	errs := make([]error, workers)
	var group sync.WaitGroup
	group.Add(workers)
	for i := 0; i < workers; i++ {
		go func(index int) {
			defer group.Done()
			client, err := GetHttpClientWithProxy("socks5://proxy.example")
			clients[index] = client
			errs[index] = err
		}(i)
	}
	group.Wait()

	for index, err := range errs {
		if err != nil {
			t.Fatalf("worker %d: %v", index, err)
		}
	}
	first := clients[0]
	for index := 1; index < workers; index++ {
		if clients[index] != first {
			t.Fatalf("worker %d received a different cached client", index)
		}
	}
}

func TestChannelHTTPClientCacheIncludesTransportPolicy(t *testing.T) {
	ResetProxyClientCache()
	t.Cleanup(ResetProxyClientCache)
	proxyURL := "http://proxy.example:8080"

	legacy, err := GetHttpClientWithProxy(proxyURL)
	require.NoError(t, err)
	defaults, err := GetHttpClientWithProxySettings(proxyURL, dto.ChannelSettings{})
	require.NoError(t, err)
	require.Same(t, legacy, defaults)

	settings := dto.ChannelSettings{HTTP2ConnectionShards: 2}
	first, err := GetHttpClientWithProxySettings("HTTP://proxy.example:8080/legacy", settings)
	require.NoError(t, err)
	canonical, err := GetHttpClientWithProxySettings(proxyURL, settings)
	require.NoError(t, err)
	require.Same(t, first, canonical)
	require.NotSame(t, legacy, first)

	settings.TLSInsecureSkipVerify = true
	insecure, err := GetHttpClientWithProxySettings(proxyURL, settings)
	require.NoError(t, err)
	require.NotSame(t, first, insecure)
	settings.HTTPProtocol = dto.HTTPProtocolHTTP1
	http1, err := GetHttpClientWithProxySettings(proxyURL, settings)
	require.NoError(t, err)
	require.NotSame(t, insecure, http1)
	settings.HTTP2ConnectionShards = 8
	sameHTTP1, err := GetHttpClientWithProxySettings(proxyURL, settings)
	require.NoError(t, err)
	require.Same(t, http1, sameHTTP1, "HTTP/1 ignores HTTP/2 shard count")

	InvalidateProxyClient("HTTP://proxy.example:8080/legacy")
	replacement, err := GetHttpClientWithProxySettings(proxyURL, settings)
	require.NoError(t, err)
	require.NotSame(t, http1, replacement)
	otherReplacement, err := GetHttpClientWithProxySettings(proxyURL, dto.ChannelSettings{HTTP2ConnectionShards: 2})
	require.NoError(t, err)
	require.NotSame(t, first, otherReplacement, "all policies for the proxy must be invalidated")
}

func TestChannelHTTPTransportClonesTLSAndPreservesProxyCancellation(t *testing.T) {
	originalDefault := http.DefaultTransport
	originalInsecure := common.TLSInsecureSkipVerify
	originalTLSConfig := common.InsecureTLSConfig
	t.Cleanup(func() {
		http.DefaultTransport = originalDefault
		common.TLSInsecureSkipVerify = originalInsecure
		common.InsecureTLSConfig = originalTLSConfig
	})
	baseline := &http.Transport{TLSClientConfig: &tls.Config{ServerName: "global.example", NextProtos: []string{"h2"}}}
	http.DefaultTransport = baseline
	common.TLSInsecureSkipVerify = false

	proxyURL, _, err := parseChannelProxyURL("socks5://proxy.example:1080")
	require.NoError(t, err)
	client, err := newChannelHTTPClient(NormalizeHTTPTransportPolicy(dto.ChannelSettings{
		HTTPProtocol: dto.HTTPProtocolHTTP1, HTTP2ConnectionShards: 8, TLSInsecureSkipVerify: true,
	}), proxyURL)
	require.NoError(t, err)
	transport, ok := client.Transport.(*http.Transport)
	require.True(t, ok, "HTTP/1 must have one transport")
	require.NotSame(t, baseline, transport)
	require.NotSame(t, baseline.TLSClientConfig, transport.TLSClientConfig)
	require.True(t, transport.TLSClientConfig.InsecureSkipVerify)
	require.False(t, baseline.TLSClientConfig.InsecureSkipVerify)
	require.Equal(t, []string{"h2"}, baseline.TLSClientConfig.NextProtos)
	require.Empty(t, transport.TLSClientConfig.NextProtos)
	require.True(t, transport.Protocols.HTTP1())
	require.False(t, transport.Protocols.HTTP2())
	require.False(t, transport.ForceAttemptHTTP2)
	require.Nil(t, transport.Proxy)
	require.NotNil(t, transport.DialContext)
	require.NotNil(t, client.CheckRedirect)

	// The SOCKS dialer must honor cancellation before contacting the proxy.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	conn, err := transport.DialContext(ctx, "tcp", "upstream.example:443")
	if conn != nil {
		_ = conn.Close()
	}
	require.ErrorIs(t, err, context.Canceled)

	common.TLSInsecureSkipVerify = true
	common.InsecureTLSConfig = &tls.Config{InsecureSkipVerify: true, NextProtos: []string{"h2"}}
	inherited, err := newChannelHTTPClient(NormalizeHTTPTransportPolicy(dto.ChannelSettings{HTTPProtocol: dto.HTTPProtocolHTTP1}), nil)
	require.NoError(t, err)
	inheritedTransport := inherited.Transport.(*http.Transport)
	require.True(t, inheritedTransport.TLSClientConfig.InsecureSkipVerify)
	require.NotSame(t, common.InsecureTLSConfig, inheritedTransport.TLSClientConfig)
	require.Equal(t, []string{"h2"}, common.InsecureTLSConfig.NextProtos)
}

type channelHTTPRecordingTransport struct {
	requests []*http.Request
	closed   bool
}

func (r *channelHTTPRecordingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r.requests = append(r.requests, req)
	return nil, req.Context().Err()
}

func (r *channelHTTPRecordingTransport) CloseIdleConnections() { r.closed = true }

func TestChannelHTTPShardsKeepOriginAndRequestContext(t *testing.T) {
	first, second := &channelHTTPRecordingTransport{}, &channelHTTPRecordingTransport{}
	transport := &channelShardedTransport{shards: []http.RoundTripper{first, second}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var requests []*http.Request
	for _, target := range []string{"https://a.example/1", "https://a.example/2", "https://b.example/1", "https://a.example/3"} {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
		require.NoError(t, err)
		requests = append(requests, req)
		_, err = transport.RoundTrip(req)
		require.True(t, errors.Is(err, context.Canceled))
	}
	require.Len(t, first.requests, 3)
	require.Len(t, second.requests, 1)
	require.Same(t, requests[0], first.requests[0])
	require.Same(t, requests[1], second.requests[0])
	require.Same(t, requests[2], first.requests[1], "new origin starts at its first shard")
	require.Same(t, requests[3], first.requests[2])
	transport.CloseIdleConnections()
	require.True(t, first.closed)
	require.True(t, second.closed)

	client, err := newChannelHTTPClient(NormalizeHTTPTransportPolicy(dto.ChannelSettings{HTTP2ConnectionShards: 2}), nil)
	require.NoError(t, err)
	sharded, ok := client.Transport.(*channelShardedTransport)
	require.True(t, ok)
	require.Len(t, sharded.shards, 2)
	left := sharded.shards[0].(*http.Transport)
	right := sharded.shards[1].(*http.Transport)
	require.NotSame(t, left, right)
	require.NotSame(t, left.TLSClientConfig, right.TLSClientConfig)
	require.True(t, left.Protocols.HTTP2())
}

func TestInvalidateProxyClientRemovesCanonicalClient(t *testing.T) {
	ResetProxyClientCache()
	t.Cleanup(ResetProxyClientCache)

	first, err := GetHttpClientWithProxy("http://proxy.example:8080")
	if err != nil {
		t.Fatalf("GetHttpClientWithProxy: %v", err)
	}
	InvalidateProxyClient("HTTP://proxy.example:8080/legacy")
	second, err := GetHttpClientWithProxy("http://proxy.example:8080")
	if err != nil {
		t.Fatalf("GetHttpClientWithProxy after invalidation: %v", err)
	}
	if first == second {
		t.Fatal("proxy client was not invalidated")
	}
}
