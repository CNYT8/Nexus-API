package dto

import (
	"encoding/json"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

func TestQwenThinkingBudgetIsPreservedIncludingZero(t *testing.T) {
	request := GeneralOpenAIRequest{
		Model:          "Qwen/Qwen3-235B-A22B-Thinking-2507",
		ThinkingBudget: json.RawMessage(`0`),
	}

	encoded, err := common.Marshal(request)
	require.NoError(t, err)
	require.JSONEq(t, `{"model":"Qwen/Qwen3-235B-A22B-Thinking-2507","thinking_budget":0}`, string(encoded))
}

func TestQwenThinkingBudgetIsFilteredForOtherModels(t *testing.T) {
	request := GeneralOpenAIRequest{
		Model:          "gpt-5",
		ThinkingBudget: json.RawMessage(`128`),
	}

	encoded, err := common.Marshal(request)
	require.NoError(t, err)
	require.JSONEq(t, `{"model":"gpt-5"}`, string(encoded))
}

func TestResponsesQwenThinkingBudgetIsPreserved(t *testing.T) {
	request := OpenAIResponsesRequest{
		Model:          "qwen-plus",
		ThinkingBudget: json.RawMessage(`128`),
	}

	encoded, err := common.Marshal(request)
	require.NoError(t, err)
	require.JSONEq(t, `{"model":"qwen-plus","thinking_budget":128}`, string(encoded))
}
