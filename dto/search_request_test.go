package dto

import (
	"bytes"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

func TestSearchRequestValidation(t *testing.T) {
	for _, tc := range []struct {
		name, path, body, model string
		invalid                 bool
	}{
		{name: "alpha", path: AlphaSearchPath, body: `{"model":"gpt-search","query":"hello","future":9007199254740993}`, model: "gpt-search"},
		{name: "tools fixed search", path: DeepSeekSearchPath, body: `{"model":"cheap-model","messages":[{"content":"hello"}]}`, model: DeepSeekSearchModel},
		{name: "tools fixed fetch", path: DeepSeekFetchPath, body: `{"url":"https://example.invalid"}`, model: DeepSeekFetchModel},
		{name: "tools opaque object", path: DeepSeekSearchPath, body: `{}`, model: DeepSeekSearchModel},
		{name: "non streaming", path: AlphaSearchPath, body: `{"model":"m","stream":false}`, model: "m"},
		{name: "array", path: DeepSeekSearchPath, body: `[]`, invalid: true},
		{name: "null", path: DeepSeekSearchPath, body: `null`, invalid: true},
		{name: "empty", path: DeepSeekSearchPath, body: ``, invalid: true},
		{name: "trailing document", path: DeepSeekSearchPath, body: `{} {}`, invalid: true},
		{name: "missing alpha model", path: AlphaSearchPath, body: `{}`, invalid: true},
		{name: "blank alpha model", path: AlphaSearchPath, body: `{"model":"  "}`, invalid: true},
		{name: "null model", path: DeepSeekSearchPath, body: `{"model":null}`, invalid: true},
		{name: "numeric model", path: DeepSeekSearchPath, body: `{"model":7}`, invalid: true},
		{name: "duplicate model", path: AlphaSearchPath, body: `{"model":"a","model":"b"}`, invalid: true},
		{name: "escaped duplicate", path: AlphaSearchPath, body: `{"model":"a","m\u006fdel":"b"}`, invalid: true},
		{name: "duplicate future field", path: DeepSeekFetchPath, body: `{"url":"a","url":"b"}`, invalid: true},
		{name: "stream", path: DeepSeekSearchPath, body: `{"stream":true}`, invalid: true},
		{name: "stream null", path: DeepSeekSearchPath, body: `{"stream":null}`, invalid: true},
		{name: "stream string", path: DeepSeekSearchPath, body: `{"stream":"false"}`, invalid: true},
		{name: "wrong version", path: "/v2/tools/search", body: `{}`, invalid: true},
		{name: "suffix", path: DeepSeekSearchPath + "/extra", body: `{}`, invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request, err := ParseSearchRequest(tc.path, []byte(tc.body))
			if (err != nil) != tc.invalid {
				t.Fatalf("error=%v, invalid=%v", err, tc.invalid)
			}
			if tc.invalid {
				return
			}
			if request.Model != tc.model || request.Path != tc.path || request.IsStream(nil) || string(request.RawBody) != tc.body {
				t.Fatalf("unexpected request: %+v", request)
			}
		})
	}
}

func TestSearchRequestDecodedPrompt(t *testing.T) {
	body := []byte(`{"query":"\u79d8","parts":["\u5bc6",{"text":"后续"}],"huge":9007199254740993}`)
	request, err := ParseSearchRequest(DeepSeekSearchPath, body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(request.GetTokenCountMeta().CombineText, "秘密后续") {
		t.Fatal("escaped or split prompt values escaped the sensitive scanner")
	}
	if !bytes.Equal(request.RawBody, body) {
		t.Fatal("prompt inspection mutated the wire request")
	}
}

func TestSearchRequestRetryBody(t *testing.T) {
	original := []byte(` { "query":"keep\u0020escape", "huge":900719925474099312345, "nested":{"model":"untouched"} } `)
	request, err := ParseSearchRequest(DeepSeekSearchPath, original)
	if err != nil {
		t.Fatal(err)
	}
	for _, model := range []string{"first-upstream", "second-upstream"} {
		request.SetModelName(model)
		body, err := request.UpstreamBody(model, true)
		if err != nil {
			t.Fatal(err)
		}
		if gjson.GetBytes(body, "model").String() != model || gjson.GetBytes(body, "nested.model").String() != "untouched" || !bytes.Contains(body, []byte(`900719925474099312345`)) || !bytes.Contains(body, []byte(`keep\u0020escape`)) {
			t.Fatalf("lossy mapping: %s", body)
		}
	}
	body, err := request.UpstreamBody("ignored", false)
	if err != nil || !bytes.Equal(body, original) || !bytes.Equal(request.RawBody, original) {
		t.Fatalf("retry changed original: body=%s error=%v", body, err)
	}
	body[0] = 'x'
	if request.RawBody[0] == 'x' {
		t.Fatal("returned body aliases immutable request")
	}
}
