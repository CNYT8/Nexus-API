package relay

import (
	"bytes"
	"errors"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/relay/channel"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
)

// searchTransportAdaptor is deliberately narrow. No conversion or response
// method is added to channel.Adaptor or imposed on unrelated providers.
type searchTransportAdaptor interface {
	GetRequestURL(*relaycommon.RelayInfo) (string, error)
	SetupRequestHeader(*gin.Context, *http.Header, *relaycommon.RelayInfo) error
}

func searchChannelSupported(channelType int, path string) bool {
	if !dto.IsSearchPath(path) {
		return false
	}
	switch channelType {
	case constant.ChannelTypeOpenAI, constant.ChannelTypeCustom, constant.ChannelTypeAdvancedCustom:
		return true // OpenAI-compatible proxy channels, not native OpenAI search.
	case constant.ChannelTypeCodex:
		return path == dto.AlphaSearchPath
	case constant.ChannelTypeDeepSeek:
		return path == dto.DeepSeekSearchPath || path == dto.DeepSeekFetchPath
	default:
		return false
	}
}

func searchRequestURL(a searchTransportAdaptor, info *relaycommon.RelayInfo, path string) (string, error) {
	if !searchChannelSupported(info.ChannelType, path) {
		return "", errors.New("channel does not support search endpoint")
	}
	switch info.ChannelType {
	case constant.ChannelTypeAdvancedCustom:
		config := info.ChannelOtherSettings.AdvancedCustom
		if config == nil {
			return "", errors.New("advanced custom search route is required")
		}
		if err := config.Validate(); err != nil {
			return "", err
		}
		route, ok := config.MatchPathForModel(path, info.OriginModelName)
		converter := strings.TrimSpace(route.Converter)
		if !ok || converter != "" && converter != dto.AdvancedCustomConverterNone {
			return "", errors.New("search requires an explicitly configured native advanced custom route")
		}
		return a.GetRequestURL(info)
	case constant.ChannelTypeOpenAI, constant.ChannelTypeCustom:
		// Preserve custom full URLs, {model}, gateway rules, and proxy queries.
		return a.GetRequestURL(info)
	case constant.ChannelTypeCodex:
		path = "/backend-api/codex/alpha/search"
	}
	return relaycommon.GetFullRequestURL(strings.TrimRight(info.ChannelBaseUrl, "/"), path, info.ChannelType), nil
}

func buildSearchHTTPRequest(c *gin.Context, a searchTransportAdaptor, info *relaycommon.RelayInfo, path string, body []byte) (*http.Request, error) {
	requestURL, err := searchRequestURL(a, info, path)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, requestURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	if err := a.SetupRequestHeader(c, &req.Header, info); err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	headers, err := channel.ResolveHeaderOverride(info, c)
	if err != nil {
		return nil, err
	}
	for key, value := range headers {
		req.Header.Set(key, value)
		if strings.EqualFold(key, "Host") {
			req.Host = value
		}
	}
	return req, nil
}

func doSearchRequest(c *gin.Context, a searchTransportAdaptor, info *relaycommon.RelayInfo, path string, body []byte) (*http.Response, error) {
	req, err := buildSearchHTTPRequest(c, a, info, path, body)
	if err != nil {
		return nil, err
	}
	// Shared transport preserves proxy/TLS settings, redirect rejection, upstream
	// request IDs and masked errors; never create an ungoverned HTTP client here.
	return channel.DoRequest(c, req, info)
}
