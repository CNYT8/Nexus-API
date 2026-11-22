package relay

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting"
	"github.com/tidwall/gjson"
)

func TestSearchResponseBillability(t *testing.T) {
	for _, tc := range []struct {
		name, path, body string
		billable, bad    bool
	}{
		{name: "alpha call", path: dto.AlphaSearchPath, body: `{"unknown":true}`, billable: true},
		{name: "search success", path: dto.DeepSeekSearchPath, body: `{"results":[{"snippet":"ok"}]}`, billable: true},
		{name: "empty search", path: dto.DeepSeekSearchPath, body: `{"results":[]}`},
		{name: "fetch success", path: dto.DeepSeekFetchPath, body: `{"content":"ok"}`, billable: true},
		{name: "empty fetch", path: dto.DeepSeekFetchPath, body: `{"content":""}`},
		{name: "whitespace content remains billable", path: dto.DeepSeekFetchPath, body: `{"content":" "}`, billable: true},
		{name: "null error", path: dto.DeepSeekFetchPath, body: `{"content":"ok","error":null}`, billable: true},
		{name: "error inside success", path: dto.AlphaSearchPath, body: `{"error":{"message":"secret"}}`, bad: true},
		{name: "missing results", path: dto.DeepSeekSearchPath, body: `{}`},
		{name: "null results", path: dto.DeepSeekSearchPath, body: `{"results":null}`},
		{name: "results object", path: dto.DeepSeekSearchPath, body: `{"results":{}}`, bad: true},
		{name: "numeric content", path: dto.DeepSeekFetchPath, body: `{"content":7}`, bad: true},
		{name: "missing content", path: dto.DeepSeekFetchPath, body: `{}`},
		{name: "null content", path: dto.DeepSeekFetchPath, body: `{"content": null }`},
		{name: "empty tools error envelope", path: dto.DeepSeekSearchPath, body: `{"results":null,"error":"denied"}`, bad: true},
		{name: "duplicate null results", path: dto.DeepSeekSearchPath, body: `{"results":null,"results":[]}`, bad: true},
		{name: "duplicate content", path: dto.DeepSeekFetchPath, body: `{"content":"","content":"paid"}`, bad: true},
		{name: "html", path: dto.AlphaSearchPath, body: `<html>error</html>`, bad: true},
		{name: "array", path: dto.AlphaSearchPath, body: `[]`, bad: true},
		{name: "trailing", path: dto.AlphaSearchPath, body: `{} {}`, bad: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			billable, err := searchResultBillable(tc.path, []byte(tc.body))
			if (err != nil) != tc.bad || billable != tc.billable {
				t.Fatalf("billable=%v, error=%v", billable, err)
			}
		})
	}
}

func TestSearchResponsePolicy(t *testing.T) {
	info := &relaycommon.RelayInfo{OutputSensitiveConfig: setting.OutputSensitiveConfig{
		Enabled: true, Action: "truncate", MatchPercent: 100, Patterns: []string{"秘密"},
	}}
	unchanged := []byte(` { "results" : [{"snippet":"safe\u0020text","url":"https://example.invalid"}], "large":900719925474099312345, "ratio":1.234500e-70 } `)
	cleaned, matched, err := sanitizeSearchResponse(unchanged, info)
	if err != nil || matched || !bytes.Equal(cleaned, unchanged) {
		t.Fatalf("unchanged wire response lost: %s, matched=%v, error=%v", cleaned, matched, err)
	}
	input := []byte(` { "results" : [{"snippet":"前缀秘"},{"future_text":"密 should disappear"}], "large":900719925474099312345, "ratio":1.234500e-70, "flag":true } `)
	cleaned, matched, err = sanitizeSearchResponse(input, info)
	if err != nil || !matched || !json.Valid(cleaned) {
		t.Fatalf("matched=%v, error=%v, output=%s", matched, err, cleaned)
	}
	if gjson.GetBytes(cleaned, "results.0.snippet").String() != "前缀" || gjson.GetBytes(cleaned, "results.1.future_text").String() != "" {
		t.Fatalf("split sensitive word leaked: %s", cleaned)
	}
	for _, token := range []string{`"large":900719925474099312345`, `"ratio":1.234500e-70`, `"flag":true`} {
		if !bytes.Contains(cleaned, []byte(token)) {
			t.Fatalf("non-string token changed: %s", cleaned)
		}
	}
	if strings.Contains(string(cleaned), "disappear") || strings.Contains(string(cleaned), "秘密") {
		t.Fatal("sensitive suffix leaked")
	}
	info.OutputSensitiveConfig.Action = "error"
	if _, matched, err = sanitizeSearchResponse(input, info); err != nil || !matched {
		t.Fatal("error action must also signal a match before headers are written")
	}
}

func TestSearchResponseThinkingFragments(t *testing.T) {
	info := &relaycommon.RelayInfo{ThinkingProcessStrip: true}
	body := []byte(`{"parts":["before<think>hidden","still hidden</think>after"],"huge":9007199254740993}`)
	cleaned, matched, err := sanitizeSearchResponse(body, info)
	if err != nil || matched || !json.Valid(cleaned) || strings.Contains(string(cleaned), "hidden") || !bytes.Contains(cleaned, []byte("9007199254740993")) {
		t.Fatalf("thinking fragments leaked: %s, error=%v", cleaned, err)
	}
	if gjson.GetBytes(cleaned, "parts.0").String()+gjson.GetBytes(cleaned, "parts.1").String() != "beforeafter" {
		t.Fatalf("visible text lost: %s", cleaned)
	}
}
