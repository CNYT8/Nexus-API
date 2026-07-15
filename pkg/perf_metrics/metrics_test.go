package perfmetrics

import "testing"

func TestBuildQueryResultKeepsRawAndWeightedMetrics(t *testing.T) {
	endTs := int64(24 * 60 * 60)
	result := buildQueryResult("gpt-test", map[bucketKey]counters{
		{model: "gpt-test", group: "default", bucketTs: 0}: {
			requestCount:   1,
			successCount:   1,
			totalLatencyMs: 1000,
			ttftSumMs:      800,
			ttftCount:      1,
			outputTokens:   100,
			generationMs:   1000,
		},
		{model: "gpt-test", group: "default", bucketTs: endTs}: {
			requestCount:   1,
			successCount:   1,
			totalLatencyMs: 100,
			ttftSumMs:      80,
			ttftCount:      1,
			outputTokens:   100,
			generationMs:   1000,
		},
	}, endTs, 24)

	if len(result.Groups) != 1 {
		t.Fatalf("expected one group, got %d", len(result.Groups))
	}

	group := result.Groups[0]
	if group.AvgLatencyMs != 550 {
		t.Fatalf("expected raw average latency 550ms, got %dms", group.AvgLatencyMs)
	}
	if group.WeightedAvgLatencyMs >= group.AvgLatencyMs {
		t.Fatalf("expected weighted latency to favor recent bucket, got weighted=%d raw=%d", group.WeightedAvgLatencyMs, group.AvgLatencyMs)
	}
	if group.RequestCount != 2 {
		t.Fatalf("expected request count 2, got %d", group.RequestCount)
	}
	if group.WeightedRequestCount <= float64(group.RequestCount) {
		t.Fatalf("expected weighted request count to include hot bucket boost, got %.2f", group.WeightedRequestCount)
	}
	if group.AdjustedTtftMs != group.AvgTtftMs {
		t.Fatalf("expected old samples without prompt tokens to keep raw ttft, got adjusted=%d raw=%d", group.AdjustedTtftMs, group.AvgTtftMs)
	}
	if len(group.Series) != 2 || group.Series[0].RequestCount != 1 || group.Series[1].RequestCount != 1 {
		t.Fatalf("expected series request counts to be preserved, got %+v", group.Series)
	}
}

func TestBuildQueryResultAdjustsTtftForLongInput(t *testing.T) {
	endTs := int64(24 * 60 * 60)
	result := buildQueryResult("claude-test", map[bucketKey]counters{
		{model: "claude-test", group: "default", bucketTs: endTs}: {
			requestCount:   2,
			successCount:   2,
			totalLatencyMs: 6000,
			ttftSumMs:      6000,
			ttftCount:      2,
			promptTokens:   60000,
			outputTokens:   200,
			generationMs:   2000,
		},
	}, endTs, 24)

	group := result.Groups[0]
	if group.AvgTtftMs != 3000 {
		t.Fatalf("expected raw ttft 3000ms, got %dms", group.AvgTtftMs)
	}
	if group.AdjustedTtftMs >= group.AvgTtftMs {
		t.Fatalf("expected adjusted ttft to be lower for long input, got adjusted=%d raw=%d", group.AdjustedTtftMs, group.AvgTtftMs)
	}
	if group.WeightedAvgTtftMs != group.AdjustedTtftMs {
		t.Fatalf("expected single hot bucket weighted ttft to use adjusted value, got weighted=%d adjusted=%d", group.WeightedAvgTtftMs, group.AdjustedTtftMs)
	}
	if group.Series[0].AdjustedTtftMs != group.AdjustedTtftMs {
		t.Fatalf("expected series ttft to carry adjusted value, got series=%d adjusted=%d", group.Series[0].AdjustedTtftMs, group.AdjustedTtftMs)
	}
}

func TestPerfMetricTimeWeightUsesRecentBucketBoost(t *testing.T) {
	endTs := int64(24 * 60 * 60)
	windowSeconds := int64(24 * 60 * 60)

	recentWeight := perfMetricTimeWeight(endTs, endTs, windowSeconds)
	oldWeight := perfMetricTimeWeight(0, endTs, windowSeconds)

	if recentWeight <= oldWeight {
		t.Fatalf("expected recent bucket weight %.2f to be greater than old bucket weight %.2f", recentWeight, oldWeight)
	}
}
