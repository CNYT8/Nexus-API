package ali

import (
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
)

func isMultimodalEmbeddingModel(model string) bool {
	return strings.Contains(model, "vl-embedding") || strings.Contains(model, "embedding-vision") || strings.HasPrefix(model, "multimodal-embedding")
}

type AliMultimodalContent struct {
	Text  string `json:"text,omitempty"`
	Image string `json:"image,omitempty"`
	Video string `json:"video,omitempty"`
}

type AliMultimodalEmbeddingRequest struct {
	Model string `json:"model"`
	Input struct {
		Contents []AliMultimodalContent `json:"contents"`
	} `json:"input"`
	Parameters *AliMultimodalEmbeddingParameters `json:"parameters,omitempty"`
}

type AliMultimodalEmbeddingParameters struct {
	Dimension *int `json:"dimension,omitempty"`
}

func convertToMultimodalEmbeddingRequest(request dto.EmbeddingRequest) (*AliMultimodalEmbeddingRequest, error) {
	if request.EncodingFormat != "" && request.EncodingFormat != "float" {
		return nil, fmt.Errorf("Ali multimodal embeddings only support encoding_format=float")
	}
	var items []any
	switch input := request.Input.(type) {
	case string:
		items = []any{input}
	case []any:
		items = input
	case []string:
		for _, text := range input {
			items = append(items, text)
		}
	case map[string]any:
		items = []any{input}
	default:
		return nil, fmt.Errorf("Ali multimodal embedding input must contain text, image, or video")
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("embedding input is empty")
	}
	converted := &AliMultimodalEmbeddingRequest{Model: request.Model}
	for _, item := range items {
		var content AliMultimodalContent
		switch value := item.(type) {
		case string:
			content.Text = value
		case map[string]any:
			for key, field := range value {
				text, ok := field.(string)
				if !ok {
					return nil, fmt.Errorf("embedding %s must be a string", key)
				}
				switch key {
				case "text":
					content.Text = text
				case "image":
					content.Image = text
				case "video":
					content.Video = text
				default:
					return nil, fmt.Errorf("unsupported multimodal embedding field %q", key)
				}
			}
		default:
			return nil, fmt.Errorf("unsupported multimodal embedding input %T", item)
		}
		if content.Text == "" && content.Image == "" && content.Video == "" {
			return nil, fmt.Errorf("multimodal embedding item is empty")
		}
		converted.Input.Contents = append(converted.Input.Contents, content)
	}
	if request.Dimensions != nil {
		converted.Parameters = &AliMultimodalEmbeddingParameters{Dimension: request.Dimensions}
	}
	return converted, nil
}

func MultimodalEmbeddingHandler(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (*types.NewAPIError, *dto.Usage) {
	defer service.CloseResponseBodyGracefully(resp)
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return types.NewError(err, types.ErrorCodeReadResponseBodyFailed), nil
	}
	var result struct {
		Output struct {
			Embeddings []dto.EmbeddingResponseItem `json:"embeddings"`
		} `json:"output"`
		Usage AliUsage `json:"usage"`
		AliError
	}
	if err := common.Unmarshal(body, &result); err != nil {
		return types.NewError(err, types.ErrorCodeBadResponseBody), nil
	}
	if result.Code != "" {
		return types.WithOpenAIError(types.OpenAIError{Message: result.Message, Type: result.Code, Code: result.Code}, http.StatusBadGateway), nil
	}
	usage := dto.Usage{}
	if details := result.Usage.InputTokensDetails; details != nil {
		usage.PromptTokens = result.Usage.InputTokens
		usage.PromptTokensDetails.TextTokens = details.TextTokens
		usage.PromptTokensDetails.ImageTokens = details.ImageTokens
	} else {
		usage.PromptTokens = result.Usage.InputTokens + result.Usage.ImageTokens
		usage.PromptTokensDetails.TextTokens = result.Usage.InputTokens
		usage.PromptTokensDetails.ImageTokens = result.Usage.ImageTokens
	}
	if usage.PromptTokens == 0 {
		usage.PromptTokens = info.GetEstimatePromptTokens()
	}
	usage.TotalTokens = usage.PromptTokens
	for i := range result.Output.Embeddings {
		result.Output.Embeddings[i].Object = "embedding"
	}
	response := dto.EmbeddingResponse{Object: "list", Model: info.UpstreamModelName, Data: result.Output.Embeddings, Usage: usage}
	encoded, err := common.Marshal(response)
	if err != nil {
		return types.NewError(err, types.ErrorCodeBadResponseBody), &usage
	}
	service.IOCopyBytesGracefully(c, resp, encoded)
	return nil, &usage
}
