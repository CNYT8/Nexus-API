package model

import (
	"fmt"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

func setupModelMonitorAvailabilityTest(t *testing.T) {
	t.Helper()
	InvalidateModelMonitorCache()
	t.Cleanup(InvalidateModelMonitorCache)
	InvalidatePricingCache()
	t.Cleanup(InvalidatePricingCache)
	resetModelMonitorTables(t)
}

func seedAvailabilityAbility(t *testing.T, group string, modelName string) {
	t.Helper()
	require.NoError(t, DB.Create(&Ability{
		Group:     group,
		Model:     modelName,
		ChannelId: 991,
		Enabled:   true,
	}).Error)
}

func TestBuildModelMonitorAvailabilityBuckets(t *testing.T) {
	setupModelMonitorAvailabilityTest(t)

	bucketSeconds := int64(modelMonitorAvailabilityBucketSeconds)
	// 1h40m past the bucket boundary: the latest slot is a partial bucket.
	now := common.GetTimestamp()
	now = now - now%bucketSeconds + 6000

	require.NoError(t, DB.Create(&Channel{
		Id:     991,
		Type:   1,
		Status: common.ChannelStatusEnabled,
	}).Error)
	seedAvailabilityAbility(t, "default", "gpt-avail-healthy")
	seedAvailabilityAbility(t, "default", "gpt-avail-broken")
	seedAvailabilityAbility(t, "default", "gpt-avail-silent")
	seedAvailabilityAbility(t, "vip", "gpt-avail-sample-only")

	// Healthy model: fast successes inside the partial latest bucket.
	for i := 0; i < 10; i++ {
		require.NoError(t, LOG_DB.Create(&Log{
			CreatedAt:        now - 600 - int64(i),
			Type:             LogTypeConsume,
			ModelName:        "gpt-avail-healthy",
			PromptTokens:     600,
			CompletionTokens: 400,
			UseTime:          2,
		}).Error)
	}
	// Broken model: distinct-request errors three hours ago land in the
	// previous full bucket.
	for i := 0; i < 10; i++ {
		require.NoError(t, LOG_DB.Create(&Log{
			CreatedAt: now - 3*3600 - int64(i),
			Type:      LogTypeError,
			ModelName: "gpt-avail-broken",
			RequestId: fmt.Sprintf("avail-err-%d", i),
			UseTime:   12,
		}).Error)
	}
	// Channel-test logs must stay excluded from availability.
	require.NoError(t, LOG_DB.Create(&Log{
		CreatedAt:        now - 300,
		Type:             LogTypeConsume,
		ModelName:        "gpt-avail-silent",
		TokenName:        "模型测试",
		Content:          "模型测试",
		PromptTokens:     100,
		CompletionTokens: 100,
		UseTime:          1,
	}).Error)
	// Sample-table (channel test source) contributes to the vip group.
	require.NoError(t, LOG_DB.Create(&ModelMonitorSample{
		CreatedAt:        now - 120,
		Source:           ModelMonitorSampleSourceChannelTest,
		ModelName:        "gpt-avail-sample-only",
		Group:            "vip",
		Status:           ModelMonitorSampleStatusSuccess,
		PromptTokens:     600,
		CompletionTokens: 400,
		UseTime:          2,
	}).Error)

	availability, err := buildModelMonitorAvailability(now)
	require.NoError(t, err)

	require.Equal(t, int(bucketSeconds), availability.BucketSeconds)
	require.Equal(t, now, availability.WindowEnd)
	latestStart := now - now%bucketSeconds
	windowStart := latestStart - int64(modelMonitorAvailabilityBuckets-1)*bucketSeconds
	require.Equal(t, windowStart, availability.WindowStart)

	groupByName := make(map[string]ModelMonitorAvailabilityGroup)
	groupOrder := make([]string, 0, len(availability.Groups))
	for _, group := range availability.Groups {
		groupByName[group.Group] = group
		groupOrder = append(groupOrder, group.Group)
	}
	require.Equal(t, []string{"default", "vip"}, groupOrder)

	defaultGroup := groupByName["default"]
	require.Len(t, defaultGroup.Buckets, modelMonitorAvailabilityBuckets)
	for i, bucket := range defaultGroup.Buckets {
		require.Equal(t, windowStart+int64(i)*bucketSeconds, bucket.Start)
		require.Equal(t, bucket.Start+bucketSeconds, bucket.End)
	}

	modelByName := make(map[string]ModelMonitorAvailabilityModel)
	modelOrder := make([]string, 0, len(defaultGroup.Models))
	for _, item := range defaultGroup.Models {
		modelByName[item.ModelName] = item
		modelOrder = append(modelOrder, item.ModelName)
	}
	// Models with data surface first, then alphabetical.
	require.Equal(t, []string{"gpt-avail-broken", "gpt-avail-healthy", "gpt-avail-silent"}, modelOrder)

	// Latest partial bucket contains the healthy model only.
	healthyBucket := modelByName["gpt-avail-healthy"].Buckets[modelMonitorAvailabilityBuckets-1]
	require.True(t, healthyBucket.HasData)
	require.Contains(t, []string{"excellent", "good"}, healthyBucket.Status)

	// The previous bucket holds the broken model.
	brokenBucket := modelByName["gpt-avail-broken"].Buckets[modelMonitorAvailabilityBuckets-2]
	require.True(t, brokenBucket.HasData)
	require.Equal(t, "poor", brokenBucket.Status)

	// A model without usable traffic stays gray in every bucket.
	for _, bucket := range modelByName["gpt-avail-silent"].Buckets {
		require.False(t, bucket.HasData)
		require.Equal(t, "unknown", bucket.Status)
	}

	// Group buckets mirror the only contributing model in each slot.
	require.True(t, defaultGroup.Buckets[modelMonitorAvailabilityBuckets-1].HasData)
	require.Equal(t, healthyBucket.Score, defaultGroup.Buckets[modelMonitorAvailabilityBuckets-1].Score)
	require.True(t, defaultGroup.Buckets[modelMonitorAvailabilityBuckets-2].HasData)
	require.Equal(t, brokenBucket.Score, defaultGroup.Buckets[modelMonitorAvailabilityBuckets-2].Score)
	require.False(t, defaultGroup.Buckets[0].HasData)

	// The vip group is fed by the sample table.
	vipGroup := groupByName["vip"]
	require.True(t, vipGroup.Buckets[modelMonitorAvailabilityBuckets-1].HasData)
	require.Len(t, vipGroup.Models, 1)
	require.True(t, vipGroup.Models[0].Buckets[modelMonitorAvailabilityBuckets-1].HasData)
}

func TestBuildModelMonitorAvailabilityGroupWeightedAverage(t *testing.T) {
	setupModelMonitorAvailabilityTest(t)

	bucketSeconds := int64(modelMonitorAvailabilityBucketSeconds)
	now := common.GetTimestamp()
	now = now - now%bucketSeconds + 600

	require.NoError(t, DB.Create(&Channel{
		Id:     991,
		Type:   1,
		Status: common.ChannelStatusEnabled,
	}).Error)
	seedAvailabilityAbility(t, "default", "gpt-avail-heavy")
	seedAvailabilityAbility(t, "default", "gpt-avail-light")

	for i := 0; i < 20; i++ {
		require.NoError(t, LOG_DB.Create(&Log{
			CreatedAt:        now - 60 - int64(i),
			Type:             LogTypeConsume,
			ModelName:        "gpt-avail-heavy",
			PromptTokens:     600,
			CompletionTokens: 400,
			UseTime:          2,
		}).Error)
	}
	for i := 0; i < 2; i++ {
		require.NoError(t, LOG_DB.Create(&Log{
			CreatedAt: now - 30 - int64(i),
			Type:      LogTypeError,
			ModelName: "gpt-avail-light",
			RequestId: fmt.Sprintf("avail-light-err-%d", i),
			UseTime:   12,
		}).Error)
	}

	availability, err := buildModelMonitorAvailability(now)
	require.NoError(t, err)
	require.Len(t, availability.Groups, 1)

	group := availability.Groups[0]
	require.Equal(t, "default", group.Group)
	require.Len(t, group.Models, 2)

	models := make(map[string]ModelMonitorAvailabilityModel)
	for _, item := range group.Models {
		models[item.ModelName] = item
	}
	heavy := models["gpt-avail-heavy"].Buckets[modelMonitorAvailabilityBuckets-1]
	light := models["gpt-avail-light"].Buckets[modelMonitorAvailabilityBuckets-1]
	require.True(t, heavy.HasData)
	require.True(t, light.HasData)
	require.Greater(t, heavy.Score, light.Score)

	groupBucket := group.Buckets[modelMonitorAvailabilityBuckets-1]
	require.True(t, groupBucket.HasData)
	// Request-weighted average: 20 healthy requests vs 2 failing ones.
	expected := clampModelMonitorScore((float64(heavy.Score)*20 + float64(light.Score)*2) / 22)
	require.Equal(t, expected, groupBucket.Score)
	require.InDelta(t, heavy.Score, groupBucket.Score, float64(heavy.Score-light.Score))
}

func TestBuildModelMonitorAvailabilityWithoutPricing(t *testing.T) {
	setupModelMonitorAvailabilityTest(t)

	availability, err := buildModelMonitorAvailability(common.GetTimestamp())
	require.NoError(t, err)
	require.Empty(t, availability.Groups)
	require.Len(t, availability.Groups, 0)
}
