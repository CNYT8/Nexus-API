package controller

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAdminUpdateSubscriptionPlanPolicyCompatibility(t *testing.T) {
	for _, tc := range []struct {
		name         string
		fields       string
		wantTarget   string
		wantOverflow bool
		wantSuccess  bool
	}{
		{name: "omitted_preserves_policy", wantTarget: "vip", wantSuccess: true},
		{name: "null_preserves_policy", fields: `,"downgrade_group":null,"allow_wallet_overflow":null`, wantTarget: "vip", wantSuccess: true},
		{name: "empty_clears_target", fields: `,"downgrade_group":""`, wantSuccess: true},
		{name: "whitespace_clears_target", fields: `,"downgrade_group":"  "`, wantSuccess: true},
		{name: "explicit_replaces_policy", fields: `,"downgrade_group":" default ","allow_wallet_overflow":true`, wantTarget: "default", wantOverflow: true, wantSuccess: true},
		{name: "explicit_false_is_preserved", fields: `,"downgrade_group":"default","allow_wallet_overflow":false`, wantTarget: "default", wantSuccess: true},
		{name: "unknown_group_rejected", fields: `,"downgrade_group":"missing","allow_wallet_overflow":true`, wantTarget: "vip"},
		{name: "non_string_group_rejected", fields: `,"downgrade_group":false`, wantTarget: "vip"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setupUserGroupRatioControllerTestDB(t)
			confirmPaymentComplianceForTest(t)
			require.NoError(t, model.DB.AutoMigrate(&model.SubscriptionPlan{}, &model.UserSubscription{}))
			plan := &model.SubscriptionPlan{
				Title: "original", PriceAmount: 10, Currency: "USD", Enabled: true,
				DurationUnit: model.SubscriptionDurationMonth, DurationValue: 1,
				QuotaResetPeriod: model.SubscriptionResetNever,
				DowngradeGroup:   "vip", AllowWalletOverflow: common.GetPointer(false),
				AllowBalancePay: common.GetPointer(false), TotalAmount: 100,
			}
			require.NoError(t, model.DB.Create(plan).Error)
			model.InvalidateSubscriptionPlanCache(plan.Id)
			t.Cleanup(func() { model.InvalidateSubscriptionPlanCache(plan.Id) })
			sub := &model.UserSubscription{
				UserId: 1, PlanId: plan.Id, Status: "active", EndTime: common.GetTimestamp() + 3600,
				DowngradeGroup: "vip", AllowWalletOverflow: common.GetPointer(false),
			}
			require.NoError(t, model.DB.Create(sub).Error)
			// Populate the plan cache so the handler must invalidate it on success.
			_, err := model.GetSubscriptionPlanById(plan.Id)
			require.NoError(t, err)
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Params = gin.Params{{Key: "id", Value: strconv.Itoa(plan.Id)}}
			ctx.Set("role", common.RoleRootUser)
			body := `{"plan":{"title":"edited","duration_unit":"month","duration_value":1,"enabled":true` + tc.fields + `}}`
			ctx.Request = httptest.NewRequest(http.MethodPut, "/api/subscription/admin/plans/"+strconv.Itoa(plan.Id), strings.NewReader(body))
			ctx.Request.Header.Set("Content-Type", "application/json")
			AdminUpdateSubscriptionPlan(ctx)
			require.Equal(t, http.StatusOK, recorder.Code)
			var response struct {
				Success bool   `json:"success"`
				Message string `json:"message"`
			}
			require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
			require.Equal(t, tc.wantSuccess, response.Success, response.Message)
			var actual model.SubscriptionPlan
			require.NoError(t, model.DB.First(&actual, plan.Id).Error)
			require.Equal(t, tc.wantTarget, actual.DowngradeGroup)
			require.NotNil(t, actual.AllowWalletOverflow)
			require.Equal(t, tc.wantOverflow, *actual.AllowWalletOverflow)
			require.NotNil(t, actual.AllowBalancePay)
			require.False(t, *actual.AllowBalancePay)
			if tc.wantSuccess {
				require.Equal(t, "edited", actual.Title)
				cached, err := model.GetSubscriptionPlanById(plan.Id)
				require.NoError(t, err)
				require.Equal(t, tc.wantTarget, cached.DowngradeGroup)
				require.Equal(t, tc.wantOverflow, *cached.AllowWalletOverflow)
			} else {
				require.Equal(t, "original", actual.Title)
			}
			var actualSub model.UserSubscription
			require.NoError(t, model.DB.First(&actualSub, sub.Id).Error)
			require.Equal(t, *sub, actualSub)
		})
	}
}
