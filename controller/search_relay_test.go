package controller

import (
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
)

func TestSearchRelayFormatBoundary(t *testing.T) {
	for _, tc := range []struct {
		path   string
		format types.RelayFormat
		bad    bool
	}{
		{dto.AlphaSearchPath, types.RelayFormatOpenAIAlphaSearch, false},
		{dto.DeepSeekSearchPath, types.RelayFormatDeepSeekTools, false},
		{dto.DeepSeekFetchPath, types.RelayFormatDeepSeekTools, false},
		{dto.AlphaSearchPath, types.RelayFormatDeepSeekTools, true},
		{dto.DeepSeekSearchPath, types.RelayFormatOpenAIAlphaSearch, true},
		{dto.DeepSeekSearchPath, types.RelayFormatOpenAI, true},
		{"/v2/alpha/search", types.RelayFormatOpenAIAlphaSearch, true},
		{"/v2/tools/fetch", types.RelayFormatDeepSeekTools, true},
	} {
		t.Run(tc.path+"/"+string(tc.format), func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", tc.path, strings.NewReader(`{"model":"search-model"}`))
			request, err := getRelayRequestWithSearch(c, tc.format)
			if (err != nil) != tc.bad {
				t.Fatalf("request=%+v error=%v bad=%v", request, err, tc.bad)
			}
			if !tc.bad {
				search, ok := request.(*dto.SearchRequest)
				if !ok || search.Path != tc.path {
					t.Fatalf("not the expected search request: %+v", request)
				}
			}
		})
	}
}

func TestSearchRetryPolicy(t *testing.T) {
	previous := operation_setting.AutomaticRetryStatusCodeRanges
	operation_setting.AutomaticRetryStatusCodeRanges = []operation_setting.StatusCodeRange{{Start: 429, End: 429}, {Start: 500, End: 599}}
	t.Cleanup(func() { operation_setting.AutomaticRetryStatusCodeRanges = previous })
	for _, tc := range []struct {
		name        string
		code        types.ErrorCode
		status      int
		remaining   int
		skip, fixed bool
		want        bool
	}{
		{name: "transport can retry", code: types.ErrorCodeDoRequestFailed, status: 502, remaining: 1, want: true},
		{name: "rate limit can retry", code: types.ErrorCodeBadResponseStatusCode, status: 429, remaining: 1, want: true},
		{name: "unsupported channel can retry", code: types.ErrorCodeInvalidApiType, status: 500, remaining: 1, want: true},
		{name: "fixed channel does not retry", code: types.ErrorCodeDoRequestFailed, status: 502, remaining: 1, fixed: true},
		{name: "exhausted retry budget", code: types.ErrorCodeDoRequestFailed, status: 502},
		{name: "sensitive rejection", code: types.ErrorCodeSensitiveWordsDetected, status: 500, remaining: 1, skip: true},
		{name: "malformed successful response", code: types.ErrorCodeBadResponseBody, status: 502, remaining: 1},
		{name: "timeout retains no-retry policy", code: types.ErrorCodeBadResponseStatusCode, status: 504, remaining: 1},
		{name: "successful response never retries", code: types.ErrorCodeBadResponseStatusCode, status: 200, remaining: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", dto.DeepSeekSearchPath, nil)
			if tc.fixed {
				c.Set("specific_channel_id", "1")
			}
			err := types.NewErrorWithStatusCode(errors.New("search failure"), tc.code, tc.status)
			if tc.skip {
				types.ErrOptionWithSkipRetry()(err)
			}
			if got := shouldRetry(c, err, tc.remaining); got != tc.want {
				t.Fatalf("retry=%v, want=%v", got, tc.want)
			}
		})
	}
}
