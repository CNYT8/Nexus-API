package jsplugin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/QuantumNous/new-api/common"
)

// The driver contract follows upstream API v1. Nexus owns routing, pricing and
// storage; unsupported required capabilities fail installation explicitly.
type Meta struct {
	APIVersion           int                         `json:"apiVersion"`
	Key                  string                      `json:"key"`
	Name                 string                      `json:"name"`
	Version              string                      `json:"version"`
	Author               map[string]any              `json:"author"`
	Description          any                         `json:"description,omitempty"`
	Models               []string                    `json:"models"`
	FetchMode            string                      `json:"fetchMode"`
	AllowedHosts         []string                    `json:"allowedHosts"`
	ChannelTypes         []int                       `json:"channelTypes,omitempty"`
	Auth                 AuthMeta                    `json:"auth"`
	RequiredCapabilities []string                    `json:"requiredCapabilities,omitempty"`
	SubmitResponseTypes  []string                    `json:"submitResponseTypes,omitempty"`
	Routes               []Route                     `json:"routes,omitempty"`
	Protocols            []any                       `json:"protocols,omitempty"`
	UsageSchema          map[string]UsageFieldSchema `json:"usageSchema,omitempty"`
	UsageProfiles        []UsageProfile              `json:"usageProfiles,omitempty"`
}
type AuthMeta struct {
	Type string `json:"type"`
}

func (a *AuthMeta) UnmarshalJSON(data []byte) error {
	if len(data) > 0 && data[0] == '"' {
		return common.Unmarshal(data, &a.Type)
	}
	type plain AuthMeta
	return common.Unmarshal(data, (*plain)(a))
}

type UsageFieldSchema struct {
	Type        string         `json:"type,omitempty"`
	Unit        string         `json:"unit,omitempty"`
	Enum        []string       `json:"enum,omitempty"`
	Description any            `json:"description,omitempty"`
	UnitLabel   any            `json:"unitLabel,omitempty"`
	EnumLabels  map[string]any `json:"enumLabels,omitempty"`
}
type UsageProfile struct {
	Models []string                    `json:"models"`
	Schema map[string]UsageFieldSchema `json:"schema"`
}
type Route struct {
	Method       string   `json:"method"`
	Path         string   `json:"path"`
	Type         string   `json:"type"`
	Action       string   `json:"action,omitempty"`
	Decode       string   `json:"decode,omitempty"`
	Render       string   `json:"render,omitempty"`
	TaskIDParam  string   `json:"taskIdParam,omitempty"`
	RetainResult *bool    `json:"retainResult,omitempty"`
	Models       []string `json:"models,omitempty"`
}
type LoadedPlugin struct {
	Meta   Meta
	Engine *Engine
	Digest string
}

var pluginKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,29}$`)
var pluginVersionPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$`)

func ValidPluginKey(key string) bool {
	return pluginKeyPattern.MatchString(key) && key != "suno" && key != "mj"
}

func CompilePlugin(ctx context.Context, source string, options Options) (*LoadedPlugin, error) {
	engine, err := Compile(source, options)
	if err != nil {
		return nil, err
	}
	value, err := engine.Export(ctx, "meta")
	if err != nil {
		return nil, err
	}
	encoded, err := common.Marshal(value)
	if err != nil {
		return nil, err
	}
	var meta Meta
	if err = common.Unmarshal(encoded, &meta); err != nil {
		return nil, fmt.Errorf("invalid plugin meta: %w", err)
	}
	if meta.APIVersion != 1 || !ValidPluginKey(meta.Key) || !pluginVersionPattern.MatchString(meta.Version) || strings.TrimSpace(meta.Name) == "" {
		return nil, errors.New("plugin requires apiVersion=1, a non-reserved key, name and semantic version")
	}
	if options.Key != "" && options.Key != meta.Key {
		return nil, errors.New("plugin key differs from installed key")
	}
	if options.Version != "" && options.Version != meta.Version {
		return nil, errors.New("plugin version differs from installed version")
	}
	if len(meta.Models) == 0 || len(meta.Models) > 256 {
		return nil, errors.New("plugin must declare 1..256 models")
	}
	seen := map[string]bool{}
	for _, name := range meta.Models {
		if strings.TrimSpace(name) != name || name == "" || len(name) > 255 || seen[name] || strings.ContainsAny(name, "\r\n\x00") {
			return nil, errors.New("invalid or duplicate plugin model")
		}
		seen[name] = true
	}
	if len(meta.AllowedHosts) > 32 {
		return nil, errors.New("at most 32 allowed hosts")
	}
	for _, host := range meta.AllowedHosts {
		u, err := url.Parse("https://" + host)
		if err != nil || u.Host != host || u.Hostname() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || strings.Contains(host, "*") {
			return nil, errors.New("allowedHosts must contain exact host names, optionally with ports")
		}
	}
	if meta.Auth.Type == "vertex_oauth" {
		meta.Auth.Type = "oauth2_jwt"
	}
	switch meta.Auth.Type {
	case "", "none", "api_key", "oauth2_jwt":
	default:
		return nil, errors.New("unsupported plugin auth type")
	}
	capabilities := map[string]bool{}
	for _, capability := range meta.RequiredCapabilities {
		if !HasCapability(capability) || capabilities[capability] {
			return nil, fmt.Errorf("unsupported or duplicate required capability %q", capability)
		}
		capabilities[capability] = true
	}
	responses := map[string]bool{}
	for _, responseType := range meta.SubmitResponseTypes {
		if responseType != "json" && responseType != "sse" || responses[responseType] {
			return nil, fmt.Errorf("unsupported or duplicate submit response type %q", responseType)
		}
		responses[responseType] = true
	}
	required := []string{"buildSubmitRequest", "parseSubmitResponse"}
	switch meta.FetchMode {
	case "", "single", "per_task":
		meta.FetchMode = "per_task"
		required = append(required, "buildQueryRequest", "parseTaskResult")
	case "batch":
		required = append(required, "buildBatchQueryRequest", "parseBatchResult")
	default:
		return nil, errors.New("fetchMode must be per_task or batch")
	}
	if responses["sse"] {
		if capabilities[CapabilitySubmitSSEDelta] || !engine.HasExport("parseSubmitEvent") {
			required = append(required, "parseSubmitEventDelta")
		} else {
			required = append(required, "parseSubmitEvent")
		}
	}
	if capabilities[CapabilitySubmitSSEDelta] && !responses["sse"] {
		return nil, errors.New("submit-sse-delta@1 requires an SSE submit response")
	}
	for _, name := range required {
		callable, err := engine.HasCallablePath(ctx, name)
		if err != nil {
			return nil, err
		}
		if !callable {
			return nil, fmt.Errorf("plugin hook %q must be callable", name)
		}
	}
	if err := validatePresentation(ctx, engine, meta); err != nil {
		return nil, err
	}
	for _, name := range []string{"parseSubmitEvent", "parseSubmitEventDelta", "listArtifacts", "buildContentRequest"} {
		if engine.HasExport(name) {
			if callable, err := engine.HasCallablePath(ctx, name); err != nil || !callable {
				return nil, fmt.Errorf("plugin hook %q must be callable", name)
			}
		}
	}
	if engine.HasExport("listArtifacts") != engine.HasExport("buildContentRequest") {
		return nil, errors.New("artifact hooks must be declared together")
	}
	if err := validateUsageHooks(ctx, engine, meta); err != nil {
		return nil, err
	}
	digest := sha256.Sum256([]byte(source))
	engine.key, engine.version = meta.Key, meta.Version
	return &LoadedPlugin{Meta: meta, Engine: engine, Digest: hex.EncodeToString(digest[:])}, nil
}
func (m Meta) SupportsModel(name string) bool {
	for _, declared := range m.Models {
		if declared == name {
			return true
		}
	}
	return false
}
func (m Meta) UsageForModels(names ...string) map[string]UsageFieldSchema {
	for _, name := range names {
		for _, profile := range m.UsageProfiles {
			for _, declared := range profile.Models {
				if strings.EqualFold(declared, name) {
					return profile.Schema
				}
			}
		}
	}
	return m.UsageSchema
}
