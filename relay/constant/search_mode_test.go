package constant

import "testing"

func TestSearchRelayModes(t *testing.T) {
	for _, tc := range []struct {
		path string
		mode int
	}{
		{"/v1/alpha/search", RelayModeAlphaSearch},
		{"/v1/tools/search", RelayModeDeepSeekSearch},
		{"/v1/tools/fetch", RelayModeDeepSeekFetch},
	} {
		if got := Path2RelayMode(tc.path); got != tc.mode {
			t.Errorf("Path2RelayMode(%q)=%d, want %d", tc.path, got, tc.mode)
		}
	}
	for _, path := range []string{"/v2/alpha/search", "/alpha/search", "/v1/alpha/searching", "/v1/tools/search/", "/v1/tools/fetch/extra", "/v1/chat/completions"} {
		switch Path2RelayMode(path) {
		case RelayModeAlphaSearch, RelayModeDeepSeekSearch, RelayModeDeepSeekFetch:
			t.Errorf("search mode escaped the exact /v1 boundary: %s", path)
		}
	}
	if RelayModeAlphaSearch != RelayModeResponsesCompact+1 || RelayModeDeepSeekSearch != RelayModeAlphaSearch+1 || RelayModeDeepSeekFetch != RelayModeDeepSeekSearch+1 {
		t.Fatal("search modes must remain appended after existing modes")
	}
}
