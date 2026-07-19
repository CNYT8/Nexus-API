package service

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
)

func FetchCodexChannelModels(channel *model.Channel) ([]string, error) {
	if channel == nil || channel.Type != constant.ChannelTypeCodex {
		return nil, fmt.Errorf("channel type is not Codex")
	}

	client, err := NewProxyHttpClient(channel.GetSetting().Proxy)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	clientVersion, err := GetLatestCodexClientVersion(ctx, client)
	if err != nil {
		return nil, fmt.Errorf("failed to get Codex client version: %w", err)
	}

	baseURL := strings.TrimRight(strings.TrimSpace(channel.GetBaseURL()), "/")
	if baseURL == "" {
		baseURL = strings.TrimRight(constant.ChannelBaseURLs[constant.ChannelTypeCodex], "/")
	}
	return fetchCodexChannelModels(ctx, channel, baseURL, client, clientVersion, false)
}

func fetchCodexChannelModels(
	ctx context.Context,
	channel *model.Channel,
	baseURL string,
	client *http.Client,
	clientVersion string,
	retried bool,
) ([]string, error) {
	oauthKeys, err := parseCodexOAuthKeys(strings.TrimSpace(channel.Key))
	if err != nil {
		return nil, err
	}

	modelLists := make([][]string, 0, len(oauthKeys))
	for index := range oauthKeys {
		if !isCodexKeyEnabled(channel, index) {
			continue
		}

		statusCode, models, fetchErr := FetchCodexModels(ctx, client, baseURL, &oauthKeys[index], clientVersion)
		if fetchErr != nil {
			return nil, fetchErr
		}
		if statusCode == http.StatusUnauthorized {
			if retried {
				return nil, fmt.Errorf("codex channel credential expired after refresh")
			}
			if channel.Id <= 0 {
				return nil, fmt.Errorf("codex channel credential expired; save the channel before retrying model fetch")
			}
			_, refreshedChannel, refreshErr := RefreshCodexChannelCredential(
				ctx,
				channel.Id,
				CodexCredentialRefreshOptions{ResetCaches: true},
			)
			if refreshErr != nil {
				return nil, fmt.Errorf("failed to refresh Codex channel credential: %w", refreshErr)
			}
			if refreshedChannel == nil {
				return nil, fmt.Errorf("failed to refresh Codex channel credential: channel not found")
			}
			refreshedBaseURL := strings.TrimRight(strings.TrimSpace(refreshedChannel.GetBaseURL()), "/")
			if refreshedBaseURL == "" {
				refreshedBaseURL = baseURL
			}
			return fetchCodexChannelModels(ctx, refreshedChannel, refreshedBaseURL, client, clientVersion, true)
		}
		if statusCode < http.StatusOK || statusCode >= http.StatusMultipleChoices {
			return nil, fmt.Errorf("codex upstream status for account %d: %d", index+1, statusCode)
		}
		if len(models) == 0 {
			return nil, fmt.Errorf("codex upstream returned no models for account %d", index+1)
		}
		modelLists = append(modelLists, models)
	}

	if len(modelLists) == 0 {
		return nil, fmt.Errorf("codex channel has no enabled accounts")
	}
	models := intersectCodexModelLists(modelLists)
	if len(models) == 0 {
		return nil, fmt.Errorf("codex accounts have no common models")
	}
	variants := ratio_setting.WithCompactModelVariants(models)
	unsupportedCompactModel := ratio_setting.WithCompactModelSuffix("codex-auto-review")
	filtered := variants[:0]
	for _, modelName := range variants {
		if modelName != unsupportedCompactModel {
			filtered = append(filtered, modelName)
		}
	}
	return filtered, nil
}

func isCodexKeyEnabled(channel *model.Channel, index int) bool {
	statusList := channel.ChannelInfo.MultiKeyStatusList
	if statusList == nil {
		return true
	}
	status, ok := statusList[index]
	return !ok || status == common.ChannelStatusEnabled
}

func intersectCodexModelLists(modelLists [][]string) []string {
	if len(modelLists) == 0 {
		return nil
	}
	commonModels := make(map[string]struct{}, len(modelLists[0]))
	for _, modelName := range modelLists[0] {
		commonModels[modelName] = struct{}{}
	}
	for _, modelList := range modelLists[1:] {
		available := make(map[string]struct{}, len(modelList))
		for _, modelName := range modelList {
			available[modelName] = struct{}{}
		}
		for modelName := range commonModels {
			if _, ok := available[modelName]; !ok {
				delete(commonModels, modelName)
			}
		}
	}

	result := make([]string, 0, len(commonModels))
	for _, modelName := range modelLists[0] {
		if _, ok := commonModels[modelName]; ok {
			result = append(result, modelName)
		}
	}
	return result
}
