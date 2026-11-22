package jsplugin

import (
	"context"
	"testing"
	"time"
)

// These tests execute actual moejs bytecode and host callbacks. They intentionally
// use an in-memory fetcher: no HTTP server, external request or credential needed.
func TestEngineESMHooksAndFetchBudget(t *testing.T) {
	engine, err := Compile(`
export const meta = { key: "fixture" };
export function run(input) {
  const reply = utils.fetch({url: "https://example.invalid/value"});
  return {value: input.value + reply.body.value, status: reply.status};
}
export function exhaust() {
  for (let n = 0; n < 5; n++) utils.fetch({url: "https://example.invalid/value"});
}
`, Options{Concurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	ctx := WithFetcher(context.Background(), func(_ context.Context, request FetchRequest) (FetchResponse, error) {
		calls++
		if request.Method != "GET" {
			t.Fatalf("method = %q", request.Method)
		}
		return FetchResponse{Status: 200, Body: []byte(`{"value":7}`)}, nil
	})
	var result struct {
		Value  int `json:"value"`
		Status int `json:"status"`
	}
	if err = engine.CallInto(ctx, &result, "run", map[string]any{"value": 5}); err != nil {
		t.Fatal(err)
	}
	if result.Value != 12 || result.Status != 200 || calls != 1 {
		t.Fatalf("result = %+v, calls = %d", result, calls)
	}
	calls = 0
	if _, err = engine.Call(ctx, "exhaust"); err == nil {
		t.Fatal("fifth fetch must fail")
	}
	if calls != 4 {
		t.Fatalf("host calls = %d, want 4", calls)
	}
}

func TestEngineInterruptDoesNotPoisonNextCall(t *testing.T) {
	engine, err := Compile(`export function run(loop) { if (loop) { while (true) {} } return 42; }`, Options{Concurrency: 1, Timeout: 100 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = engine.Call(context.Background(), "run", true); err == nil {
		t.Fatal("infinite loop was not interrupted")
	}
	var value int
	if err = engine.CallInto(context.Background(), &value, "run", false); err != nil {
		t.Fatal(err)
	}
	if value != 42 {
		t.Fatalf("value = %d", value)
	}
}

func TestEngineRejectsModuleDependenciesAndDynamicCode(t *testing.T) {
	for _, source := range []string{`import x from "fs"; export function run() { return x; }`, `export {x} from "fs";`} {
		if _, err := Compile(source, Options{}); err == nil {
			t.Fatal("module dependency was accepted")
		}
	}
	engine, err := Compile(`export function run() { return Function("return 1")(); }`, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = engine.Call(context.Background(), "run"); err == nil {
		t.Fatal("dynamic code was accepted")
	}
}

func TestRequestGuardsRejectCredentialEscape(t *testing.T) {
	for _, target := range []string{"http://api.example.invalid/task", "https://user:secret@api.example.invalid/task", "https://other.invalid/task"} {
		if err := ValidateRequestURL(target, "https://api.example.invalid", nil); err == nil {
			t.Fatalf("accepted %q", target)
		}
	}
	if err := ValidateRequestURL("https://api.example.invalid/task", "https://api.example.invalid", nil); err != nil {
		t.Fatal(err)
	}
	if err := ValidateRequestHeaders(map[string]string{"Host": "other.invalid"}); err == nil {
		t.Fatal("Host override accepted")
	}
}
