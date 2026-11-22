package service

import (
	"crypto/tls"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
)

// HTTPTransportPolicy is immutable and forms part of the channel client cache key.
// Zero-valued channel settings retain the existing global transport defaults.
type HTTPTransportPolicy struct {
	Protocol              string
	Shards                int
	TLSInsecureSkipVerify bool
}

func NormalizeHTTPTransportPolicy(settings dto.ChannelSettings) HTTPTransportPolicy {
	policy := HTTPTransportPolicy{
		Protocol:              dto.HTTPProtocolAuto,
		Shards:                1,
		TLSInsecureSkipVerify: settings.TLSInsecureSkipVerify,
	}
	if strings.EqualFold(strings.TrimSpace(settings.HTTPProtocol), dto.HTTPProtocolHTTP1) {
		policy.Protocol = dto.HTTPProtocolHTTP1
	} else {
		policy.Shards = max(1, min(settings.HTTP2ConnectionShards, dto.MaxHTTP2ConnectionShards))
	}
	return policy
}

type channelHTTPClientKey struct {
	Proxy  string
	Policy HTTPTransportPolicy
}

var channelHTTPClients = struct {
	sync.Mutex
	clients map[channelHTTPClientKey]*http.Client
}{clients: make(map[channelHTTPClientKey]*http.Client)}

// GetHttpClientWithProxySettings shares pools only when proxy and transport policy
// match. Protected URL-fetch clients remain separate from channel relay clients.
func GetHttpClientWithProxySettings(rawProxyURL string, settings dto.ChannelSettings) (*http.Client, error) {
	policy := NormalizeHTTPTransportPolicy(settings)
	if policy == NormalizeHTTPTransportPolicy(dto.ChannelSettings{}) {
		return GetHttpClientWithProxy(rawProxyURL)
	}
	parsedURL, legacySuffixStripped, err := parseChannelProxyURL(rawProxyURL)
	if err != nil {
		return nil, err
	}
	key := channelHTTPClientKey{Policy: policy}
	if parsedURL != nil {
		config := newProxyURLConfig(parsedURL)
		key.Proxy = config.cacheKey
		if legacySuffixStripped {
			warnLegacyProxyURLOnce(config)
		}
	}
	channelHTTPClients.Lock()
	defer channelHTTPClients.Unlock()
	if client := channelHTTPClients.clients[key]; client != nil {
		return client, nil
	}
	client, err := newChannelHTTPClient(policy, parsedURL)
	if err != nil {
		return nil, err
	}
	channelHTTPClients.clients[key] = client
	return client, nil
}

func parseChannelProxyURL(rawProxyURL string) (*url.URL, bool, error) {
	if strings.TrimSpace(rawProxyURL) == "" {
		return nil, false, nil
	}
	return common.ParseProxyURLRuntime(strings.TrimSpace(rawProxyURL))
}

func newChannelHTTPClient(policy HTTPTransportPolicy, proxyURL *url.URL) (*http.Client, error) {
	shards := make([]http.RoundTripper, 0, policy.Shards)
	for range policy.Shards {
		transport := newRelayHTTPTransport()
		if proxyURL != nil {
			if err := configureProxyTransport(transport, proxyURL); err != nil {
				return nil, err
			}
		}
		// Clone even the global insecure TLS config before applying channel policy.
		if transport.TLSClientConfig != nil {
			transport.TLSClientConfig = transport.TLSClientConfig.Clone()
		} else {
			transport.TLSClientConfig = &tls.Config{}
		}
		if policy.TLSInsecureSkipVerify {
			transport.TLSClientConfig.InsecureSkipVerify = true
		}
		transport.Protocols = new(http.Protocols)
		transport.Protocols.SetHTTP1(true)
		transport.Protocols.SetHTTP2(policy.Protocol != dto.HTTPProtocolHTTP1)
		transport.ForceAttemptHTTP2 = policy.Protocol != dto.HTTPProtocolHTTP1
		if policy.Protocol == dto.HTTPProtocolHTTP1 {
			transport.TLSNextProto = make(map[string]func(string, *tls.Conn) http.RoundTripper)
			transport.TLSClientConfig.NextProtos = nil
		}
		transport.MaxIdleConns = max(1, transport.MaxIdleConns/policy.Shards)
		transport.MaxIdleConnsPerHost = max(1, transport.MaxIdleConnsPerHost/policy.Shards)
		shards = append(shards, transport)
	}
	if len(shards) == 1 {
		return newRelayHTTPClient(shards[0]), nil
	}
	return newRelayHTTPClient(&channelShardedTransport{shards: shards}), nil
}

// Each origin rotates across independent pools, retaining the request's context,
// body and cancellation. A shard delegates HTTP/1 fallback to net/http as usual.
type channelShardedTransport struct {
	shards   []http.RoundTripper
	counters sync.Map // origin -> *atomic.Uint64
}

func (s *channelShardedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	origin := strings.ToLower(req.URL.Scheme) + "://" + strings.ToLower(req.URL.Host)
	counter, _ := s.counters.LoadOrStore(origin, &atomic.Uint64{})
	index := (counter.(*atomic.Uint64).Add(1) - 1) % uint64(len(s.shards))
	return s.shards[index].RoundTrip(req)
}

func (s *channelShardedTransport) CloseIdleConnections() {
	for _, shard := range s.shards {
		if closer, ok := shard.(interface{ CloseIdleConnections() }); ok {
			closer.CloseIdleConnections()
		}
	}
}

// A nil proxy clears direct and proxy policy variants; otherwise clear all
// policies for that canonical proxy, matching the legacy cache lifecycle.
func invalidateChannelHTTPClients(proxy *string) {
	channelHTTPClients.Lock()
	var removed []*http.Client
	for key, client := range channelHTTPClients.clients {
		if proxy == nil || key.Proxy == *proxy {
			removed = append(removed, client)
			delete(channelHTTPClients.clients, key)
		}
	}
	channelHTTPClients.Unlock()
	for _, client := range removed {
		client.CloseIdleConnections()
	}
}
