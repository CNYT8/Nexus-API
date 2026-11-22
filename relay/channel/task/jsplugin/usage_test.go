package jsplugin

import (
	"context"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/model"
	pluginruntime "github.com/QuantumNous/new-api/pkg/jsplugin"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// This fixture runs real moejs hooks, but never starts a server or sends HTTP.
const usageFixture = `
export const meta = {apiVersion:1,key:"usage-fixture",name:"Usage fixture",version:"1.0.0",models:["fixture-model"],usageSchema:{seconds:{type:"number",unit:"second"}}};
export function buildSubmitRequest(ctx) { return {url:ctx.baseUrl+"/submit",method:"POST",body:ctx.requestBody}; }
export function buildQueryRequest(ctx) { return {url:ctx.baseUrl+"/query"}; }
export function parseTaskResult(ctx,input,response) {
  if (response.status !== 200 || response.headers["X-Fixture"] !== "query") throw new Error("query response contract");
  return {taskId:ctx.taskId,status:"SUCCESS",completionTokens:17,totalTokens:19};
}
export function parseSubmitResponse(ctx,response) {
  if (response.statusCode !== 202 || response.headers["X-Fixture"] !== "submit") throw new Error("submit response contract");
  const result = {taskId:"upstream-fixture",taskData:response.body,state:{cursor:"private-state"}};
  if (response.body.immediate) result.immediate = {taskId:result.taskId,status:"SUCCESS",completionTokens:17,totalTokens:19};
  return result;
}
export function extractUsage(ctx) {
  if (ctx.usagePurpose !== "facts" || ctx.publicTaskId !== "public-fixture") throw new Error("estimate context");
  return {seconds:ctx.requestBody.seconds};
}
export function extractUsageOnSubmit(ctx,data) { return {seconds:data.seconds}; }
export function extractUsageOnComplete(ctx,result,data) {
  if (ctx.publicTaskId !== "public-fixture" || result.taskId !== "upstream-fixture" || result.completionTokens !== 17 || result.totalTokens !== 19) throw new Error("completion context");
  return {seconds:data.finalSeconds};
}
`

func newUsageFixture(t *testing.T) (*TaskAdaptor, *gin.Context, *model.TaskUsageSnapshot) {
	t.Helper()
	plugin, err := pluginruntime.CompilePlugin(context.Background(), usageFixture, pluginruntime.Options{Concurrency: 1})
	require.NoError(t, err)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", nil)
	info := &relaycommon.RelayInfo{
		OriginModelName: "fixture-model",
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelBaseUrl: "https://fixture.invalid", UpstreamModelName: "fixture-model",
		},
		TaskRelayInfo: &relaycommon.TaskRelayInfo{PublicTaskID: "public-fixture"},
	}
	a := &TaskAdaptor{plugin: plugin, info: info, ctx: c.Request.Context(), RevisionID: 7,
		submitContext: map[string]any{"requestBody": map[string]any{"seconds": 1.5}}}
	snapshot := &model.TaskUsageSnapshot{
		Schema:     map[string]model.TaskUsageField{"seconds": {Path: "usage.seconds", Required: true, Max: 3600}},
		Expression: "seconds * 0.01", GroupRatio: 0.8, QuotaPerUnit: 500000,
		OtherRatios: map[string]float64{"resolution": 2},
	}
	return a, c, snapshot
}

func TestScriptUsageThreeHooksAndImmediateCompletion(t *testing.T) {
	for _, immediate := range []bool{false, true} {
		a, c, snapshot := newUsageFixture(t)
		require.Nil(t, a.EstimateBilling(c, a.info), "facts must not also become price ratios")
		quota, handled, err := a.FreezeUsageBilling(snapshot)
		require.NoError(t, err)
		require.True(t, handled)
		require.Equal(t, 12000, quota)
		body := `{"seconds":2.5,"finalSeconds":0}`
		if immediate {
			body = `{"seconds":2.5,"finalSeconds":0,"immediate":true}`
		}
		response := &http.Response{StatusCode: 202, Header: http.Header{"X-Fixture": {"submit"}}, Body: io.NopCloser(strings.NewReader(body))}
		id, _, taskErr := a.DoResponse(c, response, a.info)
		require.Nil(t, taskErr)
		require.Equal(t, "upstream-fixture", id)
		if immediate {
			require.Equal(t, 0, a.UsageQuote(9000))
		} else {
			require.Equal(t, 20000, a.UsageQuote(9000))
			a.completionUsage(map[string]any{"publicTaskId": "public-fixture"},
				&relaycommon.TaskInfo{TaskID: id, Status: model.TaskStatusSuccess, CompletionTokens: 17, TotalTokens: 19},
				map[string]any{"finalSeconds": 3.5})
			require.Equal(t, 28000, a.UsageQuote(9000))
		}
		_, usageErr := a.UsageSnapshot()
		require.Empty(t, usageErr)
		require.Equal(t, "usage.seconds", snapshot.Schema["seconds"].Path)
	}
}

func TestScriptUsageRequiresPolicyAndRejectsInvalidOverlay(t *testing.T) {
	a, c, snapshot := newUsageFixture(t)
	a.EstimateBilling(c, a.info)
	_, handled, err := a.FreezeUsageBilling(nil)
	require.True(t, handled)
	require.Error(t, err, "usage plugins must not silently fall back to per-call pricing")
	_, _, err = a.FreezeUsageBilling(snapshot)
	require.NoError(t, err)
	a.AdjustBillingOnSubmit(a.info, []byte(`{"seconds":-1}`))
	require.Equal(t, 1.5, a.usage["seconds"], "invalid overlay must not replace valid estimate")
	require.Equal(t, 9000, a.UsageQuote(9000))
	_, usageErr := a.UsageSnapshot()
	require.Equal(t, "submit_usage_invalid", usageErr)
}

func TestScriptQueryCompletionUsesHTTPContractAndPersistsFacts(t *testing.T) {
	a, c, snapshot := newUsageFixture(t)
	a.EstimateBilling(c, a.info)
	_, _, err := a.FreezeUsageBilling(snapshot)
	require.NoError(t, err)
	a.task = &model.Task{TaskID: "public-fixture", PrivateData: model.TaskPrivateData{UpstreamTaskID: "upstream-fixture"}}
	a.queryContext = map[string]any{"taskId": "upstream-fixture", "publicTaskId": "public-fixture"}
	a.singleQueryContext = a.queryContext
	a.responseContext = map[string]any{"status": 200, "headers": map[string]string{"X-Fixture": "query"}}
	result, err := a.ParseTaskResult([]byte(`{"finalSeconds":4.5}`))
	require.NoError(t, err)
	require.Equal(t, model.TaskStatusSuccess, result.Status)
	require.Equal(t, 36000, a.UsageQuote(9000))
	a.ApplyPollMetadata(a.task)
	require.JSONEq(t, `{"seconds":4.5}`, a.task.PrivateData.ScriptPluginUsage)
	require.Empty(t, a.task.PrivateData.ScriptPluginUsageError)
}

func TestScriptInvalidEstimateBlocksDispatchBilling(t *testing.T) {
	a, c, snapshot := newUsageFixture(t)
	a.submitContext["requestBody"] = map[string]any{"seconds": -1}
	a.EstimateBilling(c, a.info)
	_, handled, err := a.FreezeUsageBilling(snapshot)
	require.True(t, handled)
	require.Error(t, err)
	require.Equal(t, "estimate_usage_invalid", a.usageError)
}

func TestScriptSupportedRequestDescriptorsKeepEncodingConstraints(t *testing.T) {
	a, _, _ := newUsageFixture(t)
	descriptor := requestDescriptor{
		URL: "https://fixture.invalid/task", ResponseType: "sse", BodyType: "multipart",
		Parts: []any{map[string]any{"name": "prompt", "value": "hello"}},
	}
	require.NoError(t, a.validateDescriptor(descriptor, a.info.ChannelBaseUrl))
	body, err := a.encodeDescriptor(&descriptor)
	require.NoError(t, err)
	mediaType, parameters, err := mime.ParseMediaType(descriptor.Headers["Content-Type"])
	require.NoError(t, err)
	require.Equal(t, "multipart/form-data", mediaType)
	reader := multipart.NewReader(body, parameters["boundary"])
	part, err := reader.NextPart()
	require.NoError(t, err)
	require.Equal(t, "prompt", part.FormName())
	value, err := io.ReadAll(part)
	require.NoError(t, err)
	require.Equal(t, "hello", string(value))
	require.NoError(t, part.Close())
	_, err = reader.NextPart()
	require.ErrorIs(t, err, io.EOF)

	for _, invalid := range []requestDescriptor{
		{BodyType: "multipart", Body: map[string]any{"prompt": "hello"}},
		{BodyType: "multipart", Parts: []any{"part"}},
		{BodyType: "multipart", Parts: []any{map[string]any{"name": "file", "fileRef": "request_file:missing"}}},
		{BodyType: "multipart", Parts: []any{map[string]any{"name": "prompt\r\nInjected", "value": "hello"}}},
		{BodyType: "multipart", Parts: []any{map[string]any{"name": "prompt", "value": "hello", "unexpected": true}}},
	} {
		_, err := a.encodeDescriptor(&invalid)
		require.Error(t, err)
	}
}

func TestScriptRejectsUnsupportedRequestDescriptors(t *testing.T) {
	a, _, _ := newUsageFixture(t)
	for _, descriptor := range []requestDescriptor{
		{ResponseType: "xml"}, {BodyType: "raw"}, {Parts: []any{"part"}},
		{BodyType: "json", Parts: []any{map[string]any{"name": "prompt", "value": "hello"}}},
		{Credentialless: true}, {ContentPath: []any{"output"}}, {MimeType: "video/mp4"},
	} {
		descriptor.URL = "https://fixture.invalid/task"
		require.Error(t, a.validateDescriptor(descriptor, a.info.ChannelBaseUrl))
	}
}
