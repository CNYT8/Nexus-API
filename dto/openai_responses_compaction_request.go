package dto

import (
	"encoding/json"
	"strings"

	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
)

type OpenAIResponsesCompactionRequest struct {
	Model                string          `json:"model"`
	Input                json.RawMessage `json:"input,omitempty"`
	Instructions         json.RawMessage `json:"instructions,omitempty"`
	MaxOutputTokens      *uint           `json:"max_output_tokens,omitempty"`
	Tools                json.RawMessage `json:"tools,omitempty"`
	Reasoning            *Reasoning      `json:"reasoning,omitempty"`
	Text                 json.RawMessage `json:"text,omitempty"`
	PreviousResponseID   string          `json:"previous_response_id,omitempty"`
	PromptCacheKey       json.RawMessage `json:"prompt_cache_key,omitempty"`
	PromptCacheOptions   json.RawMessage `json:"prompt_cache_options,omitempty"`
	PromptCacheRetention json.RawMessage `json:"prompt_cache_retention,omitempty"`
	ParallelToolCalls    *bool           `json:"parallel_tool_calls,omitempty"`
	ServiceTier          string          `json:"service_tier,omitempty"`
}

func (r *OpenAIResponsesCompactionRequest) GetTokenCountMeta() *types.TokenCountMeta {
	var parts []string
	if len(r.Instructions) > 0 {
		parts = append(parts, string(r.Instructions))
	}
	if len(r.Input) > 0 {
		parts = append(parts, string(r.Input))
	}
	return &types.TokenCountMeta{
		CombineText: strings.Join(parts, "\n"),
	}
}

func (r *OpenAIResponsesCompactionRequest) IsStream(c *gin.Context) bool {
	return false
}

func (r *OpenAIResponsesCompactionRequest) SetModelName(modelName string) {
	if modelName != "" {
		r.Model = modelName
	}
}
