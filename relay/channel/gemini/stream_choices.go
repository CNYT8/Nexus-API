package gemini

import "github.com/QuantumNous/new-api/dto"

// Emit a role once for each candidate, including candidates first seen in a
// later chunk. Tool declarations and arguments stay together in the payload.
func geminiStreamStartResponse(response *dto.ChatCompletionsStreamResponse, started map[int]bool) *dto.ChatCompletionsStreamResponse {
	var choices []dto.ChatCompletionsStreamResponseChoice
	for _, choice := range response.Choices {
		if started[choice.Index] {
			continue
		}
		started[choice.Index] = true
		choices = append(choices, dto.ChatCompletionsStreamResponseChoice{
			Index: choice.Index,
			Delta: dto.ChatCompletionsStreamResponseChoiceDelta{Role: "assistant"},
		})
	}
	if len(choices) == 0 {
		return nil
	}
	return &dto.ChatCompletionsStreamResponse{
		Id: response.Id, Object: response.Object, Created: response.Created,
		Model: response.Model, SystemFingerprint: response.SystemFingerprint,
		Choices: choices,
	}
}
