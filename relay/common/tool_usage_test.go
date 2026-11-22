package common

import (
	"fmt"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func setResponsesTestToolPrices(t *testing.T, prices map[string]float64) {
	t.Helper()
	setting := config.GlobalConfig.Get("tool_price_setting").(*operation_setting.ToolPriceSetting)
	previous := setting.Prices
	setting.Prices = prices
	operation_setting.RebuildToolPriceIndex()
	t.Cleanup(func() {
		setting.Prices = previous
		operation_setting.RebuildToolPriceIndex()
	})
}

func TestResponsesToolUsageCompletedEvidence(t *testing.T) {
	for _, test := range []struct {
		name      string
		item      string
		completed bool
		want      int
	}{
		{"completed image", `{"type":"image_generation_call","id":"one","status":"completed","result":"image"}`, false, 1},
		{"legacy completed boundary", `{"type":"image_generation_call","id":"one","result":"image"}`, true, 1},
		{"missing completion", `{"type":"image_generation_call","id":"one","result":"image"}`, false, 0},
		{"missing result", `{"type":"image_generation_call","id":"one","status":"completed"}`, true, 0},
		{"empty result", `{"type":"image_generation_call","id":"one","result":"  "}`, true, 0},
		{"nonstring result", `{"type":"image_generation_call","id":"one","result":42}`, true, 0},
		{"failed image", `{"type":"image_generation_call","id":"one","status":"failed","result":"image"}`, true, 0},
		{"cancelled image", `{"type":"image_generation_call","id":"one","status":"cancelled","result":"image"}`, true, 0},
		{"canceled image", `{"type":"image_generation_call","id":"one","status":"canceled","result":"image"}`, true, 0},
		{"partial image", `{"type":"image_generation_call","id":"one","status":"partial","result":"image"}`, true, 0},
		{"incomplete image", `{"type":"image_generation_call","id":"one","status":"incomplete","result":"image"}`, true, 0},
		{"in progress image", `{"type":"image_generation_call","id":"one","status":"in_progress","result":"image"}`, true, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			var counter ResponsesToolUsageCounter
			counter.ObserveItem(nil, nil, gjson.Parse(test.item), nil, test.completed)
			require.NotNil(t, counter.Counts())
			require.Equal(t, test.want, counter.Counts()["image_generation"])
		})
	}
}

func TestResponsesToolUsageAliasesAndDistinctCalls(t *testing.T) {
	var counter ResponsesToolUsageCounter
	index := 0
	counter.ObserveItem(nil, nil, gjson.Parse(`{"type":"image_generation_call","call_id":"call-a","result":"image-a"}`), &index, true)
	counter.ObserveItem(nil, nil, gjson.Parse(`{"type":"image_generation_call","id":"item-a","call_id":"call-a","result":"image-a"}`), nil, true)
	// Replay with only the later item ID and a new digest still aliases call-a.
	counter.ObserveItem(nil, nil, gjson.Parse(`{"type":"image_generation_call","id":"item-a","result":"image-a-replay"}`), nil, true)
	counter.ObserveResponse(nil, nil, gjson.Parse(`{"output":[{"type":"image_generation_call","id":"item-a","result":"image-a"},{"type":"image_generation_call","id":"item-b","result":"image-b"}]}`), true)
	require.Equal(t, 2, counter.Counts()["image_generation"])

	var digestOnly ResponsesToolUsageCounter
	for _, item := range []string{
		`{"type":"image_generation_call","result":"same-image"}`,
		`{"type":"image_generation_call","id":"later-id","result":"same-image"}`,
		`{"type":"image_generation_call","id":"later-id","result":"later-result"}`,
	} {
		digestOnly.ObserveItem(nil, nil, gjson.Parse(item), nil, true)
	}
	require.Equal(t, 1, digestOnly.Counts()["image_generation"])

	// A partial terminal array can reuse index 0 for a different identified call.
	var searches ResponsesToolUsageCounter
	searches.ObserveItem(nil, nil, gjson.Parse(`{"type":"web_search_call","id":"search-a"}`), &index, true)
	searches.ObserveResponse(nil, nil, gjson.Parse(`{"output":[{"type":"web_search_call","id":"search-b"}]}`), true)
	require.Equal(t, 2, searches.Counts()[dto.BuildInToolWebSearchPreview])

	var indexOnly ResponsesToolUsageCounter
	indexOnly.ObserveItem(nil, nil, gjson.Parse(`{"type":"file_search_call"}`), &index, true)
	indexOnly.ObserveItem(nil, nil, gjson.Parse(`{"type":"file_search_call","id":"files"}`), &index, true)
	indexOnly.ObserveItem(nil, nil, gjson.Parse(`{"type":"file_search_call","id":"files"}`), nil, true)
	require.Equal(t, 1, indexOnly.Counts()[dto.BuildInToolFileSearch])
}

func TestResponsesToolUsagePricedFunctionsAndReservedNames(t *testing.T) {
	setResponsesTestToolPrices(t, map[string]float64{
		"lookup": 3, "lookup:billing-model": 6, "lookup:free-model": 0,
		"prefix_only:billing-*": 4, "web_search": 999, "code_execution": 999,
	})
	for _, test := range []struct {
		name, model, status string
		want                int
	}{
		{"lookup", "billing-model", "completed", 1},
		{"lookup", "free-model", "completed", 0},
		{"lookup", "billing-model", "failed", 0},
		{"lookup", "billing-model", "in_progress", 0},
		{"prefix_only", "billing-model", "completed", 1},
		{"prefix_only", "other-model", "completed", 0},
		{"unpriced", "billing-model", "completed", 0},
		{"web_search", "billing-model", "completed", 0},
		{"code_execution", "billing-model", "completed", 0},
		{"image_generation", "billing-model", "completed", 0},
	} {
		t.Run(test.name+"/"+test.model+"/"+test.status, func(t *testing.T) {
			info := &RelayInfo{OriginModelName: test.model}
			var counter ResponsesToolUsageCounter
			item := gjson.Parse(fmt.Sprintf(`{"type":"function_call","id":"item","call_id":"call","name":%q,"status":%q}`, test.name, test.status))
			counter.ObserveItem(nil, info, item, nil, true)
			counter.ObserveItem(nil, info, item, nil, true)
			require.Equal(t, test.want, counter.Counts()[test.name])
		})
	}
}

func TestResponsesToolUsageFailedParentAndIdempotentCommit(t *testing.T) {
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	info := &RelayInfo{ResponsesUsageInfo: &ResponsesUsageInfo{BuiltInTools: map[string]*BuildInToolInfo{
		"web_search": {CallCount: 9}, "image_generation": {CallCount: 9},
	}}}
	var counter ResponsesToolUsageCounter
	counter.ObserveResponse(ctx, info, gjson.Parse(`{"status":"failed","output":[{"type":"web_search_call","id":"successful","status":"completed"},{"type":"web_search_call","id":"unknown"},{"type":"image_generation_call","id":"failed","status":"failed","result":"image"}]}`), true)
	require.Equal(t, map[string]int{"web_search": 1}, counter.Commit(ctx, info))
	require.Equal(t, map[string]int{"web_search": 1}, counter.Commit(ctx, info))
	require.Equal(t, 1, info.BuiltInTools["web_search"].CallCount)
	require.Zero(t, info.BuiltInTools["image_generation"].CallCount)
	require.False(t, ctx.GetBool("image_generation_call"))

	ctx.Set("image_generation_call", true)
	ctx.Set("image_generation_call_count", 9)
	var empty ResponsesToolUsageCounter
	counts := empty.Commit(ctx, info)
	require.NotNil(t, counts)
	require.Empty(t, counts)
	require.Zero(t, info.BuiltInTools["web_search"].CallCount)
	require.Zero(t, ctx.GetInt("image_generation_call_count"))
	require.False(t, ctx.GetBool("image_generation_call"))
}

func TestResponsesToolUsageCapsCounts(t *testing.T) {
	var images ResponsesToolUsageCounter
	for index := 0; index < dto.MaxImageN+2; index++ {
		images.ObserveItem(nil, nil, gjson.Parse(fmt.Sprintf(`{"type":"image_generation_call","result":"image-%d"}`, index)), &index, true)
	}
	require.Equal(t, dto.MaxImageN, images.Counts()["image_generation"])
	var searches ResponsesToolUsageCounter
	for index := 0; index < MaxBillableToolCallCount+2; index++ {
		searches.ObserveItem(nil, nil, gjson.Parse(`{"type":"file_search_call"}`), &index, true)
	}
	require.Equal(t, MaxBillableToolCallCount, searches.Counts()[dto.BuildInToolFileSearch])
}
