package dto

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
)

// MaxImageN caps the image generation count. Without this bound a huge or
// wrapped-negative n overflows quota calculation into a negative charge.
const MaxImageN = 128

// ImageBillingParameters contains only the provider scalars parsed by request
// validation. Keep this separate from the complete provider request payload.
type ImageBillingParameters struct {
	N            *uint `json:"n,omitempty"`
	PromptExtend *bool `json:"prompt_extend,omitempty"`
}

type ImageRequest struct {
	Model             string          `json:"model"`
	Prompt            string          `json:"prompt" binding:"required"`
	N                 *uint           `json:"n,omitempty"`
	Size              string          `json:"size,omitempty"`
	Quality           string          `json:"quality,omitempty"`
	ResponseFormat    string          `json:"response_format,omitempty"`
	Style             json.RawMessage `json:"style,omitempty"`
	User              json.RawMessage `json:"user,omitempty"`
	ExtraFields       json.RawMessage `json:"extra_fields,omitempty"`
	Background        json.RawMessage `json:"background,omitempty"`
	Moderation        json.RawMessage `json:"moderation,omitempty"`
	OutputFormat      json.RawMessage `json:"output_format,omitempty"`
	OutputCompression json.RawMessage `json:"output_compression,omitempty"`
	PartialImages     json.RawMessage `json:"partial_images,omitempty"`
	Stream            *bool           `json:"stream,omitempty"`
	Images            json.RawMessage `json:"images,omitempty"`
	Mask              json.RawMessage `json:"mask,omitempty"`
	InputFidelity     json.RawMessage `json:"input_fidelity,omitempty"`
	Watermark         *bool           `json:"watermark,omitempty"`
	// zhipu 4v
	WatermarkEnabled json.RawMessage `json:"watermark_enabled,omitempty"`
	UserId           json.RawMessage `json:"user_id,omitempty"`
	Image            json.RawMessage `json:"image,omitempty"`
	// 用匿名参数接收额外参数
	Extra             map[string]json.RawMessage `json:"-"`
	BillingParameters *ImageBillingParameters    `json:"-"`
}

// ImageCount resolves the validated request quantity. Top-level zero retains
// its legacy default of one; an explicit provider count must be positive.
func (i *ImageRequest) ImageCount(useProviderParameters bool) (int, error) {
	n := uint(1)
	if i.N != nil && *i.N != 0 {
		n = *i.N
	}
	if n > MaxImageN {
		return 0, fmt.Errorf("n must be an integer between 1 and %d", MaxImageN)
	}
	parameters, err := i.ImageParameters()
	if err != nil {
		return 0, err
	}
	if parameters != nil && parameters.N != nil {
		if *parameters.N > MaxImageN || *parameters.N == 0 {
			return 0, fmt.Errorf("parameters.n must be an integer between 1 and %d", MaxImageN)
		}
		if useProviderParameters {
			n = *parameters.N
		}
	}
	return int(n), nil
}

// ImageParameters also supports direct adaptor/pricing callers that did not
// pass through ingress validation. Never modify the frozen incoming request.
func (i *ImageRequest) ImageParameters() (*ImageBillingParameters, error) {
	if raw, exists := i.Extra["parameters"]; exists {
		var parameters *ImageBillingParameters
		if err := common.Unmarshal(raw, &parameters); err != nil {
			return nil, fmt.Errorf("invalid image parameters: %w", err)
		}
		return parameters, nil
	}
	return i.BillingParameters, nil
}

// ImageBillingRequestFromJSON validates the complete JSON document, not just
// successful path lookups: malformed outgoing JSON must never default to n=1.
func ImageBillingRequestFromJSON(data []byte) (*ImageRequest, error) {
	var scalars *struct {
		N          *uint                   `json:"n"`
		Parameters *ImageBillingParameters `json:"parameters"`
	}
	if err := common.Unmarshal(data, &scalars); err != nil {
		return nil, fmt.Errorf("invalid image billing parameters: %w", err)
	}
	if scalars == nil {
		return nil, fmt.Errorf("image request must be a JSON object")
	}
	return &ImageRequest{N: scalars.N, BillingParameters: scalars.Parameters}, nil
}

// ImageBillingRequestFromForm rejects ambiguous repeated scalars. Files and
// prompts are deliberately excluded from the billing request.
func ImageBillingRequestFromForm(values map[string][]string) (*ImageRequest, error) {
	request := &ImageRequest{}
	for _, name := range []string{"n", "parameters"} {
		fields := values[name]
		if len(fields) > 1 {
			return nil, fmt.Errorf("duplicate image %s field", name)
		}
		if len(fields) == 0 {
			continue
		}
		if name == "n" {
			n, err := strconv.ParseUint(strings.TrimSpace(fields[0]), 10, 64)
			if err != nil || n > MaxImageN {
				return nil, fmt.Errorf("n must be an integer between 1 and %d", MaxImageN)
			}
			request.N = common.GetPointer(uint(n))
		} else if err := common.Unmarshal([]byte(fields[0]), &request.BillingParameters); err != nil {
			return nil, fmt.Errorf("invalid image parameters: %w", err)
		}
	}
	return request, nil
}

func (i *ImageRequest) UnmarshalJSON(data []byte) error {
	// 先解析成 map[string]interface{}
	var rawMap map[string]json.RawMessage
	if err := common.Unmarshal(data, &rawMap); err != nil {
		return err
	}

	// 用 struct tag 获取所有已定义字段名
	knownFields := GetJSONFieldNames(reflect.TypeOf(*i))

	// 再正常解析已定义字段
	type Alias ImageRequest
	var known Alias
	if err := common.Unmarshal(data, &known); err != nil {
		return err
	}
	*i = ImageRequest(known)

	// 提取多余字段
	i.Extra = make(map[string]json.RawMessage)
	for k, v := range rawMap {
		if _, ok := knownFields[k]; !ok {
			i.Extra[k] = v
		}
	}
	return nil
}

// 序列化时需要重新把字段平铺
func (r ImageRequest) MarshalJSON() ([]byte, error) {
	// 将已定义字段转为 map
	type Alias ImageRequest
	alias := Alias(r)
	base, err := common.Marshal(alias)
	if err != nil {
		return nil, err
	}

	var baseMap map[string]json.RawMessage
	if err := common.Unmarshal(base, &baseMap); err != nil {
		return nil, err
	}

	// 不能合并ExtraFields！！！！！！！！
	// 合并 ExtraFields
	//for k, v := range r.Extra {
	//	if _, exists := baseMap[k]; !exists {
	//		baseMap[k] = v
	//	}
	//}

	return common.Marshal(baseMap)
}

func GetJSONFieldNames(t reflect.Type) map[string]struct{} {
	fields := make(map[string]struct{})
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)

		// 跳过匿名字段（例如 ExtraFields）
		if field.Anonymous {
			continue
		}

		tag := field.Tag.Get("json")
		if tag == "-" || tag == "" {
			continue
		}

		// 取逗号前字段名（排除 omitempty 等）
		name := tag
		if commaIdx := indexComma(tag); commaIdx != -1 {
			name = tag[:commaIdx]
		}
		fields[name] = struct{}{}
	}
	return fields
}

func indexComma(s string) int {
	for i := 0; i < len(s); i++ {
		if s[i] == ',' {
			return i
		}
	}
	return -1
}

func (i *ImageRequest) GetTokenCountMeta() *types.TokenCountMeta {
	var sizeRatio = 1.0
	var qualityRatio = 1.0

	if strings.HasPrefix(i.Model, "dall-e") {
		// Size
		if i.Size == "256x256" {
			sizeRatio = 0.4
		} else if i.Size == "512x512" {
			sizeRatio = 0.45
		} else if i.Size == "1024x1024" {
			sizeRatio = 1
		} else if i.Size == "1024x1792" || i.Size == "1792x1024" {
			sizeRatio = 2
		}

		if i.Model == "dall-e-3" && i.Quality == "hd" {
			qualityRatio = 2.0
			if i.Size == "1024x1792" || i.Size == "1792x1024" {
				qualityRatio = 1.5
			}
		}
	}

	imageN := uint(1)
	if i.N != nil && *i.N > 0 {
		imageN = *i.N
	}

	return &types.TokenCountMeta{
		CombineText:     i.Prompt,
		MaxTokens:       1584,
		ImagePriceRatio: sizeRatio * qualityRatio,
		BillingRatios:   map[string]float64{"n": float64(imageN)},
	}
}

func (i *ImageRequest) IsStream(c *gin.Context) bool {
	return i.Stream != nil && *i.Stream
}

func (i *ImageRequest) SetModelName(modelName string) {
	if modelName != "" {
		i.Model = modelName
	}
}

type ImageResponse struct {
	Data     []ImageData     `json:"data"`
	Created  int64           `json:"created"`
	Metadata json.RawMessage `json:"metadata,omitempty"`
}
type ImageData struct {
	Url           string `json:"url"`
	B64Json       string `json:"b64_json"`
	RevisedPrompt string `json:"revised_prompt"`
}
