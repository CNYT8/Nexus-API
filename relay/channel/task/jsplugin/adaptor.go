// Package jsplugin adapts upstream API-v1 JavaScript drivers to Nexus' task
// interface. Reservations, final billing, membership and rebates remain owned
// by relay_task and task_polling, including their existing status CAS.
package jsplugin

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	pluginruntime "github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/relay/channel"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

const Platform = "jsplugin"
const maxJSONBytes = 1 << 20

type requestDescriptor struct {
	URL            string            `json:"url"`
	Method         string            `json:"method"`
	Headers        map[string]string `json:"headers"`
	Body           any               `json:"body"`
	BodyType       string            `json:"bodyType"`
	ResponseType   string            `json:"responseType"`
	Credentialless bool              `json:"credentialless"`
	Action         string            `json:"action"`
	Model          string            `json:"model"`
	RewriteModel   string            `json:"rewriteModel"`
	Parts          []any             `json:"parts"`
	ContentPath    []any             `json:"contentPath"`
	MimeType       string            `json:"mimeType"`
	bodyJSON       []byte
}
type submitResponse struct {
	TaskID    string      `json:"taskId"`
	TaskData  any         `json:"taskData"`
	Immediate *taskResult `json:"immediate"`
}
type taskResult struct {
	Code             int    `json:"code"`
	TaskID           string `json:"taskId"`
	Status           string `json:"status"`
	Progress         string `json:"progress"`
	Reason           string `json:"reason"`
	URL              string `json:"url"`
	RemoteURL        string `json:"remoteUrl"`
	CompletionTokens int    `json:"completionTokens"`
	TotalTokens      int    `json:"totalTokens"`
}

func (r taskResult) taskInfo() *relaycommon.TaskInfo {
	return &relaycommon.TaskInfo{Code: r.Code, TaskID: r.TaskID, Status: r.Status,
		Progress: r.Progress, Reason: r.Reason, Url: r.URL, RemoteUrl: r.RemoteURL,
		CompletionTokens: max(0, r.CompletionTokens), TotalTokens: max(0, r.TotalTokens)}
}

type TaskAdaptor struct {
	RevisionID         int64
	plugin             *pluginruntime.LoadedPlugin
	info               *relaycommon.RelayInfo
	ctx                context.Context
	task               *model.Task
	submit             *requestDescriptor
	submitContext      map[string]any
	queryContext       map[string]any
	responseContext    map[string]any
	state              string
	statePresent       bool
	pollData           []byte
	immediate          *relaycommon.TaskInfo
	parsedSubmit       bool
	usage              map[string]any
	usageError         string
	usageSnapshot      *model.TaskUsageSnapshot
	singleQueryContext map[string]any
	files              map[string]requestFile
	batch              map[int64]*batchPollResult
	batchCurrent       *batchPollResult
	keepReservation    bool
}

func New(id int64, plugin *pluginruntime.LoadedPlugin) *TaskAdaptor {
	return &TaskAdaptor{RevisionID: id, plugin: plugin, ctx: context.Background()}
}
func (a *TaskAdaptor) Init(info *relaycommon.RelayInfo) {
	a.info = info
	if a.ctx == nil {
		a.ctx = context.Background()
	}
}
func (a *TaskAdaptor) BindTask(ctx context.Context, task *model.Task) error {
	if task == nil || !task.ResultRetrievable() || task.PrivateData.ScriptPluginID <= 0 {
		return errors.New("task has no retrievable script revision")
	}
	plugin, err := service.LoadScriptPlugin(ctx, task.PrivateData.ScriptPluginID, false)
	if err != nil {
		return err
	}
	if task.PrivateData.ScriptPluginDigest != plugin.Digest {
		return errors.New("task script revision digest mismatch")
	}
	a.ctx, a.task, a.plugin, a.RevisionID = ctx, task, plugin, task.PrivateData.ScriptPluginID
	a.queryContext, a.responseContext, a.pollData = nil, nil, nil
	a.batchCurrent = a.batch[task.ID]
	a.state, a.statePresent, a.immediate, a.parsedSubmit = task.PrivateData.ScriptPluginState, false, nil, false
	a.info.OriginModelName = task.Properties.OriginModelName
	a.info.UpstreamModelName = task.Properties.UpstreamModelName
	a.usage, a.usageSnapshot = nil, nil
	a.usageError = task.PrivateData.ScriptPluginUsageError
	if task.PrivateData.ScriptPluginUsage != "" && common.Unmarshal([]byte(task.PrivateData.ScriptPluginUsage), &a.usage) != nil {
		a.usageError = "stored_usage_invalid"
	}
	if task.PrivateData.BillingContext != nil {
		a.usageSnapshot = task.PrivateData.BillingContext.UsageSnapshot
	}
	if a.hasUsage() && a.usageSnapshot == nil {
		a.usageError = "frozen_usage_policy_missing"
	}
	return nil
}
func (a *TaskAdaptor) ScriptSnapshot() (int64, string, string) {
	return a.RevisionID, a.plugin.Digest, a.state
}
func (a *TaskAdaptor) ApplyPollMetadata(task *model.Task) {
	var state *string
	if a.statePresent {
		state = &a.state
	}
	usage, usageError := a.UsageSnapshot()
	task.SetScriptPollMetadata(state, usage, usageError)
	if a.pollData != nil {
		task.Data = append([]byte(nil), a.pollData...)
	}
}
func (a *TaskAdaptor) ValidateRequestAndSetAction(c *gin.Context, info *relaycommon.RelayInfo) *dto.TaskError {
	if a.plugin == nil {
		return localError("script plugin not loaded", "script_plugin_missing")
	}
	// Selection has already passed the host token, model and channel checks.
	if !a.plugin.Meta.SupportsModel(info.OriginModelName) {
		return localError("model is not declared by the script plugin", "script_plugin_model")
	}
	if len(a.plugin.Meta.ChannelTypes) > 0 {
		allowed := false
		for _, channelType := range a.plugin.Meta.ChannelTypes {
			if channelType == info.ChannelType {
				allowed = true
			}
		}
		if !allowed {
			return localError("channel type is not supported by the script plugin", "script_plugin_channel")
		}
	}
	input, err := ReadInput(c)
	if err != nil {
		return localError(err.Error(), "script_plugin_body")
	}
	body := input.Object
	requestBody := input.Body
	if intent, ok := PreparedIntent(c); ok {
		if intent.RevisionID != a.RevisionID || intent.Digest != a.plugin.Digest || intent.Intent.Model != info.OriginModelName {
			return localError("script selection changed after authorization", "script_plugin_binding")
		}
		requestBody = intent.Intent.RequestBody
		info.Action = intent.Intent.Action
	}
	if requested, exists := body["model"]; exists && requested != info.OriginModelName {
		return localError("request model differs from authorized model", "script_plugin_model")
	}
	a.ctx = c.Request.Context()
	params := map[string]string{}
	for _, param := range c.Params {
		params[param.Key] = param.Value
	}
	headers := map[string]string{}
	for name, values := range c.Request.Header {
		switch strings.ToLower(name) {
		case "authorization", "proxy-authorization", "cookie", "x-api-key", "api-key", "x-goog-api-key", "mj-api-secret":
			continue
		}
		headers[name] = strings.Join(values, ", ")
	}
	if info.Action == "" {
		info.Action = "submit"
	}
	a.files = make(map[string]requestFile, len(input.Files))
	for _, file := range input.Files {
		a.files[file.Ref] = file
	}
	origins, err := a.originViews(c, info)
	if err != nil {
		return localError(err.Error(), "script_plugin_origin")
	}
	a.submitContext = map[string]any{"originTasks": origins, "path": c.Request.URL.Path, "method": c.Request.Method,
		"params": params, "query": c.Request.URL.Query(), "requestBody": requestBody, "requestHeaders": headers,
		"files": input.Files, "action": info.Action, "originTaskId": info.OriginTaskID,
		"model": info.OriginModelName, "baseUrl": info.ChannelBaseUrl, "userSetting": info.UserSetting}
	c.Set("task_request", body)
	return nil
}
func localError(message, code string) *dto.TaskError {
	return service.TaskErrorWrapperLocal(errors.New(message), code, http.StatusBadRequest)
}
func (a *TaskAdaptor) BuildRequestURL(info *relaycommon.RelayInfo) (string, error) {
	if a.submit == nil {
		return "", errors.New("script submit request not built")
	}
	return a.submit.URL, pluginruntime.ValidateRequestURL(a.submit.URL, info.ChannelBaseUrl, a.plugin.Meta.AllowedHosts)
}
func (a *TaskAdaptor) BuildRequestHeader(_ *gin.Context, request *http.Request, _ *relaycommon.RelayInfo) error {
	if a.submit == nil {
		return errors.New("script submit request not built")
	}
	for name, value := range a.submit.Headers {
		request.Header.Set(name, value)
	}
	return nil
}
func (a *TaskAdaptor) BuildRequestBody(c *gin.Context, info *relaycommon.RelayInfo) (io.Reader, error) {
	a.submitContext["publicTaskId"], a.submitContext["upstreamModel"] = info.PublicTaskID, info.UpstreamModelName
	if err := a.credentials(a.submitContext, info.ApiKey, info.ChannelSetting.Proxy); err != nil {
		return nil, err
	}
	ctx := pluginruntime.WithFetcher(c.Request.Context(), a.hookFetcher(info.ChannelBaseUrl, info.ChannelSetting.Proxy))
	descriptor, err := a.callDescriptor(ctx, "buildSubmitRequest", a.submitContext)
	if err != nil {
		return nil, errors.New("script buildSubmitRequest failed")
	}
	if descriptor.Model != "" && descriptor.Model != info.OriginModelName {
		return nil, errors.New("script cannot change authorized model")
	}
	if descriptor.RewriteModel != "" && descriptor.RewriteModel != info.UpstreamModelName {
		return nil, errors.New("script cannot change channel model mapping")
	}
	if len(descriptor.Action) > 40 || strings.ContainsAny(descriptor.Action, "\r\n\x00") {
		return nil, errors.New("invalid script action")
	}
	if descriptor.Action != "" {
		info.Action = descriptor.Action
		a.submitContext["action"] = descriptor.Action
	}
	if descriptor.Method == "" {
		descriptor.Method = http.MethodPost
	}
	if err := a.validateDescriptor(descriptor, info.ChannelBaseUrl); err != nil {
		return nil, err
	}
	if descriptor.ResponseType == "sse" {
		declared := false
		for _, kind := range a.plugin.Meta.SubmitResponseTypes {
			declared = declared || kind == "sse"
		}
		if !declared {
			return nil, errors.New("SSE submit response was not declared")
		}
	}
	body, err := a.encodeDescriptor(&descriptor)
	if err != nil {
		return nil, err
	}
	a.submit = &descriptor
	return body, nil
}
func (a *TaskAdaptor) validateDescriptor(descriptor requestDescriptor, baseURL string) error {
	if descriptor.Credentialless || len(descriptor.ContentPath) != 0 || descriptor.MimeType != "" {
		return errors.New("artifact-only request capabilities are not supported by task requests")
	}
	if descriptor.BodyType != "" && descriptor.BodyType != "json" && descriptor.BodyType != "multipart" {
		return errors.New("invalid script bodyType")
	}
	if descriptor.ResponseType != "" && descriptor.ResponseType != "json" && descriptor.ResponseType != "sse" {
		return errors.New("unsupported script responseType")
	}
	if len(descriptor.Parts) != 0 && descriptor.BodyType != "multipart" {
		return errors.New("parts require multipart bodyType")
	}
	if err := pluginruntime.ValidateRequestURL(descriptor.URL, baseURL, a.plugin.Meta.AllowedHosts); err != nil {
		return err
	}
	return pluginruntime.ValidateRequestHeaders(descriptor.Headers)
}
func descriptorBody(value any) (io.Reader, error) {
	if value == nil {
		return nil, nil
	}
	if text, ok := value.(string); ok {
		if len(text) > maxJSONBytes {
			return nil, errors.New("script request too large")
		}
		return strings.NewReader(text), nil
	}
	data, err := common.Marshal(value)
	if err != nil {
		return nil, err
	}
	if len(data) > maxJSONBytes {
		return nil, errors.New("script request too large")
	}
	return bytes.NewReader(data), nil
}
func (a *TaskAdaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, body io.Reader) (*http.Response, error) {
	if a.submit == nil {
		return nil, errors.New("script submit request not built")
	}
	descriptor, err := a.withChannelHeaders(*a.submit)
	if err != nil {
		return nil, err
	}
	return a.doRequest(c.Request.Context(), descriptor, info.ChannelBaseUrl, info.ChannelSetting.Proxy, body)
}
func (a *TaskAdaptor) doRequest(ctx context.Context, descriptor requestDescriptor, baseURL, proxy string, body io.Reader) (*http.Response, error) {
	if err := a.validateDescriptor(descriptor, baseURL); err != nil {
		return nil, err
	}
	client, err := service.GetHttpClientWithProxySettings(proxy, a.info.ChannelSetting)
	if err != nil {
		return nil, err
	}
	isolated := *client
	isolated.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if isolated.Timeout == 0 || isolated.Timeout > 60*time.Second {
		isolated.Timeout = 60 * time.Second
	}
	method := strings.ToUpper(descriptor.Method)
	if method == "" {
		method = http.MethodGet
	}
	switch method {
	case "GET", "HEAD", "POST", "PUT", "PATCH", "DELETE":
	default:
		return nil, errors.New("unsupported script HTTP method")
	}
	request, err := http.NewRequestWithContext(ctx, method, descriptor.URL, body)
	if err != nil {
		return nil, err
	}
	for name, value := range descriptor.Headers {
		request.Header.Set(name, value)
	}
	if body != nil && request.Header.Get("Content-Type") == "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := isolated.Do(request)
	if err != nil {
		return nil, errors.New("script upstream transport failed")
	}
	return response, nil
}
func boundedBody(response *http.Response) ([]byte, error) {
	if response == nil || response.Body == nil {
		return nil, errors.New("empty script upstream response")
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, maxJSONBytes+1))
	if err != nil {
		return nil, errors.New("script upstream response read failed")
	}
	if len(data) > maxJSONBytes {
		return nil, errors.New("script upstream response exceeds 1 MiB")
	}
	return data, nil
}
func jsonValue(data []byte) any {
	var value any
	if common.Unmarshal(data, &value) == nil {
		return value
	}
	return string(data)
}
func orderedJSONValue(data []byte) any {
	var value any
	if common.Unmarshal(data, &value) == nil {
		return pluginruntime.RawJSON(data)
	}
	return string(data)
}
func convert(value, target any) error {
	data, err := common.Marshal(value)
	if err != nil {
		return err
	}
	return common.Unmarshal(data, target)
}
func (a *TaskAdaptor) rememberState(value any) error {
	object, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	state, exists := object["state"]
	if !exists {
		return nil
	}
	data, err := common.Marshal(state)
	if err != nil || len(data) > maxJSONBytes {
		return errors.New("invalid script state")
	}
	a.state, a.statePresent = string(data), true
	return nil
}
func (a *TaskAdaptor) DoResponse(c *gin.Context, response *http.Response, info *relaycommon.RelayInfo) (id string, resultData []byte, taskError *dto.TaskError) {
	defer func() {
		if taskError != nil && (a.keepReservation || AcceptedSubmission(c) && c.Writer.Written()) {
			a.keepReservation, a.parsedSubmit = true, true
			a.usageError = "submit_stream_delivery_interrupted"
			a.immediate = &relaycommon.TaskInfo{TaskID: info.PublicTaskID, Status: model.TaskStatusSuccess, Progress: "100%"}
			id, resultData, taskError = info.PublicTaskID, []byte(`{}`), nil
		}
	}()
	body, err := a.submitResponseBody(c, response)
	if err != nil {
		return "", nil, service.TaskErrorWrapperLocal(err, "script_response", http.StatusBadGateway)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", nil, service.TaskErrorWrapperLocal(errors.New("script upstream rejected submission"), "script_upstream_status", http.StatusBadGateway)
	}
	value, raw, err := a.callObject(a.hookContext(c.Request.Context()), "parseSubmitResponse", a.submitContext, map[string]any{
		"statusCode": response.StatusCode, "headers": flatHeaders(response.Header), "body": body})
	if object, ok := value.(map[string]any); ok {
		if _, exists := object["clientResponse"]; exists {
			return "", nil, service.TaskErrorWrapperLocal(errors.New("script clientResponse is unsupported"), "script_submit_result", http.StatusBadGateway)
		}
	}
	var parsed submitResponse
	if err != nil || convert(value, &parsed) != nil || strings.TrimSpace(parsed.TaskID) == "" || len(parsed.TaskID) > 191 {
		return "", nil, service.TaskErrorWrapperLocal(errors.New("invalid script submit result"), "script_submit_result", http.StatusBadGateway)
	}
	if err = a.rememberRawState(raw); err != nil {
		return "", nil, service.TaskErrorWrapperLocal(err, "script_state", http.StatusBadGateway)
	}
	taskData := []byte(raw["taskData"])
	if len(taskData) == 0 {
		taskData = []byte("null")
	}
	if len(taskData) > maxJSONBytes {
		return "", nil, service.TaskErrorWrapperLocal(errors.New("invalid script task data"), "script_data", http.StatusBadGateway)
	}
	a.parsedSubmit = true
	a.AdjustBillingOnSubmit(info, taskData)
	status, progress := "queued", 0
	if parsed.Immediate != nil {
		a.immediate = parsed.Immediate.taskInfo()
		query := map[string]any{"taskId": parsed.TaskID, "publicTaskId": info.PublicTaskID,
			"action": info.Action, "model": info.OriginModelName, "upstreamModel": info.UpstreamModelName,
			"baseUrl": info.ChannelBaseUrl, "data": orderedJSONValue(taskData), "state": nil}
		if a.state != "" {
			query["state"] = orderedState(a.state)
		}
		if err := a.credentials(query, info.ApiKey, info.ChannelSetting.Proxy); err != nil {
			a.usageError = "completion_auth_failed"
		} else {
			a.completionUsage(query, a.immediate, orderedJSONValue(taskData))
		}
		if a.immediate.Status == model.TaskStatusSuccess {
			status, progress = "completed", 100
		}
		if a.immediate.Status == model.TaskStatusFailure {
			status, progress = "failed", 100
		}
	}
	if _, prepared := PreparedIntent(c); prepared {
		if err := a.presentSubmit(c, info, parsed.TaskID, taskData); err != nil {
			return "", nil, service.TaskErrorWrapperLocal(err, "script_presentation", http.StatusBadGateway)
		}
		return parsed.TaskID, taskData, nil
	}
	public := &dto.OpenAIVideo{
		ID: info.PublicTaskID, Object: "video", Model: info.OriginModelName,
		Status: status, Progress: progress, CreatedAt: time.Now().Unix(),
	}
	if a.immediate != nil {
		switch a.immediate.Status {
		case model.TaskStatusSuccess:
			// Deliver explicit outputs now, before submit-time retention removes
			// them. taskData itself stays private and intact for usage/settlement.
			public.Metadata = service.TaskResultResponseMetadata(taskData, a.immediate.Url)
		case model.TaskStatusFailure:
			// Provider reasons and raw taskData may contain credentials or state.
			public.Error = &dto.OpenAIVideoError{Code: "task_failed", Message: "Task failed"}
		}
	}
	c.Header("Cache-Control", "private, no-store")
	c.JSON(http.StatusOK, public)
	return parsed.TaskID, taskData, nil
}
func (a *TaskAdaptor) GetModelList() []string {
	if a.plugin == nil {
		return nil
	}
	return append([]string(nil), a.plugin.Meta.Models...)
}
func (a *TaskAdaptor) GetChannelName() string {
	if a.plugin == nil {
		return "Script plugin"
	}
	return a.plugin.Meta.Name
}
func (a *TaskAdaptor) FetchTask(baseURL, key string, _ map[string]any, proxy string) (*http.Response, error) {
	if a.task == nil || a.plugin == nil {
		return nil, errors.New("script poll must bind a task revision")
	}
	if a.batchCurrent != nil {
		return a.batchCurrent.response()
	}
	query := map[string]any{"taskId": a.task.GetUpstreamTaskID(), "publicTaskId": a.task.TaskID,
		"action": a.task.Action, "model": a.task.Properties.OriginModelName, "upstreamModel": a.task.Properties.UpstreamModelName,
		"baseUrl": baseURL, "data": orderedJSONValue(a.task.Data), "state": nil}
	if query["upstreamModel"] == "" {
		query["upstreamModel"] = query["model"]
	}
	if a.state != "" {
		query["state"] = orderedState(a.state)
	}
	if err := a.credentials(query, key, proxy); err != nil {
		return nil, err
	}
	a.queryContext, a.singleQueryContext = query, query
	a.info.ApiKey = key
	var descriptor requestDescriptor
	ctx := pluginruntime.WithFetcher(a.ctx, a.hookFetcher(baseURL, proxy))
	var err error
	if a.plugin.Meta.FetchMode == "batch" {
		batch := map[string]any{"baseUrl": baseURL, "tasks": []any{query}}
		if err = a.credentials(batch, key, proxy); err != nil {
			return nil, err
		}
		a.queryContext = batch
		descriptor, err = a.callDescriptor(ctx, "buildBatchQueryRequest", batch, []any{query})
	} else {
		descriptor, err = a.callDescriptor(ctx, "buildQueryRequest", query)
	}
	if err != nil {
		return nil, errors.New("script query hook failed")
	}
	body, err := a.encodeDescriptor(&descriptor)
	if err != nil {
		return nil, err
	}
	descriptor, err = a.withChannelHeaders(descriptor)
	if err != nil {
		return nil, err
	}
	response, err := a.doRequest(a.ctx, descriptor, baseURL, proxy, body)
	if err != nil {
		return nil, err
	}
	headers := map[string]string{}
	for name := range response.Header {
		headers[name] = response.Header.Get(name)
	}
	a.responseContext = map[string]any{"status": response.StatusCode, "headers": headers}
	// Bound the reply before it reaches the shared legacy polling reader.
	data, err := boundedBody(response)
	if err != nil {
		return nil, err
	}
	response.Body = io.NopCloser(bytes.NewReader(data))
	return response, nil
}
func (a *TaskAdaptor) ParseTaskResult(data []byte) (*relaycommon.TaskInfo, error) {
	if a.parsedSubmit {
		return a.immediate, nil
	}
	if a.batchCurrent != nil {
		return a.parseCachedBatch()
	}
	if a.plugin == nil || a.queryContext == nil {
		return nil, errors.New("script query context missing")
	}
	var value any
	var err error
	if a.plugin.Meta.FetchMode == "batch" {
		value, err = a.plugin.Engine.Call(a.hookContext(a.ctx), "parseBatchResult", a.queryContext, orderedJSONValue(data), a.responseContext)
		if err != nil {
			return nil, errors.New("script batch result hook failed")
		}
		items, ok := value.([]any)
		if !ok || len(items) > 100 {
			return nil, errors.New("invalid script batch result")
		}
		value = nil
		for _, item := range items {
			object, ok := item.(map[string]any)
			if ok && object["taskId"] == a.task.GetUpstreamTaskID() {
				value = item
				if raw, exists := object["data"]; exists {
					a.pollData, err = common.Marshal(raw)
				}
				break
			}
		}
		if value == nil {
			return nil, errors.New("script batch result omitted task")
		}
	} else {
		value, err = a.plugin.Engine.Call(a.hookContext(a.ctx), "parseTaskResult", a.queryContext, orderedJSONValue(data), a.responseContext)
	}
	if err != nil {
		return nil, errors.New("script result hook failed")
	}
	var result taskResult
	if err = convert(value, &result); err != nil {
		return nil, errors.New("invalid script task result")
	}
	if result.TaskID != "" && result.TaskID != a.task.GetUpstreamTaskID() {
		return nil, errors.New("script result taskId mismatch")
	}
	if err = a.rememberState(value); err != nil {
		return nil, err
	}
	if len(a.pollData) > maxJSONBytes {
		return nil, errors.New("script poll data too large")
	}
	usageData := jsonValue(data)
	if a.pollData != nil {
		usageData = jsonValue(a.pollData)
	}
	if a.statePresent {
		a.singleQueryContext["state"] = jsonValue([]byte(a.state))
	}
	info := result.taskInfo()
	a.completionUsage(a.singleQueryContext, info, usageData)
	return info, nil
}
func (a *TaskAdaptor) hookContext(ctx context.Context) context.Context {
	return pluginruntime.WithFetcher(ctx, a.hookFetcher(a.info.ChannelBaseUrl, a.info.ChannelSetting.Proxy))
}

func (a *TaskAdaptor) hookFetcher(baseURL, proxy string) pluginruntime.Fetcher {
	return func(ctx context.Context, request pluginruntime.FetchRequest) (pluginruntime.FetchResponse, error) {
		// Upstream FetchRequest has no Body field. Reject non-read methods here
		// too; JS body/unknown fields are rejected by the engine before I/O.
		if request.Method != http.MethodGet && request.Method != http.MethodHead {
			return pluginruntime.FetchResponse{}, errors.New("utils.fetch supports only GET and HEAD without a body")
		}
		descriptor := requestDescriptor{URL: request.URL, Method: request.Method, Headers: request.Headers}
		response, err := a.doRequest(ctx, descriptor, baseURL, proxy, nil)
		if err != nil {
			return pluginruntime.FetchResponse{}, err
		}
		data, err := boundedBody(response)
		if err != nil {
			return pluginruntime.FetchResponse{}, err
		}
		headers := map[string]string{}
		for name := range response.Header {
			headers[name] = response.Header.Get(name)
		}
		return pluginruntime.FetchResponse{Status: response.StatusCode, Headers: headers, Body: data}, nil
	}
}

func (a *TaskAdaptor) withChannelHeaders(descriptor requestDescriptor) (requestDescriptor, error) {
	// No client-header forwarding here: only explicit channel overrides with
	// the selected channel key. Host/hop-by-hop overrides remain forbidden.
	overrides, err := channel.ResolveHeaderOverride(a.info, nil)
	if err != nil {
		return descriptor, errors.New("invalid script channel header override")
	}
	headers := make(map[string]string, len(descriptor.Headers)+len(overrides))
	for name, value := range descriptor.Headers {
		headers[http.CanonicalHeaderKey(name)] = value
	}
	for name, value := range overrides {
		headers[http.CanonicalHeaderKey(name)] = value
	}
	if err := pluginruntime.ValidateRequestHeaders(headers); err != nil {
		return descriptor, err
	}
	descriptor.Headers = headers
	return descriptor, nil
}

func (a *TaskAdaptor) ConvertToOpenAIVideo(task *model.Task) ([]byte, error) {
	if task == nil {
		return nil, errors.New("task is missing")
	}
	if !task.ResultRetrievable() {
		return nil, model.ErrTaskResultDiscarded
	}
	return common.Marshal(task.ToOpenAIVideo())
}

var _ channel.TaskAdaptor = (*TaskAdaptor)(nil)
var _ channel.OpenAIVideoConverter = (*TaskAdaptor)(nil)
