package service

import (
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	constant2 "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
)

func getImageToken(c *gin.Context, fileMeta *types.FileMeta, model string, stream bool) (int, error) {
	if fileMeta == nil || fileMeta.Source == nil {
		return 0, fmt.Errorf("image_url_is_nil")
	}

	// Defaults for 4o/4.1/4.5 family unless overridden below
	baseTokens := 85

	// Model classification
	lowerModel := strings.ToLower(model)

	// Special cases from existing behavior
	if strings.HasPrefix(lowerModel, "glm-4") {
		return 1047, nil
	}

	major, minor := imageModelVersion(lowerModel, "gpt-")
	isPatchBased := (major >= 6 || major == 5 && (minor >= 2 || strings.Contains(lowerModel, "mini") || strings.Contains(lowerModel, "nano"))) ||
		strings.Contains(lowerModel, "gpt-4.1-mini") || strings.Contains(lowerModel, "gpt-4.1-nano") || strings.Contains(lowerModel, "o4-mini")
	isOtherFamily := strings.Contains(lowerModel, "claude") || strings.Contains(lowerModel, "gemini")

	// Tile-based model tokens and bases per doc
	if !isPatchBased {
		if strings.HasPrefix(lowerModel, "gpt-4o-mini") {
			baseTokens = 2833
		} else if strings.HasPrefix(lowerModel, "gpt-5-chat-latest") || (strings.HasPrefix(lowerModel, "gpt-5") && !strings.Contains(lowerModel, "mini") && !strings.Contains(lowerModel, "nano")) {
			baseTokens = 70
		} else if strings.HasPrefix(lowerModel, "o1") || strings.HasPrefix(lowerModel, "o3") || strings.HasPrefix(lowerModel, "o1-pro") {
			baseTokens = 75
		} else if strings.Contains(lowerModel, "computer-use-preview") {
			baseTokens = 65
		} else if strings.Contains(lowerModel, "4.1") || strings.Contains(lowerModel, "4o") || strings.Contains(lowerModel, "4.5") {
			baseTokens = 85
		}
	}

	// Respect existing feature flags/short-circuits
	if fileMeta.Detail == "low" && !isPatchBased && !isOtherFamily {
		return baseTokens, nil
	}

	// Whether to count image tokens at all
	if !constant.GetMediaToken {
		return 3 * baseTokens, nil
	}

	if !constant.GetMediaTokenNotStream && !stream {
		return 3 * baseTokens, nil
	}
	if strings.Contains(lowerModel, "gemini") {
		if major, _ := imageModelVersion(lowerModel, "gemini-"); major == 0 || major >= 3 {
			return estimateImageTokens(lowerModel, 1024, 1024, fileMeta.Detail), nil
		}
	}

	// 使用统一的文件服务获取图片配置
	config, format, err := GetImageConfig(c, fileMeta.Source)
	if err != nil {
		return 0, err
	}
	if config.Width <= 0 || config.Height <= 0 {
		// not an image, but might be a valid file
		if format != "" {
			// file type
			return 3 * baseTokens, nil
		}
		return 0, errors.New(fmt.Sprintf("fail to decode image config: %s", fileMeta.GetIdentifier()))
	}

	width := config.Width
	height := config.Height
	logger.LogDebug(c, "image token input: format=%s, width=%d, height=%d", format, width, height)

	return estimateImageTokens(lowerModel, width, height, fileMeta.Detail), nil
}

func imageModelVersion(model, prefix string) (major, minor int) {
	_, rest, ok := strings.Cut(model, prefix)
	if !ok {
		return 0, 0
	}
	parts := strings.FieldsFunc(rest, func(r rune) bool { return r == '-' || r == '.' })
	found := false
	for _, part := range parts {
		n := len(part) - len(strings.TrimLeft(part, "0123456789"))
		if n == 0 || n > 2 {
			if found {
				break
			}
			continue
		}
		value, _ := strconv.Atoi(part[:n])
		if found {
			return major, value
		}
		major, found = value, true
		if n < len(part) {
			break
		}
	}
	return major, 0
}

// estimateImageTokens mirrors upstream family sizing with bounded dimensions.
// It estimates request preconsumption; provider usage remains authoritative.
func estimateImageTokens(model string, width, height int, detail string) int {
	model, detail = strings.ToLower(model), strings.ToLower(detail)
	if width <= 0 || height <= 0 {
		width, height = 1024, 1024
	}
	fit := func(limit int) {
		if max(width, height) > limit {
			scale := float64(limit) / float64(max(width, height))
			width, height = max(1, int(float64(width)*scale)), max(1, int(float64(height)*scale))
		}
	}
	fit(65535)
	ceil := func(n, d int) int { return (n + d - 1) / d }
	if strings.Contains(model, "gemini") {
		major, _ := imageModelVersion(model, "gemini-")
		if major == 0 || major >= 3 {
			switch detail {
			case "media_resolution_low":
				return 280
			case "media_resolution_medium":
				return 560
			case "media_resolution_ultra_high":
				return 2240
			default:
				return 1120
			}
		}
		if width <= 384 && height <= 384 {
			return 258
		}
		tile := min(max(min(width, height)*2/3, 256), 768)
		return (min(ceil(width, tile)*ceil(height, tile), 12) + 1) * 258
	}
	if strings.Contains(model, "claude") {
		edge, budget, framing := 1568, 1568, 4
		major, minor := imageModelVersion(model, "claude-")
		if major == 0 || major > 4 || major == 4 && minor >= 7 {
			edge, budget, framing = 2576, 4784, 3
		}
		fits := func(w, h int) bool {
			return ceil(w, 28)*28 <= edge && ceil(h, 28)*28 <= edge && ceil(w, 28)*ceil(h, 28) <= budget
		}
		if !fits(width, height) {
			if height > width {
				width, height = height, width
			}
			ratio := float64(width) / float64(height)
			lo, hi := 1, width
			for lo+1 < hi {
				mid := (lo + hi) / 2
				if fits(mid, max(1, int(math.RoundToEven(float64(mid)/ratio)))) {
					lo = mid
				} else {
					hi = mid
				}
			}
			width, height = lo, max(1, int(math.RoundToEven(float64(lo)/ratio)))
		}
		return ceil(width, 28)*ceil(height, 28) + framing
	}
	major, minor := imageModelVersion(model, "gpt-")
	base, tile, budget, side, short := 85, 170, 0, 0, 0
	multiplier := float32(0)
	switch {
	case major >= 6:
		multiplier = 1.2
		if detail == "low" {
			side = 512
		} else if detail == "high" {
			side, budget = 2048, 2500
		}
	case major == 5 && minor >= 5:
		multiplier = 1.2
		if detail == "low" {
			side = 512
		} else if detail == "high" {
			side, short = 2048, 768
		} else if minor == 5 {
			side, budget = 6000, 10000
		}
	case major == 5 && minor == 4:
		multiplier, side, budget = 1.2, 2048, 2500
		if detail == "low" {
			budget = 6144
		} else if detail == "original" {
			side, budget = 6000, 10000
		}
	case major == 5 && minor >= 2:
		multiplier, side, budget = 1.2, 2048, 6144
	case major == 5 && strings.Contains(model, "nano"):
		multiplier, budget = 1.5, 1536
	case major == 5 && strings.Contains(model, "mini"):
		multiplier, budget = 1.2, 1536
	case major == 5:
		base, tile = 70, 140
	case strings.Contains(model, "gpt-4.1-mini"):
		multiplier, side, budget = 1.62, 2048, 6144
	case strings.Contains(model, "gpt-4.1-nano"):
		multiplier, budget = 2.46, 1536
	case strings.Contains(model, "o4-mini"):
		multiplier, budget = 1.72, 1536
	case strings.Contains(model, "4o-mini"):
		base, tile = 2833, 5667
	case strings.Contains(model, "o1") || strings.Contains(model, "o3"):
		base, tile = 75, 150
	case strings.Contains(model, "computer-use-preview"):
		base, tile = 65, 129
	}
	if multiplier == 0 {
		if detail == "low" {
			return base
		}
		fit(2048)
		if min(width, height) > 768 {
			scale := 768.0 / float64(min(width, height))
			width, height = max(1, int(float64(width)*scale)), max(1, int(float64(height)*scale))
		}
		return base + tile*ceil(width, 512)*ceil(height, 512)
	}
	if side > 0 {
		fit(side)
	}
	patches := ceil(width, 32) * ceil(height, 32)
	if short > 0 && min(width, height) > short {
		patches = ceil(short, 32) * ceil(max(width, height)*short, min(width, height)*32)
	}
	if budget > 0 && patches > budget {
		scale := math.Sqrt(float64(32*32*budget) / (float64(width) * float64(height)))
		w, h := float64(width)*scale/32, float64(height)*scale/32
		scale *= math.Min(math.Floor(w)/w, math.Floor(h)/h)
		width, height = max(1, int(float64(width)*scale)), max(1, int(float64(height)*scale))
		patches = min(budget, ceil(width, 32)*ceil(height, 32))
	}
	return int(math.Ceil(float64(min(patches, 30000)) * float64(multiplier)))
}

func EstimateRequestToken(c *gin.Context, meta *types.TokenCountMeta, info *relaycommon.RelayInfo) (int, error) {
	// 是否统计token
	if !constant.CountToken {
		return 0, nil
	}

	if meta == nil {
		return 0, errors.New("token count meta is nil")
	}

	if info.RelayFormat == types.RelayFormatOpenAIRealtime {
		return 0, nil
	}
	if info.RelayMode == constant2.RelayModeAudioTranscription || info.RelayMode == constant2.RelayModeAudioTranslation {
		multiForm, err := common.ParseMultipartFormReusable(c)
		if err != nil {
			return 0, fmt.Errorf("error parsing multipart form: %v", err)
		}
		fileHeaders := multiForm.File["file"]
		totalAudioToken := 0
		for _, fileHeader := range fileHeaders {
			file, err := fileHeader.Open()
			if err != nil {
				return 0, fmt.Errorf("error opening audio file: %v", err)
			}
			defer file.Close()
			// get ext and io.seeker
			ext := filepath.Ext(fileHeader.Filename)
			duration, err := common.GetAudioDuration(c.Request.Context(), file, ext)
			if err != nil {
				return 0, fmt.Errorf("error getting audio duration: %v", err)
			}
			// 一分钟 1000 token，与 $price / minute 对齐。
			// duration 来自用户上传文件的元数据，可被伪造成天文数字，
			// 必须饱和转换防止 int 回绕成负数 token。
			audioTokens := common.QuotaFromFloat(math.Round(math.Ceil(duration) / 60.0 * 1000))
			if audioTokens < 0 {
				audioTokens = 0
			}
			totalAudioToken += audioTokens
		}
		return totalAudioToken, nil
	}

	if limit := operation_setting.GetTokenCountMaxBytes(); limit > 0 && len(meta.CombineText) > limit {
		return 0, fmt.Errorf("token count input exceeds %d bytes", limit)
	}
	model := common.GetContextKeyString(c, constant.ContextKeyOriginalModel)
	tkm := 0

	if meta.TokenType == types.TokenTypeTextNumber {
		tkm += utf8.RuneCountInString(meta.CombineText)
	} else {
		tkm += CountTextToken(meta.CombineText, model)
	}

	if info.RelayFormat == types.RelayFormatOpenAI {
		tkm += meta.ToolsCount * 8
		tkm += meta.MessagesCount * 3 // 每条消息的格式化token数量
		tkm += meta.NameCount * 3
		tkm += 3
	}

	shouldFetchFiles := true

	if info.RelayFormat == types.RelayFormatGemini {
		shouldFetchFiles = false
	}

	// 是否本地计算媒体token数量
	if !constant.GetMediaToken {
		shouldFetchFiles = false
	}

	// 是否在非流模式下本地计算媒体token数量
	if !constant.GetMediaTokenNotStream && !info.IsStream {
		shouldFetchFiles = false
	}

	// 使用统一的文件服务获取文件类型
	for _, file := range meta.Files {
		if file.Source == nil {
			continue
		}

		// 如果文件类型未知且需要获取，通过 MIME 类型检测
		if file.FileType == "" || (file.Source.IsURL() && shouldFetchFiles) {
			// 注意：这里我们直接调用 LoadFileSource 而不是 GetMimeType
			// 因为 GetMimeType 内部可能会调用 GetFileTypeFromUrl (HEAD 请求)
			// 而我们这里既然要计算 token，通常需要完整数据
			cachedData, err := LoadFileSource(c, file.Source, "token_counter")
			if err != nil {
				if shouldFetchFiles {
					return 0, fmt.Errorf("error getting file type: %v", err)
				}
				continue
			}
			file.FileType = DetectFileType(cachedData.MimeType)
		}
	}

	for i, file := range meta.Files {
		switch file.FileType {
		case types.FileTypeImage:
			if common.IsOpenAITextModel(model) || strings.Contains(strings.ToLower(model), "claude") || strings.Contains(strings.ToLower(model), "gemini") {
				token, err := getImageToken(c, file, model, info.IsStream)
				if err != nil {
					return 0, fmt.Errorf("error counting image token, media index[%d], identifier[%s], err: %v", i, file.GetIdentifier(), err)
				}
				tkm += token
			} else {
				tkm += 520
			}
		case types.FileTypeAudio:
			tkm += 256
		case types.FileTypeVideo:
			tkm += 4096 * 2
		case types.FileTypeFile:
			tkm += 4096
		default:
			tkm += 4096 // Default case for unknown file types
		}
	}

	common.SetContextKey(c, constant.ContextKeyPromptTokens, tkm)
	return tkm, nil
}

func CountTokenRealtime(info *relaycommon.RelayInfo, request dto.RealtimeEvent, model string) (int, int, error) {
	audioToken := 0
	textToken := 0
	switch request.Type {
	case dto.RealtimeEventTypeSessionUpdate:
		if request.Session != nil {
			msgTokens := CountTextToken(request.Session.Instructions, model)
			textToken += msgTokens
		}
	case dto.RealtimeEventResponseAudioDelta:
		// count audio token
		atk, err := CountAudioTokenOutput(request.Delta, info.OutputAudioFormat)
		if err != nil {
			return 0, 0, fmt.Errorf("error counting audio token: %v", err)
		}
		audioToken += atk
	case dto.RealtimeEventResponseAudioTranscriptionDelta, dto.RealtimeEventResponseFunctionCallArgumentsDelta:
		// count text token
		tkm := CountTextToken(request.Delta, model)
		textToken += tkm
	case dto.RealtimeEventInputAudioBufferAppend:
		// count audio token
		atk, err := CountAudioTokenInput(request.Audio, info.InputAudioFormat)
		if err != nil {
			return 0, 0, fmt.Errorf("error counting audio token: %v", err)
		}
		audioToken += atk
	case dto.RealtimeEventConversationItemCreated:
		if request.Item != nil {
			switch request.Item.Type {
			case "message":
				for _, content := range request.Item.Content {
					if content.Type == "input_text" {
						tokens := CountTextToken(content.Text, model)
						textToken += tokens
					}
				}
			}
		}
	case dto.RealtimeEventTypeResponseDone:
		// count tools token
		if !info.IsFirstRequest {
			if info.RealtimeTools != nil && len(info.RealtimeTools) > 0 {
				for _, tool := range info.RealtimeTools {
					toolTokens := CountTokenInput(tool, model)
					textToken += 8
					textToken += toolTokens
				}
			}
		}
	}
	return textToken, audioToken, nil
}

func CountTokenInput(input any, model string) int {
	switch v := input.(type) {
	case string:
		return CountTextToken(v, model)
	case []string:
		text := ""
		for _, s := range v {
			text += s
		}
		return CountTextToken(text, model)
	case []interface{}:
		text := ""
		for _, item := range v {
			text += fmt.Sprintf("%v", item)
		}
		return CountTextToken(text, model)
	}
	return CountTokenInput(fmt.Sprintf("%v", input), model)
}

func CountAudioTokenInput(audioBase64 string, audioFormat string) (int, error) {
	if audioBase64 == "" {
		return 0, nil
	}
	duration, err := parseAudio(audioBase64, audioFormat)
	if err != nil {
		return 0, err
	}
	// duration 来自用户提供的音频元数据，饱和转换防止 int 回绕
	return common.QuotaFromFloat(duration / 60 * 100 / 0.06), nil
}

func CountAudioTokenOutput(audioBase64 string, audioFormat string) (int, error) {
	if audioBase64 == "" {
		return 0, nil
	}
	duration, err := parseAudio(audioBase64, audioFormat)
	if err != nil {
		return 0, err
	}
	// duration 来自上游返回的音频元数据，饱和转换防止 int 回绕
	return common.QuotaFromFloat(duration / 60 * 200 / 0.24), nil
}

// CountTextToken 统计文本的token数量，仅OpenAI模型使用tokenizer，其余模型使用估算
func CountTextToken(text string, model string) int {
	if text == "" {
		return 0
	}
	if common.IsOpenAITextModel(model) {
		tokenEncoder := getTokenEncoder(model)
		return getTokenNum(tokenEncoder, text)
	} else {
		// 非openai模型，使用tiktoken-go计算没有意义，使用估算节省资源
		return EstimateTokenByModel(model, text)
	}
}
