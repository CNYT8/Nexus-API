package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type codexRoundTripFunc func(*http.Request) (*http.Response, error)

func (roundTrip codexRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTrip(request)
}

func TestFetchCodexChannelModelsIntersectsEnabledAccounts(t *testing.T) {
	client := &http.Client{Transport: codexRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		assert.Equal(t, "/backend-api/codex/models", request.URL.Path)
		assert.Equal(t, "test-version", request.URL.Query().Get("client_version"))
		assert.Equal(t, "codex-cli/test-version", request.Header.Get("User-Agent"))

		accountID := request.Header.Get("ChatGPT-Account-Id")
		assert.Equal(t, "Bearer token-"+strings.TrimPrefix(accountID, "account-"), request.Header.Get("Authorization"))
		body := ""
		statusCode := http.StatusOK
		switch accountID {
		case "account-a":
			body = `{"models":[{"slug":"gpt-5"},{"slug":"codex-auto-review"},{"slug":"only-a"}]}`
		case "account-b":
			body = `{"models":[{"slug":"gpt-5"},{"slug":"codex-auto-review"},{"slug":"only-b"}]}`
		default:
			body = `{"error":"unknown account"}`
			statusCode = http.StatusBadRequest
		}
		return &http.Response{
			StatusCode: statusCode,
			Header:     http.Header{"Content-Type": {"application/json"}},
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    request,
		}, nil
	})}

	channel := &model.Channel{
		Type: constant.ChannelTypeCodex,
		Key: strings.Join([]string{
			`{"type":"codex","access_token":"token-a","account_id":"account-a"}`,
			`{"type":"codex","access_token":"token-b","account_id":"account-b"}`,
		}, "\n"),
		ChannelInfo: model.ChannelInfo{IsMultiKey: true},
	}

	models, err := fetchCodexChannelModels(context.Background(), channel, "https://chatgpt.test", client, "test-version", false)
	require.NoError(t, err)
	require.Equal(t, []string{"gpt-5", "codex-auto-review", "gpt-5-openai-compact"}, models)
}

func TestFetchCodexChannelModelsSkipsDisabledAccounts(t *testing.T) {
	client := &http.Client{Transport: codexRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		assert.Equal(t, "account-a", request.Header.Get("ChatGPT-Account-Id"))
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": {"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"models":[{"slug":"gpt-5"},{"slug":"only-a"}]}`)),
			Request:    request,
		}, nil
	})}

	channel := &model.Channel{
		Type: constant.ChannelTypeCodex,
		Key: strings.Join([]string{
			`{"type":"codex","access_token":"token-a","account_id":"account-a"}`,
			`{"type":"codex","access_token":"token-b","account_id":"account-b"}`,
		}, "\n"),
		ChannelInfo: model.ChannelInfo{
			IsMultiKey:         true,
			MultiKeyStatusList: map[int]int{1: 0},
		},
	}

	models, err := fetchCodexChannelModels(context.Background(), channel, "https://chatgpt.test", client, "test-version", false)
	require.NoError(t, err)
	require.Equal(t, []string{"gpt-5", "only-a", "gpt-5-openai-compact", "only-a-openai-compact"}, models)
}
