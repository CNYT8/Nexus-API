package relay

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type searchHandlerBillingProbe struct {
	settlements, refunds, actual int
}

func (b *searchHandlerBillingProbe) Reserve(int) error { return nil }
func (b *searchHandlerBillingProbe) Settle(actual int) error {
	b.actual = actual
	b.settlements++
	return nil
}
func (b *searchHandlerBillingProbe) Refund(*gin.Context)      { b.refunds++ }
func (b *searchHandlerBillingProbe) NeedsRefund() bool        { return b.settlements == 0 }
func (b *searchHandlerBillingProbe) GetPreConsumedQuota() int { return 25 }

func searchHandlerContext(t *testing.T, path string) (*gin.Context, *httptest.ResponseRecorder, *relaycommon.RelayInfo, *searchHandlerBillingProbe) {
	t.Helper()
	body := `{"model":"search-model","opaque":9007199254740993}`
	request, err := dto.ParseSearchRequest(path, []byte(body))
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeOpenAI)
	common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, "https://search.invalid")
	common.SetContextKey(c, constant.ContextKeyChannelKey, "channel-secret")
	common.SetContextKey(c, constant.ContextKeyOriginalModel, request.Model)
	billing := &searchHandlerBillingProbe{}
	info := &relaycommon.RelayInfo{Request: request, OriginModelName: request.Model, RequestURLPath: path,
		RelayMode: relayconstant.Path2RelayMode(path), Billing: billing,
		PriceData: types.PriceData{UsePrice: true, Quota: 25, QuotaToPreConsume: 25},
	}
	return c, w, info, billing
}

func TestSearchHandlerFailures(t *testing.T) {
	client := service.GetHttpClient()
	if client == nil {
		service.InitHttpClient()
		client = service.GetHttpClient()
	}
	previous := *client
	t.Cleanup(func() { *client = previous })
	for _, tc := range []struct {
		name, body string
		status     int
		transport  bool
		sensitive  bool
		code       types.ErrorCode
	}{
		{name: "masked transport failure", transport: true, status: 502, code: types.ErrorCodeDoRequestFailed},
		{name: "upstream rate limit", status: 429, body: `{"error":{"message":"retry later","type":"rate_limit"}}`},
		{name: "invalid success JSON", status: 200, body: `<html>not a result</html>`, code: types.ErrorCodeBadResponseBody},
		{name: "invalid tool results shape", status: 200, body: `{"results":{}}`, code: types.ErrorCodeBadResponseBody},
		{name: "error envelope with success status", status: 200, body: `{"error":{"message":"upstream-secret"}}`, code: types.ErrorCodeBadResponseBody},
		{name: "sensitive error before write", status: 200, body: `{"results":[{"snippet":"safe秘密tail"}]}`, sensitive: true, code: types.ErrorCodeSensitiveWordsDetected},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, w, info, billing := searchHandlerContext(t, dto.DeepSeekSearchPath)
			if tc.sensitive {
				info.OutputSensitiveConfig = setting.OutputSensitiveConfig{Enabled: true, Action: "error", MatchPercent: 100, Patterns: []string{"秘密"}}
			}
			calls := 0
			client.Transport = searchRoundTripper(func(req *http.Request) (*http.Response, error) {
				calls++
				if tc.transport {
					return nil, errors.New("proxy password=upstream-secret")
				}
				return &http.Response{StatusCode: tc.status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(tc.body)), Request: req}, nil
			})
			apiErr := DeepSeekToolsHelper(c, info)
			if apiErr == nil || calls != 1 || w.Body.Len() != 0 || c.Writer.Written() || billing.settlements != 0 || billing.refunds != 0 {
				t.Fatalf("error=%v calls=%d body=%q billing=%+v", apiErr, calls, w.Body.String(), billing)
			}
			if tc.code != "" && apiErr.GetErrorCode() != tc.code {
				t.Fatalf("error code=%s, want=%s", apiErr.GetErrorCode(), tc.code)
			}
			if tc.status == 429 && apiErr.StatusCode != 429 {
				t.Fatalf("lost upstream status: %d", apiErr.StatusCode)
			}
			if tc.transport && strings.Contains(apiErr.Error(), "upstream-secret") {
				t.Fatal("transport secret escaped the public error mask")
			}
			if tc.sensitive && !types.IsSkipRetryError(apiErr) {
				t.Fatal("sensitive policy error must not retry upstream work")
			}
		})
	}
}

func TestSearchHandlerEmptyResultsSettleZero(t *testing.T) {
	client := service.GetHttpClient()
	if client == nil {
		service.InitHttpClient()
		client = service.GetHttpClient()
	}
	previousClient := *client
	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousSQLite, previousRedis := common.UsingSQLite, common.RedisEnabled
	previousMySQL, previousPostgreSQL := common.UsingMySQL, common.UsingPostgreSQL
	previousBatch, previousLogConsume := common.BatchUpdateEnabled, common.LogConsumeEnabled
	previousDataExport := common.DataExportEnabled
	t.Cleanup(func() {
		*client = previousClient
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.UsingSQLite, common.RedisEnabled = previousSQLite, previousRedis
		common.UsingMySQL, common.UsingPostgreSQL = previousMySQL, previousPostgreSQL
		common.BatchUpdateEnabled, common.LogConsumeEnabled = previousBatch, previousLogConsume
		common.DataExportEnabled = previousDataExport
	})
	common.UsingSQLite, common.RedisEnabled = true, false
	common.UsingMySQL, common.UsingPostgreSQL = false, false
	common.BatchUpdateEnabled, common.LogConsumeEnabled = false, true
	common.DataExportEnabled = false

	for _, tc := range []struct {
		name, body string
	}{
		{name: "omitted results", body: `{}`},
		{name: "null results", body: `{"results":null}`},
		{name: "empty results", body: `{"results":[]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			sqlDB.SetMaxOpenConns(1)
			model.DB, model.LOG_DB = db, db
			t.Cleanup(func() {
				model.DB, model.LOG_DB = previousDB, previousLogDB
				require.NoError(t, sqlDB.Close())
			})
			require.NoError(t, db.AutoMigrate(&model.User{}, &model.Channel{}, &model.Log{}))
			user := model.User{Username: "search-empty-user", AffCode: "search-empty-aff", Quota: 1000, UsedQuota: 17, RequestCount: 3}
			channel := model.Channel{Type: constant.ChannelTypeOpenAI, Name: "search-empty-channel", Key: "channel-secret", UsedQuota: 19}
			require.NoError(t, db.Create(&user).Error)
			require.NoError(t, db.Create(&channel).Error)

			c, w, info, billing := searchHandlerContext(t, dto.DeepSeekSearchPath)
			info.UserId, info.StartTime = user.Id, time.Now()
			info.FinalPreConsumedQuota = billing.GetPreConsumedQuota()
			common.SetContextKey(c, constant.ContextKeyChannelId, channel.Id)
			c.Set("username", user.Username)
			calls := 0
			client.Transport = searchRoundTripper(func(req *http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(tc.body)), Request: req}, nil
			})

			require.Nil(t, DeepSeekToolsHelper(c, info))
			require.Equal(t, 1, calls)
			require.Equal(t, http.StatusOK, w.Code)
			require.True(t, c.Writer.Written())
			require.Equal(t, "application/json", w.Header().Get("Content-Type"))
			require.Equal(t, tc.body, w.Body.String())
			require.Equal(t, 1, billing.settlements)
			require.Zero(t, billing.actual)
			require.Zero(t, billing.refunds)
			require.False(t, billing.NeedsRefund())

			var storedUser model.User
			require.NoError(t, db.First(&storedUser, user.Id).Error)
			require.Equal(t, user.Quota, storedUser.Quota)
			require.Equal(t, user.UsedQuota, storedUser.UsedQuota)
			require.Equal(t, user.RequestCount+1, storedUser.RequestCount)
			var storedChannel model.Channel
			require.NoError(t, db.First(&storedChannel, channel.Id).Error)
			require.Equal(t, channel.UsedQuota, storedChannel.UsedQuota)
			var logs []model.Log
			require.NoError(t, db.Find(&logs).Error)
			require.Len(t, logs, 1)
			log := logs[0]
			require.Equal(t, model.LogTypeConsume, log.Type)
			require.Equal(t, user.Id, log.UserId)
			require.Equal(t, channel.Id, log.ChannelId)
			require.Equal(t, info.OriginModelName, log.ModelName)
			require.Zero(t, log.Quota)
			var other map[string]interface{}
			require.NoError(t, json.Unmarshal([]byte(log.Other), &other))
			require.Equal(t, "search_call", other["usage_semantic"])
			require.Equal(t, dto.DeepSeekSearchPath, other["search_endpoint"])
			require.Equal(t, float64(0), other["search_call_count"])
			require.Equal(t, true, other["search_empty_result"])
			require.NotContains(t, log.Other, "search_settlement_pending")
		})
	}
}

func TestSearchHandlerPreDispatchGuards(t *testing.T) {
	client := service.GetHttpClient()
	if client == nil {
		service.InitHttpClient()
		client = service.GetHttpClient()
	}
	previous := *client
	t.Cleanup(func() { *client = previous })
	calls := 0
	client.Transport = searchRoundTripper(func(*http.Request) (*http.Response, error) {
		calls++
		return nil, errors.New("unexpected upstream dispatch")
	})
	for _, tc := range []struct {
		name string
		edit func(*gin.Context, *relaycommon.RelayInfo)
	}{
		{name: "missing billing session", edit: func(_ *gin.Context, info *relaycommon.RelayInfo) { info.Billing = nil }},
		{name: "wrong endpoint", edit: func(c *gin.Context, _ *relaycommon.RelayInfo) { c.Request.URL.Path = "/v2/alpha/search" }},
		{name: "unsupported native provider", edit: func(c *gin.Context, _ *relaycommon.RelayInfo) {
			common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeAnthropic)
		}},
		{name: "streaming override", edit: func(c *gin.Context, _ *relaycommon.RelayInfo) {
			common.SetContextKey(c, constant.ContextKeyChannelParamOverride, map[string]interface{}{"stream": true})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, w, info, billing := searchHandlerContext(t, dto.AlphaSearchPath)
			tc.edit(c, info)
			if apiErr := AlphaSearchHelper(c, info); apiErr == nil || w.Body.Len() != 0 || billing.settlements != 0 || calls != 0 {
				t.Fatalf("pre-dispatch guard failed: error=%v body=%q billing=%+v", apiErr, w.Body.String(), billing)
			}
		})
	}
}
