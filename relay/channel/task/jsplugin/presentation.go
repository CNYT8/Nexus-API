package jsplugin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	pluginruntime "github.com/QuantumNous/new-api/pkg/jsplugin"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

const preparedIntentKey = "script_plugin_prepared_intent"
const DeferredPresentationKey = "script_plugin_deferred_presentation"

type Presentation struct {
	RevisionID int64
	Digest     string
	Plugin     *pluginruntime.LoadedPlugin
	Route      pluginruntime.Route
	Protocol   string
	Intent     pluginruntime.SubmitIntent
	Context    map[string]any
	Stream     bool
	Background bool
}

func PreparedIntent(c *gin.Context) (*Presentation, bool) {
	value, exists := c.Get(preparedIntentKey)
	prepared, ok := value.(*Presentation)
	return prepared, exists && ok && prepared != nil
}

// MatchNativeRoute does not register or shadow host routes. The router chooses
// the native namespace and invokes this only after normal token authentication.
func MatchNativeRoute(meta pluginruntime.Meta, method, path string) (pluginruntime.Route, gin.Params, bool) {
	for _, route := range meta.Routes {
		if route.Method != method {
			continue
		}
		pattern, actual := strings.Split(route.Path, "/"), strings.Split(path, "/")
		if len(pattern) != len(actual) {
			continue
		}
		params, matched := gin.Params{}, true
		for i, segment := range pattern {
			if strings.HasPrefix(segment, ":") {
				if actual[i] == "" {
					matched = false
					break
				}
				params = append(params, gin.Param{Key: segment[1:], Value: actual[i]})
			} else if segment != actual[i] {
				matched = false
				break
			}
		}
		if matched {
			return route, params, true
		}
	}
	return pluginruntime.Route{}, nil, false
}

func presentationContext(c *gin.Context, input *Input) map[string]any {
	params, headers := map[string]string{}, map[string]string{}
	for _, param := range c.Params {
		params[param.Key] = param.Value
	}
	for _, name := range []string{"Accept", "Content-Type", "Range", "If-Range", "If-None-Match"} {
		if value := c.GetHeader(name); value != "" {
			headers[name] = value
		}
	}
	body := map[string]any{"kind": "json", "value": input.Body}
	contentType := strings.ToLower(c.GetHeader("Content-Type"))
	if strings.HasPrefix(contentType, "multipart/form-data") || strings.HasPrefix(contentType, "application/x-www-form-urlencoded") {
		fields := map[string][]string{}
		for key, raw := range input.Object {
			switch value := raw.(type) {
			case string:
				fields[key] = []string{value}
			case []any:
				for _, item := range value {
					text, _ := item.(string)
					fields[key] = append(fields[key], text)
				}
			}
		}
		body = map[string]any{"kind": "multipart", "fields": fields, "files": input.Files}
	}
	return map[string]any{"method": c.Request.Method, "path": c.Request.URL.Path, "params": params, "query": c.Request.URL.Query(), "headers": headers, "body": body}
}

// PreparePresentation runs decoders without credentials or network access.
// Main must feed Intent.Model through its normal token/model/channel checks.
func PreparePresentation(c *gin.Context, revisionID int64, plugin *pluginruntime.LoadedPlugin, route pluginruntime.Route, protocol string) (*Presentation, error) {
	input, err := ReadInput(c)
	if err != nil {
		return nil, err
	}
	prepared := &Presentation{RevisionID: revisionID, Digest: plugin.Digest, Plugin: plugin, Route: route, Protocol: protocol, Context: presentationContext(c, input)}
	prepared.Stream, _ = input.Object["stream"].(bool)
	prepared.Background, _ = input.Object["background"].(bool)
	prepared.Context["protocol"], prepared.Context["stream"] = protocol, prepared.Stream
	var encoded pluginruntime.RawJSON
	if protocol != "" {
		encoded, err = plugin.Engine.CallJSON(c.Request.Context(), "protocols", []string{protocol, "decodeRequest"}, prepared.Context)
	} else if route.Decode != "" {
		encoded, err = plugin.Engine.CallJSON(c.Request.Context(), "native", []string{route.Decode}, prepared.Context)
	} else {
		parameter := route.TaskIDParam
		if parameter == "" {
			parameter = "task_id"
		}
		prepared.Intent = pluginruntime.SubmitIntent{Kind: "query", TaskIDs: []string{c.Param(parameter)}}
	}
	if err != nil {
		return nil, errors.New("script request decoder failed")
	}
	if len(encoded) != 0 {
		if err = common.Unmarshal(encoded, &prepared.Intent); err != nil {
			return nil, errors.New("invalid script request intent")
		}
		var fields map[string]json.RawMessage
		if err = json.Unmarshal(encoded, &fields); err != nil {
			return nil, err
		}
		if body, ok := fields["requestBody"]; ok {
			prepared.Intent.RequestBody = pluginruntime.RawJSON(body)
		}
	}
	intent := prepared.Intent
	switch intent.Kind {
	case "submit":
		if !plugin.Meta.SupportsModel(intent.Model) || intent.RequestBody == nil || len(intent.Action) > 40 || strings.ContainsAny(intent.Action, "\r\n\x00") || len(intent.TaskIDs) != 0 || len(intent.OriginTaskIDs) > 16 {
			return nil, errors.New("invalid script submit intent")
		}
		if protocol != "" && !plugin.Meta.ClaimsProtocol(protocol, intent.Model) {
			return nil, errors.New("protocol does not support this model")
		}
		if len(route.Models) != 0 {
			allowed := false
			for _, name := range route.Models {
				allowed = allowed || name == intent.Model
			}
			if !allowed {
				return nil, errors.New("native route does not support this model")
			}
		}
		if protocol == "openai_responses" {
			mode := "sync"
			if prepared.Background {
				mode = "background"
			} else if prepared.Stream {
				mode = "stream"
			}
			claims, _ := plugin.Meta.ProtocolClaims()
			allowed := false
			for _, claim := range claims {
				if claim.Name == protocol {
					for _, supported := range claim.Supports {
						allowed = allowed || supported == mode
					}
				}
			}
			if !allowed {
				return nil, errors.New("unsupported response protocol mode")
			}
		}
	case "query":
		if protocol != "" || route.Type == "submit" || len(intent.TaskIDs) == 0 || len(intent.TaskIDs) > 100 {
			return nil, errors.New("invalid script query intent")
		}
	default:
		return nil, errors.New("unknown script request intent")
	}
	seen := map[string]bool{}
	for _, id := range append(append([]string{}, intent.TaskIDs...), intent.OriginTaskIDs...) {
		if id == "" || len(id) > 191 || strings.ContainsAny(id, "\r\n\x00") || seen[id] {
			return nil, errors.New("invalid or duplicate script task ID")
		}
		seen[id] = true
	}
	c.Set(preparedIntentKey, prepared)
	return prepared, nil
}

func (a *TaskAdaptor) originViews(c *gin.Context, info *relaycommon.RelayInfo) ([]any, error) {
	prepared, ok := PreparedIntent(c)
	if !ok {
		return []any{}, nil
	}
	views := make([]any, 0, len(prepared.Intent.OriginTaskIDs))
	for _, id := range prepared.Intent.OriginTaskIDs {
		task, exists, err := model.GetByTaskId(info.UserId, id)
		if err != nil || !exists || task == nil || task.UserId != info.UserId || task.ChannelId != info.ChannelId || task.HasForeignExecutionEvidence() || !task.ResultRetrievable() || task.PrivateData.ScriptPluginID != a.RevisionID || task.PrivateData.ScriptPluginDigest != a.plugin.Digest {
			return nil, errors.New("source task is unavailable for this channel and revision")
		}
		views = append(views, map[string]any{"taskId": task.TaskID, "upstreamTaskId": task.GetUpstreamTaskID(), "action": task.Action, "model": task.Properties.OriginModelName, "data": orderedJSONValue(task.Data), "state": orderedState(task.PrivateData.ScriptPluginState)})
	}
	return views, nil
}

func orderedState(state string) any {
	if state == "" {
		return nil
	}
	return orderedJSONValue([]byte(state))
}

// TaskView deliberately excludes channel keys, billing context and plugin state.
func TaskView(task *model.Task, version string) map[string]any {
	return map[string]any{"task_id": task.TaskID, "status": task.Status, "action": task.Action, "progress": task.Progress, "fail_reason": task.FailReason, "submit_time": task.SubmitTime, "start_time": task.StartTime, "finish_time": task.FinishTime, "created_at": task.CreatedAt, "data": orderedJSONValue(task.Data), "producerVersion": version}
}

func RenderPresentation(ctx context.Context, prepared *Presentation, task *model.Task) (pluginruntime.NativeReply, error) {
	if task == nil || !task.ResultRetrievable() || task.PrivateData.ScriptPluginID != prepared.RevisionID || task.PrivateData.ScriptPluginDigest != prepared.Digest {
		return pluginruntime.NativeReply{}, errors.New("task revision differs from presenter")
	}
	view := TaskView(task, prepared.Plugin.Meta.Version)
	renderer := make(map[string]any, len(prepared.Context)+2)
	for name, value := range prepared.Context {
		renderer[name] = value
	}
	artifacts, err := ListArtifacts(ctx, prepared.Plugin, task)
	if err != nil {
		return pluginruntime.NativeReply{}, err
	}
	renderer["artifacts"] = ArtifactURLs(task.TaskID, artifacts)
	root, members := "native", []string{prepared.Route.Render}
	if prepared.Protocol != "" {
		root, members = "protocols", []string{prepared.Protocol, "render"}
		if prepared.Protocol == "openai_responses" {
			members[1] = "renderFinal"
		}
	}
	encoded, err := prepared.Plugin.Engine.CallJSON(ctx, root, members, renderer, view)
	if err != nil {
		return pluginruntime.NativeReply{}, errors.New("script presentation failed")
	}
	var value any
	if err = common.Unmarshal(encoded, &value); err != nil {
		return pluginruntime.NativeReply{}, err
	}
	reply, err := pluginruntime.ParseNativeReply(value)
	if err != nil {
		return reply, err
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(encoded, &fields) == nil && fields["__body"] != nil {
		reply.Body = pluginruntime.RawJSON(fields["__body"])
	} else {
		reply.Body = encoded
	}
	return reply, nil
}

func WritePresentation(c *gin.Context, reply pluginruntime.NativeReply) {
	for name, value := range reply.Headers {
		c.Header(name, value)
	}
	c.Header("Cache-Control", "private, no-store")
	c.JSON(reply.Status, reply.Body)
}

func (a *TaskAdaptor) presentSubmit(c *gin.Context, info *relaycommon.RelayInfo, upstreamID string, data []byte) error {
	prepared, ok := PreparedIntent(c)
	if !ok || c.Writer.Written() {
		return nil
	}
	if prepared.Protocol == "openai_image" || prepared.Protocol == "openai_responses" {
		// The controller observes the persisted task only after the ordinary
		// submission path has completed storage and billing transitions.
		c.Set(DeferredPresentationKey, true)
		return nil
	}
	task := &model.Task{TaskID: info.PublicTaskID, Action: info.Action, Status: model.TaskStatusSubmitted, Data: data, SubmitTime: time.Now().Unix()}
	task.PrivateData.ScriptPluginID, task.PrivateData.ScriptPluginDigest = a.RevisionID, a.plugin.Digest
	if a.immediate != nil {
		task.Status = model.TaskStatus(a.immediate.Status)
		task.Progress = a.immediate.Progress
	}
	reply, err := RenderPresentation(c.Request.Context(), prepared, task)
	if err != nil {
		return err
	}
	WritePresentation(c, reply)
	return nil
}

// AcceptedSubmission prohibits retry after accepting a provider event stream.
func AcceptedSubmission(c *gin.Context) bool { return c.GetBool(AcceptedSubmitStreamKey) }

var _ = http.StatusOK
var _ = service.LoadScriptPlugin
