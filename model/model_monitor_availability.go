package model

import (
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/QuantumNous/new-api/common"
	"golang.org/x/sync/singleflight"
)

const (
	modelMonitorAvailabilityBucketSeconds = 2 * 60 * 60
	modelMonitorAvailabilityBuckets       = 12
)

// ModelMonitorAvailabilityBucket is one 2-hour slot of the rolling 24h window.
// The last bucket is the in-progress slot: it only contains samples up to now,
// and its score is recomputed on every refresh until the slot is complete.
type ModelMonitorAvailabilityBucket struct {
	Start   int64  `json:"start"`
	End     int64  `json:"end"`
	HasData bool   `json:"has_data"`
	Score   int    `json:"score"`
	Status  string `json:"status"`
}

type ModelMonitorAvailabilityModel struct {
	ModelName string                           `json:"model_name"`
	Buckets   []ModelMonitorAvailabilityBucket `json:"buckets"`
}

type ModelMonitorAvailabilityGroup struct {
	Group   string                           `json:"group"`
	Buckets []ModelMonitorAvailabilityBucket `json:"buckets"`
	Models  []ModelMonitorAvailabilityModel  `json:"models"`
}

type ModelMonitorAvailability struct {
	UpdatedAt      int64                           `json:"updated_at"`
	RefreshSeconds int                             `json:"refresh_seconds"`
	BucketSeconds  int                             `json:"bucket_seconds"`
	WindowStart    int64                           `json:"window_start"`
	WindowEnd      int64                           `json:"window_end"`
	Groups         []ModelMonitorAvailabilityGroup `json:"groups"`
}

var modelMonitorAvailabilityCache = struct {
	sync.RWMutex
	availability *ModelMonitorAvailability
	expiresAt    int64
}{}

var modelMonitorAvailabilityBuildGroup singleflight.Group

type modelMonitorAvailabilityKey struct {
	modelName string
	group     string
	index     int
}

type modelMonitorAvailabilityRow struct {
	ModelName                string
	GroupName                string
	BucketStart              int64
	RawErrorLogCount         int64
	SuccessSampleCount       int64
	ErrorSampleCount         int64
	WeightedSuccess          float64
	WeightedErrors           float64
	WeightedPromptTokens     float64
	WeightedCompletionTokens float64
	WeightedUseTime          float64
	WeightedEmptyOutputs     float64
	WeightedSlowRequests     float64
	LatestHealthyAt          int64
	LatestUnhealthyAt        int64
}

func modelMonitorAvailabilityBucketExpr() string {
	return "(created_at - created_at % " + strconv.Itoa(modelMonitorAvailabilityBucketSeconds) + ")"
}

func mergeModelMonitorAvailabilityRows(buckets map[modelMonitorAvailabilityKey]modelMonitorBucket, rows []modelMonitorAvailabilityRow, windowStart int64, activeModelSet map[string]struct{}) {
	for _, item := range rows {
		modelName := strings.TrimSpace(item.ModelName)
		if modelName == "" {
			continue
		}
		if _, ok := activeModelSet[modelName]; !ok {
			continue
		}
		index := int((item.BucketStart - windowStart) / modelMonitorAvailabilityBucketSeconds)
		if index < 0 || index >= modelMonitorAvailabilityBuckets {
			continue
		}
		key := modelMonitorAvailabilityKey{
			modelName: modelName,
			group:     normalizeModelMonitorGroup(item.GroupName),
			index:     index,
		}
		bucket := modelMonitorBucket{
			rawErrorLogCount:         item.RawErrorLogCount,
			sampleCount:              item.SuccessSampleCount + item.ErrorSampleCount,
			errorSampleCount:         item.ErrorSampleCount,
			weightedSuccess:          item.WeightedSuccess,
			weightedErrors:           item.WeightedErrors,
			weightedPromptTokens:     item.WeightedPromptTokens,
			weightedCompletionTokens: item.WeightedCompletionTokens,
			weightedUseTime:          item.WeightedUseTime,
			weightedEmptyOutputs:     item.WeightedEmptyOutputs,
			weightedSlowRequests:     item.WeightedSlowRequests,
			latestHealthyAt:          item.LatestHealthyAt,
			latestUnhealthyAt:        item.LatestUnhealthyAt,
		}
		buckets[key] = mergeModelMonitorBucket(buckets[key], bucket)
	}
}

func queryModelMonitorAvailabilityLogRows(windowStart int64, activeModels []string) ([]modelMonitorAvailabilityRow, error) {
	groupSQL := "COALESCE(NULLIF(TRIM(" + logGroupCol + "), ''), 'default')"
	bucketExpr := modelMonitorAvailabilityBucketExpr()
	selectSQL := "model_name, " + groupSQL + " AS group_name, " + bucketExpr + " AS bucket_start, " +
		"SUM(CASE WHEN type = ? THEN 1 ELSE 0 END) AS raw_error_log_count, " +
		"SUM(CASE WHEN type = ? THEN 1 ELSE 0 END) AS success_sample_count, " +
		"(COUNT(DISTINCT CASE WHEN type = ? THEN NULLIF(request_id, '') ELSE NULL END) + " +
		"SUM(CASE WHEN type = ? AND (request_id IS NULL OR request_id = '') THEN 1 ELSE 0 END)) AS error_sample_count, " +
		"SUM(CASE WHEN type = ? THEN 1 ELSE 0 END) AS weighted_success, " +
		"SUM(CASE WHEN type = ? THEN 1 ELSE 0 END) AS weighted_errors, " +
		"SUM(CASE WHEN type = ? THEN prompt_tokens ELSE 0 END) AS weighted_prompt_tokens, " +
		"SUM(CASE WHEN type = ? THEN completion_tokens ELSE 0 END) AS weighted_completion_tokens, " +
		"SUM(CASE WHEN type = ? THEN use_time ELSE 0 END) AS weighted_use_time, " +
		"SUM(CASE WHEN type = ? AND prompt_tokens > 0 AND completion_tokens <= 0 THEN 1 ELSE 0 END) AS weighted_empty_outputs, " +
		"SUM(CASE WHEN type = ? AND use_time >= (? + completion_tokens / ?) THEN 1 ELSE 0 END) AS weighted_slow_requests, " +
		"MAX(CASE WHEN type = ? AND NOT (prompt_tokens > 0 AND completion_tokens <= 0) THEN created_at ELSE 0 END) AS latest_healthy_at, " +
		"MAX(CASE WHEN type = ? OR (type = ? AND prompt_tokens > 0 AND completion_tokens <= 0) THEN created_at ELSE 0 END) AS latest_unhealthy_at"

	args := []any{
		LogTypeError, LogTypeConsume,
		LogTypeError, LogTypeError,
		LogTypeConsume, LogTypeError,
		LogTypeConsume, LogTypeConsume, LogTypeConsume, LogTypeConsume,
		LogTypeConsume, modelMonitorSlowSeconds, modelMonitorOutputTimeTPS,
		LogTypeConsume, LogTypeError, LogTypeConsume,
	}

	var rows []modelMonitorAvailabilityRow
	err := LOG_DB.Model(&Log{}).
		Select(selectSQL, args...).
		Where("created_at >= ? AND model_name IN ? AND type IN ?", windowStart, activeModels, []int{LogTypeConsume, LogTypeError}).
		Where("NOT (COALESCE(token_name, '') = ? AND COALESCE(content, '') = ? AND token_id = ?)", "模型测试", "模型测试", 0).
		Group("model_name, " + groupSQL + ", " + bucketExpr).
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	return rows, nil
}

func queryModelMonitorAvailabilitySampleRows(windowStart int64, activeModels []string) ([]modelMonitorAvailabilityRow, error) {
	groupSQL := "COALESCE(NULLIF(TRIM(" + logGroupCol + "), ''), 'default')"
	bucketExpr := modelMonitorAvailabilityBucketExpr()
	selectSQL := "model_name, " + groupSQL + " AS group_name, " + bucketExpr + " AS bucket_start, " +
		"SUM(CASE WHEN status = ? THEN 1 ELSE 0 END) AS raw_error_log_count, " +
		"SUM(CASE WHEN status = ? THEN 1 ELSE 0 END) AS success_sample_count, " +
		"SUM(CASE WHEN status = ? THEN 1 ELSE 0 END) AS error_sample_count, " +
		"SUM(CASE WHEN status = ? THEN 1 ELSE 0 END) AS weighted_success, " +
		"SUM(CASE WHEN status = ? THEN 1 ELSE 0 END) AS weighted_errors, " +
		"SUM(CASE WHEN status = ? THEN prompt_tokens ELSE 0 END) AS weighted_prompt_tokens, " +
		"SUM(CASE WHEN status = ? THEN completion_tokens ELSE 0 END) AS weighted_completion_tokens, " +
		"SUM(CASE WHEN status = ? THEN use_time ELSE 0 END) AS weighted_use_time, " +
		"SUM(CASE WHEN status = ? AND prompt_tokens > 0 AND completion_tokens <= 0 THEN 1 ELSE 0 END) AS weighted_empty_outputs, " +
		"SUM(CASE WHEN status = ? AND use_time >= (? + completion_tokens / ?) THEN 1 ELSE 0 END) AS weighted_slow_requests, " +
		"MAX(CASE WHEN status = ? AND NOT (prompt_tokens > 0 AND completion_tokens <= 0) THEN created_at ELSE 0 END) AS latest_healthy_at, " +
		"MAX(CASE WHEN status = ? OR (status = ? AND prompt_tokens > 0 AND completion_tokens <= 0) THEN created_at ELSE 0 END) AS latest_unhealthy_at"

	args := []any{
		ModelMonitorSampleStatusError, ModelMonitorSampleStatusSuccess, ModelMonitorSampleStatusError,
		ModelMonitorSampleStatusSuccess, ModelMonitorSampleStatusError,
		ModelMonitorSampleStatusSuccess, ModelMonitorSampleStatusSuccess, ModelMonitorSampleStatusSuccess,
		ModelMonitorSampleStatusSuccess,
		ModelMonitorSampleStatusSuccess, modelMonitorSlowSeconds, modelMonitorOutputTimeTPS,
		ModelMonitorSampleStatusSuccess, ModelMonitorSampleStatusError, ModelMonitorSampleStatusSuccess,
	}

	var rows []modelMonitorAvailabilityRow
	err := LOG_DB.Model(&ModelMonitorSample{}).
		Select(selectSQL, args...).
		Where("created_at >= ? AND model_name IN ? AND status IN ?", windowStart, activeModels, []int{ModelMonitorSampleStatusSuccess, ModelMonitorSampleStatusError}).
		Group("model_name, " + groupSQL + ", " + bucketExpr).
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	return rows, nil
}

type modelMonitorAvailabilityModelBuild struct {
	entry      ModelMonitorAvailabilityModel
	weights    []float64
	hasAnyData bool
}

func buildModelMonitorAvailability(now int64) (*ModelMonitorAvailability, error) {
	bucketSeconds := int64(modelMonitorAvailabilityBucketSeconds)
	latestStart := now - now%bucketSeconds
	windowStart := latestStart - int64(modelMonitorAvailabilityBuckets-1)*bucketSeconds

	pricing := GetPricing()
	activeModelSet := make(map[string]struct{}, len(pricing))
	groupModelSet := make(map[string]map[string]struct{})
	for _, item := range pricing {
		modelName := strings.TrimSpace(item.ModelName)
		if modelName == "" {
			continue
		}
		activeModelSet[modelName] = struct{}{}
		for _, group := range normalizeModelMonitorGroups(item.EnableGroup) {
			if groupModelSet[group] == nil {
				groupModelSet[group] = make(map[string]struct{})
			}
			groupModelSet[group][modelName] = struct{}{}
		}
	}

	groups := make([]string, 0, len(groupModelSet))
	for group := range groupModelSet {
		groups = append(groups, group)
	}
	sort.Slice(groups, func(i, j int) bool {
		if groups[i] == "default" {
			return true
		}
		if groups[j] == "default" {
			return false
		}
		return groups[i] < groups[j]
	})

	buckets := make(map[modelMonitorAvailabilityKey]modelMonitorBucket)
	if len(activeModelSet) > 0 {
		activeModels := make([]string, 0, len(activeModelSet))
		for modelName := range activeModelSet {
			activeModels = append(activeModels, modelName)
		}
		logRows, err := queryModelMonitorAvailabilityLogRows(windowStart, activeModels)
		if err != nil {
			return nil, err
		}
		mergeModelMonitorAvailabilityRows(buckets, logRows, windowStart, activeModelSet)
		sampleRows, err := queryModelMonitorAvailabilitySampleRows(windowStart, activeModels)
		if err != nil {
			return nil, err
		}
		mergeModelMonitorAvailabilityRows(buckets, sampleRows, windowStart, activeModelSet)
	}

	newBucket := func(start int64) ModelMonitorAvailabilityBucket {
		return ModelMonitorAvailabilityBucket{
			Start:   start,
			End:     start + bucketSeconds,
			HasData: false,
			Status:  "unknown",
		}
	}

	outGroups := make([]ModelMonitorAvailabilityGroup, 0, len(groups))
	for _, group := range groups {
		modelBuilds := make([]modelMonitorAvailabilityModelBuild, 0, len(groupModelSet[group]))
		for modelName := range groupModelSet[group] {
			build := modelMonitorAvailabilityModelBuild{
				entry: ModelMonitorAvailabilityModel{
					ModelName: modelName,
					Buckets:   make([]ModelMonitorAvailabilityBucket, modelMonitorAvailabilityBuckets),
				},
				weights: make([]float64, modelMonitorAvailabilityBuckets),
			}
			for i := 0; i < modelMonitorAvailabilityBuckets; i++ {
				start := windowStart + int64(i)*bucketSeconds
				bucket := newBucket(start)
				b, ok := buckets[modelMonitorAvailabilityKey{modelName: modelName, group: group, index: i}]
				if ok && b.sampleCount > 0 {
					score := scoreModelMonitorBucketAt(b, now)
					status, _ := modelMonitorStatus(score, true)
					bucket.HasData = true
					bucket.Score = score
					bucket.Status = status
					weight := modelMonitorEffectiveRequestWeight(b)
					if weight <= 0 {
						weight = 1
					}
					build.weights[i] = weight
					build.hasAnyData = true
				}
				build.entry.Buckets[i] = bucket
			}
			modelBuilds = append(modelBuilds, build)
		}

		sort.Slice(modelBuilds, func(i, j int) bool {
			if modelBuilds[i].hasAnyData != modelBuilds[j].hasAnyData {
				return modelBuilds[i].hasAnyData
			}
			return modelBuilds[i].entry.ModelName < modelBuilds[j].entry.ModelName
		})

		groupBuckets := make([]ModelMonitorAvailabilityBucket, modelMonitorAvailabilityBuckets)
		for i := 0; i < modelMonitorAvailabilityBuckets; i++ {
			start := windowStart + int64(i)*bucketSeconds
			bucket := newBucket(start)
			var weighted float64
			var totalWeight float64
			for _, build := range modelBuilds {
				if !build.entry.Buckets[i].HasData {
					continue
				}
				weighted += float64(build.entry.Buckets[i].Score) * build.weights[i]
				totalWeight += build.weights[i]
			}
			if totalWeight > 0 {
				score := clampModelMonitorScore(weighted / totalWeight)
				status, _ := modelMonitorStatus(score, true)
				bucket.HasData = true
				bucket.Score = score
				bucket.Status = status
			}
			groupBuckets[i] = bucket
		}

		models := make([]ModelMonitorAvailabilityModel, 0, len(modelBuilds))
		for _, build := range modelBuilds {
			models = append(models, build.entry)
		}
		outGroups = append(outGroups, ModelMonitorAvailabilityGroup{
			Group:   group,
			Buckets: groupBuckets,
			Models:  models,
		})
	}

	return &ModelMonitorAvailability{
		UpdatedAt:      now,
		RefreshSeconds: modelMonitorCacheSeconds,
		BucketSeconds:  int(bucketSeconds),
		WindowStart:    windowStart,
		WindowEnd:      now,
		Groups:         outGroups,
	}, nil
}

func cloneModelMonitorAvailability(availability *ModelMonitorAvailability) *ModelMonitorAvailability {
	if availability == nil {
		return nil
	}
	out := *availability
	out.Groups = make([]ModelMonitorAvailabilityGroup, len(availability.Groups))
	for i := range availability.Groups {
		out.Groups[i] = availability.Groups[i]
		out.Groups[i].Buckets = append([]ModelMonitorAvailabilityBucket(nil), availability.Groups[i].Buckets...)
		out.Groups[i].Models = make([]ModelMonitorAvailabilityModel, len(availability.Groups[i].Models))
		for j := range availability.Groups[i].Models {
			out.Groups[i].Models[j] = availability.Groups[i].Models[j]
			out.Groups[i].Models[j].Buckets = append([]ModelMonitorAvailabilityBucket(nil), availability.Groups[i].Models[j].Buckets...)
		}
	}
	return &out
}

func invalidateModelMonitorAvailabilityCache() {
	modelMonitorAvailabilityCache.Lock()
	defer modelMonitorAvailabilityCache.Unlock()
	modelMonitorAvailabilityCache.availability = nil
	modelMonitorAvailabilityCache.expiresAt = 0
}

func GetModelMonitorAvailability() (*ModelMonitorAvailability, error) {
	now := common.GetTimestamp()
	modelMonitorAvailabilityCache.RLock()
	if modelMonitorAvailabilityCache.availability != nil && now < modelMonitorAvailabilityCache.expiresAt {
		availability := cloneModelMonitorAvailability(modelMonitorAvailabilityCache.availability)
		modelMonitorAvailabilityCache.RUnlock()
		return availability, nil
	}
	staleAvailability := cloneModelMonitorAvailability(modelMonitorAvailabilityCache.availability)
	modelMonitorAvailabilityCache.RUnlock()

	result, err, _ := modelMonitorAvailabilityBuildGroup.Do("availability", func() (interface{}, error) {
		buildNow := common.GetTimestamp()
		modelMonitorAvailabilityCache.RLock()
		if modelMonitorAvailabilityCache.availability != nil && buildNow < modelMonitorAvailabilityCache.expiresAt {
			availability := cloneModelMonitorAvailability(modelMonitorAvailabilityCache.availability)
			modelMonitorAvailabilityCache.RUnlock()
			return availability, nil
		}
		modelMonitorAvailabilityCache.RUnlock()

		availability, err := buildModelMonitorAvailability(buildNow)
		if err != nil {
			return nil, err
		}

		modelMonitorAvailabilityCache.Lock()
		modelMonitorAvailabilityCache.availability = cloneModelMonitorAvailability(availability)
		modelMonitorAvailabilityCache.expiresAt = buildNow + modelMonitorCacheSeconds
		modelMonitorAvailabilityCache.Unlock()

		return cloneModelMonitorAvailability(availability), nil
	})
	if err != nil {
		if staleAvailability != nil {
			return staleAvailability, nil
		}
		return nil, err
	}
	availability, _ := result.(*ModelMonitorAvailability)
	if availability == nil {
		return staleAvailability, nil
	}
	return cloneModelMonitorAvailability(availability), nil
}
