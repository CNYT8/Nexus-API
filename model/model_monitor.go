package model

import (
	"math"
	"sort"
	"strings"

	"github.com/QuantumNous/new-api/common"
)

const (
	modelMonitorWindowSeconds = 7 * 24 * 60 * 60
	modelMonitorHotSeconds    = 3 * 24 * 60 * 60
)

type ModelMonitorModel struct {
	ModelName string `json:"model_name"`
	Score     int    `json:"score"`
}

type ModelMonitorVendor struct {
	ID          int                 `json:"id"`
	Name        string              `json:"name"`
	Description string              `json:"description,omitempty"`
	Icon        string              `json:"icon,omitempty"`
	Score       int                 `json:"score"`
	Models      []ModelMonitorModel `json:"models"`
}

type ModelMonitorSummary struct {
	WindowDays  int                  `json:"window_days"`
	HotDays     int                  `json:"hot_days"`
	UpdatedAt   int64                `json:"updated_at"`
	ModelCount  int                  `json:"model_count"`
	VendorCount int                  `json:"vendor_count"`
	BestScore   int                  `json:"best_score"`
	Vendors     []ModelMonitorVendor `json:"vendors"`
}

type modelMonitorBucket struct {
	weightedRequests float64
	weightedSuccess  float64
	weightedErrors   float64
	weightedTokens   float64
	weightedUseTime  float64
	lastSeen         int64
}

func modelMonitorWeight(createdAt int64, now int64) float64 {
	age := now - createdAt
	if age < 0 {
		age = 0
	}
	if age <= modelMonitorHotSeconds {
		// 三天内放大，越近越高。
		return 2.0 - float64(age)/float64(modelMonitorHotSeconds)*0.5
	}
	if age >= modelMonitorWindowSeconds {
		return 0.2
	}
	restAge := age - modelMonitorHotSeconds
	restWindow := modelMonitorWindowSeconds - modelMonitorHotSeconds
	return 1.0 - float64(restAge)/float64(restWindow)*0.65
}

func modelMonitorWeightSQL() string {
	return "CASE " +
		"WHEN (? - created_at) <= 0 THEN ? " +
		"WHEN (? - created_at) <= ? THEN ? - (((? - created_at) / ?) * ?) " +
		"WHEN (? - created_at) >= ? THEN ? " +
		"ELSE ? - (((? - created_at) - ?) / ?) * ? " +
		"END"
}

func modelMonitorWeightSQLArgs(now int64) []any {
	return []any{
		now, 2.0,
		now, modelMonitorHotSeconds, 2.0, now, float64(modelMonitorHotSeconds), 0.5,
		now, modelMonitorWindowSeconds, 0.2,
		1.0, now, modelMonitorHotSeconds, float64(modelMonitorWindowSeconds - modelMonitorHotSeconds), 0.65,
	}
}

func appendModelMonitorWeightSQLArgs(args []any, now int64) []any {
	return append(args, modelMonitorWeightSQLArgs(now)...)
}

func clampModelMonitorScore(score float64) int {
	if score < 1 {
		return 1
	}
	if score > 100 {
		return 100
	}
	return int(math.Round(score))
}

func scoreModelMonitorBucket(bucket modelMonitorBucket) int {
	if bucket.weightedRequests <= 0 {
		return 1
	}

	successRate := bucket.weightedSuccess / bucket.weightedRequests
	errorRate := bucket.weightedErrors / bucket.weightedRequests
	avgTokens := bucket.weightedTokens / bucket.weightedRequests
	avgUseTime := bucket.weightedUseTime / bucket.weightedRequests

	tokenFactor := math.Log1p(avgTokens) / math.Log1p(32000)
	if tokenFactor > 1 {
		tokenFactor = 1
	}

	latencyFactor := 1.0
	if avgUseTime > 0 {
		latencyFactor = 1 - math.Min(avgUseTime, 30)/30
	}

	sampleFactor := math.Min(1, math.Log1p(bucket.weightedRequests)/math.Log1p(30))
	score := 45 + successRate*34 - errorRate*38 + tokenFactor*11 + latencyFactor*8 + sampleFactor*6
	if bucket.weightedRequests < 2 {
		score = math.Min(score, 68)
	}
	return clampModelMonitorScore(score)
}

func modelMonitorVendorFallback(modelName string) string {
	lowerName := strings.ToLower(modelName)
	switch {
	case strings.Contains(lowerName, "gpt"), strings.Contains(lowerName, "dall-e"), strings.Contains(lowerName, "whisper"), strings.HasPrefix(lowerName, "o1"), strings.HasPrefix(lowerName, "o3"), strings.HasPrefix(lowerName, "o4"):
		return "OpenAI"
	case strings.Contains(lowerName, "claude"):
		return "Anthropic"
	case strings.Contains(lowerName, "gemini"), strings.Contains(lowerName, "gemma"):
		return "Gemini"
	case strings.Contains(lowerName, "qwen"):
		return "通义千问"
	case strings.Contains(lowerName, "deepseek"):
		return "DeepSeek"
	case strings.Contains(lowerName, "glm"), strings.Contains(lowerName, "chatglm"):
		return "智谱"
	case strings.Contains(lowerName, "moonshot"), strings.Contains(lowerName, "kimi"):
		return "Moonshot"
	case strings.Contains(lowerName, "mistral"), strings.Contains(lowerName, "codestral"):
		return "Mistral AI"
	case strings.Contains(lowerName, "grok"):
		return "xAI"
	case strings.Contains(lowerName, "llama"):
		return "Llama"
	case strings.Contains(lowerName, "doubao"):
		return "豆包"
	default:
		return "未知供应商"
	}
}

func getModelMonitorVendorMap(pricing []Pricing, vendors []PricingVendor) map[string]PricingVendor {
	vendorByID := make(map[int]PricingVendor, len(vendors))
	for _, vendor := range vendors {
		vendorByID[vendor.ID] = vendor
	}

	modelVendorMap := make(map[string]PricingVendor, len(pricing))
	for _, item := range pricing {
		if item.ModelName == "" {
			continue
		}
		if vendor, ok := vendorByID[item.VendorID]; ok && vendor.Name != "" {
			modelVendorMap[item.ModelName] = vendor
		}
	}
	return modelVendorMap
}

func GetModelMonitorSummary() (*ModelMonitorSummary, error) {
	now := common.GetTimestamp()
	since := now - modelMonitorWindowSeconds

	weightSQL := modelMonitorWeightSQL()
	selectSQL := "model_name, " +
		"SUM(" + weightSQL + ") AS weighted_requests, " +
		"SUM(CASE WHEN type = ? THEN " + weightSQL + " ELSE 0 END) AS weighted_success, " +
		"SUM(CASE WHEN type = ? THEN " + weightSQL + " ELSE 0 END) AS weighted_errors, " +
		"SUM((prompt_tokens + completion_tokens) * (" + weightSQL + ")) AS weighted_tokens, " +
		"SUM(use_time * (" + weightSQL + ")) AS weighted_use_time, " +
		"MAX(created_at) AS last_seen"

	selectArgs := make([]any, 0, 83)
	selectArgs = appendModelMonitorWeightSQLArgs(selectArgs, now)
	selectArgs = append(selectArgs, LogTypeConsume)
	selectArgs = appendModelMonitorWeightSQLArgs(selectArgs, now)
	selectArgs = append(selectArgs, LogTypeError)
	selectArgs = appendModelMonitorWeightSQLArgs(selectArgs, now)
	selectArgs = appendModelMonitorWeightSQLArgs(selectArgs, now)
	selectArgs = appendModelMonitorWeightSQLArgs(selectArgs, now)

	type row struct {
		ModelName        string
		WeightedRequests float64
		WeightedSuccess  float64
		WeightedErrors   float64
		WeightedTokens   float64
		WeightedUseTime  float64
		LastSeen         int64
	}

	var rows []row
	if err := LOG_DB.Model(&Log{}).
		Select(selectSQL, selectArgs...).
		Where("created_at >= ? AND model_name <> '' AND type IN ?", since, []int{LogTypeConsume, LogTypeError}).
		Group("model_name").
		Find(&rows).Error; err != nil {
		return nil, err
	}

	buckets := make(map[string]modelMonitorBucket)
	for _, item := range rows {
		modelName := strings.TrimSpace(item.ModelName)
		if modelName == "" {
			continue
		}
		buckets[modelName] = modelMonitorBucket{
			weightedRequests: item.WeightedRequests,
			weightedSuccess:  item.WeightedSuccess,
			weightedErrors:   item.WeightedErrors,
			weightedTokens:   item.WeightedTokens,
			weightedUseTime:  item.WeightedUseTime,
			lastSeen:         item.LastSeen,
		}
	}

	pricing := GetPricing()
	vendorByModel := getModelMonitorVendorMap(pricing, GetVendors())
	vendorMap := make(map[string]*ModelMonitorVendor)

	for modelName, bucket := range buckets {
		score := scoreModelMonitorBucket(bucket)
		vendor, ok := vendorByModel[modelName]
		if !ok || vendor.Name == "" {
			vendor.Name = modelMonitorVendorFallback(modelName)
		}

		group := vendorMap[vendor.Name]
		if group == nil {
			group = &ModelMonitorVendor{
				ID:          vendor.ID,
				Name:        vendor.Name,
				Description: vendor.Description,
				Icon:        vendor.Icon,
				Models:      make([]ModelMonitorModel, 0),
			}
			vendorMap[vendor.Name] = group
		}

		group.Models = append(group.Models, ModelMonitorModel{
			ModelName: modelName,
			Score:     score,
		})
	}

	vendors := make([]ModelMonitorVendor, 0, len(vendorMap))
	bestScore := 0
	modelCount := 0
	for _, vendor := range vendorMap {
		sort.Slice(vendor.Models, func(i, j int) bool {
			if vendor.Models[i].Score == vendor.Models[j].Score {
				return vendor.Models[i].ModelName < vendor.Models[j].ModelName
			}
			return vendor.Models[i].Score > vendor.Models[j].Score
		})
		var weightedScore float64
		var totalWeight float64
		for _, item := range vendor.Models {
			bucket := buckets[item.ModelName]
			weight := bucket.weightedRequests
			if weight <= 0 {
				weight = 1
			}
			weightedScore += float64(item.Score) * weight
			totalWeight += weight
			modelCount++
			if item.Score > bestScore {
				bestScore = item.Score
			}
		}
		if totalWeight > 0 {
			vendor.Score = clampModelMonitorScore(weightedScore / totalWeight)
		}
		vendors = append(vendors, *vendor)
	}

	sort.Slice(vendors, func(i, j int) bool {
		if vendors[i].Score == vendors[j].Score {
			return vendors[i].Name < vendors[j].Name
		}
		return vendors[i].Score > vendors[j].Score
	})

	return &ModelMonitorSummary{
		WindowDays:  7,
		HotDays:     3,
		UpdatedAt:   now,
		ModelCount:  modelCount,
		VendorCount: len(vendors),
		BestScore:   bestScore,
		Vendors:     vendors,
	}, nil
}
