package relay

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/membership_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// Keep pricing, sessions and wallet/token transactions real; replace provider I/O.
// Embedding the interface makes an unexpected provider method fail the test.
type submitRetryAdaptor struct {
	channel.TaskAdaptor
	sends      int
	requestErr error
	estimate   map[string]float64
	adjust     map[string]float64
	onRequest  func(*gin.Context, *relaycommon.RelayInfo)
}

func (*submitRetryAdaptor) Init(*relaycommon.RelayInfo) {}
func (*submitRetryAdaptor) ValidateRequestAndSetAction(*gin.Context, *relaycommon.RelayInfo) *dto.TaskError {
	return nil
}
func (a *submitRetryAdaptor) EstimateBilling(*gin.Context, *relaycommon.RelayInfo) map[string]float64 {
	return a.estimate
}
func (a *submitRetryAdaptor) AdjustBillingOnSubmit(*relaycommon.RelayInfo, []byte) map[string]float64 {
	return a.adjust
}
func (*submitRetryAdaptor) BuildRequestBody(*gin.Context, *relaycommon.RelayInfo) (io.Reader, error) {
	return strings.NewReader(`{}`), nil
}
func (a *submitRetryAdaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, _ io.Reader) (*http.Response, error) {
	a.sends++
	if a.onRequest != nil {
		a.onRequest(c, info)
	}
	if a.requestErr != nil {
		return nil, a.requestErr
	}
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
}
func (*submitRetryAdaptor) DoResponse(_ *gin.Context, resp *http.Response, _ *relaycommon.RelayInfo) (string, []byte, *dto.TaskError) {
	_ = resp.Body.Close()
	return "upstream-retry", []byte(`{"id":"upstream-retry"}`), nil
}
func (a *submitRetryAdaptor) factory(constant.TaskPlatform) channel.TaskAdaptor { return a }

func setupSubmitRetryTest(t *testing.T, tokenQuota int, perCall bool) (*gorm.DB, *gin.Context, *relaycommon.RelayInfo) {
	t.Helper()
	db, user, token := setupMJPendingLedgerTest(t, true, tokenQuota)
	require.NoError(t, db.AutoMigrate(&model.UserGroupRatioOverride{}))
	oldPrices := ratio_setting.ModelPrice2JSONString()
	oldModels := ratio_setting.ModelRatio2JSONString()
	oldGroups := ratio_setting.GroupRatio2JSONString()
	oldGroupGroups := ratio_setting.GroupGroupRatio2JSONString()
	oldMembership := *membership_setting.GetMembershipSetting()
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(oldPrices))
		require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(oldModels))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(oldGroups))
		require.NoError(t, ratio_setting.UpdateGroupGroupRatioByJSONString(oldGroupGroups))
		*membership_setting.GetMembershipSetting() = oldMembership
	})
	common.QuotaPerUnit = 100
	membership_setting.GetMembershipSetting().Enabled = false
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{}`))
	if perCall {
		require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"submit-retry-test":1}`))
	}
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"submit-retry-test":2}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"submit-retry-base":1,"submit-retry-double":2}`))
	require.NoError(t, ratio_setting.UpdateGroupGroupRatioByJSONString(`{}`))
	for _, group := range []string{"submit-retry-base", "submit-retry-double"} {
		require.NoError(t, model.DeleteUserGroupRatioOverride(user.Id, group))
	}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	requestCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", strings.NewReader(`{}`)).WithContext(requestCtx)
	c.Set("platform", "submit-retry-test")
	c.Set("auto_group", "submit-retry-base")
	info := &relaycommon.RelayInfo{
		UserId: user.Id, TokenId: token.Id, TokenKey: token.Key,
		UserGroup: "default", UsingGroup: "auto", OriginModelName: "submit-retry-test",
		TaskRelayInfo: &relaycommon.TaskRelayInfo{},
	}
	info.UserSetting.BillingPreference = "wallet_only"
	return db, c, info
}

func TestRelayTaskSubmitRetryReservesBeforeDispatch(t *testing.T) {
	for _, tc := range []struct {
		name          string
		perCall       bool
		tokenQuota    int
		otherReserve  int
		firstGroup    string
		retryGroup    string
		firstQuota    int
		quote         int
		reserved      int
		wantErrorCode string
	}{
		{name: "token_shortfall_100_to_200", perCall: true, tokenQuota: 150, firstGroup: "submit-retry-base", retryGroup: "submit-retry-double", firstQuota: 100, quote: 200, reserved: 100, wantErrorCode: "pre_consume_token_quota_failed"},
		{name: "wallet_shortfall_100_to_200", perCall: true, tokenQuota: 10000, otherReserve: 9850, firstGroup: "submit-retry-base", retryGroup: "submit-retry-double", firstQuota: 100, quote: 200, reserved: 100, wantErrorCode: "insufficient_user_quota"},
		{name: "per_call_growth", perCall: true, tokenQuota: 10000, firstGroup: "submit-retry-base", retryGroup: "submit-retry-double", firstQuota: 100, quote: 200, reserved: 200},
		{name: "per_token_growth", tokenQuota: 10000, firstGroup: "submit-retry-base", retryGroup: "submit-retry-double", firstQuota: 100, quote: 200, reserved: 200},
		{name: "lower_quote_keeps_real_reserve", perCall: true, tokenQuota: 10000, firstGroup: "submit-retry-double", retryGroup: "submit-retry-base", firstQuota: 200, quote: 100, reserved: 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, c, info := setupSubmitRetryTest(t, tc.tokenQuota, tc.perCall)
			c.Set("auto_group", tc.firstGroup)
			first := &submitRetryAdaptor{requestErr: errors.New("retryable upstream failure")}
			result, taskErr := relayTaskSubmitWithAdaptor(c, info, first.factory)
			require.Nil(t, result)
			require.NotNil(t, taskErr)
			require.False(t, taskErr.LocalError)
			require.Equal(t, 1, first.sends)
			require.Equal(t, tc.firstQuota, info.Billing.GetPreConsumedQuota())
			if tc.otherReserve > 0 {
				_, err := service.ReservePendingTaskWalletQuota(info.UserId, tc.otherReserve)
				require.NoError(t, err)
			}
			walletBefore := mjSubmitUserQuota(t, db, info.UserId)
			tokenBefore := mjSubmitTokenRemain(t, db, info.TokenId)
			c.Set("auto_group", tc.retryGroup)
			retry := &submitRetryAdaptor{onRequest: func(c *gin.Context, info *relaycommon.RelayInfo) {
				require.Equal(t, tc.reserved, info.Billing.GetPreConsumedQuota())
				require.Equal(t, tc.reserved, info.FinalPreConsumedQuota)
				require.Equal(t, tc.reserved, c.GetInt(service.ContextKeyTaskWalletBillingQuota))
			}}
			result, taskErr = relayTaskSubmitWithAdaptor(c, info, retry.factory)
			if tc.wantErrorCode != "" {
				require.Nil(t, result)
				require.NotNil(t, taskErr)
				require.True(t, taskErr.LocalError)
				require.Equal(t, http.StatusForbidden, taskErr.StatusCode)
				require.Equal(t, tc.wantErrorCode, taskErr.Code)
				require.Zero(t, retry.sends, "insufficient retry must not reach upstream")
			} else {
				require.Nil(t, taskErr)
				require.NotNil(t, result)
				require.Equal(t, 1, retry.sends)
				require.Equal(t, tc.quote, result.Quota)
			}
			require.Equal(t, tc.perCall, info.PriceData.UsePrice)
			require.Equal(t, tc.quote, info.PriceData.Quota, "a failed reserve must not overwrite the quote")
			require.Equal(t, tc.reserved, info.Billing.GetPreConsumedQuota())
			require.Equal(t, tc.reserved, info.FinalPreConsumedQuota)
			require.Equal(t, tc.reserved, c.GetInt(service.ContextKeyTaskWalletBillingQuota))
			delta := tc.reserved - tc.firstQuota
			require.Equal(t, walletBefore-delta, mjSubmitUserQuota(t, db, info.UserId))
			require.Equal(t, tokenBefore-delta, mjSubmitTokenRemain(t, db, info.TokenId))
			var reservation model.WalletReservation
			require.NoError(t, db.Where("reservation_key = ?", c.GetString(service.ContextKeyTaskWalletBillingKey)).First(&reservation).Error)
			require.EqualValues(t, tc.reserved, reservation.ReservedQuota)
		})
	}
}

func TestRelayTaskSubmitRetryChecksQuotaClamp(t *testing.T) {
	_, c, info := setupSubmitRetryTest(t, 10000, true)
	first := &submitRetryAdaptor{requestErr: errors.New("retryable upstream failure")}
	_, taskErr := relayTaskSubmitWithAdaptor(c, info, first.factory)
	require.NotNil(t, taskErr)
	require.Equal(t, 100, info.Billing.GetPreConsumedQuota())
	retry := &submitRetryAdaptor{estimate: map[string]float64{"seconds": float64(common.MaxQuota)}}
	result, taskErr := relayTaskSubmitWithAdaptor(c, info, retry.factory)
	require.Nil(t, result)
	require.NotNil(t, taskErr)
	require.True(t, taskErr.LocalError)
	require.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
	require.NotNil(t, info.QuotaClamp)
	require.Zero(t, retry.sends)
	require.Equal(t, 100, info.Billing.GetPreConsumedQuota())
	require.Equal(t, 100, c.GetInt(service.ContextKeyTaskWalletBillingQuota))
}

type submitDispatchFailure struct{ relaycommon.BillingSettler }

func (*submitDispatchFailure) MarkDispatched() error { return errors.New("dispatch barrier failed") }

func TestRelayTaskSubmitDispatchFailureStopsRequest(t *testing.T) {
	_, c, info := setupSubmitRetryTest(t, 10000, true)
	first := &submitRetryAdaptor{requestErr: errors.New("retryable upstream failure")}
	_, taskErr := relayTaskSubmitWithAdaptor(c, info, first.factory)
	require.NotNil(t, taskErr)
	info.Billing = &submitDispatchFailure{BillingSettler: info.Billing}
	c.Set("auto_group", "submit-retry-double")
	retry := &submitRetryAdaptor{}
	result, taskErr := relayTaskSubmitWithAdaptor(c, info, retry.factory)
	require.Nil(t, result)
	require.NotNil(t, taskErr)
	require.True(t, taskErr.LocalError)
	require.Equal(t, http.StatusInternalServerError, taskErr.StatusCode)
	require.Equal(t, "billing_dispatch_failed", taskErr.Code)
	require.Zero(t, retry.sends)
	require.Equal(t, 200, info.Billing.GetPreConsumedQuota())
	require.Equal(t, 200, info.FinalPreConsumedQuota)
	require.Equal(t, 200, c.GetInt(service.ContextKeyTaskWalletBillingQuota))
}

func TestRelayTaskSubmitAdjustmentKeepsQuoteSeparateFromReserve(t *testing.T) {
	_, c, info := setupSubmitRetryTest(t, 10000, true)
	adaptor := &submitRetryAdaptor{adjust: map[string]float64{"seconds": 2}}
	result, taskErr := relayTaskSubmitWithAdaptor(c, info, adaptor.factory)
	require.Nil(t, taskErr)
	require.NotNil(t, result)
	require.Equal(t, 200, result.Quota)
	require.Equal(t, 200, info.PriceData.Quota)
	require.Equal(t, 100, info.Billing.GetPreConsumedQuota())
	require.Equal(t, 100, info.FinalPreConsumedQuota)
	require.Equal(t, 100, c.GetInt(service.ContextKeyTaskWalletBillingQuota))
}
