package dto

import "strings"

// MergeGeminiUsageMetadataNonZero merges cumulative snapshots, not deltas.
// A field omitted by a later frame must not erase an earlier reported count.
func MergeGeminiUsageMetadataNonZero(current, incoming *GeminiUsageMetadata) *GeminiUsageMetadata {
	if current == nil && incoming == nil {
		return nil
	}
	merged := &GeminiUsageMetadata{}
	if current != nil {
		*merged = *current
	}
	if incoming != nil {
		for _, field := range []struct{ dst, src *int }{
			{&merged.PromptTokenCount, &incoming.PromptTokenCount},
			{&merged.ToolUsePromptTokenCount, &incoming.ToolUsePromptTokenCount},
			{&merged.CandidatesTokenCount, &incoming.CandidatesTokenCount},
			{&merged.TotalTokenCount, &incoming.TotalTokenCount},
			{&merged.ThoughtsTokenCount, &incoming.ThoughtsTokenCount},
			{&merged.CachedContentTokenCount, &incoming.CachedContentTokenCount},
		} {
			if *field.src > 0 {
				*field.dst = *field.src
			}
		}
	} else {
		incoming = &GeminiUsageMetadata{}
	}
	merged.PromptTokensDetails = mergeGeminiTokenDetails(merged.PromptTokensDetails, incoming.PromptTokensDetails)
	merged.ToolUsePromptTokensDetails = mergeGeminiTokenDetails(merged.ToolUsePromptTokensDetails, incoming.ToolUsePromptTokensDetails)
	merged.CandidatesTokensDetails = mergeGeminiTokenDetails(merged.CandidatesTokensDetails, incoming.CandidatesTokensDetails)
	if total := merged.PromptTokenCount + merged.ToolUsePromptTokenCount + merged.CandidatesTokenCount + merged.ThoughtsTokenCount; total > merged.TotalTokenCount {
		merged.TotalTokenCount = total
	}
	return merged
}

func mergeGeminiTokenDetails(current, incoming []GeminiPromptTokensDetails) []GeminiPromptTokensDetails {
	reported := make(map[string]bool)
	for _, detail := range incoming {
		if detail.TokenCount > 0 {
			reported[strings.ToUpper(strings.TrimSpace(detail.Modality))] = true
		}
	}
	merged := make([]GeminiPromptTokensDetails, 0, len(current)+len(incoming))
	for _, detail := range current {
		if !reported[strings.ToUpper(strings.TrimSpace(detail.Modality))] {
			merged = append(merged, detail)
		}
	}
	for _, detail := range incoming {
		if detail.TokenCount > 0 {
			merged = append(merged, detail)
		}
	}
	return merged
}
