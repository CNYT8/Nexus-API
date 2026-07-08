package model

import (
	"math"
	"sort"
	"strings"
	"sync"

	"github.com/QuantumNous/new-api/common"
	"golang.org/x/sync/singleflight"
)

const (
	modelMonitorWindowSeconds = 7 * 24 * 60 * 60
	modelMonitorHotSeconds    = 3 * 24 * 60 * 60
	modelMonitorCacheSeconds  = 60
	modelMonitorSlowSeconds   = 30
)

type ModelMonitorModel struct {
	ModelName  string `json:"model_name"`
	Group      string `json:"group"`
	Score      int    `json:"score"`
	Status     string `json:"status"`
	StatusText string `json:"status_text"`
	HasData    bool   `json:"has_data"`
}

type ModelMonitorVendor struct {
	ID           int                 `json:"id"`
	Name         string              `json:"name"`
	Description  string              `json:"description,omitempty"`
	Icon         string              `json:"icon,omitempty"`
	Score        int                 `json:"score"`
	Status       string              `json:"status"`
	StatusText   string              `json:"status_text"`
	KnownCount   int                 `json:"known_count"`
	UnknownCount int                 `json:"unknown_count"`
	Models       []ModelMonitorModel `json:"models"`
}

type ModelMonitorSummary struct {
	WindowDays     int                  `json:"window_days"`
	HotDays        int                  `json:"hot_days"`
	RefreshSeconds int                  `json:"refresh_seconds"`
	UpdatedAt      int64                `json:"updated_at"`
	ModelCount     int                  `json:"model_count"`
	KnownCount     int                  `json:"known_count"`
	UnknownCount   int                  `json:"unknown_count"`
	VendorCount    int                  `json:"vendor_count"`
	BestScore      int                  `json:"best_score"`
	Vendors        []ModelMonitorVendor `json:"vendors"`
}

var modelMonitorCache = struct {
	sync.RWMutex
	summary   *ModelMonitorSummary
	expiresAt int64
}{}

var modelMonitorBuildGroup singleflight.Group

type modelMonitorBucket struct {
	weightedRequests         float64
	weightedSuccess          float64
	weightedErrors           float64
	weightedPromptTokens     float64
	weightedCompletionTokens float64
	weightedTokens           float64
	weightedUseTime          float64
	weightedEmptyOutputs     float64
	weightedSlowRequests     float64
	lastSeen                 int64
}

type modelMonitorModelKey struct {
	modelName string
	group     string
}

type modelMonitorModelEntry struct {
	modelName string
	group     string
	vendor    PricingVendor
}

func normalizeModelMonitorGroup(group string) string {
	group = strings.TrimSpace(group)
	if group == "" {
		return "default"
	}
	return group
}

func normalizeModelMonitorGroups(groups []string) []string {
	if len(groups) == 0 {
		return []string{"default"}
	}
	seen := make(map[string]struct{}, len(groups))
	normalized := make([]string, 0, len(groups))
	for _, group := range groups {
		group = normalizeModelMonitorGroup(group)
		if _, ok := seen[group]; ok {
			continue
		}
		seen[group] = struct{}{}
		normalized = append(normalized, group)
	}
	if len(normalized) == 0 {
		return []string{"default"}
	}
	sort.Slice(normalized, func(i, j int) bool {
		if normalized[i] == "default" {
			return true
		}
		if normalized[j] == "default" {
			return false
		}
		return normalized[i] < normalized[j]
	})
	return normalized
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

func clampModelMonitorRatio(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}

func modelMonitorSafeRatio(value float64, total float64) float64 {
	if total <= 0 {
		return 0
	}
	return value / total
}

func modelMonitorLatencyScore(avgUseTime float64, slowRate float64) float64 {
	if avgUseTime <= 0 {
		return 72
	}
	if avgUseTime <= 2 {
		return 100 * (1 - clampModelMonitorRatio(slowRate)*0.12)
	}
	if avgUseTime >= 45 {
		return 15
	}
	score := 100 - ((avgUseTime-2)/43)*85
	return math.Max(0, score-clampModelMonitorRatio(slowRate)*18)
}

func modelMonitorThroughputScore(completionTokens float64, useTime float64, hasSuccess bool) float64 {
	if !hasSuccess {
		return 0
	}
	if completionTokens <= 0 {
		return 25
	}
	if useTime <= 0 {
		return 70
	}
	tokensPerSecond := completionTokens / useTime
	switch {
	case tokensPerSecond >= 20:
		return 100
	case tokensPerSecond >= 10:
		return 85
	case tokensPerSecond >= 4:
		return 65
	case tokensPerSecond >= 1:
		return 42
	default:
		return 25
	}
}

func modelMonitorOutputBalanceScore(avgPromptTokens float64, avgCompletionTokens float64, hasSuccess bool) float64 {
	if !hasSuccess {
		return 0
	}
	if avgCompletionTokens <= 0 {
		return 20
	}
	if avgPromptTokens <= 0 {
		return 75
	}
	outputRatio := avgCompletionTokens / avgPromptTokens
	switch {
	case outputRatio >= 0.2:
		return 100
	case outputRatio >= 0.08:
		return 85
	case outputRatio >= 0.03:
		return 65
	default:
		return 45
	}
}

func modelMonitorStatus(score int, hasData bool) (string, string) {
	if !hasData {
		return "unknown", "未知状态"
	}
	if score >= 85 {
		return "excellent", "优秀"
	}
	if score >= 70 {
		return "good", "良好"
	}
	if score >= 45 {
		return "unstable", "不稳定"
	}
	return "poor", "体验较差"
}

func scoreModelMonitorBucket(bucket modelMonitorBucket) int {
	if bucket.weightedRequests <= 0 {
		return 1
	}

	hasSuccess := bucket.weightedSuccess > 0
	successRate := clampModelMonitorRatio(modelMonitorSafeRatio(bucket.weightedSuccess, bucket.weightedRequests))
	errorRate := clampModelMonitorRatio(modelMonitorSafeRatio(bucket.weightedErrors, bucket.weightedRequests))
	emptyRate := clampModelMonitorRatio(modelMonitorSafeRatio(bucket.weightedEmptyOutputs, bucket.weightedSuccess))
	slowRate := clampModelMonitorRatio(modelMonitorSafeRatio(bucket.weightedSlowRequests, bucket.weightedRequests))
	avgUseTime := modelMonitorSafeRatio(bucket.weightedUseTime, bucket.weightedRequests)
	avgPromptTokens := modelMonitorSafeRatio(bucket.weightedPromptTokens, bucket.weightedSuccess)
	avgCompletionTokens := modelMonitorSafeRatio(bucket.weightedCompletionTokens, bucket.weightedSuccess)

	reliabilityScore := successRate * 100
	emptyOutputScore := 100 * (1 - emptyRate)
	if !hasSuccess {
		emptyOutputScore = 0
	}
	latencyScore := modelMonitorLatencyScore(avgUseTime, slowRate)
	throughputScore := modelMonitorThroughputScore(bucket.weightedCompletionTokens, bucket.weightedUseTime, hasSuccess)
	outputBalanceScore := modelMonitorOutputBalanceScore(avgPromptTokens, avgCompletionTokens, hasSuccess)
	tokenVolumeScore := 0.0
	if hasSuccess {
		tokenVolumeScore = math.Min(1, math.Log1p(avgCompletionTokens)/math.Log1p(800)) * 100
	}
	sampleScore := math.Min(1, math.Log1p(bucket.weightedRequests)/math.Log1p(40)) * 100

	score := reliabilityScore*0.32 +
		emptyOutputScore*0.18 +
		latencyScore*0.16 +
		throughputScore*0.12 +
		outputBalanceScore*0.10 +
		tokenVolumeScore*0.06 +
		sampleScore*0.06
	if errorRate >= 0.8 {
		score = math.Min(score, 42)
	}
	if emptyRate >= 0.7 {
		score = math.Min(score, 50)
	}
	if hasSuccess && avgPromptTokens > 0 && avgCompletionTokens/avgPromptTokens < 0.02 {
		score = math.Min(score, 72)
	}
	if hasSuccess && bucket.weightedCompletionTokens <= 0 {
		score = math.Min(score, 52)
	}
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

func getOrCreateModelMonitorVendor(vendorMap map[string]*ModelMonitorVendor, vendor PricingVendor, modelName string) *ModelMonitorVendor {
	if vendor.Name == "" {
		vendor.Name = modelMonitorVendorFallback(modelName)
	}
	group := vendorMap[vendor.Name]
	if group != nil {
		return group
	}
	group = &ModelMonitorVendor{
		ID:          vendor.ID,
		Name:        vendor.Name,
		Description: vendor.Description,
		Icon:        vendor.Icon,
		Models:      make([]ModelMonitorModel, 0),
	}
	vendorMap[vendor.Name] = group
	return group
}

func appendModelMonitorModel(vendorMap map[string]*ModelMonitorVendor, vendor PricingVendor, modelName string, group string, bucket modelMonitorBucket, hasData bool) {
	score := 0
	if hasData {
		score = scoreModelMonitorBucket(bucket)
	}
	status, statusText := modelMonitorStatus(score, hasData)
	vendorGroup := getOrCreateModelMonitorVendor(vendorMap, vendor, modelName)
	vendorGroup.Models = append(vendorGroup.Models, ModelMonitorModel{
		ModelName:  modelName,
		Group:      normalizeModelMonitorGroup(group),
		Score:      score,
		Status:     status,
		StatusText: statusText,
		HasData:    hasData,
	})
}

func cloneModelMonitorSummary(summary *ModelMonitorSummary) *ModelMonitorSummary {
	if summary == nil {
		return nil
	}
	out := *summary
	out.Vendors = make([]ModelMonitorVendor, len(summary.Vendors))
	for i := range summary.Vendors {
		out.Vendors[i] = summary.Vendors[i]
		out.Vendors[i].Models = append([]ModelMonitorModel(nil), summary.Vendors[i].Models...)
	}
	return &out
}

func InvalidateModelMonitorCache() {
	modelMonitorCache.Lock()
	defer modelMonitorCache.Unlock()
	modelMonitorCache.summary = nil
	modelMonitorCache.expiresAt = 0
}

func GetModelMonitorSummary() (*ModelMonitorSummary, error) {
	now := common.GetTimestamp()
	modelMonitorCache.RLock()
	if modelMonitorCache.summary != nil && now < modelMonitorCache.expiresAt {
		summary := cloneModelMonitorSummary(modelMonitorCache.summary)
		modelMonitorCache.RUnlock()
		return summary, nil
	}
	staleSummary := cloneModelMonitorSummary(modelMonitorCache.summary)
	modelMonitorCache.RUnlock()

	result, err, _ := modelMonitorBuildGroup.Do("summary", func() (interface{}, error) {
		buildNow := common.GetTimestamp()
		modelMonitorCache.RLock()
		if modelMonitorCache.summary != nil && buildNow < modelMonitorCache.expiresAt {
			summary := cloneModelMonitorSummary(modelMonitorCache.summary)
			modelMonitorCache.RUnlock()
			return summary, nil
		}
		modelMonitorCache.RUnlock()

		summary, err := buildModelMonitorSummary(buildNow)
		if err != nil {
			return nil, err
		}

		modelMonitorCache.Lock()
		modelMonitorCache.summary = cloneModelMonitorSummary(summary)
		modelMonitorCache.expiresAt = buildNow + modelMonitorCacheSeconds
		modelMonitorCache.Unlock()

		return cloneModelMonitorSummary(summary), nil
	})
	if err != nil {
		if staleSummary != nil {
			return staleSummary, nil
		}
		return nil, err
	}
	summary, _ := result.(*ModelMonitorSummary)
	if summary == nil {
		return staleSummary, nil
	}
	return cloneModelMonitorSummary(summary), nil
}

func buildModelMonitorSummary(now int64) (*ModelMonitorSummary, error) {
	since := now - modelMonitorWindowSeconds
	pricing := GetPricing()
	vendorByModel := getModelMonitorVendorMap(pricing, GetVendors())
	activeModels := make([]string, 0, len(pricing))
	activeModelSet := make(map[string]struct{}, len(pricing))
	activeEntrySet := make(map[modelMonitorModelKey]struct{}, len(pricing))
	activeEntries := make([]modelMonitorModelEntry, 0, len(pricing))

	for _, item := range pricing {
		modelName := strings.TrimSpace(item.ModelName)
		if modelName == "" {
			continue
		}
		if _, ok := activeModelSet[modelName]; !ok {
			activeModelSet[modelName] = struct{}{}
			activeModels = append(activeModels, modelName)
		}
		vendor, ok := vendorByModel[modelName]
		if !ok || vendor.Name == "" {
			vendor.Name = modelMonitorVendorFallback(modelName)
		}
		for _, group := range normalizeModelMonitorGroups(item.EnableGroup) {
			key := modelMonitorModelKey{
				modelName: modelName,
				group:     group,
			}
			if _, ok := activeEntrySet[key]; ok {
				continue
			}
			activeEntrySet[key] = struct{}{}
			activeEntries = append(activeEntries, modelMonitorModelEntry{
				modelName: modelName,
				group:     group,
				vendor:    vendor,
			})
		}
	}

	weightSQL := modelMonitorWeightSQL()
	groupSQL := "COALESCE(NULLIF(TRIM(" + logGroupCol + "), ''), 'default')"
	selectSQL := "model_name, " + groupSQL + " AS group_name, " +
		"SUM(" + weightSQL + ") AS weighted_requests, " +
		"SUM(CASE WHEN type = ? THEN " + weightSQL + " ELSE 0 END) AS weighted_success, " +
		"SUM(CASE WHEN type = ? THEN " + weightSQL + " ELSE 0 END) AS weighted_errors, " +
		"SUM(CASE WHEN type = ? THEN prompt_tokens * (" + weightSQL + ") ELSE 0 END) AS weighted_prompt_tokens, " +
		"SUM(CASE WHEN type = ? THEN completion_tokens * (" + weightSQL + ") ELSE 0 END) AS weighted_completion_tokens, " +
		"SUM((prompt_tokens + completion_tokens) * (" + weightSQL + ")) AS weighted_tokens, " +
		"SUM(use_time * (" + weightSQL + ")) AS weighted_use_time, " +
		"SUM(CASE WHEN type = ? AND prompt_tokens > 0 AND completion_tokens <= 0 THEN " + weightSQL + " ELSE 0 END) AS weighted_empty_outputs, " +
		"SUM(CASE WHEN use_time >= ? THEN " + weightSQL + " ELSE 0 END) AS weighted_slow_requests, " +
		"MAX(created_at) AS last_seen"

	selectArgs := make([]any, 0, 150)
	selectArgs = appendModelMonitorWeightSQLArgs(selectArgs, now)
	selectArgs = append(selectArgs, LogTypeConsume)
	selectArgs = appendModelMonitorWeightSQLArgs(selectArgs, now)
	selectArgs = append(selectArgs, LogTypeError)
	selectArgs = appendModelMonitorWeightSQLArgs(selectArgs, now)
	selectArgs = append(selectArgs, LogTypeConsume)
	selectArgs = appendModelMonitorWeightSQLArgs(selectArgs, now)
	selectArgs = append(selectArgs, LogTypeConsume)
	selectArgs = appendModelMonitorWeightSQLArgs(selectArgs, now)
	selectArgs = appendModelMonitorWeightSQLArgs(selectArgs, now)
	selectArgs = appendModelMonitorWeightSQLArgs(selectArgs, now)
	selectArgs = append(selectArgs, LogTypeConsume)
	selectArgs = appendModelMonitorWeightSQLArgs(selectArgs, now)
	selectArgs = append(selectArgs, modelMonitorSlowSeconds)
	selectArgs = appendModelMonitorWeightSQLArgs(selectArgs, now)

	type row struct {
		ModelName                string
		GroupName                string
		WeightedRequests         float64
		WeightedSuccess          float64
		WeightedErrors           float64
		WeightedPromptTokens     float64
		WeightedCompletionTokens float64
		WeightedTokens           float64
		WeightedUseTime          float64
		WeightedEmptyOutputs     float64
		WeightedSlowRequests     float64
		LastSeen                 int64
	}

	var rows []row
	if len(activeModels) > 0 {
		if err := LOG_DB.Model(&Log{}).
			Select(selectSQL, selectArgs...).
			Where("created_at >= ? AND model_name IN ? AND type IN ?", since, activeModels, []int{LogTypeConsume, LogTypeError}).
			Group("model_name, " + groupSQL).
			Find(&rows).Error; err != nil {
			return nil, err
		}
	}

	buckets := make(map[modelMonitorModelKey]modelMonitorBucket)
	for _, item := range rows {
		modelName := strings.TrimSpace(item.ModelName)
		if modelName == "" {
			continue
		}
		if _, ok := activeModelSet[modelName]; !ok {
			continue
		}
		key := modelMonitorModelKey{
			modelName: modelName,
			group:     normalizeModelMonitorGroup(item.GroupName),
		}
		if _, ok := activeEntrySet[key]; !ok {
			continue
		}
		buckets[key] = modelMonitorBucket{
			weightedRequests:         item.WeightedRequests,
			weightedSuccess:          item.WeightedSuccess,
			weightedErrors:           item.WeightedErrors,
			weightedPromptTokens:     item.WeightedPromptTokens,
			weightedCompletionTokens: item.WeightedCompletionTokens,
			weightedTokens:           item.WeightedTokens,
			weightedUseTime:          item.WeightedUseTime,
			weightedEmptyOutputs:     item.WeightedEmptyOutputs,
			weightedSlowRequests:     item.WeightedSlowRequests,
			lastSeen:                 item.LastSeen,
		}
	}

	vendorMap := make(map[string]*ModelMonitorVendor)

	for _, item := range activeEntries {
		key := modelMonitorModelKey{
			modelName: item.modelName,
			group:     item.group,
		}
		bucket, hasData := buckets[key]
		appendModelMonitorModel(vendorMap, item.vendor, item.modelName, item.group, bucket, hasData)
	}

	vendors := make([]ModelMonitorVendor, 0, len(vendorMap))
	bestScore := 0
	modelCount := 0
	knownCount := 0
	unknownCount := 0
	for _, vendor := range vendorMap {
		sort.Slice(vendor.Models, func(i, j int) bool {
			if vendor.Models[i].HasData != vendor.Models[j].HasData {
				return vendor.Models[i].HasData
			}
			if vendor.Models[i].Score == vendor.Models[j].Score {
				if vendor.Models[i].ModelName == vendor.Models[j].ModelName {
					return vendor.Models[i].Group < vendor.Models[j].Group
				}
				return vendor.Models[i].ModelName < vendor.Models[j].ModelName
			}
			return vendor.Models[i].Score > vendor.Models[j].Score
		})
		var weightedScore float64
		var totalWeight float64
		for _, item := range vendor.Models {
			modelCount++
			if !item.HasData {
				vendor.UnknownCount++
				unknownCount++
				continue
			}
			vendor.KnownCount++
			knownCount++
			bucket := buckets[modelMonitorModelKey{
				modelName: item.ModelName,
				group:     normalizeModelMonitorGroup(item.Group),
			}]
			weight := bucket.weightedRequests
			if weight <= 0 {
				weight = 1
			}
			weightedScore += float64(item.Score) * weight
			totalWeight += weight
			if item.Score > bestScore {
				bestScore = item.Score
			}
		}
		if totalWeight > 0 {
			vendor.Score = clampModelMonitorScore(weightedScore / totalWeight)
			vendor.Status, vendor.StatusText = modelMonitorStatus(vendor.Score, true)
		} else {
			vendor.Status, vendor.StatusText = modelMonitorStatus(0, false)
		}
		vendors = append(vendors, *vendor)
	}

	sort.Slice(vendors, func(i, j int) bool {
		if (vendors[i].KnownCount > 0) != (vendors[j].KnownCount > 0) {
			return vendors[i].KnownCount > 0
		}
		if vendors[i].Score == vendors[j].Score {
			return vendors[i].Name < vendors[j].Name
		}
		return vendors[i].Score > vendors[j].Score
	})

	return &ModelMonitorSummary{
		WindowDays:     7,
		HotDays:        3,
		RefreshSeconds: modelMonitorCacheSeconds,
		UpdatedAt:      now,
		ModelCount:     modelCount,
		KnownCount:     knownCount,
		UnknownCount:   unknownCount,
		VendorCount:    len(vendors),
		BestScore:      bestScore,
		Vendors:        vendors,
	}, nil
}
