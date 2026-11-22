package jsplugin

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	pluginruntime "github.com/QuantumNous/new-api/pkg/jsplugin"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// Hooks run in the real engine; no server or network transport is involved.
const submitResultFixture = `
export const meta = {apiVersion:1,key:"submit-result-fixture",name:"Submit result fixture",version:"1.0.0",models:["fixture-model"],usageSchema:{seconds:{type:"number",unit:"second"}}};
export function buildSubmitRequest(ctx) { return {url:ctx.baseUrl+"/submit",method:"POST",body:ctx.requestBody}; }
export function buildQueryRequest(ctx) { return {url:ctx.baseUrl+"/query"}; }
export function parseTaskResult(ctx,input,response) { return {taskId:ctx.taskId,status:"IN_PROGRESS"}; }
export function parseSubmitResponse(ctx,response) {
  const body = response.body;
  const result = {taskId:"upstream-private-id",taskData:body.data,state:{cursor:"private-state"}};
  if (body.status) result.immediate = {taskId:result.taskId,status:body.status,url:body.url,remoteUrl:"https://example.com/private-remote",reason:"private-provider-error private-key"};
  return result;
}
export function extractUsage(ctx) { return {seconds:3}; }
export function extractUsageOnSubmit(ctx,data) { return {seconds:data.seconds}; }
export function extractUsageOnComplete(ctx,result,data) { return {seconds:data.finalSeconds}; }
`

func TestDoResponseDeliversImmediateResultBeforeRetention(t *testing.T) {
	plugin, err := pluginruntime.CompilePlugin(context.Background(), submitResultFixture, pluginruntime.Options{Concurrency: 1})
	require.NoError(t, err)
	retain, discard := true, false
	for _, policy := range []struct {
		name  string
		value *bool
	}{{"default", nil}, {"retain", &retain}, {"discard", &discard}} {
		for _, outcome := range []struct {
			name         string
			status       model.TaskStatus
			finalSeconds float64
			wantQuota    int
			wantUsageErr string
		}{
			{name: "async", wantQuota: 15000},
			{name: "nonterminal", status: model.TaskStatusInProgress, wantQuota: 15000},
			{name: "success", status: model.TaskStatusSuccess, finalSeconds: 1.5, wantQuota: 7500},
			{name: "zero_success", status: model.TaskStatusSuccess, wantQuota: 0},
			{name: "invalid_usage_success", status: model.TaskStatusSuccess, finalSeconds: -1, wantQuota: 15000, wantUsageErr: "completion_usage_invalid"},
			{name: "failure", status: model.TaskStatusFailure, wantQuota: 0},
		} {
			t.Run(policy.name+"/"+outcome.name, func(t *testing.T) {
				w := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(w)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", nil)
				info := &relaycommon.RelayInfo{
					OriginModelName: "fixture-model",
					ChannelMeta: &relaycommon.ChannelMeta{
						ChannelBaseUrl: "https://fixture.invalid", ApiKey: "private-key", UpstreamModelName: "fixture-model",
					},
					TaskRelayInfo: &relaycommon.TaskRelayInfo{PublicTaskID: "public-result", Action: "generate"},
				}
				a := &TaskAdaptor{plugin: plugin, info: info, ctx: context.Background(), submitContext: map[string]any{"requestBody": map[string]any{}}}
				snapshot := &model.TaskUsageSnapshot{
					PluginID: 23, Schema: map[string]model.TaskUsageField{"seconds": {Path: "usage.seconds", Required: true, Max: 3600}},
					Expression: "seconds * 0.01", GroupRatio: 1, QuotaPerUnit: 500000, RetainResult: policy.value,
				}
				a.EstimateBilling(c, info)
				reserved, handled, err := a.FreezeUsageBilling(snapshot)
				require.NoError(t, err)
				require.True(t, handled)
				require.Equal(t, 15000, reserved)
				const outputURL = "https://example.com/output.mp4?signature=output-access"
				data := map[string]any{
					"seconds": 3, "finalSeconds": outcome.finalSeconds, "result_text": "delivered output",
					"artifacts": []any{map[string]any{"kind": "video", "url": outputURL, "content_url": "/forged-owner-link"}},
					"key":       "private-key", "state": "private-state", "billing_key": "private-reservation",
					"billing_context": map[string]any{"expression": "private-expression"},
				}
				body, err := json.Marshal(map[string]any{"status": outcome.status, "url": outputURL, "data": data})
				require.NoError(t, err)
				upstreamID, raw, taskErr := a.DoResponse(c, &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(body))}, info)
				require.Nil(t, taskErr)
				require.Equal(t, "upstream-private-id", upstreamID)
				expectedRaw, err := json.Marshal(data)
				require.NoError(t, err)
				require.JSONEq(t, string(expectedRaw), string(raw), "billing and storage must retain the complete internal taskData")
				reply := append([]byte(nil), w.Body.Bytes()...)
				require.Equal(t, http.StatusOK, w.Code)
				require.Equal(t, "private, no-store", w.Header().Get("Cache-Control"))
				var public dto.OpenAIVideo
				require.NoError(t, json.Unmarshal(reply, &public))
				require.Equal(t, "public-result", public.ID)
				require.Equal(t, "video", public.Object)
				require.Equal(t, "fixture-model", public.Model)
				require.Positive(t, public.CreatedAt)
				keys := []string{"id", "object", "model", "status", "progress", "created_at"}
				switch outcome.status {
				case model.TaskStatusSuccess:
					require.Equal(t, "completed", public.Status)
					require.Equal(t, 100, public.Progress)
					require.Nil(t, public.Error)
					require.Equal(t, outputURL, public.Metadata["url"])
					require.Equal(t, "delivered output", public.Metadata["text"])
					artifacts, err := json.Marshal(public.Metadata["artifacts"])
					require.NoError(t, err)
					require.JSONEq(t, `[{"id":"1","kind":"file","url":"`+outputURL+`"}]`, string(artifacts))
					keys = append(keys, "metadata")
				case model.TaskStatusFailure:
					require.Equal(t, "failed", public.Status)
					require.Equal(t, 100, public.Progress)
					require.Equal(t, &dto.OpenAIVideoError{Code: "task_failed", Message: "Task failed"}, public.Error)
					require.Nil(t, public.Metadata)
					keys = append(keys, "error")
				default:
					require.Equal(t, "queued", public.Status)
					require.Zero(t, public.Progress)
					require.Nil(t, public.Metadata)
					require.Nil(t, public.Error)
				}
				var envelope map[string]json.RawMessage
				require.NoError(t, json.Unmarshal(reply, &envelope))
				actualKeys := make([]string, 0, len(envelope))
				for key := range envelope {
					actualKeys = append(actualKeys, key)
				}
				require.ElementsMatch(t, keys, actualKeys)
				for _, private := range []string{"private-key", "private-state", "upstream-private-id", "private-reservation", "private-expression", "private-provider-error", "private-remote", "forged-owner-link", "content_url", "billing_context", "seconds", "finalSeconds", "completion_usage_invalid"} {
					require.NotContains(t, string(reply), private)
				}

				// Follow the host's order: parse the terminal status, freeze its
				// target using private facts, then apply retention to the new row.
				facts, usageErr := a.UsageSnapshot()
				require.Equal(t, outcome.wantUsageErr, usageErr)
				task := &model.Task{
					TaskID: info.PublicTaskID, Status: model.TaskStatusSubmitted, Quota: reserved, Data: raw,
					PrivateData: model.TaskPrivateData{
						Key: info.ApiKey, UpstreamTaskID: upstreamID, BillingKey: "private-reservation", TokenId: 31,
						ScriptPluginID: 5, ScriptPluginDigest: plugin.Digest, ScriptPluginState: a.state,
						ScriptPluginUsage: facts, ScriptPluginUsageError: usageErr,
						BillingSource: "subscription", SubscriptionId: 9, SubscriptionPeriodStart: 1700000000,
						BillingContext: &model.TaskBillingContext{UsageSnapshot: snapshot},
					},
				}
				instant, err := a.ParseTaskResult(raw)
				require.NoError(t, err)
				terminal := outcome.status == model.TaskStatusSuccess || outcome.status == model.TaskStatusFailure
				if terminal {
					require.NotNil(t, instant)
					task.Status, task.FailReason = model.TaskStatus(instant.Status), instant.Reason
					task.PrivateData.ResultURL = instant.Url
					task.PrivateData.ResultMetadata = service.CollectTaskResultMetadata(raw, instant.Url)
					task.BillingState, task.BillingQuota = model.TaskBillingPending, a.UsageQuote(reserved)
					if task.Status == model.TaskStatusFailure {
						task.BillingQuota = 0
					} else {
						target, _, handled := service.ScriptTaskUsageBillingTarget(task)
						require.True(t, handled)
						task.BillingQuota = target
					}
					require.Equal(t, outcome.wantQuota, task.BillingQuota)
				}
				wantDiscard := terminal && policy.value != nil && !*policy.value
				require.Equal(t, wantDiscard, service.ApplyImmediateTaskResultRetention(task))
				require.Equal(t, wantDiscard, task.PrivateData.ResultDiscarded)
				require.Equal(t, reply, w.Body.Bytes(), "discarding storage must not erase the already delivered response")
				require.JSONEq(t, string(expectedRaw), string(raw), "retention must not rewrite adapter result bytes")
				require.Equal(t, facts, task.PrivateData.ScriptPluginUsage)
				require.Equal(t, usageErr, task.PrivateData.ScriptPluginUsageError)
				require.Equal(t, snapshot, task.PrivateData.BillingContext.UsageSnapshot)
				require.Equal(t, "private-reservation", task.PrivateData.BillingKey)
				require.Equal(t, 31, task.PrivateData.TokenId)
				require.Equal(t, "subscription", task.PrivateData.BillingSource)
				require.Equal(t, 9, task.PrivateData.SubscriptionId)
				require.Equal(t, int64(1700000000), task.PrivateData.SubscriptionPeriodStart)
				if wantDiscard {
					require.Empty(t, task.Data)
					require.Empty(t, task.FailReason)
					require.Empty(t, task.PrivateData.Key)
					require.Empty(t, task.PrivateData.UpstreamTaskID)
					require.Empty(t, task.PrivateData.ScriptPluginState)
					require.Nil(t, task.PrivateData.ResultMetadata)
					require.False(t, task.ResultRetrievable())
					target, _, handled := service.ScriptTaskUsageBillingTarget(task)
					require.True(t, handled)
					require.Equal(t, outcome.wantQuota, target, "recovery must use the frozen target after raw data is gone")
				} else {
					require.JSONEq(t, string(expectedRaw), string(task.Data))
					require.Equal(t, a.state, task.PrivateData.ScriptPluginState)
					require.True(t, task.ResultRetrievable())
				}
			})
		}
	}
}
