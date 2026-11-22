package jsplugin

import (
	"context"
	"math"
	"strings"
	"testing"
)

const contractFixture = `
export const meta = {apiVersion:1,key:"fixture",name:"Fixture",version:"1.0.0",models:["fixture"],EXTRA};
export function buildSubmitRequest() { return {}; }
export function parseSubmitResponse() { return {taskId:"one"}; }
export function buildQueryRequest() { return {}; }
export function parseTaskResult() { return {status:"SUCCESS"}; }
`

func TestManifestRejectsUnsupportedExecutionContracts(t *testing.T) {
	for _, extra := range []string{
		`routes:[{method:"POST",path:"/native",type:"submit"}]`,
		`protocols:[{name:"openai_video"}]`,
		`submitResponseTypes:["sse"]`,
		`requiredCapabilities:["multipart@1"]`,
		`usageSchema:{seconds:{unit:"second"}}`,
	} {
		source := strings.Replace(contractFixture, "EXTRA", extra, 1)
		if _, err := CompilePlugin(context.Background(), source, Options{}); err == nil {
			t.Fatalf("accepted unsupported contract: %s", extra)
		}
	}
	source := strings.Replace(contractFixture, "EXTRA", `usageSchema:{seconds:{unit:"second"}}`, 1) +
		`export function extractUsage() { return {seconds:0}; }`
	if _, err := CompilePlugin(context.Background(), source, Options{}); err != nil {
		t.Fatalf("numeric usage contract rejected: %v", err)
	}
}

func TestUsageFactsRejectInvalidAndPreserveZero(t *testing.T) {
	schema := map[string]UsageFieldSchema{"seconds": {Unit: "second"}, "credits": {Unit: "credit"}}
	facts, err := ValidateUsageFacts(map[string]any{"seconds": float64(0), "credits": 3.5}, schema)
	if err != nil || facts["seconds"] != float64(0) || facts["credits"] != 3.5 {
		t.Fatalf("facts = %v, error = %v", facts, err)
	}
	for _, value := range []any{-1.0, math.NaN(), math.Inf(1), 3601.0, "5", nil} {
		if _, err := ValidateUsageFacts(map[string]any{"seconds": value}, schema); err == nil {
			t.Fatalf("accepted invalid fact %v", value)
		}
	}
	if _, err := ValidateUsageFacts(map[string]any{"quota": 1.0}, schema); err == nil {
		t.Fatal("undeclared host quota accepted")
	}
}

func TestFetchRejectsBodyBeforeHostCallback(t *testing.T) {
	engine, err := Compile(`export function run() { return utils.fetch({url:"https://example.invalid",body:{secret:1}}); }`, Options{})
	if err != nil {
		t.Fatal(err)
	}
	called := false
	ctx := WithFetcher(context.Background(), func(context.Context, FetchRequest) (FetchResponse, error) {
		called = true
		return FetchResponse{}, nil
	})
	if _, err := engine.Call(ctx, "run"); err == nil || called {
		t.Fatalf("body accepted or sent: called=%v err=%v", called, err)
	}
}
