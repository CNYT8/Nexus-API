package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/common/codexkey"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
)

type CodexCredentialRefreshOptions struct {
	ResetCaches         bool
	RefreshOnlyExpiring bool
}

type CodexOAuthKey = codexkey.OAuthKey

func parseCodexOAuthKey(raw string) (*CodexOAuthKey, error) {
	return codexkey.Parse(raw)
}

func parseCodexOAuthKeys(raw string) ([]CodexOAuthKey, error) {
	return codexkey.ParseMany(raw)
}

func RefreshCodexChannelCredential(ctx context.Context, channelID int, opts CodexCredentialRefreshOptions) (*CodexOAuthKey, *model.Channel, error) {
	ch, err := model.GetChannelById(channelID, true)
	if err != nil {
		return nil, nil, err
	}
	if ch == nil {
		return nil, nil, fmt.Errorf("channel not found")
	}
	if ch.Type != constant.ChannelTypeCodex {
		return nil, nil, fmt.Errorf("channel type is not Codex")
	}

	oauthKeys, err := parseCodexOAuthKeys(strings.TrimSpace(ch.Key))
	if err != nil {
		return nil, nil, err
	}

	now := time.Now()
	var firstRefreshed *CodexOAuthKey
	var firstRefreshErr error
	var refreshableCount int
	for i := range oauthKeys {
		oauthKey := &oauthKeys[i]
		if strings.TrimSpace(oauthKey.RefreshToken) == "" {
			continue
		}
		refreshableCount++
		if opts.RefreshOnlyExpiring && !codexOAuthKeyNeedsRefresh(oauthKey, now, codexCredentialRefreshThreshold) {
			continue
		}

		refreshCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		res, err := RefreshCodexOAuthTokenWithProxy(refreshCtx, oauthKey.RefreshToken, ch.GetSetting().Proxy)
		cancel()
		if err != nil {
			if firstRefreshErr == nil {
				firstRefreshErr = err
			}
			continue
		}

		oauthKey.AccessToken = res.AccessToken
		oauthKey.RefreshToken = res.RefreshToken
		oauthKey.LastRefresh = now.Format(time.RFC3339)
		oauthKey.Expired = res.ExpiresAt.Format(time.RFC3339)
		if strings.TrimSpace(oauthKey.Type) == "" {
			oauthKey.Type = "codex"
		}

		if strings.TrimSpace(oauthKey.AccountID) == "" {
			if accountID, ok := ExtractCodexAccountIDFromJWT(oauthKey.AccessToken); ok {
				oauthKey.AccountID = accountID
			}
		}
		if strings.TrimSpace(oauthKey.Email) == "" {
			if email, ok := ExtractEmailFromJWT(oauthKey.AccessToken); ok {
				oauthKey.Email = email
			}
		}
		if firstRefreshed == nil {
			keyCopy := *oauthKey
			firstRefreshed = &keyCopy
		}
	}

	if refreshableCount == 0 {
		return nil, nil, fmt.Errorf("codex channel: refresh_token is required to refresh credential")
	}
	if firstRefreshed == nil {
		if firstRefreshErr != nil {
			return nil, nil, firstRefreshErr
		}
		return nil, nil, fmt.Errorf("codex channel: no credential needs refresh")
	}

	encodedLines, err := encodeCodexOAuthKeys(oauthKeys)
	if err != nil {
		return nil, nil, err
	}

	if len(oauthKeys) > 1 {
		ch.ChannelInfo.IsMultiKey = true
		ch.ChannelInfo.MultiKeySize = len(oauthKeys)
		if ch.ChannelInfo.MultiKeyMode == "" {
			ch.ChannelInfo.MultiKeyMode = constant.MultiKeyModeRandom
		}
	}
	updates := map[string]interface{}{
		"key": strings.Join(encodedLines, "\n"),
	}
	if ch.ChannelInfo.IsMultiKey {
		updates["channel_info"] = ch.ChannelInfo
	}
	if err := model.DB.Model(&model.Channel{}).Where("id = ?", ch.Id).Updates(updates).Error; err != nil {
		return nil, nil, err
	}

	if opts.ResetCaches {
		model.InitChannelCache()
		ResetProxyClientCache()
	}

	return firstRefreshed, ch, nil
}

func codexOAuthKeyNeedsRefresh(oauthKey *CodexOAuthKey, now time.Time, threshold time.Duration) bool {
	if oauthKey == nil {
		return false
	}
	expiredAtRaw := strings.TrimSpace(oauthKey.Expired)
	expiredAt, err := time.Parse(time.RFC3339, expiredAtRaw)
	if err != nil || expiredAt.IsZero() {
		return true
	}
	return expiredAt.Sub(now) <= threshold
}

func encodeCodexOAuthKeys(oauthKeys []CodexOAuthKey) ([]string, error) {
	lines := make([]string, 0, len(oauthKeys))
	for _, oauthKey := range oauthKeys {
		if strings.TrimSpace(oauthKey.Type) == "" {
			oauthKey.Type = "codex"
		}
		encoded, err := common.Marshal(oauthKey)
		if err != nil {
			return nil, err
		}
		lines = append(lines, string(encoded))
	}
	return lines, nil
}
