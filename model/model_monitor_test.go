package model

import (
	"math"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

func TestModelMonitorWeight(t *testing.T) {
	now := int64(1000000)

	if got := modelMonitorWeight(now, now); got != 2 {
		t.Fatalf("recent weight = %v, want 2", got)
	}

	hotEdge := modelMonitorWeight(now-modelMonitorHotSeconds, now)
	if hotEdge < 1.49 || hotEdge > 1.51 {
		t.Fatalf("hot edge weight = %v, want about 1.5", hotEdge)
	}
	afterHotEdge := modelMonitorWeight(now-modelMonitorHotSeconds-1, now)
	if math.Abs(hotEdge-afterHotEdge) > 0.001 {
		t.Fatalf("hot edge is discontinuous: at=%v after=%v", hotEdge, afterHotEdge)
	}

	if got := modelMonitorWeight(now-modelMonitorWindowSeconds, now); got != 0.2 {
		t.Fatalf("window edge weight = %v, want 0.2", got)
	}
	beforeWindowEdge := modelMonitorWeight(now-modelMonitorWindowSeconds+1, now)
	if math.Abs(beforeWindowEdge-modelMonitorWindowEdgeWeight) > 0.001 {
		t.Fatalf("window edge is discontinuous: before=%v at=%v", beforeWindowEdge, modelMonitorWindowEdgeWeight)
	}

	for _, age := range []int64{0, 1, modelMonitorHotSeconds, modelMonitorHotSeconds + 1, modelMonitorWindowSeconds - 1, modelMonitorWindowSeconds} {
		args := appendModelMonitorWeightSQLArgs(nil, now)
		args = append(args, now-age)
		var row struct {
			Weight float64
		}
		err := DB.Raw(
			"SELECT "+modelMonitorWeightSQL()+" AS weight FROM (SELECT ? AS created_at) AS monitor_sample",
			args...,
		).Scan(&row).Error
		require.NoError(t, err)
		require.InDelta(t, modelMonitorWeight(now-age, now), row.Weight, 0.000001)
	}
}

func TestScoreModelMonitorBucket(t *testing.T) {
	healthy := modelMonitorBucket{
		sampleCount:              10,
		weightedSuccess:          10,
		weightedPromptTokens:     6000,
		weightedCompletionTokens: 4000,
		weightedUseTime:          20,
	}
	if score := scoreModelMonitorBucket(healthy); score < 85 {
		t.Fatalf("healthy score = %d, want >= 85", score)
	}

	longInputHealthy := modelMonitorBucket{
		sampleCount:              10,
		weightedSuccess:          10,
		weightedPromptTokens:     240000,
		weightedCompletionTokens: 4000,
		weightedUseTime:          120,
		weightedSlowRequests:     1,
	}
	if score := scoreModelMonitorBucket(longInputHealthy); score < 80 {
		t.Fatalf("long input healthy score = %d, want >= 80", score)
	}

	poor := modelMonitorBucket{
		rawErrorLogCount: 10,
		sampleCount:      10,
		errorSampleCount: 10,
		weightedErrors:   10,
	}
	if score := scoreModelMonitorBucket(poor); score >= 45 {
		t.Fatalf("poor score = %d, want < 45", score)
	}

	lowSample := modelMonitorBucket{
		sampleCount:              1,
		weightedSuccess:          1,
		weightedPromptTokens:     600,
		weightedCompletionTokens: 400,
		weightedUseTime:          1,
	}
	if score := scoreModelMonitorBucket(lowSample); score > 68 {
		t.Fatalf("low sample score = %d, want <= 68", score)
	}

	emptyOutput := modelMonitorBucket{
		sampleCount:          8,
		weightedSuccess:      8,
		weightedPromptTokens: 8000,
		weightedUseTime:      16,
		weightedEmptyOutputs: 8,
	}
	if score := scoreModelMonitorBucket(emptyOutput); score >= 55 {
		t.Fatalf("empty output score = %d, want < 55", score)
	}

	thinOutput := modelMonitorBucket{
		sampleCount:              10,
		weightedSuccess:          10,
		weightedPromptTokens:     10000,
		weightedCompletionTokens: 80,
		weightedUseTime:          60,
		weightedSlowRequests:     3,
	}
	if score := scoreModelMonitorBucket(thinOutput); score >= 75 {
		t.Fatalf("thin output score = %d, want < 75", score)
	}

	longOutputHealthy := modelMonitorBucket{
		sampleCount:              10,
		weightedSuccess:          10,
		weightedPromptTokens:     5000,
		weightedCompletionTokens: 10000,
		weightedUseTime:          500,
	}
	if score := scoreModelMonitorBucket(longOutputHealthy); score < 85 {
		t.Fatalf("long output healthy score = %d, want >= 85", score)
	}

	partialFailure := modelMonitorBucket{
		rawErrorLogCount:         1,
		sampleCount:              2,
		errorSampleCount:         1,
		weightedSuccess:          1,
		weightedErrors:           1,
		weightedPromptTokens:     600,
		weightedCompletionTokens: 400,
		weightedUseTime:          1,
	}
	if score := scoreModelMonitorBucket(partialFailure); score > 58 {
		t.Fatalf("half-failing score = %d, want <= 58", score)
	}
}

func TestModelMonitorSubsecondAndOutputLatencyAdjustments(t *testing.T) {
	if score := modelMonitorLatencyScore(0, 0); score != 100 {
		t.Fatalf("sub-second latency score = %v, want 100", score)
	}
	if score := modelMonitorThroughputScore(100, 0, true); score != 100 {
		t.Fatalf("sub-second throughput score = %v, want 100", score)
	}

	adjusted := modelMonitorOutputAdjustedUseTime(50, 1000)
	if adjusted >= 50 || adjusted < 7.5 {
		t.Fatalf("long-output adjusted use time = %v, want in [7.5, 50)", adjusted)
	}
	if got := modelMonitorOutputAdjustedUseTime(12, 0); got != 12 {
		t.Fatalf("zero-output adjusted use time = %v, want 12", got)
	}
}

func TestScoreModelMonitorBucketDoesNotTreatWeightAsSampleCount(t *testing.T) {
	bucket := modelMonitorBucket{
		sampleCount:              1,
		weightedSuccess:          modelMonitorFreshWeight,
		weightedPromptTokens:     600 * modelMonitorFreshWeight,
		weightedCompletionTokens: 400 * modelMonitorFreshWeight,
		weightedUseTime:          modelMonitorFreshWeight,
	}
	if score := scoreModelMonitorBucket(bucket); score > 68 {
		t.Fatalf("one fresh sample score = %d, want <= 68", score)
	}
}

func TestScoreModelMonitorBucketIgnoresUniformWeightDecay(t *testing.T) {
	recent := modelMonitorBucket{
		sampleCount:              10,
		weightedSuccess:          10,
		weightedPromptTokens:     6000,
		weightedCompletionTokens: 4000,
		weightedUseTime:          20,
	}
	older := recent
	older.weightedSuccess *= 0.35
	older.weightedPromptTokens *= 0.35
	older.weightedCompletionTokens *= 0.35
	older.weightedUseTime *= 0.35

	recentScore := scoreModelMonitorBucket(recent)
	olderScore := scoreModelMonitorBucket(older)
	if olderScore != recentScore {
		t.Fatalf("uniform time decay changed score: recent=%d older=%d", recentScore, olderScore)
	}
}

func TestModelMonitorEffectiveErrorWeightDiscountsRetryFanOut(t *testing.T) {
	bucket := modelMonitorBucket{
		rawErrorLogCount: 3,
		sampleCount:      1,
		errorSampleCount: 1,
		weightedErrors:   6,
	}
	if got := modelMonitorEffectiveErrorWeight(bucket); math.Abs(got-2) > 0.001 {
		t.Fatalf("effective retry error weight = %v, want 2", got)
	}
}

func resetModelMonitorTables(t *testing.T) {
	t.Helper()
	require.NoError(t, LOG_DB.Exec("DELETE FROM logs").Error)
	require.NoError(t, LOG_DB.Exec("DELETE FROM model_monitor_samples").Error)
	require.NoError(t, DB.Exec("DELETE FROM channels").Error)
	require.NoError(t, DB.Exec("DELETE FROM abilities").Error)
	require.NoError(t, DB.Exec("DELETE FROM models").Error)
	require.NoError(t, DB.Exec("DELETE FROM vendors").Error)
	t.Cleanup(func() {
		_ = LOG_DB.Exec("DELETE FROM logs").Error
		_ = LOG_DB.Exec("DELETE FROM model_monitor_samples").Error
		_ = DB.Exec("DELETE FROM channels").Error
		_ = DB.Exec("DELETE FROM abilities").Error
		_ = DB.Exec("DELETE FROM models").Error
		_ = DB.Exec("DELETE FROM vendors").Error
		InvalidateModelMonitorCache()
		InvalidatePricingCache()
	})
}

func TestGetModelMonitorSummaryAggregatesRecentLogs(t *testing.T) {
	InvalidateModelMonitorCache()
	t.Cleanup(InvalidateModelMonitorCache)
	InvalidatePricingCache()
	t.Cleanup(InvalidatePricingCache)
	resetModelMonitorTables(t)

	now := common.GetTimestamp()
	require.NoError(t, DB.Create(&Channel{
		Id:     990,
		Type:   1,
		Status: common.ChannelStatusEnabled,
	}).Error)
	for _, modelName := range []string{"gpt-monitor-test", "gpt-empty-monitor-test"} {
		require.NoError(t, DB.Create(&Ability{
			Group:     "default",
			Model:     modelName,
			ChannelId: 990,
			Enabled:   true,
		}).Error)
	}
	require.NoError(t, LOG_DB.Create(&Log{
		CreatedAt:        now - 60,
		Type:             LogTypeConsume,
		ModelName:        "gpt-monitor-test",
		PromptTokens:     600,
		CompletionTokens: 400,
		UseTime:          2,
	}).Error)
	require.NoError(t, LOG_DB.Create(&Log{
		CreatedAt:        now - 90,
		Type:             LogTypeConsume,
		ModelName:        "gpt-empty-monitor-test",
		PromptTokens:     800,
		CompletionTokens: 0,
		UseTime:          3,
	}).Error)
	require.NoError(t, LOG_DB.Create(&Log{
		CreatedAt: now - 120,
		Type:      LogTypeError,
		ModelName: "gpt-monitor-test",
		UseTime:   12,
	}).Error)
	require.NoError(t, LOG_DB.Create(&Log{
		CreatedAt:        now - modelMonitorWindowSeconds - 60,
		Type:             LogTypeConsume,
		ModelName:        "old-monitor-test",
		PromptTokens:     100,
		CompletionTokens: 100,
		UseTime:          1,
	}).Error)

	summary, err := GetModelMonitorSummary()
	require.NoError(t, err)
	require.Equal(t, 2, summary.ModelCount)
	require.Equal(t, 1, summary.VendorCount)
	require.Len(t, summary.Vendors, 1)
	require.Equal(t, "OpenAI", summary.Vendors[0].Name)
	require.Len(t, summary.Vendors[0].Models, 2)

	models := make(map[string]ModelMonitorModel)
	for _, item := range summary.Vendors[0].Models {
		models[item.ModelName] = item
	}
	normalModel := models["gpt-monitor-test"]
	emptyModel := models["gpt-empty-monitor-test"]
	require.True(t, normalModel.HasData)
	require.True(t, emptyModel.HasData)
	require.NotEqual(t, "unknown", normalModel.Status)
	require.NotEqual(t, "unknown", emptyModel.Status)
	require.GreaterOrEqual(t, normalModel.Score, 1)
	require.LessOrEqual(t, normalModel.Score, 100)
	require.Less(t, emptyModel.Score, normalModel.Score)
}

func TestGetModelMonitorSummaryAggregatesChannelTestSamples(t *testing.T) {
	InvalidateModelMonitorCache()
	t.Cleanup(InvalidateModelMonitorCache)
	InvalidatePricingCache()
	t.Cleanup(InvalidatePricingCache)
	resetModelMonitorTables(t)

	now := common.GetTimestamp()
	require.NoError(t, DB.Create(&Channel{
		Id:     991,
		Type:   1,
		Status: common.ChannelStatusEnabled,
	}).Error)
	for _, modelName := range []string{"gpt-channel-test-sample", "gpt-channel-test-error", "gpt-legacy-test-log"} {
		require.NoError(t, DB.Create(&Ability{
			Group:     "vip",
			Model:     modelName,
			ChannelId: 991,
			Enabled:   true,
		}).Error)
	}
	require.NoError(t, LOG_DB.Create(&Log{
		CreatedAt:        now - 60,
		Type:             LogTypeConsume,
		Content:          "模型测试",
		TokenName:        "模型测试",
		TokenId:          0,
		ModelName:        "gpt-legacy-test-log",
		Group:            "vip",
		PromptTokens:     800,
		CompletionTokens: 800,
		UseTime:          1,
	}).Error)
	require.NoError(t, LOG_DB.Create(&ModelMonitorSample{
		CreatedAt:        now - 30,
		Source:           ModelMonitorSampleSourceChannelTest,
		ChannelId:        991,
		ModelName:        "gpt-channel-test-sample",
		Group:            "vip",
		Status:           ModelMonitorSampleStatusSuccess,
		PromptTokens:     12,
		CompletionTokens: 8,
		UseTime:          0.25,
	}).Error)
	require.NoError(t, LOG_DB.Create(&ModelMonitorSample{
		CreatedAt: now - 30,
		Source:    ModelMonitorSampleSourceChannelTest,
		ChannelId: 991,
		ModelName: "gpt-channel-test-error",
		Group:     "vip",
		Status:    ModelMonitorSampleStatusError,
		UseTime:   2,
	}).Error)

	summary, err := GetModelMonitorSummary()
	require.NoError(t, err)
	require.Equal(t, 3, summary.ModelCount)
	require.Len(t, summary.Vendors, 1)

	models := make(map[string]ModelMonitorModel)
	for _, item := range summary.Vendors[0].Models {
		models[item.ModelName+"/"+item.Group] = item
	}
	successModel := models["gpt-channel-test-sample/vip"]
	errorModel := models["gpt-channel-test-error/vip"]
	legacyModel := models["gpt-legacy-test-log/vip"]
	require.True(t, successModel.HasData)
	require.True(t, errorModel.HasData)
	require.False(t, legacyModel.HasData)
	require.Greater(t, successModel.Score, errorModel.Score)
}

func TestRecordModelMonitorSamplesUsesChannelGroups(t *testing.T) {
	resetModelMonitorTables(t)

	require.NoError(t, RecordModelMonitorSamples(RecordModelMonitorSampleParams{
		Source:           ModelMonitorSampleSourceChannelTest,
		ChannelId:        992,
		ModelName:        "gpt-channel-groups-sample",
		Group:            "default",
		Success:          true,
		PromptTokens:     12,
		CompletionTokens: 8,
		UseTimeSeconds:   0.2,
	}, []string{"vip", "svip", "vip"}))

	var samples []ModelMonitorSample
	require.NoError(t, LOG_DB.Where("model_name = ?", "gpt-channel-groups-sample").Order("id asc").Find(&samples).Error)
	require.Len(t, samples, 2)
	require.Equal(t, "vip", samples[0].Group)
	require.Equal(t, "svip", samples[1].Group)
}

func TestGetModelMonitorSummarySeparatesSameModelByGroup(t *testing.T) {
	InvalidateModelMonitorCache()
	t.Cleanup(InvalidateModelMonitorCache)
	InvalidatePricingCache()
	t.Cleanup(InvalidatePricingCache)
	resetModelMonitorTables(t)

	now := common.GetTimestamp()
	require.NoError(t, DB.Create(&Channel{
		Id:     996,
		Type:   1,
		Status: common.ChannelStatusEnabled,
	}).Error)
	for _, group := range []string{"default", "vip", "svip"} {
		require.NoError(t, DB.Create(&Ability{
			Group:     group,
			Model:     "gpt-group-monitor-test",
			ChannelId: 996,
			Enabled:   true,
		}).Error)
	}
	require.NoError(t, LOG_DB.Create(&Log{
		CreatedAt:        now - 60,
		Type:             LogTypeConsume,
		ModelName:        "gpt-group-monitor-test",
		Group:            "default",
		PromptTokens:     600,
		CompletionTokens: 400,
		UseTime:          2,
	}).Error)
	require.NoError(t, LOG_DB.Create(&Log{
		CreatedAt: now - 60,
		Type:      LogTypeError,
		ModelName: "gpt-group-monitor-test",
		Group:     "vip",
		UseTime:   20,
	}).Error)

	summary, err := GetModelMonitorSummary()
	require.NoError(t, err)
	require.Equal(t, 3, summary.ModelCount)
	require.Equal(t, 2, summary.KnownCount)
	require.Equal(t, 1, summary.UnknownCount)
	require.Len(t, summary.Vendors, 1)
	require.Len(t, summary.Vendors[0].Models, 3)

	models := make(map[string]ModelMonitorModel)
	for _, item := range summary.Vendors[0].Models {
		models[item.ModelName+"/"+item.Group] = item
	}
	defaultModel := models["gpt-group-monitor-test/default"]
	vipModel := models["gpt-group-monitor-test/vip"]
	svipModel := models["gpt-group-monitor-test/svip"]
	require.Equal(t, "default", defaultModel.Group)
	require.True(t, defaultModel.HasData)
	require.Equal(t, "vip", vipModel.Group)
	require.True(t, vipModel.HasData)
	require.Equal(t, "svip", svipModel.Group)
	require.False(t, svipModel.HasData)
	require.Greater(t, defaultModel.Score, vipModel.Score)
}

func TestGetModelMonitorSummaryDeduplicatesRetryErrorSamples(t *testing.T) {
	InvalidateModelMonitorCache()
	t.Cleanup(InvalidateModelMonitorCache)
	InvalidatePricingCache()
	t.Cleanup(InvalidatePricingCache)
	resetModelMonitorTables(t)

	now := common.GetTimestamp()
	require.NoError(t, DB.Create(&Channel{
		Id:     997,
		Type:   1,
		Status: common.ChannelStatusEnabled,
	}).Error)
	modelNames := []string{"gpt-retry-error-sample-test", "gpt-independent-error-sample-test"}
	for _, modelName := range modelNames {
		require.NoError(t, DB.Create(&Ability{
			Group:     "default",
			Model:     modelName,
			ChannelId: 997,
			Enabled:   true,
		}).Error)
	}

	for _, modelName := range modelNames {
		for i := 0; i < 10; i++ {
			require.NoError(t, LOG_DB.Create(&Log{
				CreatedAt:        now - int64(i+1),
				Type:             LogTypeConsume,
				ModelName:        modelName,
				Group:            "default",
				PromptTokens:     600,
				CompletionTokens: 400,
				UseTime:          2,
			}).Error)
		}
	}
	for i, requestID := range []string{"same-retry-request", "same-retry-request", "same-retry-request"} {
		require.NoError(t, LOG_DB.Create(&Log{
			CreatedAt: now - int64(i+1),
			Type:      LogTypeError,
			ModelName: "gpt-retry-error-sample-test",
			Group:     "default",
			UseTime:   8,
			RequestId: requestID,
		}).Error)
	}
	for i, requestID := range []string{"independent-error-1", "independent-error-2", "independent-error-3"} {
		require.NoError(t, LOG_DB.Create(&Log{
			CreatedAt: now - int64(i+1),
			Type:      LogTypeError,
			ModelName: "gpt-independent-error-sample-test",
			Group:     "default",
			UseTime:   8,
			RequestId: requestID,
		}).Error)
	}

	summary, err := GetModelMonitorSummary()
	require.NoError(t, err)
	require.Len(t, summary.Vendors, 1)
	models := make(map[string]ModelMonitorModel)
	for _, item := range summary.Vendors[0].Models {
		models[item.ModelName] = item
	}
	retryErrors := models["gpt-retry-error-sample-test"]
	independentErrors := models["gpt-independent-error-sample-test"]
	require.Greater(t, retryErrors.Score, independentErrors.Score)
}

func TestGetModelMonitorSummaryIncludesUnknownEnabledModels(t *testing.T) {
	InvalidateModelMonitorCache()
	t.Cleanup(InvalidateModelMonitorCache)
	InvalidatePricingCache()
	t.Cleanup(InvalidatePricingCache)
	resetModelMonitorTables(t)

	require.NoError(t, DB.Create(&Channel{
		Id:     991,
		Type:   1,
		Status: common.ChannelStatusEnabled,
	}).Error)
	require.NoError(t, DB.Create(&Ability{
		Group:     "default",
		Model:     "unused-monitor-test",
		ChannelId: 991,
		Enabled:   true,
	}).Error)

	summary, err := GetModelMonitorSummary()
	require.NoError(t, err)
	require.Equal(t, 1, summary.ModelCount)
	require.Equal(t, 0, summary.KnownCount)
	require.Equal(t, 1, summary.UnknownCount)
	require.Len(t, summary.Vendors, 1)
	require.Equal(t, "未知状态", summary.Vendors[0].Models[0].StatusText)
	require.False(t, summary.Vendors[0].Models[0].HasData)
	require.Equal(t, 0, summary.Vendors[0].Models[0].Score)
}

func TestGetModelMonitorSummaryExcludesDisabledModelsWithLogs(t *testing.T) {
	InvalidateModelMonitorCache()
	t.Cleanup(InvalidateModelMonitorCache)
	InvalidatePricingCache()
	t.Cleanup(InvalidatePricingCache)
	resetModelMonitorTables(t)

	now := common.GetTimestamp()
	require.NoError(t, DB.Create(&Channel{
		Id:     992,
		Type:   1,
		Status: common.ChannelStatusEnabled,
	}).Error)
	require.NoError(t, DB.Create(&Ability{
		Group:     "default",
		Model:     "active-monitor-test",
		ChannelId: 992,
		Enabled:   true,
	}).Error)
	require.NoError(t, LOG_DB.Create(&Log{
		CreatedAt:        now - 60,
		Type:             LogTypeConsume,
		ModelName:        "active-monitor-test",
		PromptTokens:     600,
		CompletionTokens: 400,
		UseTime:          2,
	}).Error)
	require.NoError(t, LOG_DB.Create(&Log{
		CreatedAt: now - 60,
		Type:      LogTypeError,
		ModelName: "disabled-monitor-test",
		UseTime:   8,
	}).Error)

	summary, err := GetModelMonitorSummary()
	require.NoError(t, err)
	require.Equal(t, 1, summary.ModelCount)
	require.Len(t, summary.Vendors, 1)
	require.Len(t, summary.Vendors[0].Models, 1)
	require.Equal(t, "active-monitor-test", summary.Vendors[0].Models[0].ModelName)
	require.True(t, summary.Vendors[0].Models[0].HasData)
}

func TestGetModelMonitorSummaryExcludesModelsOnDisabledChannels(t *testing.T) {
	InvalidateModelMonitorCache()
	t.Cleanup(InvalidateModelMonitorCache)
	InvalidatePricingCache()
	t.Cleanup(InvalidatePricingCache)
	resetModelMonitorTables(t)

	now := common.GetTimestamp()
	require.NoError(t, DB.Create(&Channel{
		Id:     993,
		Type:   1,
		Status: common.ChannelStatusEnabled,
	}).Error)
	require.NoError(t, DB.Create(&Channel{
		Id:     994,
		Type:   1,
		Status: common.ChannelStatusManuallyDisabled,
	}).Error)
	require.NoError(t, DB.Create(&Ability{
		Group:     "default",
		Model:     "active-channel-monitor-test",
		ChannelId: 993,
		Enabled:   true,
	}).Error)
	require.NoError(t, DB.Create(&Ability{
		Group:     "default",
		Model:     "disabled-channel-monitor-test",
		ChannelId: 994,
		Enabled:   true,
	}).Error)
	for _, modelName := range []string{"active-channel-monitor-test", "disabled-channel-monitor-test"} {
		require.NoError(t, LOG_DB.Create(&Log{
			CreatedAt:        now - 60,
			Type:             LogTypeConsume,
			ModelName:        modelName,
			PromptTokens:     600,
			CompletionTokens: 400,
			UseTime:          2,
		}).Error)
	}

	summary, err := GetModelMonitorSummary()
	require.NoError(t, err)
	require.Equal(t, 1, summary.ModelCount)
	require.Len(t, summary.Vendors, 1)
	require.Len(t, summary.Vendors[0].Models, 1)
	require.Equal(t, "active-channel-monitor-test", summary.Vendors[0].Models[0].ModelName)
}

func TestGetModelMonitorSummaryKeepsMatchedModelsUnderOriginalVendor(t *testing.T) {
	InvalidateModelMonitorCache()
	t.Cleanup(InvalidateModelMonitorCache)
	InvalidatePricingCache()
	t.Cleanup(InvalidatePricingCache)
	resetModelMonitorTables(t)

	now := common.GetTimestamp()
	require.NoError(t, DB.Create(&Vendor{
		Id:     1201,
		Name:   "OpenAI",
		Status: 1,
		Icon:   "OpenAI",
	}).Error)
	require.NoError(t, DB.Create(&Model{
		Id:        1201,
		ModelName: "gpt-4o",
		VendorID:  1201,
		Status:    1,
		NameRule:  NameRuleExact,
		Tags:      "stable,flagship",
	}).Error)
	require.NoError(t, DB.Create(&Model{
		Id:        1202,
		ModelName: "gpt-4o",
		VendorID:  1201,
		Status:    1,
		NameRule:  NameRulePrefix,
		Tags:      "tagged,derived",
	}).Error)
	require.NoError(t, DB.Create(&Channel{
		Id:     995,
		Type:   1,
		Status: common.ChannelStatusEnabled,
	}).Error)
	require.NoError(t, DB.Create(&Ability{
		Group:     "default",
		Model:     "gpt-4o@tagged",
		ChannelId: 995,
		Enabled:   true,
	}).Error)
	require.NoError(t, LOG_DB.Create(&Log{
		CreatedAt:        now - 60,
		Type:             LogTypeConsume,
		ModelName:        "gpt-4o@tagged",
		PromptTokens:     600,
		CompletionTokens: 400,
		UseTime:          2,
	}).Error)

	summary, err := GetModelMonitorSummary()
	require.NoError(t, err)
	require.Len(t, summary.Vendors, 1)
	require.Equal(t, "OpenAI", summary.Vendors[0].Name)
	require.Len(t, summary.Vendors[0].Models, 1)
	require.Equal(t, "gpt-4o@tagged", summary.Vendors[0].Models[0].ModelName)
	pricingByModel := make(map[string]Pricing)
	for _, item := range GetPricing() {
		pricingByModel[item.ModelName] = item
	}
	require.Equal(t, 1201, pricingByModel["gpt-4o@tagged"].VendorID)
}
