package helper

import (
	"strings"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/setting/reasoning"
	"github.com/gin-gonic/gin"
)

const modelModifiersContextKey = "nexus_explicit_model_modifiers"

type modelModifierState struct {
	info      *relaycommon.RelayInfo
	modifiers reasoning.ModelModifiers
}

// ApplyReasoningModelSuffix is called after mapping and before conversion. It
// can be called again on a converted request: mapped controls remain available
// after the model name has been stripped. Legacy aliases are left to adapters.
func ApplyReasoningModelSuffix(c *gin.Context, info *relaycommon.RelayInfo, outbound ...dto.Request) error {
	if info == nil || model_setting.GetGlobalSettings().PassThroughRequestEnabled ||
		info.ChannelMeta != nil && info.ChannelSetting.PassThroughBodyEnabled {
		return nil
	}
	if info.ChannelMeta == nil {
		info.ChannelMeta = &relaycommon.ChannelMeta{}
	}
	model := info.UpstreamModelName
	if model == "" {
		model = info.OriginModelName
	}
	if model_setting.ShouldPreserveThinkingSuffix(info.OriginModelName) || model_setting.ShouldPreserveThinkingSuffix(model) {
		return nil
	}
	originModel := info.OriginModelName
	if info.RelayMode == relayconstant.RelayModeResponsesCompact {
		originModel = strings.TrimSuffix(originModel, ratio_setting.CompactModelSuffix)
		model = strings.TrimSuffix(model, ratio_setting.CompactModelSuffix)
	}
	origin, err := reasoning.ParseModelModifiers(originModel)
	if err != nil {
		return err
	}
	mapped, err := reasoning.ParseModelModifiers(model)
	if err != nil {
		return err
	}
	modifiers := reasoning.MergeModelModifiers(origin, mapped)
	if c != nil && !mapped.HasModifiers {
		if value, ok := c.Get(modelModifiersContextKey); ok {
			if previous, ok := value.(modelModifierState); ok && previous.info == info && previous.modifiers.Base == model {
				modifiers = previous.modifiers
			}
		}
	}
	if !modifiers.HasModifiers {
		return nil
	}
	if modifiers.HasThinking {
		modifiers.Base = trimOverriddenReasoningSuffix(modifiers.Base)
	}
	if c != nil {
		c.Set(modelModifiersContextKey, modelModifierState{info: info, modifiers: modifiers})
	}
	if len(outbound) == 0 && info.Request != nil {
		outbound = []dto.Request{info.Request}
	}
	for _, request := range outbound {
		if request == nil {
			continue
		}
		if err := applyModelModifiers(request, modifiers); err != nil {
			return err
		}
		if chat, ok := request.(*dto.GeneralOpenAIRequest); ok && modifiers.HasThinking && info.ChannelType == constant.ChannelTypeOpenRouter {
			values := map[string]any{"enabled": modifiers.Mode != "disabled"}
			if modifiers.Mode != "disabled" {
				if modifiers.Budget != nil && *modifiers.Budget > 0 {
					values["max_tokens"] = *modifiers.Budget
				} else {
					values["effort"] = modifierEffort(modifiers)
				}
			}
			chat.Reasoning, err = patchModifierObject(chat.Reasoning, []string{"effort", "max_tokens", "enabled"}, values)
			if err != nil {
				return err
			}
			chat.THINKING = nil
		}
		request.SetModelName(modifiers.Base)
	}
	info.UpstreamModelName = modifiers.Base
	if modifiers.HasThinking {
		info.ReasoningEffort = modifierEffort(modifiers)
	}
	return nil
}

// ApplyRequestModelModifiers supports converters without a RelayInfo argument.
// The context carries mapped controls from the adapter; direct converter calls
// can still parse a model with explicit modifiers.
func ApplyRequestModelModifiers(c *gin.Context, request dto.Request, model string) error {
	if model_setting.GetGlobalSettings().PassThroughRequestEnabled || model_setting.ShouldPreserveThinkingSuffix(model) {
		return nil
	}
	modifiers, err := reasoning.ParseModelModifiers(model)
	if err != nil {
		return err
	}
	if c != nil {
		if value, ok := c.Get(modelModifiersContextKey); ok {
			if state, ok := value.(modelModifierState); ok && state.modifiers.Base == trimOverriddenReasoningSuffix(modifiers.Base) {
				modifiers = state.modifiers
			}
		}
	}
	if !modifiers.HasModifiers {
		return nil
	}
	if modifiers.HasThinking {
		modifiers.Base = trimOverriddenReasoningSuffix(modifiers.Base)
	}
	if err := applyModelModifiers(request, modifiers); err != nil {
		return err
	}
	request.SetModelName(modifiers.Base)
	return nil
}

func trimOverriddenReasoningSuffix(model string) string {
	bare := model[strings.LastIndex(model, "/")+1:]
	lower := strings.ToLower(bare)
	if !strings.HasPrefix(lower, "claude-") && !strings.HasPrefix(lower, "gemini-") &&
		!strings.HasPrefix(lower, "gpt-") && !strings.HasPrefix(lower, "o1") &&
		!strings.HasPrefix(lower, "o3") && !strings.HasPrefix(lower, "o4") && !strings.HasPrefix(lower, "deepseek-v4-") {
		return model
	}
	if i := strings.LastIndex(lower, "-thinking-"); i >= 0 {
		return model[:len(model)-len(bare)+i]
	}
	for _, suffix := range []string{"-nothinking", "-thinking", "-minimal", "-medium", "-xhigh", "-high", "-low", "-none", "-max"} {
		if strings.HasSuffix(lower, suffix) {
			// This is a real OpenAI model family, not a max-effort alias.
			if suffix == "-max" && strings.Contains(lower, "codex-max") {
				return model
			}
			return model[:len(model)-len(suffix)]
		}
	}
	return model
}
