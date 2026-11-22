package jsplugin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/QuantumNous/new-api/model"
	pluginruntime "github.com/QuantumNous/new-api/pkg/jsplugin"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

type batchPollResult struct {
	status  int
	headers http.Header
	item    json.RawMessage
	query   map[string]any
	err     error
}

func (r *batchPollResult) response() (*http.Response, error) {
	if r.err != nil {
		return nil, r.err
	}
	return &http.Response{StatusCode: r.status, Header: r.headers.Clone(), Body: io.NopCloser(bytes.NewReader(r.item))}, nil
}

func (a *TaskAdaptor) callObject(ctx context.Context, hook string, args ...any) (any, map[string]json.RawMessage, error) {
	raw, err := a.plugin.Engine.CallJSON(ctx, hook, nil, args...)
	if err != nil {
		return nil, nil, err
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil || object == nil {
		return nil, nil, errors.New("script hook must return an object")
	}
	return raw, object, nil
}

func (a *TaskAdaptor) rememberRawState(object map[string]json.RawMessage) error {
	state, exists := object["state"]
	if !exists {
		return nil
	}
	if len(state) > maxJSONBytes || !json.Valid(state) {
		return errors.New("invalid script state")
	}
	a.state, a.statePresent = string(state), true
	return nil
}

// PrepareBatchPoll performs one request per fixed revision and credential group,
// then caches individual results for the ordinary per-task CAS/billing path.
// It never writes tasks or changes quota. Errors stay attached to each task.
func (a *TaskAdaptor) PrepareBatchPoll(ctx context.Context, tasks []*model.Task, baseURL, channelKey, proxy string) {
	a.batch = make(map[int64]*batchPollResult)
	type groupKey struct {
		revision    int64
		digest, key string
		channel     int
	}
	groups := map[groupKey][]*model.Task{}
	for _, task := range tasks {
		if task == nil || task.HasForeignExecutionEvidence() || !task.ResultRetrievable() {
			continue
		}
		key := channelKey
		if task.PrivateData.Key != "" {
			key = task.PrivateData.Key
		}
		group := groupKey{task.PrivateData.ScriptPluginID, task.PrivateData.ScriptPluginDigest, key, task.ChannelId}
		groups[group] = append(groups[group], task)
	}
	for group, tasks := range groups {
		for start := 0; start < len(tasks); start += 100 {
			end := min(start+100, len(tasks))
			part := tasks[start:end]
			if err := a.BindTask(ctx, part[0]); err != nil {
				for _, task := range part {
					a.batch[task.ID] = &batchPollResult{err: errors.New("batch revision unavailable")}
				}
				continue
			}
			if a.plugin.Meta.FetchMode != "batch" {
				continue
			}
			a.prepareBatchGroup(ctx, part, baseURL, group.key, proxy)
		}
	}
	a.batchCurrent = nil
}

func (a *TaskAdaptor) prepareBatchGroup(ctx context.Context, tasks []*model.Task, baseURL, key, proxy string) {
	fail := func(err error) {
		for _, task := range tasks {
			a.batch[task.ID] = &batchPollResult{err: err}
		}
	}
	queries := make([]any, 0, len(tasks))
	wanted := make(map[string]*model.Task, len(tasks))
	for _, task := range tasks {
		id := task.GetUpstreamTaskID()
		if id == "" || wanted[id] != nil {
			fail(errors.New("duplicate or missing upstream ID in batch"))
			return
		}
		wanted[id] = task
		query := map[string]any{"taskId": id, "publicTaskId": task.TaskID, "action": task.Action, "model": task.Properties.OriginModelName, "upstreamModel": task.Properties.UpstreamModelName, "baseUrl": baseURL, "data": orderedJSONValue(task.Data), "state": orderedState(task.PrivateData.ScriptPluginState)}
		if err := a.credentials(query, key, proxy); err != nil {
			fail(errors.New("batch credentials unavailable"))
			return
		}
		queries = append(queries, query)
		a.batch[task.ID] = &batchPollResult{query: query}
	}
	batchContext := map[string]any{"baseUrl": baseURL, "tasks": queries}
	if err := a.credentials(batchContext, key, proxy); err != nil {
		fail(errors.New("batch credentials unavailable"))
		return
	}
	descriptor, err := a.callDescriptor(a.hookContext(ctx), "buildBatchQueryRequest", batchContext, queries)
	if err != nil {
		fail(errors.New("batch request hook failed"))
		return
	}
	if descriptor.ResponseType == "sse" {
		fail(errors.New("batch polling requires JSON"))
		return
	}
	body, err := a.encodeDescriptor(&descriptor)
	if err != nil {
		fail(err)
		return
	}
	descriptor, err = a.withChannelHeaders(descriptor)
	if err != nil {
		fail(err)
		return
	}
	response, err := a.doRequest(ctx, descriptor, baseURL, proxy, body)
	if err != nil {
		fail(errors.New("batch transport failed"))
		return
	}
	data, err := boundedBody(response)
	if err != nil {
		fail(err)
		return
	}
	for _, task := range tasks {
		r := a.batch[task.ID]
		r.status, r.headers = response.StatusCode, response.Header
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return
	}
	responseContext := map[string]any{"statusCode": response.StatusCode, "headers": flatHeaders(response.Header)}
	encoded, err := a.plugin.Engine.CallJSON(a.hookContext(ctx), "parseBatchResult", nil, batchContext, orderedJSONValue(data), responseContext)
	var items []json.RawMessage
	if err != nil || json.Unmarshal(encoded, &items) != nil || len(items) > 100 {
		fail(errors.New("invalid batch response"))
		return
	}
	seen := map[string]bool{}
	for _, item := range items {
		var identity struct {
			TaskID string `json:"taskId"`
		}
		if json.Unmarshal(item, &identity) != nil || wanted[identity.TaskID] == nil || seen[identity.TaskID] {
			fail(errors.New("batch returned unknown or duplicate task ID"))
			return
		}
		seen[identity.TaskID] = true
		a.batch[wanted[identity.TaskID].ID].item = item
	}
	for id, task := range wanted {
		if !seen[id] {
			a.batch[task.ID].err = errors.New("batch response omitted task")
		}
	}
}

func (a *TaskAdaptor) parseCachedBatch() (*relaycommon.TaskInfo, error) {
	cached := a.batchCurrent
	if cached.err != nil {
		return nil, cached.err
	}
	var fields map[string]json.RawMessage
	var parsed taskResult
	if json.Unmarshal(cached.item, &fields) != nil || json.Unmarshal(cached.item, &parsed) != nil || parsed.TaskID != a.task.GetUpstreamTaskID() {
		return nil, errors.New("invalid cached batch task")
	}
	if err := a.rememberRawState(fields); err != nil {
		return nil, err
	}
	data := fields["data"]
	if data == nil {
		data = fields["taskData"]
	}
	if data == nil {
		data = cached.item
	}
	a.pollData = append([]byte(nil), data...)
	a.queryContext, a.singleQueryContext = cached.query, cached.query
	result := parsed.taskInfo()
	a.completionUsage(cached.query, result, pluginruntime.RawJSON(data))
	return result, nil
}

func (a *TaskAdaptor) HasPreparedBatch(taskID int64) bool { return a.batch[taskID] != nil }
