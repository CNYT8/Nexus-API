package perfmetrics

import (
	"context"
	"fmt"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/perf_metrics_setting"
)

var hotBuckets sync.Map

// seriesSchema is a stable client cache/schema marker. Do not change it when
// hiding fields or making response-only privacy hardening changes.
const seriesSchema = "2bf39497c475bb46"

const (
	perfMetricPromptBaselineTokens = 512
	perfMetricPromptScaleTokens    = 2048
	perfMetricMaxPromptTtftFactor  = 1.85
)

func Init() {
	go flushLoop()
}

func RecordRelaySample(info *relaycommon.RelayInfo, success bool, promptTokens int64, outputTokens int64) {
	if info == nil {
		return
	}
	if promptTokens <= 0 {
		promptTokens = int64(info.GetEstimatePromptTokens())
	}
	now := time.Now()
	hasTtft := info.IsStream && info.HasSendResponse()
	ttftMs := int64(0)
	if hasTtft {
		ttftMs = info.FirstResponseTime.Sub(info.StartTime).Milliseconds()
	}
	latencyMs := now.Sub(info.StartTime).Milliseconds()
	generationMs := latencyMs
	if hasTtft {
		generationMs = now.Sub(info.FirstResponseTime).Milliseconds()
	}
	if generationMs <= 0 {
		generationMs = latencyMs
	}
	Record(Sample{
		Model:        info.OriginModelName,
		Group:        info.UsingGroup,
		LatencyMs:    latencyMs,
		TtftMs:       ttftMs,
		HasTtft:      hasTtft,
		PromptTokens: promptTokens,
		Success:      success,
		OutputTokens: outputTokens,
		GenerationMs: generationMs,
	})
}

func Record(sample Sample) {
	setting := perf_metrics_setting.GetSetting()
	if !setting.Enabled || sample.Model == "" {
		return
	}
	if sample.Group == "" {
		sample.Group = "default"
	}
	if sample.LatencyMs < 0 {
		sample.LatencyMs = 0
	}

	key := bucketKey{
		model:    sample.Model,
		group:    sample.Group,
		bucketTs: bucketStart(time.Now().Unix()),
	}
	actual, _ := hotBuckets.LoadOrStore(key, &atomicBucket{})
	actual.(*atomicBucket).add(sample)
	recordRedis(key, sample)
}

func Query(params QueryParams) (QueryResult, error) {
	if params.Hours <= 0 {
		params.Hours = 24
	}
	if params.Hours > 24*30 {
		params.Hours = 24 * 30
	}
	endTs := time.Now().Unix()
	startTs := endTs - int64(params.Hours)*3600

	merged := map[bucketKey]counters{}
	rows, err := model.GetPerfMetrics(params.Model, params.Group, startTs, endTs)
	if err != nil {
		return QueryResult{}, err
	}
	for _, row := range rows {
		mergeCounters(merged, bucketKey{
			model:    row.ModelName,
			group:    row.Group,
			bucketTs: row.BucketTs,
		}, counters{
			requestCount:   row.RequestCount,
			successCount:   row.SuccessCount,
			totalLatencyMs: row.TotalLatencyMs,
			ttftSumMs:      row.TtftSumMs,
			ttftCount:      row.TtftCount,
			promptTokens:   row.PromptTokens,
			outputTokens:   row.OutputTokens,
			generationMs:   row.GenerationMs,
		})
	}

	hotBuckets.Range(func(key, value any) bool {
		k := key.(bucketKey)
		if k.model != params.Model || k.bucketTs < startTs || k.bucketTs > endTs {
			return true
		}
		if params.Group != "" && k.group != params.Group {
			return true
		}
		mergeCounters(merged, k, value.(*atomicBucket).snapshot())
		return true
	})

	return buildQueryResult(params.Model, merged, endTs, params.Hours), nil
}

func QuerySummaryAll(hours int, groups []string) (SummaryAllResult, error) {
	if hours <= 0 {
		hours = 24
	}
	if hours > 24*30 {
		hours = 24 * 30
	}
	endTs := time.Now().Unix()
	startTs := endTs - int64(hours)*3600
	allowedGroups := allowedGroupSet(groups)

	rows, err := model.GetPerfMetricsSummaryAll(startTs, endTs, groups)
	if err != nil {
		return SummaryAllResult{}, err
	}

	totals := map[string]counters{}
	weightedTotals := map[string]weightedCounters{}
	windowSeconds := int64(hours) * 3600
	for _, row := range rows {
		current := totals[row.ModelName]
		value := counters{
			requestCount:   row.RequestCount,
			successCount:   row.SuccessCount,
			totalLatencyMs: row.TotalLatencyMs,
			ttftSumMs:      row.TtftSumMs,
			ttftCount:      row.TtftCount,
			promptTokens:   row.PromptTokens,
			outputTokens:   row.OutputTokens,
			generationMs:   row.GenerationMs,
		}
		current.requestCount += value.requestCount
		current.successCount += value.successCount
		current.totalLatencyMs += value.totalLatencyMs
		current.ttftSumMs += value.ttftSumMs
		current.ttftCount += value.ttftCount
		current.promptTokens += value.promptTokens
		current.outputTokens += value.outputTokens
		current.generationMs += value.generationMs
		totals[row.ModelName] = current

		weighted := weightedTotals[row.ModelName]
		weighted.add(value, perfMetricTimeWeight(row.BucketTs, endTs, windowSeconds))
		weightedTotals[row.ModelName] = weighted
	}

	hotBuckets.Range(func(key, value any) bool {
		k := key.(bucketKey)
		if k.bucketTs < startTs || k.bucketTs > endTs {
			return true
		}
		if allowedGroups != nil {
			if _, ok := allowedGroups[k.group]; !ok {
				return true
			}
		}
		snap := value.(*atomicBucket).snapshot()
		if snap.requestCount == 0 {
			return true
		}
		cur := totals[k.model]
		cur.requestCount += snap.requestCount
		cur.successCount += snap.successCount
		cur.totalLatencyMs += snap.totalLatencyMs
		cur.ttftSumMs += snap.ttftSumMs
		cur.ttftCount += snap.ttftCount
		cur.promptTokens += snap.promptTokens
		cur.outputTokens += snap.outputTokens
		cur.generationMs += snap.generationMs
		totals[k.model] = cur

		weighted := weightedTotals[k.model]
		weighted.add(snap, perfMetricTimeWeight(k.bucketTs, endTs, windowSeconds))
		weightedTotals[k.model] = weighted
		return true
	})

	models := make([]ModelSummary, 0, len(totals))
	for name, total := range totals {
		if total.requestCount == 0 {
			continue
		}
		avgLatency := total.totalLatencyMs / total.requestCount
		successRate := float64(total.successCount) / float64(total.requestCount) * 100
		avgTps := 0.0
		if total.generationMs > 0 {
			avgTps = float64(total.outputTokens) / (float64(total.generationMs) / 1000.0)
		}
		weighted := weightedTotals[name]
		models = append(models, ModelSummary{
			ModelName:            name,
			AvgLatencyMs:         avgLatency,
			SuccessRate:          math.Round(successRate*100) / 100,
			AvgTps:               math.Round(avgTps*100) / 100,
			WeightedRequestCount: roundFloat(weighted.requestCount, 2),
			WeightedAvgLatencyMs: weightedAvg(weighted.totalLatencyMs, weighted.requestCount),
			WeightedSuccessRate:  weightedSuccessRate(weighted),
			WeightedAvgTps:       weightedAvgTps(weighted),
			RequestCount:         total.requestCount,
		})
	}
	sort.Slice(models, func(i, j int) bool {
		return models[i].RequestCount > models[j].RequestCount
	})

	return SummaryAllResult{Models: models}, nil
}

func allowedGroupSet(groups []string) map[string]struct{} {
	if groups == nil {
		return nil
	}
	allowed := make(map[string]struct{}, len(groups))
	for _, group := range groups {
		allowed[group] = struct{}{}
	}
	return allowed
}

func bucketStart(ts int64) int64 {
	bucketSeconds := perf_metrics_setting.GetBucketSeconds()
	if bucketSeconds <= 0 {
		bucketSeconds = 3600
	}
	return ts - (ts % bucketSeconds)
}

func mergeCounters(merged map[bucketKey]counters, key bucketKey, value counters) {
	if value.requestCount == 0 {
		return
	}
	current := merged[key]
	current.requestCount += value.requestCount
	current.successCount += value.successCount
	current.totalLatencyMs += value.totalLatencyMs
	current.ttftSumMs += value.ttftSumMs
	current.ttftCount += value.ttftCount
	current.promptTokens += value.promptTokens
	current.outputTokens += value.outputTokens
	current.generationMs += value.generationMs
	merged[key] = current
}

func buildQueryResult(modelName string, merged map[bucketKey]counters, endTs int64, hours int) QueryResult {
	groupBuckets := map[string]map[int64]counters{}
	for key, value := range merged {
		if value.requestCount == 0 {
			continue
		}
		if _, ok := groupBuckets[key.group]; !ok {
			groupBuckets[key.group] = map[int64]counters{}
		}
		groupBuckets[key.group][key.bucketTs] = value
	}

	groups := make([]string, 0, len(groupBuckets))
	for group := range groupBuckets {
		groups = append(groups, group)
	}
	sort.Strings(groups)

	results := make([]GroupResult, 0, len(groups))
	windowSeconds := int64(hours) * 3600
	for _, group := range groups {
		buckets := groupBuckets[group]
		timestamps := make([]int64, 0, len(buckets))
		for ts := range buckets {
			timestamps = append(timestamps, ts)
		}
		sort.Slice(timestamps, func(i, j int) bool {
			return timestamps[i] < timestamps[j]
		})

		total := counters{}
		weighted := weightedCounters{}
		series := make([]BucketPoint, 0, len(timestamps))
		for _, ts := range timestamps {
			value := buckets[ts]
			total.requestCount += value.requestCount
			total.successCount += value.successCount
			total.totalLatencyMs += value.totalLatencyMs
			total.ttftSumMs += value.ttftSumMs
			total.ttftCount += value.ttftCount
			total.promptTokens += value.promptTokens
			total.outputTokens += value.outputTokens
			total.generationMs += value.generationMs
			weighted.add(value, perfMetricTimeWeight(ts, endTs, windowSeconds))
			series = append(series, bucketPoint(ts, value))
		}

		results = append(results, GroupResult{
			Group:                group,
			AvgTtftMs:            avg(total.ttftSumMs, total.ttftCount),
			AdjustedTtftMs:       adjustedTtft(total),
			AvgLatencyMs:         avg(total.totalLatencyMs, total.requestCount),
			SuccessRate:          successRate(total),
			AvgTps:               avgTps(total),
			RequestCount:         total.requestCount,
			SuccessCount:         total.successCount,
			PromptTokens:         total.promptTokens,
			WeightedRequestCount: roundFloat(weighted.requestCount, 2),
			WeightedAvgTtftMs:    weightedAdjustedTtft(weighted),
			WeightedAvgLatencyMs: weightedAvg(weighted.totalLatencyMs, weighted.requestCount),
			WeightedSuccessRate:  weightedSuccessRate(weighted),
			WeightedAvgTps:       weightedAvgTps(weighted),
			Series:               series,
		})
	}

	return QueryResult{
		ModelName:    modelName,
		SeriesSchema: seriesSchema,
		Groups:       results,
	}
}

func bucketPoint(ts int64, value counters) BucketPoint {
	return BucketPoint{
		Ts:             ts,
		AvgTtftMs:      avg(value.ttftSumMs, value.ttftCount),
		AdjustedTtftMs: adjustedTtft(value),
		AvgLatencyMs:   avg(value.totalLatencyMs, value.requestCount),
		SuccessRate:    successRate(value),
		AvgTps:         avgTps(value),
		RequestCount:   value.requestCount,
		SuccessCount:   value.successCount,
		PromptTokens:   value.promptTokens,
	}
}

type weightedCounters struct {
	requestCount   float64
	successCount   float64
	totalLatencyMs float64
	ttftSumMs      float64
	ttftCount      float64
	promptTokens   float64
	outputTokens   float64
	generationMs   float64
}

func (c *weightedCounters) add(value counters, weight float64) {
	if value.requestCount == 0 || weight <= 0 {
		return
	}
	c.requestCount += float64(value.requestCount) * weight
	c.successCount += float64(value.successCount) * weight
	c.totalLatencyMs += float64(value.totalLatencyMs) * weight
	c.ttftSumMs += float64(value.ttftSumMs) * weight
	c.ttftCount += float64(value.ttftCount) * weight
	c.promptTokens += float64(value.promptTokens) * weight
	c.outputTokens += float64(value.outputTokens) * weight
	c.generationMs += float64(value.generationMs) * weight
}

func perfMetricTimeWeight(bucketTs int64, endTs int64, windowSeconds int64) float64 {
	if windowSeconds <= 0 {
		return 1
	}
	age := endTs - bucketTs
	if age < 0 {
		age = 0
	}
	hotSeconds := windowSeconds * 3 / 7
	if hotSeconds <= 0 {
		hotSeconds = windowSeconds
	}
	if age <= hotSeconds {
		return 2.0 - float64(age)/float64(hotSeconds)*0.5
	}
	if age >= windowSeconds {
		return 0.2
	}
	restAge := age - hotSeconds
	restWindow := windowSeconds - hotSeconds
	if restWindow <= 0 {
		return 1
	}
	return 1.0 - float64(restAge)/float64(restWindow)*0.65
}

func avg(sum int64, count int64) int64 {
	if count <= 0 {
		return 0
	}
	return sum / count
}

func weightedAvg(sum float64, count float64) int64 {
	if count <= 0 {
		return 0
	}
	return int64(math.Round(sum / count))
}

func adjustedTtft(value counters) int64 {
	rawTtft := avg(value.ttftSumMs, value.ttftCount)
	if rawTtft <= 0 || value.promptTokens <= 0 || value.requestCount <= 0 {
		return rawTtft
	}
	return promptAdjustedTtft(float64(rawTtft), float64(value.promptTokens)/float64(value.requestCount))
}

func weightedAdjustedTtft(value weightedCounters) int64 {
	rawTtft := weightedAvg(value.ttftSumMs, value.ttftCount)
	if rawTtft <= 0 || value.promptTokens <= 0 || value.requestCount <= 0 {
		return rawTtft
	}
	return promptAdjustedTtft(float64(rawTtft), value.promptTokens/value.requestCount)
}

func promptAdjustedTtft(rawTtftMs float64, avgPromptTokens float64) int64 {
	if rawTtftMs <= 0 || avgPromptTokens <= perfMetricPromptBaselineTokens {
		return int64(math.Round(rawTtftMs))
	}
	excessTokens := avgPromptTokens - perfMetricPromptBaselineTokens
	factor := 1 + math.Log1p(excessTokens/perfMetricPromptScaleTokens)*0.28
	if factor > perfMetricMaxPromptTtftFactor {
		factor = perfMetricMaxPromptTtftFactor
	}
	return int64(math.Round(rawTtftMs / factor))
}

func successRate(value counters) float64 {
	if value.requestCount <= 0 {
		return 0
	}
	return float64(value.successCount) / float64(value.requestCount) * 100
}

func avgTps(value counters) float64 {
	if value.outputTokens <= 0 || value.generationMs <= 0 {
		return 0
	}
	return float64(value.outputTokens) / (float64(value.generationMs) / 1000)
}

func weightedSuccessRate(value weightedCounters) float64 {
	if value.requestCount <= 0 {
		return 0
	}
	return roundFloat(value.successCount/value.requestCount*100, 2)
}

func weightedAvgTps(value weightedCounters) float64 {
	if value.outputTokens <= 0 || value.generationMs <= 0 {
		return 0
	}
	return roundFloat(value.outputTokens/(value.generationMs/1000), 2)
}

func roundFloat(value float64, precision int) float64 {
	if precision < 0 {
		return value
	}
	factor := math.Pow10(precision)
	return math.Round(value*factor) / factor
}

func recordRedis(key bucketKey, sample Sample) {
	if !common.RedisEnabled || common.RDB == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	redisKey := redisBucketKey(key)
	pipe := common.RDB.TxPipeline()
	pipe.HIncrBy(ctx, redisKey, "req", 1)
	if sample.Success {
		pipe.HIncrBy(ctx, redisKey, "ok", 1)
	}
	if sample.LatencyMs > 0 {
		pipe.HIncrBy(ctx, redisKey, "lat", sample.LatencyMs)
	}
	if sample.HasTtft && sample.TtftMs >= 0 {
		pipe.HIncrBy(ctx, redisKey, "ttft", sample.TtftMs)
		pipe.HIncrBy(ctx, redisKey, "ttft_n", 1)
	}
	if sample.PromptTokens > 0 {
		pipe.HIncrBy(ctx, redisKey, "prompt", sample.PromptTokens)
	}
	if sample.OutputTokens > 0 && sample.GenerationMs > 0 {
		pipe.HIncrBy(ctx, redisKey, "out", sample.OutputTokens)
		pipe.HIncrBy(ctx, redisKey, "gen_ms", sample.GenerationMs)
	}
	pipe.Expire(ctx, redisKey, time.Hour)
	_, _ = pipe.Exec(ctx)
}

func mergeRedisActiveBuckets(merged map[bucketKey]counters, params QueryParams, startTs int64, endTs int64) {
	if !common.RedisEnabled || common.RDB == nil || params.Model == "" || params.Group == "" {
		return
	}
	active := bucketStart(time.Now().Unix())
	if active < startTs || active > endTs {
		return
	}
	key := bucketKey{model: params.Model, group: params.Group, bucketTs: active}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	values, err := common.RDB.HGetAll(ctx, redisBucketKey(key)).Result()
	if err != nil || len(values) == 0 {
		return
	}
	mergeCounters(merged, key, redisCounters(values))
}

func redisBucketKey(key bucketKey) string {
	return fmt.Sprintf("perf:%s:%s:%d", key.model, key.group, key.bucketTs)
}
