package dto

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"sync"
)

type ChannelSettings struct {
	ResponsesWebSocketEnabled bool   `json:"responses_websocket_enabled,omitempty"`
	ForceFormat               bool   `json:"force_format,omitempty"`
	ThinkingToContent         bool   `json:"thinking_to_content,omitempty"`
	ThinkingProcessStrip      bool   `json:"thinking_process_strip,omitempty"`
	Proxy                     string `json:"proxy"`
	HTTPProtocol              string `json:"http_protocol,omitempty"`
	HTTP2ConnectionShards     int    `json:"http2_connection_shards,omitempty"`
	TLSInsecureSkipVerify     bool   `json:"tls_insecure_skip_verify,omitempty"`
	PassThroughBodyEnabled    bool   `json:"pass_through_body_enabled,omitempty"`
	SystemPrompt              string `json:"system_prompt,omitempty"`
	SystemPromptOverride      bool   `json:"system_prompt_override,omitempty"`
}

const (
	HTTPProtocolAuto         = "auto"
	HTTPProtocolHTTP1        = "http1"
	MaxHTTP2ConnectionShards = 8
)

func (s ChannelSettings) ValidateHTTPTransport() error {
	switch strings.ToLower(strings.TrimSpace(s.HTTPProtocol)) {
	case "", HTTPProtocolAuto, HTTPProtocolHTTP1:
	default:
		return fmt.Errorf("http_protocol must be one of %q or %q", HTTPProtocolAuto, HTTPProtocolHTTP1)
	}
	if s.HTTP2ConnectionShards < 0 || s.HTTP2ConnectionShards > MaxHTTP2ConnectionShards {
		return fmt.Errorf("http2_connection_shards must be between 0 and %d", MaxHTTP2ConnectionShards)
	}
	return nil
}

type VertexKeyType string

const (
	VertexKeyTypeJSON   VertexKeyType = "json"
	VertexKeyTypeAPIKey VertexKeyType = "api_key"
)

type VertexAPIVersion string

const (
	VertexAPIVersionV1      VertexAPIVersion = "v1"
	VertexAPIVersionV1Beta1 VertexAPIVersion = "v1beta1"
)

type AwsKeyType string

const (
	AwsKeyTypeAKSK   AwsKeyType = "ak_sk" // 默认
	AwsKeyTypeApiKey AwsKeyType = "api_key"
)

type AwsApiType string

const (
	AwsApiTypeInvokeModel AwsApiType = "invoke_model"
	AwsApiTypeMantle      AwsApiType = "mantle"
)

type ChannelOtherSettings struct {
	AzureResponsesVersion                 string                `json:"azure_responses_version,omitempty"`
	VertexKeyType                         VertexKeyType         `json:"vertex_key_type,omitempty"`    // "json" or "api_key"
	VertexProjectID                       string                `json:"vertex_project_id,omitempty"`  // 仅当 vertex_key_type=api_key 时使用；JSON 模式下从 service account 解析
	VertexAPIVersion                      VertexAPIVersion      `json:"vertex_api_version,omitempty"` // "v1" or "v1beta1"
	OpenRouterEnterprise                  *bool                 `json:"openrouter_enterprise,omitempty"`
	ClaudeBetaQuery                       bool                  `json:"claude_beta_query,omitempty"`         // Claude 渠道是否强制追加 ?beta=true
	AllowServiceTier                      bool                  `json:"allow_service_tier,omitempty"`        // 是否允许 service_tier 透传（默认过滤以避免额外计费）
	AllowInferenceGeo                     bool                  `json:"allow_inference_geo,omitempty"`       // 是否允许 inference_geo 透传（仅 Claude，默认过滤以满足数据驻留合规
	AllowSpeed                            bool                  `json:"allow_speed,omitempty"`               // 是否允许 speed 透传（仅 Claude，默认过滤以避免意外切换推理速度模式）
	AllowSafetyIdentifier                 bool                  `json:"allow_safety_identifier,omitempty"`   // 是否允许 safety_identifier 透传（默认过滤以保护用户隐私）
	DisableStore                          bool                  `json:"disable_store,omitempty"`             // 是否禁用 store 透传（默认允许透传，禁用后可能导致 Codex 无法使用）
	AllowIncludeObfuscation               bool                  `json:"allow_include_obfuscation,omitempty"` // 是否允许 stream_options.include_obfuscation 透传（默认过滤以避免关闭流混淆保护）
	AwsKeyType                            AwsKeyType            `json:"aws_key_type,omitempty"`
	AwsApiType                            AwsApiType            `json:"aws_api_type,omitempty"`
	UpstreamModelUpdateCheckEnabled       bool                  `json:"upstream_model_update_check_enabled,omitempty"`        // 是否检测上游模型更新
	UpstreamModelUpdateAutoSyncEnabled    bool                  `json:"upstream_model_update_auto_sync_enabled,omitempty"`    // 是否自动同步上游模型更新
	UpstreamModelUpdateLastCheckTime      int64                 `json:"upstream_model_update_last_check_time,omitempty"`      // 上次检测时间
	UpstreamModelUpdateLastDetectedModels []string              `json:"upstream_model_update_last_detected_models,omitempty"` // 上次检测到的可加入模型
	UpstreamModelUpdateLastRemovedModels  []string              `json:"upstream_model_update_last_removed_models,omitempty"`  // 上次检测到的可删除模型
	UpstreamModelUpdateIgnoredModels      []string              `json:"upstream_model_update_ignored_models,omitempty"`       // 手动忽略的模型
	AdvancedCustom                        *AdvancedCustomConfig `json:"advanced_custom,omitempty"`
}

func (s *ChannelOtherSettings) IsOpenRouterEnterprise() bool {
	if s == nil || s.OpenRouterEnterprise == nil {
		return false
	}
	return *s.OpenRouterEnterprise
}

const (
	AdvancedCustomConverterNone                                         = "none"
	AdvancedCustomConverterAnthropicMessagesToOpenAIChatCompletions     = "anthropic_messages_to_openai_chat_completions"
	AdvancedCustomConverterOpenAIChatCompletionsToAnthropicMessages     = "openai_chat_completions_to_anthropic_messages"
	AdvancedCustomConverterOpenAIChatCompletionsToOpenAIResponses       = "openai_chat_completions_to_openai_responses"
	AdvancedCustomConverterGeminiGenerateContentToOpenAIChatCompletions = "gemini_generate_content_to_openai_chat_completions"
	AdvancedCustomConverterOpenAIChatCompletionsToGeminiGenerateContent = "openai_chat_completions_to_gemini_generate_content"
	AdvancedCustomConverterOpenAIResponsesToOpenAIChatCompletions       = "openai_responses_to_openai_chat_completions"
	AdvancedCustomConverterOpenAIResponsesToAnthropicMessages           = "openai_responses_to_anthropic_messages"
	AdvancedCustomConverterOpenAIResponsesToGeminiGenerateContent       = "openai_responses_to_gemini_generate_content"
)

const (
	AdvancedCustomAuthTypeNone   = "none"
	AdvancedCustomAuthTypeHeader = "header"
	AdvancedCustomAuthTypeQuery  = "query"
)

type AdvancedCustomConfig struct {
	Routes []AdvancedCustomRoute `json:"advanced_routes,omitempty"`
}

type AdvancedCustomRoute struct {
	IncomingPath           string                   `json:"incoming_path,omitempty"`
	Models                 []string                 `json:"models,omitempty"`
	UpstreamPath           string                   `json:"upstream_path,omitempty"`
	Converter              string                   `json:"converter,omitempty"`
	Auth                   *AdvancedCustomRouteAuth `json:"auth,omitempty"`
	PassThroughBodyEnabled bool                     `json:"pass_through_body_enabled,omitempty"`
}

func (r AdvancedCustomRoute) SupportsPassThroughBody() bool {
	converter := strings.TrimSpace(r.Converter)
	return converter == "" || converter == AdvancedCustomConverterNone
}

type AdvancedCustomRouteAuth struct {
	Type  string `json:"type,omitempty"`
	Name  string `json:"name,omitempty"`
	Value string `json:"value,omitempty"`
}

const advancedCustomModelPlaceholder = "{model}"

// AdvancedCustomModelListPath identifies the optional OpenAI Models discovery route.
const AdvancedCustomModelListPath = "/v1/models"

// MatchPath returns the first route whose IncomingPath matches requestPath.
// Matching mirrors the relay adaptor: exact match, {model} placeholder, and
// :generateContent <-> :streamGenerateContent equivalence.
func (c *AdvancedCustomConfig) MatchPath(requestPath string) (AdvancedCustomRoute, bool) {
	if c == nil {
		return AdvancedCustomRoute{}, false
	}
	for _, route := range c.Routes {
		if matchAdvancedCustomIncomingPath(strings.TrimSpace(route.IncomingPath), requestPath) {
			return route, true
		}
	}
	return AdvancedCustomRoute{}, false
}

// MatchPathForModel matches the original client model, before model mapping.
// Empty models is a fallback; re: rules use Go's bounded-time regexp engine.
func (c *AdvancedCustomConfig) MatchPathForModel(requestPath string, model string) (AdvancedCustomRoute, bool) {
	if c == nil {
		return AdvancedCustomRoute{}, false
	}
	model = strings.TrimSpace(model)
	for _, route := range c.Routes {
		if !matchAdvancedCustomIncomingPath(strings.TrimSpace(route.IncomingPath), requestPath) {
			continue
		}
		models := normalizeAdvancedCustomRouteModels(route.Models)
		if len(models) == 0 {
			return route, true
		}
		for _, rule := range models {
			if strings.HasPrefix(rule, "re:") {
				re := compileAdvancedCustomModelRegex(strings.TrimPrefix(rule, "re:"))
				if re != nil && re.MatchString(model) {
					return route, true
				}
			} else if rule == model {
				return route, true
			}
		}
	}
	return AdvancedCustomRoute{}, false
}

var advancedCustomModelRegexCache sync.Map

func compileAdvancedCustomModelRegex(pattern string) *regexp.Regexp {
	if cached, ok := advancedCustomModelRegexCache.Load(pattern); ok {
		re, _ := cached.(*regexp.Regexp)
		return re
	}
	re, err := regexp.Compile(pattern)
	if err != nil || pattern == "" {
		re = nil
	}
	advancedCustomModelRegexCache.Store(pattern, re)
	return re
}

func normalizeAdvancedCustomRouteModels(models []string) []string {
	normalized := make([]string, 0, len(models))
	for _, model := range models {
		if model = strings.TrimSpace(model); model != "" {
			normalized = append(normalized, model)
		}
	}
	return normalized
}

// ModelListRoute returns the explicitly configured OpenAI Models discovery route.
func (c *AdvancedCustomConfig) ModelListRoute() (AdvancedCustomRoute, bool) {
	if c == nil {
		return AdvancedCustomRoute{}, false
	}
	for _, route := range c.Routes {
		if strings.TrimSpace(route.IncomingPath) == AdvancedCustomModelListPath {
			return route, true
		}
	}
	return AdvancedCustomRoute{}, false
}

// SupportsPath reports whether any route matches requestPath.
func (c *AdvancedCustomConfig) SupportsPath(requestPath string) bool {
	_, ok := c.MatchPath(requestPath)
	return ok
}

func matchAdvancedCustomIncomingPath(configuredPath string, requestPath string) bool {
	if matchAdvancedCustomIncomingPathTemplate(configuredPath, requestPath) {
		return true
	}
	if strings.Contains(configuredPath, ":generateContent") {
		streamPath := strings.Replace(configuredPath, ":generateContent", ":streamGenerateContent", 1)
		return matchAdvancedCustomIncomingPathTemplate(streamPath, requestPath)
	}
	return false
}

func matchAdvancedCustomIncomingPathTemplate(configuredPath string, requestPath string) bool {
	if !strings.Contains(configuredPath, advancedCustomModelPlaceholder) {
		return configuredPath == requestPath
	}

	parts := strings.Split(configuredPath, advancedCustomModelPlaceholder)
	if len(parts) != 2 {
		return false
	}
	if !strings.HasPrefix(requestPath, parts[0]) || !strings.HasSuffix(requestPath, parts[1]) {
		return false
	}

	model := strings.TrimSuffix(strings.TrimPrefix(requestPath, parts[0]), parts[1])
	return model != "" && !strings.Contains(model, "/")
}

func IsAdvancedCustomConverterAllowed(converter string) bool {
	switch converter {
	case AdvancedCustomConverterNone,
		AdvancedCustomConverterAnthropicMessagesToOpenAIChatCompletions,
		AdvancedCustomConverterOpenAIChatCompletionsToAnthropicMessages,
		AdvancedCustomConverterOpenAIChatCompletionsToOpenAIResponses,
		AdvancedCustomConverterGeminiGenerateContentToOpenAIChatCompletions,
		AdvancedCustomConverterOpenAIChatCompletionsToGeminiGenerateContent,
		AdvancedCustomConverterOpenAIResponsesToOpenAIChatCompletions,
		AdvancedCustomConverterOpenAIResponsesToAnthropicMessages,
		AdvancedCustomConverterOpenAIResponsesToGeminiGenerateContent:
		return true
	default:
		return false
	}
}

func (c *AdvancedCustomConfig) Validate() error {
	if c == nil {
		return fmt.Errorf("advanced_custom is required")
	}
	if len(c.Routes) == 0 {
		return fmt.Errorf("advanced_custom requires at least one route")
	}

	pathModels := make(map[string]map[string]struct{}, len(c.Routes))
	catchAllPaths := make(map[string]bool, len(c.Routes))
	for i := range c.Routes {
		route := c.Routes[i]
		route.IncomingPath = strings.TrimSpace(route.IncomingPath)
		upstreamPath := strings.TrimSpace(route.UpstreamPath)
		route.Converter = strings.TrimSpace(route.Converter)
		if route.Converter == "" {
			route.Converter = AdvancedCustomConverterNone
		}

		if route.IncomingPath == "" {
			return fmt.Errorf("advanced_custom.advanced_routes[%d].incoming_path is required", i)
		}
		if !strings.HasPrefix(route.IncomingPath, "/") {
			return fmt.Errorf("advanced_custom.advanced_routes[%d].incoming_path must start with /", i)
		}
		if strings.Contains(route.IncomingPath, "?") {
			return fmt.Errorf("advanced_custom.advanced_routes[%d].incoming_path must not include query", i)
		}
		models := normalizeAdvancedCustomRouteModels(route.Models)
		if route.IncomingPath == AdvancedCustomModelListPath {
			if len(models) != 0 || route.PassThroughBodyEnabled {
				return fmt.Errorf("advanced_custom.advanced_routes[%d]: /v1/models does not support model rules or body passthrough", i)
			}
			if route.Converter != AdvancedCustomConverterNone {
				return fmt.Errorf("advanced_custom.advanced_routes[%d].converter must be none for /v1/models", i)
			}
			if strings.Contains(upstreamPath, advancedCustomModelPlaceholder) {
				return fmt.Errorf("advanced_custom.advanced_routes[%d].upstream_path must not contain %s for /v1/models", i, advancedCustomModelPlaceholder)
			}
		}
		if catchAllPaths[route.IncomingPath] {
			return fmt.Errorf("advanced_custom.advanced_routes[%d]: catch-all route must be unique and last for incoming_path: %s", i, route.IncomingPath)
		}
		if len(models) == 0 {
			catchAllPaths[route.IncomingPath] = true
		}
		if pathModels[route.IncomingPath] == nil {
			pathModels[route.IncomingPath] = make(map[string]struct{})
		}
		for _, rule := range models {
			if _, duplicate := pathModels[route.IncomingPath][rule]; duplicate {
				return fmt.Errorf("advanced_custom.advanced_routes[%d].models contains duplicate rule for incoming_path: %s", i, route.IncomingPath)
			}
			if strings.HasPrefix(rule, "re:") && compileAdvancedCustomModelRegex(strings.TrimPrefix(rule, "re:")) == nil {
				return fmt.Errorf("advanced_custom.advanced_routes[%d].models regex is empty or invalid: %s", i, rule)
			}
			pathModels[route.IncomingPath][rule] = struct{}{}
		}
		if route.PassThroughBodyEnabled && !route.SupportsPassThroughBody() {
			return fmt.Errorf("advanced_custom.advanced_routes[%d].pass_through_body_enabled requires converter none", i)
		}

		if upstreamPath == "" {
			return fmt.Errorf("advanced_custom.advanced_routes[%d].upstream_path is required", i)
		}
		if err := validateAdvancedCustomUpstreamTarget(i, upstreamPath); err != nil {
			return err
		}

		if !IsAdvancedCustomConverterAllowed(route.Converter) {
			return fmt.Errorf("advanced_custom.advanced_routes[%d].converter is not registered: %s", i, route.Converter)
		}
		if err := validateAdvancedCustomConverterPath(i, route.IncomingPath, route.Converter); err != nil {
			return err
		}
		if err := validateAdvancedCustomRouteAuth(i, route.Auth); err != nil {
			return err
		}
	}

	return nil
}

func validateAdvancedCustomUpstreamTarget(index int, upstreamPath string) error {
	if strings.HasPrefix(upstreamPath, "/") {
		if strings.HasPrefix(upstreamPath, "//") {
			return fmt.Errorf("advanced_custom.advanced_routes[%d].upstream_path must be a full URL or a path starting with /", index)
		}
		return nil
	}

	parsedURL, err := url.Parse(upstreamPath)
	if err != nil || parsedURL.Scheme == "" || parsedURL.Host == "" {
		return fmt.Errorf("advanced_custom.advanced_routes[%d].upstream_path must be a full URL or a path starting with /", index)
	}
	if !strings.EqualFold(parsedURL.Scheme, "http") && !strings.EqualFold(parsedURL.Scheme, "https") {
		return fmt.Errorf("advanced_custom.advanced_routes[%d].upstream_path must use http or https", index)
	}
	return nil
}

func validateAdvancedCustomConverterPath(index int, incomingPath string, converter string) error {
	switch converter {
	case AdvancedCustomConverterNone:
		return nil
	case AdvancedCustomConverterAnthropicMessagesToOpenAIChatCompletions:
		if incomingPath == "/v1/messages" {
			return nil
		}
	case AdvancedCustomConverterOpenAIChatCompletionsToAnthropicMessages,
		AdvancedCustomConverterOpenAIChatCompletionsToOpenAIResponses,
		AdvancedCustomConverterOpenAIChatCompletionsToGeminiGenerateContent:
		if incomingPath == "/v1/chat/completions" {
			return nil
		}
	case AdvancedCustomConverterOpenAIResponsesToOpenAIChatCompletions,
		AdvancedCustomConverterOpenAIResponsesToAnthropicMessages,
		AdvancedCustomConverterOpenAIResponsesToGeminiGenerateContent:
		if incomingPath == "/v1/responses" {
			return nil
		}
	case AdvancedCustomConverterGeminiGenerateContentToOpenAIChatCompletions:
		if strings.Contains(incomingPath, ":generateContent") || strings.Contains(incomingPath, ":streamGenerateContent") {
			return nil
		}
	}
	return fmt.Errorf("advanced_custom.advanced_routes[%d].converter does not match incoming_path: %s", index, converter)
}

func validateAdvancedCustomRouteAuth(index int, auth *AdvancedCustomRouteAuth) error {
	if auth == nil {
		return nil
	}
	authType := strings.TrimSpace(auth.Type)
	switch authType {
	case AdvancedCustomAuthTypeNone:
		return nil
	case AdvancedCustomAuthTypeHeader, AdvancedCustomAuthTypeQuery:
		if strings.TrimSpace(auth.Name) == "" {
			return fmt.Errorf("advanced_custom.advanced_routes[%d].auth.name is required", index)
		}
		if strings.TrimSpace(auth.Value) == "" {
			return fmt.Errorf("advanced_custom.advanced_routes[%d].auth.value is required", index)
		}
		return nil
	default:
		return fmt.Errorf("advanced_custom.advanced_routes[%d].auth.type is invalid: %s", index, auth.Type)
	}
}

func (s *ChannelOtherSettings) Validate() error {
	if s == nil {
		return nil
	}
	switch s.VertexAPIVersion {
	case "", VertexAPIVersionV1, VertexAPIVersionV1Beta1:
	default:
		return fmt.Errorf("vertex_api_version must be one of %q or %q", VertexAPIVersionV1, VertexAPIVersionV1Beta1)
	}
	if s.AdvancedCustom != nil {
		if err := s.AdvancedCustom.Validate(); err != nil {
			return err
		}
	}
	return nil
}
