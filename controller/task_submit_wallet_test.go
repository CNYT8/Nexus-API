package controller

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// setupSubmittedTaskWalletTest reuses the isolated controller top-up database
// and adds the task/token schema plus an initialized wallet ledger.
func setupSubmittedTaskWalletTest(t *testing.T) (*gorm.DB, *model.User, *model.Token) {
	t.Helper()
	env := setupTopUpQuotaTestEnv(t)
	db := env.db
	require.NoError(t, db.AutoMigrate(&model.Task{}, &model.Token{}, &model.Log{}, &model.Channel{}))
	require.NoError(t, db.Create(&model.RebateSystemState{Key: "wallet_credit_baseline_v1", Value: "initialized:submit-task-controller", UpdatedAt: time.Now().UTC()}).Error)
	user := model.User{Username: "submit-task-wallet", Status: common.UserStatusEnabled, AffCode: "submit-task-wallet-aff"}
	require.NoError(t, db.Create(&user).Error)
	credited, err := model.CreditTopUpTx(db, user.Id, "submit-task-topup", 10000)
	require.NoError(t, err)
	require.True(t, credited)
	token := model.Token{UserId: user.Id, Key: "submit-task-token", RemainQuota: 10000, Status: common.TokenStatusEnabled}
	require.NoError(t, db.Create(&token).Error)
	return db, &user, &token
}

func submittedTaskUserQuota(t *testing.T, db *gorm.DB, id int) int {
	t.Helper()
	var user model.User
	require.NoError(t, db.Select("quota").First(&user, id).Error)
	return user.Quota
}

func submittedTaskTokenRemain(t *testing.T, db *gorm.DB, id int) int {
	t.Helper()
	var token model.Token
	require.NoError(t, db.Select("remain_quota").First(&token, id).Error)
	return token.RemainQuota
}

func TestPersistSubmittedTaskAlignsWalletReservation(t *testing.T) {
	for _, tc := range []struct {
		name       string
		finalQuota int
		wantWallet int
	}{
		{name: "shrink", finalQuota: 2500, wantWallet: 7500},
		{name: "grow", finalQuota: 4000, wantWallet: 6000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, user, token := setupSubmittedTaskWalletTest(t)
			key, err := service.ReservePendingTaskWalletQuota(user.Id, 3000)
			require.NoError(t, err)
			info := &relaycommon.RelayInfo{
				UserId:          user.Id,
				TokenId:         token.Id,
				TokenKey:        token.Key,
				BillingSource:   service.BillingSourceWallet,
				OriginModelName: "veo-submit-test",
				UsingGroup:      "default",
				ChannelMeta:     &relaycommon.ChannelMeta{ChannelId: 1},
				TaskRelayInfo:   &relaycommon.TaskRelayInfo{},
			}
			require.NoError(t, service.PreConsumeTokenQuota(info, 3000))
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Set(service.ContextKeyTaskWalletBillingKey, key)
			ctx.Set(service.ContextKeyTaskWalletBillingQuota, 3000)
			result := &relay.TaskSubmitResult{
				Platform:       constant.TaskPlatform("test"),
				Quota:          tc.finalQuota,
				TaskData:       []byte(`{"id":"upstream"}`),
				UpstreamTaskID: "upstream-1",
			}

			task := persistSubmittedTask(ctx, info, result)
			require.NotNil(t, task)
			require.Equal(t, key, task.PrivateData.BillingKey)
			require.Equal(t, tc.finalQuota, task.Quota)
			require.Equal(t, tc.wantWallet, submittedTaskUserQuota(t, db, user.Id))
			require.Equal(t, tc.wantWallet, submittedTaskTokenRemain(t, db, token.Id))

			var stored model.Task
			require.NoError(t, db.First(&stored, task.ID).Error)
			require.Equal(t, model.TaskStatusNotStart, stored.Status)
			require.Equal(t, key, stored.PrivateData.BillingKey)
			require.Equal(t, tc.finalQuota, stored.Quota)

			var reservation model.WalletReservation
			require.NoError(t, db.Where("reservation_key = ?", key).First(&reservation).Error)
			require.Equal(t, model.WalletReservationReserved, reservation.State)
			require.EqualValues(t, tc.finalQuota, reservation.ReservedQuota)
		})
	}
}

func TestSettleTerminalSubmittedTaskReleasesReservation(t *testing.T) {
	for _, tc := range []struct {
		name      string
		status    model.TaskStatus
		wantQuota int
		wantState string
	}{
		{name: "success", status: model.TaskStatusSuccess, wantQuota: 7000, wantState: model.WalletReservationFinalized},
		{name: "failure", status: model.TaskStatusFailure, wantQuota: 10000, wantState: model.WalletReservationRefunded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, user, token := setupSubmittedTaskWalletTest(t)
			key, err := service.ReservePendingTaskWalletQuota(user.Id, 3000)
			require.NoError(t, err)
			info := &relaycommon.RelayInfo{
				UserId:        user.Id,
				TokenId:       token.Id,
				TokenKey:      token.Key,
				BillingSource: service.BillingSourceWallet,
				ChannelMeta:   &relaycommon.ChannelMeta{ChannelId: 1},
				TaskRelayInfo: &relaycommon.TaskRelayInfo{},
			}
			require.NoError(t, service.PreConsumeTokenQuota(info, 3000))
			task := model.InitTask(constant.TaskPlatform("test"), info)
			task.PrivateData.BillingKey = key
			task.PrivateData.BillingSource = service.BillingSourceWallet
			task.PrivateData.TokenId = token.Id
			task.Quota = 3000
			task.Status = tc.status
			task.Data = []byte(`{}`)
			require.NoError(t, db.Create(task).Error)

			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Request = httptest.NewRequest("POST", "/v1/videos", nil)
			settleTerminalSubmittedTask(ctx, task)
			require.Equal(t, tc.wantQuota, submittedTaskUserQuota(t, db, user.Id))
			require.Equal(t, tc.wantQuota, submittedTaskTokenRemain(t, db, token.Id))
			var reservation model.WalletReservation
			require.NoError(t, db.Where("reservation_key = ?", key).First(&reservation).Error)
			require.Equal(t, tc.wantState, reservation.State)
		})
	}
}
