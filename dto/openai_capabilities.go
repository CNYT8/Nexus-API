package dto

import (
	"strings"
	"time"
)

// OpenAIChatCapabilities keeps parameter restrictions independent of model family.
type OpenAIChatCapabilities struct {
	UseMaxCompletionTokens bool
	UseDeveloperRole       bool
	SupportsTemperature    bool
	SupportsTopP           bool
	SupportsLogProbs       bool
}

func GetOpenAIChatCapabilities(modelName, reasoningEffort string) OpenAIChatCapabilities {
	caps := OpenAIChatCapabilities{SupportsTemperature: true, SupportsTopP: true, SupportsLogProbs: true}
	if IsOpenAIReasoningOModel(modelName) {
		caps.UseMaxCompletionTokens = true
		caps.UseDeveloperRole = !strings.HasPrefix(modelName, "o1-mini") && !strings.HasPrefix(modelName, "o1-preview")
		caps.SupportsTemperature = false
		return caps
	}
	isSolLuna := isOpenAIModelSnapshot(modelName, "gpt-6-sol") || isOpenAIModelSnapshot(modelName, "gpt-6-luna")
	isTerra := isOpenAIModelSnapshot(modelName, "gpt-6.1-terra")
	if !IsOpenAIGPT5Model(modelName) && !isSolLuna && !isTerra && !isOpenAIModelSnapshot(modelName, "gpt-6-astra") && !isOpenAIModelSnapshot(modelName, "gpt-6.1-sol") {
		return caps
	}
	caps.UseMaxCompletionTokens = true
	caps.UseDeveloperRole = true
	sampling := false
	if reasoningEffort == "" || reasoningEffort == "none" {
		sampling = isSolLuna || (isTerra && reasoningEffort == "none")
		for _, model := range []string{"gpt-5.1", "gpt-5.2", "gpt-5.4"} {
			if isOpenAIModelSnapshot(modelName, model) {
				sampling = true
				break
			}
		}
	}
	caps.SupportsTemperature = sampling
	caps.SupportsTopP = sampling
	caps.SupportsLogProbs = sampling
	return caps
}

func isOpenAIModelSnapshot(modelName, baseModel string) bool {
	if modelName == baseModel {
		return true
	}
	snapshot, ok := strings.CutPrefix(modelName, baseModel+"-")
	if !ok {
		return false
	}
	_, err := time.Parse(time.DateOnly, snapshot)
	return err == nil
}
