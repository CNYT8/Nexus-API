package helper

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/QuantumNous/new-api/setting/reasoning"
	"github.com/tidwall/sjson"
)

func modifierEffort(modifiers reasoning.ModelModifiers) string {
	if modifiers.Mode == "disabled" {
		return "none"
	}
	if modifiers.Effort != "" {
		return modifiers.Effort
	}
	if modifiers.Budget != nil {
		switch budget := *modifiers.Budget; {
		case budget == 0:
			return "none"
		case budget > 0 && budget <= 1024:
			return "low"
		case budget > 0 && budget <= 8192:
			return "medium"
		}
	}
	return "high"
}

func patchModifierObject(raw json.RawMessage, remove []string, values map[string]any) (json.RawMessage, error) {
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		raw = json.RawMessage(`{}`)
	}
	if !json.Valid(raw) || bytes.TrimSpace(raw)[0] != '{' {
		return nil, fmt.Errorf("reasoning controls must be a JSON object")
	}
	var err error
	for _, key := range remove {
		raw, err = sjson.DeleteBytes(raw, key)
		if err != nil {
			return nil, err
		}
	}
	for key, value := range values {
		raw, err = sjson.SetBytes(raw, key, value)
		if err != nil {
			return nil, err
		}
	}
	return raw, nil
}

func applyModelModifiers(request dto.Request, modifiers reasoning.ModelModifiers) error {
	switch req := request.(type) {
	case *dto.GeneralOpenAIRequest:
		if modifiers.Temperature != nil {
			req.Temperature = modifiers.Temperature
		}
		if modifiers.TopP != nil {
			req.TopP = modifiers.TopP
		}
		if !modifiers.HasThinking {
			return nil
		}
		req.ReasoningEffort = modifierEffort(modifiers)
		enabled := modifiers.Mode != "disabled"
		if len(req.Reasoning) > 0 {
			values := map[string]any{"enabled": enabled}
			if enabled {
				values["effort"] = req.ReasoningEffort
			}
			var err error
			req.Reasoning, err = patchModifierObject(req.Reasoning, []string{"effort", "max_tokens", "enabled"}, values)
			if err != nil {
				return err
			}
		}
		if len(req.THINKING) > 0 {
			kind := "disabled"
			if enabled {
				kind = "enabled"
			}
			values := map[string]any{"type": kind}
			if enabled && modifiers.Budget != nil && *modifiers.Budget > 0 {
				values["budget_tokens"] = *modifiers.Budget
			}
			var err error
			req.THINKING, err = patchModifierObject(req.THINKING, []string{"type", "budget_tokens"}, values)
			if err != nil {
				return err
			}
		}
		if len(req.EnableThinking) > 0 || dto.IsQwenThinkingBudgetModel(modifiers.Base) {
			req.EnableThinking, _ = json.Marshal(enabled)
		}
		if len(req.ThinkingBudget) > 0 || dto.IsQwenThinkingBudgetModel(modifiers.Base) {
			req.ThinkingBudget = nil
			if modifiers.Budget != nil {
				req.ThinkingBudget, _ = json.Marshal(*modifiers.Budget)
			}
		}
	case *dto.OpenAIResponsesRequest:
		if modifiers.Temperature != nil {
			req.Temperature = modifiers.Temperature
		}
		if modifiers.TopP != nil {
			req.TopP = modifiers.TopP
		}
		if modifiers.HasThinking {
			if req.Reasoning == nil {
				req.Reasoning = &dto.Reasoning{}
			}
			req.Reasoning.Effort = modifierEffort(modifiers)
			if len(req.EnableThinking) > 0 || dto.IsQwenThinkingBudgetModel(modifiers.Base) {
				req.EnableThinking, _ = json.Marshal(modifiers.Mode != "disabled")
			}
			if len(req.ThinkingBudget) > 0 || dto.IsQwenThinkingBudgetModel(modifiers.Base) {
				req.ThinkingBudget = nil
				if modifiers.Budget != nil {
					req.ThinkingBudget, _ = json.Marshal(*modifiers.Budget)
				}
			}
		}
	case *dto.OpenAIResponsesCompactionRequest:
		// Compaction has no generation controls; only normalize its model name.
		return nil
	case *dto.ClaudeRequest:
		return applyClaudeModelModifiers(req, modifiers)
	case *dto.GeminiChatRequest:
		return applyGeminiModelModifiers(req, modifiers)
	default:
		return fmt.Errorf("explicit model modifiers are not supported for request type %T", request)
	}
	return nil
}

func applyClaudeModelModifiers(req *dto.ClaudeRequest, modifiers reasoning.ModelModifiers) error {
	if modifiers.Temperature != nil {
		req.Temperature = modifiers.Temperature
	}
	if modifiers.TopP != nil {
		req.TopP = modifiers.TopP
	}
	if !modifiers.HasThinking {
		return nil
	}
	model := strings.ToLower(modifiers.Base)
	if i := strings.Index(model, "claude-"); i >= 0 {
		model = model[i:]
	}
	hasPrefix := func(prefixes ...string) bool {
		for _, prefix := range prefixes {
			if strings.HasPrefix(model, prefix) {
				return true
			}
		}
		return false
	}
	strict := hasPrefix("claude-opus-4-7", "claude-opus-4-8", "claude-opus-5", "claude-sonnet-5", "claude-haiku-5", "claude-fable-5", "claude-mythos-")
	adaptive := strict || hasPrefix("claude-opus-4-6", "claude-sonnet-4-6")
	thinking := dto.Thinking{}
	if req.Thinking != nil {
		thinking = *req.Thinking
	}
	thinking.BudgetTokens = nil
	effort := modifierEffort(modifiers)
	if effort == "minimal" {
		effort = "low"
	}
	if effort == "xhigh" && !strict {
		effort = "max"
	}
	if !adaptive && (effort == "max" || effort == "xhigh") {
		effort = "high"
	}
	if modifiers.Mode == "disabled" {
		if hasPrefix("claude-fable-5", "claude-mythos-", "claude-opus-5-5") {
			return fmt.Errorf("model %q cannot disable thinking", modifiers.Base)
		}
		thinking.Type = "disabled"
		if hasPrefix("claude-sonnet-5-5") {
			thinking.Type = "between_tools"
		}
		effort = ""
	} else if adaptive && (modifiers.Budget == nil || strict || modifiers.Mode == "adaptive") {
		thinking.Type = "adaptive"
		if strict && thinking.Display == "" {
			thinking.Display = "summarized"
		}
	} else {
		thinking.Type = "enabled"
		cap := req.MaxTokens
		if cap == nil {
			value := uint(model_setting.GetClaudeSettings().GetDefaultMaxTokens(modifiers.Base))
			cap = &value
			req.MaxTokens = cap
		}
		if uint64(*cap) > uint64(math.MaxInt) {
			return fmt.Errorf("output token limit is too large for a thinking budget")
		}
		budget := 0
		if modifiers.Budget != nil && *modifiers.Budget > 0 {
			budget = *modifiers.Budget
		} else {
			percentage := 0.8
			switch effort {
			case "low":
				percentage = 0.2
			case "medium":
				percentage = 0.5
			}
			budget = max(1024, int(float64(*cap)*percentage))
		}
		if budget < 1024 || uint(budget) >= *cap {
			return fmt.Errorf("Claude thinking budget must be at least 1024 and less than the explicit output token limit")
		}
		thinking.BudgetTokens = &budget
		if !hasPrefix("claude-opus-4-5") {
			effort = ""
		}
	}
	values := map[string]any{}
	if effort != "" {
		values["effort"] = effort
	}
	var err error
	req.OutputConfig, err = patchModifierObject(req.OutputConfig, []string{"effort"}, values)
	if err != nil {
		return err
	}
	req.Thinking = &thinking
	if strict {
		req.Temperature, req.TopP, req.TopK = nil, nil, nil
	} else if thinking.Type == "enabled" || thinking.Type == "adaptive" {
		temperature := 1.0
		req.Temperature, req.TopP = &temperature, nil
	}
	return nil
}

func applyGeminiModelModifiers(req *dto.GeminiChatRequest, modifiers reasoning.ModelModifiers) error {
	config := &req.GenerationConfig
	if modifiers.Temperature != nil {
		config.Temperature = modifiers.Temperature
	}
	if modifiers.TopP != nil {
		config.TopP = modifiers.TopP
	}
	if !modifiers.HasThinking {
		return nil
	}
	model := strings.ToLower(modifiers.Base)
	model = model[strings.LastIndex(model, "/")+1:]
	if strings.Contains(model, "-tts") || strings.Contains(model, "-image") || strings.Contains(model, "-native-audio") || strings.Contains(model, "-live") {
		return fmt.Errorf("explicit thinking modifiers are not supported for model %q", modifiers.Base)
	}
	thinking := dto.GeminiThinkingConfig{}
	if config.ThinkingConfig != nil {
		thinking = *config.ThinkingConfig
	}
	thinking.ThinkingBudget, thinking.ThinkingLevel = nil, ""
	effort := modifierEffort(modifiers)
	if modifiers.Mode == "disabled" {
		include := false
		thinking.IncludeThoughts = &include
	} else if thinking.IncludeThoughts == nil {
		include := true
		thinking.IncludeThoughts = &include
	}
	switch {
	case strings.HasPrefix(model, "gemini-2.5-"):
		minimum, maximum := 0, 24576
		if strings.HasPrefix(model, "gemini-2.5-pro") {
			minimum, maximum = 128, 32768
		} else if strings.HasPrefix(model, "gemini-2.5-flash-lite") {
			minimum = 512
		}
		budget := -1
		if modifiers.Mode == "disabled" {
			budget = 0
			if strings.HasPrefix(model, "gemini-2.5-pro") {
				budget = minimum
			}
		} else if modifiers.Budget != nil {
			budget = *modifiers.Budget
		} else if modifiers.Effort != "" {
			switch effort {
			case "minimal", "low":
				budget = 1024
			case "medium":
				budget = 8192
			case "high":
				budget = 24576
			case "xhigh", "max":
				budget = maximum
			}
		}
		if budget > 0 {
			budget = min(max(budget, minimum), maximum)
		}
		thinking.ThinkingBudget = &budget
	case strings.HasPrefix(model, "gemini-3"), strings.HasPrefix(model, "gemini-4-argon"), model == "gemini-pro-latest", model == "gemini-flash-latest", model == "gemini-flash-lite-latest":
		if effort == "none" {
			effort = "minimal"
		}
		if effort == "xhigh" || effort == "max" {
			effort = "high"
		}
		if strings.HasPrefix(model, "gemini-3-pro") {
			if effort == "minimal" || effort == "low" {
				effort = "low"
			} else {
				effort = "high"
			}
		} else if effort == "minimal" && (strings.HasPrefix(model, "gemini-3.1-pro") || strings.HasPrefix(model, "gemini-4-argon") || model == "gemini-pro-latest") {
			effort = "low"
		}
		thinking.ThinkingLevel = effort
	default:
		return fmt.Errorf("model %q has no known Gemini thinking configuration", modifiers.Base)
	}
	config.ThinkingConfig = &thinking
	return nil
}
