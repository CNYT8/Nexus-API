package controller

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
)

func TestApplySingleToMultiKeyConversionAppendsAndPreservesStrategy(t *testing.T) {
	convert := true
	mode := string(constant.MultiKeyModePolling)
	origin := &model.Channel{
		Type: constant.ChannelTypeOpenAI,
		Key:  "sk-existing",
		ChannelInfo: model.ChannelInfo{
			MultiKeyStatusList:     map[int]int{0: 2},
			MultiKeyDisabledReason: map[int]string{0: "stale"},
			MultiKeyDisabledTime:   map[int]int64{0: 100},
			MultiKeyPollingIndex:   1,
		},
	}
	channel := PatchChannel{
		Channel: model.Channel{
			Type:        constant.ChannelTypeOpenAI,
			Key:         "sk-new\nsk-third",
			ChannelInfo: origin.ChannelInfo,
		},
		MultiKeyMode:      &mode,
		ConvertToMultiKey: &convert,
	}

	converted, err := applySingleToMultiKeyConversion(&channel, origin)
	if err != nil {
		t.Fatal(err)
	}
	if !converted {
		t.Fatal("expected conversion to be applied")
	}
	keys := channel.GetKeys()
	if len(keys) != 3 {
		t.Fatalf("expected 3 keys, got %d: %q", len(keys), channel.Key)
	}
	if keys[0] != "sk-existing" || keys[1] != "sk-new" || keys[2] != "sk-third" {
		t.Fatalf("expected existing key followed by new keys, got %#v", keys)
	}
	if !channel.ChannelInfo.IsMultiKey {
		t.Fatal("expected channel to be in multi-key mode")
	}
	if channel.ChannelInfo.MultiKeySize != 3 {
		t.Fatalf("expected multi key size 3, got %d", channel.ChannelInfo.MultiKeySize)
	}
	if channel.ChannelInfo.MultiKeyMode != constant.MultiKeyModePolling {
		t.Fatalf("expected polling mode, got %q", channel.ChannelInfo.MultiKeyMode)
	}
	if channel.ChannelInfo.MultiKeyStatusList != nil ||
		channel.ChannelInfo.MultiKeyDisabledReason != nil ||
		channel.ChannelInfo.MultiKeyDisabledTime != nil ||
		channel.ChannelInfo.MultiKeyPollingIndex != 0 {
		t.Fatal("expected stale per-key state to be cleared during conversion")
	}
}

func TestApplySingleToMultiKeyConversionRequiresDistinctNewKey(t *testing.T) {
	convert := true
	origin := &model.Channel{Type: constant.ChannelTypeOpenAI, Key: "sk-existing"}
	channel := PatchChannel{
		Channel: model.Channel{
			Type:        constant.ChannelTypeOpenAI,
			Key:         "sk-existing",
			ChannelInfo: origin.ChannelInfo,
		},
		ConvertToMultiKey: &convert,
	}

	converted, err := applySingleToMultiKeyConversion(&channel, origin)
	if !converted {
		t.Fatal("expected conversion request to be recognized")
	}
	if err == nil || !strings.Contains(err.Error(), "至少需要两个不同的密钥") {
		t.Fatalf("expected distinct-key validation error, got %v", err)
	}
}

func TestApplySingleToMultiKeyConversionRejectsEmptyAndInvalidMode(t *testing.T) {
	convert := true
	origin := &model.Channel{Type: constant.ChannelTypeOpenAI, Key: "sk-existing"}

	empty := PatchChannel{
		Channel: model.Channel{
			Type:        constant.ChannelTypeOpenAI,
			ChannelInfo: origin.ChannelInfo,
		},
		ConvertToMultiKey: &convert,
	}
	if converted, err := applySingleToMultiKeyConversion(&empty, origin); !converted || err == nil {
		t.Fatalf("expected empty-key conversion to be rejected, converted=%v err=%v", converted, err)
	}

	invalidMode := "first"
	invalid := PatchChannel{
		Channel: model.Channel{
			Type:        constant.ChannelTypeOpenAI,
			Key:         "sk-new",
			ChannelInfo: origin.ChannelInfo,
		},
		MultiKeyMode:      &invalidMode,
		ConvertToMultiKey: &convert,
	}
	if converted, err := applySingleToMultiKeyConversion(&invalid, origin); !converted || err == nil {
		t.Fatalf("expected invalid strategy to be rejected, converted=%v err=%v", converted, err)
	}
}

func TestApplySingleToMultiKeyConversionCompactsVertexJSON(t *testing.T) {
	convert := true
	origin := &model.Channel{
		Type:          constant.ChannelTypeVertexAi,
		Key:           "{\n  \"project_id\": \"project-a\",\n  \"private_key\": \"key-a\"\n}",
		OtherSettings: `{"vertex_key_type":"json"}`,
	}
	channel := PatchChannel{
		Channel: model.Channel{
			Type:          constant.ChannelTypeVertexAi,
			Key:           "{\n  \"project_id\": \"project-b\",\n  \"private_key\": \"key-b\"\n}",
			OtherSettings: origin.OtherSettings,
			ChannelInfo:   origin.ChannelInfo,
		},
		ConvertToMultiKey: &convert,
	}

	converted, err := applySingleToMultiKeyConversion(&channel, origin)
	if err != nil {
		t.Fatal(err)
	}
	if !converted {
		t.Fatal("expected conversion to be applied")
	}
	keys := channel.GetKeys()
	if len(keys) != 2 {
		t.Fatalf("expected 2 compact Vertex keys, got %d: %q", len(keys), channel.Key)
	}
	for _, key := range keys {
		var object map[string]interface{}
		if err := json.Unmarshal([]byte(key), &object); err != nil {
			t.Fatalf("expected valid compact Vertex key %q: %v", key, err)
		}
	}
	if strings.Contains(channel.Key, "\n  ") {
		t.Fatalf("expected pretty JSON to be compacted before newline aggregation: %q", channel.Key)
	}
	if channel.ChannelInfo.MultiKeyMode != constant.MultiKeyModeRandom {
		t.Fatalf("expected default random mode, got %q", channel.ChannelInfo.MultiKeyMode)
	}
}

func TestApplySingleToMultiKeyConversionSupportsCodexAppend(t *testing.T) {
	convert := true
	mode := string(constant.MultiKeyModePolling)
	origin := &model.Channel{
		Type: constant.ChannelTypeCodex,
		Key:  `{"type":"codex","access_token":"token-a","account_id":"account-a"}`,
	}
	channel := PatchChannel{
		Channel: model.Channel{
			Type:        constant.ChannelTypeCodex,
			Key:         `{"type":"codex","access_token":"token-b","account_id":"account-b"}`,
			ChannelInfo: origin.ChannelInfo,
		},
		MultiKeyMode:      &mode,
		ConvertToMultiKey: &convert,
	}

	converted, err := applySingleToMultiKeyConversion(&channel, origin)
	if err != nil {
		t.Fatal(err)
	}
	if !converted || !channel.ChannelInfo.IsMultiKey {
		t.Fatal("expected Codex channel to become multi-account")
	}
	if channel.ChannelInfo.MultiKeySize != 2 {
		t.Fatalf("expected 2 Codex configs, got %d", channel.ChannelInfo.MultiKeySize)
	}
	if channel.ChannelInfo.MultiKeyMode != constant.MultiKeyModePolling {
		t.Fatalf("expected polling mode, got %q", channel.ChannelInfo.MultiKeyMode)
	}
	if channel.KeyMode == nil || *channel.KeyMode != "append" {
		t.Fatal("expected conversion to force append semantics")
	}
}

func TestApplySingleToMultiKeyConversionDoesNotReconvertExistingMultiKey(t *testing.T) {
	convert := true
	origin := &model.Channel{
		Type: constant.ChannelTypeOpenAI,
		Key:  "sk-a\nsk-b",
		ChannelInfo: model.ChannelInfo{
			IsMultiKey:   true,
			MultiKeySize: 2,
			MultiKeyMode: constant.MultiKeyModeRandom,
		},
	}
	channel := PatchChannel{
		Channel: model.Channel{
			Type:        constant.ChannelTypeOpenAI,
			Key:         "sk-c",
			ChannelInfo: origin.ChannelInfo,
		},
		ConvertToMultiKey: &convert,
	}

	converted, err := applySingleToMultiKeyConversion(&channel, origin)
	if err != nil {
		t.Fatal(err)
	}
	if converted {
		t.Fatal("expected existing multi-key channel to use the normal update path")
	}
}
