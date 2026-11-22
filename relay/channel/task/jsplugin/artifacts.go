package jsplugin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/QuantumNous/new-api/model"
	pluginruntime "github.com/QuantumNous/new-api/pkg/jsplugin"
)

var artifactKeyPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._~-]{0,127}$`)

type Artifact struct {
	Key      string `json:"key"`
	Type     string `json:"type"`
	MimeType string `json:"mimeType,omitempty"`
}

type ArtifactClientRequest struct {
	Method  string            `json:"method"`
	Headers map[string]string `json:"headers"`
}

// ContentRequest is executed by the host only after checking task ownership.
// Credentialless requests require the mandatory SSRF transport and must never
// inherit channel headers, cookies, proxy credentials or redirect credentials.
type ContentRequest struct {
	URL            string
	Method         string
	Headers        map[string]string
	Body           []byte
	Credentialless bool
	ContentPath    []any
	MimeType       string
}

func artifactContext(plugin *pluginruntime.LoadedPlugin, task *model.Task) (map[string]any, error) {
	if plugin == nil || task == nil || !task.ResultRetrievable() || task.HasForeignExecutionEvidence() || task.PrivateData.ScriptPluginID <= 0 || task.PrivateData.ScriptPluginDigest != plugin.Digest {
		return nil, errors.New("task artifact revision is unavailable")
	}
	if len(task.Data) != 0 && !json.Valid(task.Data) || task.PrivateData.ScriptPluginState != "" && !json.Valid([]byte(task.PrivateData.ScriptPluginState)) {
		return nil, errors.New("invalid task artifact data or state")
	}
	return map[string]any{"taskId": task.TaskID, "status": string(task.Status), "action": task.Action, "data": orderedJSONValue(task.Data), "state": orderedState(task.PrivateData.ScriptPluginState), "producerVersion": plugin.Meta.Version}, nil
}

func ListArtifacts(ctx context.Context, plugin *pluginruntime.LoadedPlugin, task *model.Task) ([]Artifact, error) {
	hookContext, err := artifactContext(plugin, task)
	if err != nil {
		return nil, err
	}
	if task.Status != model.TaskStatusSuccess || !plugin.Engine.HasExport("listArtifacts") {
		return []Artifact{}, nil
	}
	encoded, err := plugin.Engine.CallJSON(ctx, "listArtifacts", nil, hookContext)
	if err != nil {
		return nil, errors.New("script listArtifacts failed")
	}
	var values []map[string]json.RawMessage
	if json.Unmarshal(encoded, &values) != nil || values == nil || len(values) > 64 {
		return nil, errors.New("script artifacts must be an array of at most 64 entries")
	}
	artifacts := make([]Artifact, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		for name := range value {
			if name != "key" && name != "type" && name != "mimeType" {
				return nil, errors.New("unknown script artifact field")
			}
		}
		var artifact Artifact
		if json.Unmarshal(value["key"], &artifact.Key) != nil || json.Unmarshal(value["type"], &artifact.Type) != nil || !artifactKeyPattern.MatchString(artifact.Key) || seen[artifact.Key] {
			return nil, errors.New("invalid or duplicate script artifact key")
		}
		if artifact.Type != "video" && artifact.Type != "audio" && artifact.Type != "image" && artifact.Type != "file" {
			return nil, errors.New("invalid script artifact type")
		}
		if raw, exists := value["mimeType"]; exists && (string(raw) == "null" || json.Unmarshal(raw, &artifact.MimeType) != nil) {
			return nil, errors.New("invalid script artifact mimeType")
		}
		if len(artifact.MimeType) > 255 || strings.ContainsAny(artifact.MimeType, "\r\n\x00") {
			return nil, errors.New("invalid script artifact mimeType")
		}
		seen[artifact.Key] = true
		artifacts = append(artifacts, artifact)
	}
	return artifacts, nil
}

func ArtifactURLs(taskID string, artifacts []Artifact) map[string]any {
	result := make(map[string]any, len(artifacts))
	for _, artifact := range artifacts {
		value := map[string]any{"key": artifact.Key, "type": artifact.Type, "url": "/api/task/self/" + url.PathEscape(taskID) + "/artifacts/" + url.PathEscape(artifact.Key) + "/content"}
		if artifact.MimeType != "" {
			value["mimeType"] = artifact.MimeType
		}
		result[artifact.Key] = value
	}
	return result
}

func (a *TaskAdaptor) ListArtifacts(task *model.Task) ([]Artifact, error) {
	if task == nil || task.PrivateData.ScriptPluginID != a.RevisionID {
		return nil, errors.New("task artifact revision mismatch")
	}
	return ListArtifacts(a.ctx, a.plugin, task)
}

func (a *TaskAdaptor) BuildContentRequest(ctx context.Context, task *model.Task, artifactKey string, clientRequest ArtifactClientRequest) (*ContentRequest, error) {
	if a.info == nil || task == nil || task.ChannelId != a.info.ChannelId || task.PrivateData.ScriptPluginID != a.RevisionID || !artifactKeyPattern.MatchString(artifactKey) {
		return nil, errors.New("task artifact channel or revision mismatch")
	}
	artifacts, err := ListArtifacts(ctx, a.plugin, task)
	if err != nil {
		return nil, err
	}
	found := false
	for _, artifact := range artifacts {
		found = found || artifact.Key == artifactKey
	}
	if !found {
		return nil, errors.New("task artifact is unavailable")
	}
	hookContext, err := artifactContext(a.plugin, task)
	if err != nil {
		return nil, err
	}
	headers := map[string]string{}
	for name, value := range clientRequest.Headers {
		switch http.CanonicalHeaderKey(name) {
		case "Range", "If-Range", "If-Match", "If-None-Match", "If-Modified-Since", "If-Unmodified-Since":
			if len(value) <= 8192 && !strings.ContainsAny(value, "\r\n\x00") {
				headers[http.CanonicalHeaderKey(name)] = value
			}
		}
	}
	clientRequest.Headers = headers
	hookContext["upstreamTaskId"], hookContext["artifactKey"] = task.GetUpstreamTaskID(), artifactKey
	hookContext["baseUrl"], hookContext["clientRequest"] = a.info.ChannelBaseUrl, clientRequest
	if err = a.credentials(hookContext, a.info.ApiKey, a.info.ChannelSetting.Proxy); err != nil {
		return nil, err
	}
	descriptor, err := a.callDescriptor(pluginruntime.WithFetcher(ctx, a.hookFetcher(a.info.ChannelBaseUrl, a.info.ChannelSetting.Proxy)), "buildContentRequest", hookContext)
	if err != nil {
		return nil, errors.New("script buildContentRequest failed")
	}
	method := strings.ToUpper(strings.TrimSpace(descriptor.Method))
	if method == "" {
		method = strings.ToUpper(clientRequest.Method)
	}
	if method == "" {
		method = http.MethodGet
	}
	if method != http.MethodGet && method != http.MethodHead && method != http.MethodPost {
		return nil, errors.New("invalid artifact request method")
	}
	if descriptor.BodyType != "" && descriptor.BodyType != "json" || len(descriptor.Parts) != 0 || descriptor.ResponseType != "" && descriptor.ResponseType != "json" {
		return nil, errors.New("invalid artifact request encoding")
	}
	if descriptor.Credentialless {
		u, parseErr := url.Parse(descriptor.URL)
		if parseErr != nil || u.Hostname() == "" || u.User != nil || u.Fragment != "" || u.Scheme != "http" && u.Scheme != "https" || len(descriptor.URL) > 8192 || method == http.MethodPost || len(descriptor.Headers) != 0 || descriptor.Body != nil {
			return nil, errors.New("invalid credentialless artifact request")
		}
	} else {
		if err = pluginruntime.ValidateRequestURL(descriptor.URL, a.info.ChannelBaseUrl, a.plugin.Meta.AllowedHosts); err != nil {
			return nil, err
		}
		if descriptor, err = a.withChannelHeaders(descriptor); err != nil {
			return nil, err
		}
	}
	if err = pluginruntime.ValidateJSONPath(descriptor.ContentPath); err != nil {
		return nil, err
	}
	if len(descriptor.MimeType) > 255 || strings.ContainsAny(descriptor.MimeType, "\r\n\x00") {
		return nil, errors.New("invalid artifact mimeType")
	}
	body := descriptor.bodyJSON
	if descriptor.Body == nil {
		body = nil
	} else if text, ok := descriptor.Body.(string); ok {
		body = []byte(text)
	}
	if len(body) > maxJSONBytes {
		return nil, errors.New("artifact body exceeds size limit")
	}
	return &ContentRequest{URL: descriptor.URL, Method: method, Headers: descriptor.Headers, Body: body, Credentialless: descriptor.Credentialless, ContentPath: descriptor.ContentPath, MimeType: descriptor.MimeType}, nil
}
