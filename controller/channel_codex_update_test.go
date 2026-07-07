package controller

import (
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
)

func TestApplyCodexChannelKeyUpdateAppendSingleToMulti(t *testing.T) {
	origin := &model.Channel{
		Type: constant.ChannelTypeCodex,
		Key:  `{"type":"codex","access_token":"token-a","account_id":"account-a"}`,
	}
	mode := "append"
	channel := PatchChannel{
		Channel: model.Channel{
			Type: constant.ChannelTypeCodex,
			Key:  `{"type":"codex","access_token":"token-b","account_id":"account-b"}`,
		},
		KeyMode: &mode,
	}

	if err := applyCodexChannelKeyUpdate(&channel, origin); err != nil {
		t.Fatal(err)
	}

	keys := splitStoredChannelKeys(channel.Key)
	if len(keys) != 2 {
		t.Fatalf("expected 2 codex configs, got %d: %s", len(keys), channel.Key)
	}
	if !channel.ChannelInfo.IsMultiKey {
		t.Fatal("expected codex channel to become multi-account")
	}
	if channel.ChannelInfo.MultiKeySize != 2 {
		t.Fatalf("expected multi key size 2, got %d", channel.ChannelInfo.MultiKeySize)
	}
	if channel.ChannelInfo.MultiKeyMode != constant.MultiKeyModeRandom {
		t.Fatalf("expected default random mode, got %q", channel.ChannelInfo.MultiKeyMode)
	}
}

func TestApplyCodexChannelKeyUpdateAppendDeduplicatesAccount(t *testing.T) {
	origin := &model.Channel{
		Type: constant.ChannelTypeCodex,
		Key:  `{"type":"codex","access_token":"token-a","account_id":"account-a"}`,
	}
	mode := "append"
	channel := PatchChannel{
		Channel: model.Channel{
			Type: constant.ChannelTypeCodex,
			Key:  `{"type":"codex","access_token":"token-new","account_id":"account-a"}`,
		},
		KeyMode: &mode,
	}

	if err := applyCodexChannelKeyUpdate(&channel, origin); err != nil {
		t.Fatal(err)
	}

	keys := splitStoredChannelKeys(channel.Key)
	if len(keys) != 1 {
		t.Fatalf("expected duplicate account to be ignored, got %d: %s", len(keys), channel.Key)
	}
	if strings.Contains(channel.Key, "token-new") {
		t.Fatalf("expected existing config to win duplicate account merge, got %s", channel.Key)
	}
	if channel.ChannelInfo.IsMultiKey {
		t.Fatal("expected duplicate-only append to stay single-account")
	}
}

func TestApplyCodexChannelKeyUpdateAppendKeepsRequestedMultiKeyMode(t *testing.T) {
	origin := &model.Channel{
		Type: constant.ChannelTypeCodex,
		Key:  `{"type":"codex","access_token":"token-a","account_id":"account-a"}`,
	}
	mode := "append"
	multiKeyMode := string(constant.MultiKeyModePolling)
	channel := PatchChannel{
		Channel: model.Channel{
			Type: constant.ChannelTypeCodex,
			Key:  `{"type":"codex","access_token":"token-b","account_id":"account-b"}`,
			ChannelInfo: model.ChannelInfo{
				MultiKeyMode: constant.MultiKeyMode(multiKeyMode),
			},
		},
		KeyMode:      &mode,
		MultiKeyMode: &multiKeyMode,
	}

	if err := applyCodexChannelKeyUpdate(&channel, origin); err != nil {
		t.Fatal(err)
	}

	if !channel.ChannelInfo.IsMultiKey {
		t.Fatal("expected codex channel to become multi-account")
	}
	if channel.ChannelInfo.MultiKeyMode != constant.MultiKeyModePolling {
		t.Fatalf("expected polling mode, got %q", channel.ChannelInfo.MultiKeyMode)
	}
}

func TestApplyCodexChannelKeyUpdateReplaceClearsMultiState(t *testing.T) {
	origin := &model.Channel{
		Type: constant.ChannelTypeCodex,
		Key: strings.Join([]string{
			`{"type":"codex","access_token":"token-a","account_id":"account-a"}`,
			`{"type":"codex","access_token":"token-b","account_id":"account-b"}`,
		}, "\n"),
		ChannelInfo: model.ChannelInfo{
			IsMultiKey:             true,
			MultiKeySize:           2,
			MultiKeyMode:           constant.MultiKeyModePolling,
			MultiKeyStatusList:     map[int]int{1: 2},
			MultiKeyDisabledReason: map[int]string{1: "test"},
			MultiKeyDisabledTime:   map[int]int64{1: 100},
			MultiKeyPollingIndex:   1,
		},
	}
	mode := "replace"
	channel := PatchChannel{
		Channel: model.Channel{
			Type:        constant.ChannelTypeCodex,
			Key:         `{"type":"codex","access_token":"token-c","account_id":"account-c"}`,
			ChannelInfo: origin.ChannelInfo,
		},
		KeyMode: &mode,
	}

	if err := applyCodexChannelKeyUpdate(&channel, origin); err != nil {
		t.Fatal(err)
	}

	keys := splitStoredChannelKeys(channel.Key)
	if len(keys) != 1 {
		t.Fatalf("expected one replacement config, got %d: %s", len(keys), channel.Key)
	}
	if channel.ChannelInfo.IsMultiKey {
		t.Fatal("expected replace with one config to clear multi-account mode")
	}
	if channel.ChannelInfo.MultiKeySize != 1 {
		t.Fatalf("expected single-account multi key size 1, got %d", channel.ChannelInfo.MultiKeySize)
	}
	if channel.ChannelInfo.MultiKeyStatusList != nil ||
		channel.ChannelInfo.MultiKeyDisabledReason != nil ||
		channel.ChannelInfo.MultiKeyDisabledTime != nil {
		t.Fatal("expected stale multi-account status maps to be cleared")
	}
}
