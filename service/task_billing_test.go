package service

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// Each ledger test owns its database and restores the package globals. The
// one-connection pool deliberately detects accidental outer-DB calls in a Tx.
func setupTaskLedgerTest(t *testing.T) (*gorm.DB, *model.Task) {
	t.Helper()
	db := setupWalletSettlementTestDB(t)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(&model.Task{}, &model.Token{}, &model.Log{}, &model.Channel{}, &model.WalletReservation{}, &model.WalletLotHold{}, &model.WalletDebtRepayment{}, &model.RebateRecord{}, &model.RebateCapBucket{}))
	originalSetting := operation_setting.GetRebateSetting()
	originalUnit, originalBatch := common.QuotaPerUnit, common.BatchUpdateEnabled
	common.QuotaPerUnit, common.BatchUpdateEnabled = 500000, false
	require.NoError(t, operation_setting.UpdateRebateSetting(operation_setting.RebateSetting{Enabled: true, RateBasisPoints: 250, PayoutMode: operation_setting.RebatePayoutImmediate, PayoutHour: 7, InviterLimitPeriod: operation_setting.RebateLimitPeriodMonthly, SiteTimezone: "UTC"}))
	t.Cleanup(func() {
		common.QuotaPerUnit, common.BatchUpdateEnabled = originalUnit, originalBatch
		require.NoError(t, operation_setting.UpdateRebateSetting(originalSetting))
	})
	require.NoError(t, db.Create(&model.RebateSystemState{Key: "wallet_credit_baseline_v1", Value: "initialized:task-test", UpdatedAt: time.Now().UTC()}).Error)
	inviter := model.User{Username: "task-inviter", AffCode: "task-parent", Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&inviter).Error)
	user := model.User{Username: "task-invitee", AffCode: "task-child", InviterId: inviter.Id, Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&user).Error)
	credited, err := model.CreditTopUpTx(db, user.Id, "task-paid-topup", 10000)
	require.NoError(t, err)
	require.True(t, credited)
	token := model.Token{UserId: user.Id, Key: "task-ledger-token", RemainQuota: 10000, Status: common.TokenStatusEnabled}
	require.NoError(t, db.Create(&token).Error)
	task := makeTask(user.Id, 0, 0, token.Id, BillingSourceWallet, 0)
	task.TaskID = "same-upstream-id-is-not-an-accounting-identity"
	require.NoError(t, db.Create(task).Error)
	return db, task
}

func taskLedgerCount(t *testing.T, db *gorm.DB, value interface{}) int64 {
	t.Helper()
	var count int64
	require.NoError(t, db.Model(value).Count(&count).Error)
	return count
}

func markTaskSuccess(t *testing.T, db *gorm.DB, task *model.Task) {
	t.Helper()
	require.NoError(t, db.Model(task).UpdateColumn("status", model.TaskStatusSuccess).Error)
	task.Status = model.TaskStatusSuccess
}

func TestTaskLedgerReserveAndSubmitFailureNeverReward(t *testing.T) {
	db, task := setupTaskLedgerTest(t)
	require.NoError(t, ReserveTaskWalletQuota(task, 3000))
	require.NoError(t, ReserveTaskWalletQuota(task, 3000))
	require.Equal(t, 7000, getUserQuota(t, task.UserId))
	require.Equal(t, 7000, getTokenRemainQuota(t, task.PrivateData.TokenId))
	require.Zero(t, taskLedgerCount(t, db, &model.RebateRecord{}))
	stale := *task
	RefundTaskQuota(context.Background(), task, "submit failed")
	RefundTaskQuota(context.Background(), &stale, "retry submit failed")
	require.Equal(t, 10000, getUserQuota(t, task.UserId))
	require.Equal(t, 10000, getTokenRemainQuota(t, task.PrivateData.TokenId))
	require.Zero(t, taskLedgerCount(t, db, &model.RebateRecord{}))
	require.Zero(t, taskLedgerCount(t, db, &model.WalletConsumptionSettlement{}))
	require.EqualValues(t, 1, countLogs(t))
	require.Error(t, ReserveTaskWalletQuota(task, 3000))
}

func TestTaskLedgerFinalConsumptionIsOnceAcrossReloads(t *testing.T) {
	for _, actual := range []int{0, 2000, 3000, 4500} {
		t.Run(fmt.Sprint(actual), func(t *testing.T) {
			db, task := setupTaskLedgerTest(t)
			require.NoError(t, ReserveTaskWalletQuota(task, 3000))
			markTaskSuccess(t, db, task)
			stale := *task
			RecalculateTaskQuota(context.Background(), task, actual, "completed")
			RecalculateTaskQuota(context.Background(), &stale, actual, "stale retry")
			var reloaded model.Task
			require.NoError(t, db.First(&reloaded, task.ID).Error)
			RecalculateTaskQuota(context.Background(), &reloaded, actual, "reload retry")
			require.Equal(t, actual, reloaded.Quota)
			require.Equal(t, 10000-actual, getUserQuota(t, task.UserId))
			require.Equal(t, 10000-actual, getTokenRemainQuota(t, task.PrivateData.TokenId))
			require.EqualValues(t, 1, taskLedgerCount(t, db, &model.WalletConsumptionSettlement{}))
			expectedRewards := int64(1)
			if actual == 0 {
				expectedRewards = 0
			}
			require.Equal(t, expectedRewards, taskLedgerCount(t, db, &model.RebateRecord{}))
			// Adjustments and an outcome reversal cannot create a second reward
			// or refund successful consumption.
			RecalculateTaskQuota(context.Background(), &stale, actual+500, "changed actual")
			RefundTaskQuota(context.Background(), &stale, "late failure")
			require.Equal(t, 10000-actual, getUserQuota(t, task.UserId))
			require.Equal(t, 10000-actual, getTokenRemainQuota(t, task.PrivateData.TokenId))
			require.Equal(t, expectedRewards, taskLedgerCount(t, db, &model.RebateRecord{}))
		})
	}
}

func TestTaskLedgerConcurrentFinalization(t *testing.T) {
	db, task := setupTaskLedgerTest(t)
	require.NoError(t, ReserveTaskWalletQuota(task, 3000))
	markTaskSuccess(t, db, task)
	var wg sync.WaitGroup
	errs := make(chan error, 12)
	for i := 0; i < 12; i++ {
		copy := *task
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, err := settleTaskWalletQuota(&copy, 4500, false)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	require.Equal(t, 5500, getUserQuota(t, task.UserId))
	require.Equal(t, 5500, getTokenRemainQuota(t, task.PrivateData.TokenId))
	require.EqualValues(t, 1, taskLedgerCount(t, db, &model.RebateRecord{}))
}

func TestTaskLedgerFailedTokenWriteRollsBackWholeFinalization(t *testing.T) {
	db, task := setupTaskLedgerTest(t)
	require.NoError(t, ReserveTaskWalletQuota(task, 3000))
	markTaskSuccess(t, db, task)
	require.NoError(t, db.Exec("CREATE TRIGGER task_token_failure BEFORE UPDATE OF remain_quota ON tokens BEGIN SELECT RAISE(FAIL, 'injected token failure'); END").Error)
	_, _, err := settleTaskWalletQuota(task, 4500, false)
	require.ErrorContains(t, err, "injected token failure")
	require.Equal(t, 7000, getUserQuota(t, task.UserId))
	require.Equal(t, 7000, getTokenRemainQuota(t, task.PrivateData.TokenId))
	require.Zero(t, taskLedgerCount(t, db, &model.RebateRecord{}))
	require.Zero(t, taskLedgerCount(t, db, &model.WalletConsumptionSettlement{}))
	require.NoError(t, db.Exec("DROP TRIGGER task_token_failure").Error)

	// The token write failed after the durable finalizing barrier committed, so
	// recovery (or an identical retry) can still replay the wallet settlement.
	var reservation model.WalletReservation
	key, err := TaskWalletBillingKey(task)
	require.NoError(t, err)
	require.NoError(t, db.Where("reservation_key = ?", key).First(&reservation).Error)
	require.Equal(t, model.WalletReservationFinalizing, reservation.State)

	applied, _, err := settleTaskWalletQuota(task, 4500, false)
	require.NoError(t, err)
	require.True(t, applied)
	require.EqualValues(t, 1, taskLedgerCount(t, db, &model.RebateRecord{}))
}

func TestTaskLedgerFailedReservationDoesNotDebitWalletOrReward(t *testing.T) {
	db, task := setupTaskLedgerTest(t)
	require.NoError(t, db.Model(&model.Token{}).Where("id = ?", task.PrivateData.TokenId).UpdateColumn("remain_quota", 100).Error)
	require.ErrorIs(t, ReserveTaskWalletQuota(task, 3000), model.ErrInsufficientQuota)
	require.Equal(t, 10000, getUserQuota(t, task.UserId))
	require.Zero(t, taskLedgerCount(t, db, &model.WalletReservation{}))
	require.Zero(t, taskLedgerCount(t, db, &model.WalletLotHold{}))
	require.Zero(t, taskLedgerCount(t, db, &model.RebateRecord{}))
}

func TestTaskLedgerRequiresRealIdentityAndReservation(t *testing.T) {
	db, task := setupTaskLedgerTest(t)
	copy := *task
	copy.ID = 0
	require.ErrorIs(t, ReserveTaskWalletQuota(&copy, 100), ErrWalletBillingIdentityRequired)
	copy = *task
	copy.ID += 1234
	require.Error(t, ReserveTaskWalletQuota(&copy, 100))
	markTaskSuccess(t, db, task)
	_, _, err := settleTaskWalletQuota(task, 100, false)
	require.Error(t, err)
	require.Equal(t, 10000, getUserQuota(t, task.UserId))
	require.Zero(t, taskLedgerCount(t, db, &model.RebateRecord{}))
	// Different persisted rows with the same upstream/public ID remain distinct.
	copy = *task
	copy.ID, copy.Status = 0, model.TaskStatusInProgress
	require.NoError(t, db.Create(&copy).Error)
	key1, err := TaskWalletBillingKey(task)
	require.NoError(t, err)
	key2, err := TaskWalletBillingKey(&copy)
	require.NoError(t, err)
	require.NotEqual(t, key1, key2)
}

func TestTaskLedgerRejectsTokenOverflowAndWrongOwner(t *testing.T) {
	for _, wrongOwner := range []bool{false, true} {
		t.Run(fmt.Sprint(wrongOwner), func(t *testing.T) {
			db, task := setupTaskLedgerTest(t)
			require.NoError(t, ReserveTaskWalletQuota(task, 3000))
			markTaskSuccess(t, db, task)
			changes := map[string]interface{}{"used_quota": math.MaxInt}
			if wrongOwner {
				changes = map[string]interface{}{"user_id": task.UserId + 99}
			}
			require.NoError(t, db.Model(&model.Token{}).Where("id = ?", task.PrivateData.TokenId).Updates(changes).Error)
			_, _, err := settleTaskWalletQuota(task, 4500, false)
			require.Error(t, err)
			require.Equal(t, 7000, getUserQuota(t, task.UserId))
			require.Zero(t, taskLedgerCount(t, db, &model.RebateRecord{}))
		})
	}
}

func TestLegacyWalletLedgerFailsClosedWithoutDurableIdentity(t *testing.T) {
	db, task := setupTaskLedgerTest(t)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	info := &relaycommon.RelayInfo{UserId: task.UserId, TokenId: task.PrivateData.TokenId, RequestId: "transient-request-is-not-a-reservation", FinalPreConsumedQuota: 3000}
	for _, delta := range []int{-100, 0, 100} {
		require.ErrorIs(t, PostConsumeQuota(info, delta, 3000, false), ErrWalletBillingIdentityRequired)
	}
	require.ErrorIs(t, SettleBilling(ctx, info, 3000), ErrWalletBillingIdentityRequired)
	require.Equal(t, 10000, getUserQuota(t, task.UserId))
	require.Zero(t, taskLedgerCount(t, db, &model.RebateRecord{}))
}

func TestAsyncSubmitLedgerFailsClosedBeforeDebit(t *testing.T) {
	db, task := setupTaskLedgerTest(t)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	info := &relaycommon.RelayInfo{UserId: task.UserId, TokenId: task.PrivateData.TokenId, TaskRelayInfo: &relaycommon.TaskRelayInfo{PublicTaskID: task.TaskID}}
	require.NotNil(t, PreConsumeBilling(ctx, 3000, info))
	require.Nil(t, info.Billing)
	require.ErrorIs(t, SettleBilling(ctx, info, 3000), ErrWalletBillingIdentityRequired)
	require.Equal(t, 10000, getUserQuota(t, task.UserId))
	require.Equal(t, 10000, getTokenRemainQuota(t, task.PrivateData.TokenId))
	require.Zero(t, taskLedgerCount(t, db, &model.RebateRecord{}))

	// A free async task has no wallet debit and must not be blocked or noisy.
	free := &relaycommon.RelayInfo{UserId: task.UserId, TaskRelayInfo: &relaycommon.TaskRelayInfo{PublicTaskID: task.TaskID}}
	require.NoError(t, SettleBilling(ctx, free, 0))
}

// stubPollingAdaptor implements TaskPollingAdaptor for completion-path tests.
type stubPollingAdaptor struct {
	adjust int
}

func (s stubPollingAdaptor) Init(*relaycommon.RelayInfo) {}

func (s stubPollingAdaptor) FetchTask(string, string, map[string]any, string) (*http.Response, error) {
	return nil, nil
}

func (s stubPollingAdaptor) ParseTaskResult([]byte) (*relaycommon.TaskInfo, error) {
	return nil, nil
}

func (s stubPollingAdaptor) AdjustBillingOnComplete(*model.Task, *relaycommon.TaskInfo) int {
	return s.adjust
}

func TestTaskLedgerPollingCompletionFinalizesOnAllSuccessPaths(t *testing.T) {
	for _, perCall := range []bool{true, false} {
		t.Run(fmt.Sprintf("perCall=%v", perCall), func(t *testing.T) {
			db, task := setupTaskLedgerTest(t)
			require.NoError(t, ReserveTaskWalletQuota(task, 3000))
			markTaskSuccess(t, db, task)
			task.PrivateData.BillingContext.PerCallBilling = perCall

			// Per-call tasks skip adjustments, and tasks without adaptor/token
			// adjustments have no delta; both must still finalize the durable
			// reservation exactly once instead of leaving it for TTL refund.
			settleTaskBillingOnComplete(context.Background(), stubPollingAdaptor{}, task, &relaycommon.TaskInfo{})
			require.Equal(t, 7000, getUserQuota(t, task.UserId))
			require.Equal(t, 7000, getTokenRemainQuota(t, task.PrivateData.TokenId))
			require.EqualValues(t, 1, taskLedgerCount(t, db, &model.WalletConsumptionSettlement{}))
			require.EqualValues(t, 1, taskLedgerCount(t, db, &model.RebateRecord{}))

			key, err := TaskWalletBillingKey(task)
			require.NoError(t, err)
			var reservation model.WalletReservation
			require.NoError(t, db.Where("reservation_key = ?", key).First(&reservation).Error)
			require.Equal(t, model.WalletReservationFinalized, reservation.State)

			// A duplicated completion callback must not debit or reward twice.
			settleTaskBillingOnComplete(context.Background(), stubPollingAdaptor{}, task, &relaycommon.TaskInfo{})
			require.Equal(t, 7000, getUserQuota(t, task.UserId))
			require.Equal(t, 7000, getTokenRemainQuota(t, task.PrivateData.TokenId))
			require.EqualValues(t, 1, taskLedgerCount(t, db, &model.WalletConsumptionSettlement{}))
			require.EqualValues(t, 1, taskLedgerCount(t, db, &model.RebateRecord{}))
		})
	}
}

func TestMain(m *testing.M) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		panic("failed to open test db: " + err.Error())
	}
	sqlDB, err := db.DB()
	if err != nil {
		panic("failed to get sql.DB: " + err.Error())
	}
	sqlDB.SetMaxOpenConns(1)

	model.DB = db
	model.LOG_DB = db

	common.UsingSQLite = true
	common.RedisEnabled = false
	common.BatchUpdateEnabled = false
	common.LogConsumeEnabled = true

	if err := db.AutoMigrate(
		&model.Task{},
		&model.User{},
		&model.Token{},
		&model.Log{},
		&model.Channel{},
		&model.TopUp{},
		&model.UserSubscription{},
		&model.UserGroupRatioOverride{},
		// The production schema always migrates the wallet ledger tables at
		// startup. Legacy tests must see the same schema so an absent baseline
		// marker means "inactive", not "missing table".
		&model.RebateSystemState{},
		&model.WalletCreditLot{},
		&model.WalletConsumptionSettlement{},
		&model.WalletConsumptionAllocation{},
		&model.WalletReservation{},
		&model.WalletLotHold{},
		&model.RebateRecord{},
		&model.RebateCapBucket{},
	); err != nil {
		panic("failed to migrate: " + err.Error())
	}

	os.Exit(m.Run())
}

// ---------------------------------------------------------------------------
// Seed helpers
// ---------------------------------------------------------------------------

func truncate(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		model.DB.Exec("DELETE FROM tasks")
		model.DB.Exec("DELETE FROM users")
		model.DB.Exec("DELETE FROM tokens")
		model.DB.Exec("DELETE FROM logs")
		model.DB.Exec("DELETE FROM channels")
		model.DB.Exec("DELETE FROM top_ups")
		model.DB.Exec("DELETE FROM user_subscriptions")
		model.DB.Exec("DELETE FROM user_group_ratio_overrides")
		model.DB.Exec("DELETE FROM wallet_credit_lots")
		model.DB.Exec("DELETE FROM wallet_consumption_settlements")
		model.DB.Exec("DELETE FROM wallet_consumption_allocations")
		model.DB.Exec("DELETE FROM wallet_reservations")
		model.DB.Exec("DELETE FROM wallet_lot_holds")
		model.DB.Exec("DELETE FROM rebate_records")
		model.DB.Exec("DELETE FROM rebate_cap_buckets")
		model.DB.Exec("DELETE FROM rebate_system_states")
	})
}

func seedUser(t *testing.T, id int, quota int) {
	t.Helper()
	user := &model.User{Id: id, Username: "test_user", Quota: quota, Status: common.UserStatusEnabled}
	require.NoError(t, model.DB.Create(user).Error)
}

func seedToken(t *testing.T, id int, userId int, key string, remainQuota int) {
	t.Helper()
	token := &model.Token{
		Id:          id,
		UserId:      userId,
		Key:         key,
		Name:        "test_token",
		Status:      common.TokenStatusEnabled,
		RemainQuota: remainQuota,
		UsedQuota:   0,
	}
	require.NoError(t, model.DB.Create(token).Error)
}

func seedSubscription(t *testing.T, id int, userId int, amountTotal int64, amountUsed int64) {
	t.Helper()
	sub := &model.UserSubscription{
		Id:          id,
		UserId:      userId,
		AmountTotal: amountTotal,
		AmountUsed:  amountUsed,
		Status:      "active",
		StartTime:   time.Now().Unix(),
		EndTime:     time.Now().Add(30 * 24 * time.Hour).Unix(),
	}
	require.NoError(t, model.DB.Create(sub).Error)
}

func seedChannel(t *testing.T, id int) {
	t.Helper()
	ch := &model.Channel{Id: id, Name: "test_channel", Key: "sk-test", Status: common.ChannelStatusEnabled}
	require.NoError(t, model.DB.Create(ch).Error)
}

func makeTask(userId, channelId, quota, tokenId int, billingSource string, subscriptionId int) *model.Task {
	return &model.Task{
		TaskID:    "task_" + time.Now().Format("150405.000"),
		UserId:    userId,
		ChannelId: channelId,
		Quota:     quota,
		Status:    model.TaskStatus(model.TaskStatusInProgress),
		Group:     "default",
		Data:      json.RawMessage(`{}`),
		CreatedAt: time.Now().Unix(),
		UpdatedAt: time.Now().Unix(),
		Properties: model.Properties{
			OriginModelName: "test-model",
		},
		PrivateData: model.TaskPrivateData{
			BillingSource:  billingSource,
			SubscriptionId: subscriptionId,
			TokenId:        tokenId,
			BillingContext: &model.TaskBillingContext{
				ModelPrice:      0.02,
				GroupRatio:      1.0,
				OriginModelName: "test-model",
			},
		},
	}
}

// ---------------------------------------------------------------------------
// Read-back helpers
// ---------------------------------------------------------------------------

func getUserQuota(t *testing.T, id int) int {
	t.Helper()
	var user model.User
	require.NoError(t, model.DB.Select("quota").Where("id = ?", id).First(&user).Error)
	return user.Quota
}

func getTokenRemainQuota(t *testing.T, id int) int {
	t.Helper()
	var token model.Token
	require.NoError(t, model.DB.Select("remain_quota").Where("id = ?", id).First(&token).Error)
	return token.RemainQuota
}

func getTokenUsedQuota(t *testing.T, id int) int {
	t.Helper()
	var token model.Token
	require.NoError(t, model.DB.Select("used_quota").Where("id = ?", id).First(&token).Error)
	return token.UsedQuota
}

func getSubscriptionUsed(t *testing.T, id int) int64 {
	t.Helper()
	var sub model.UserSubscription
	require.NoError(t, model.DB.Select("amount_used").Where("id = ?", id).First(&sub).Error)
	return sub.AmountUsed
}

func getLastLog(t *testing.T) *model.Log {
	t.Helper()
	var log model.Log
	err := model.LOG_DB.Order("id desc").First(&log).Error
	if err != nil {
		return nil
	}
	return &log
}

func countLogs(t *testing.T) int64 {
	t.Helper()
	var count int64
	model.LOG_DB.Model(&model.Log{}).Count(&count)
	return count
}

// ===========================================================================
// RefundTaskQuota tests
// ===========================================================================

func TestRefundTaskQuota_Wallet(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID, tokenID, channelID = 1, 1, 1
	const initQuota, preConsumed = 10000, 3000
	const tokenRemain = 5000

	seedUser(t, userID, initQuota)
	seedToken(t, tokenID, userID, "sk-test-key", tokenRemain)
	seedChannel(t, channelID)

	task := makeTask(userID, channelID, preConsumed, tokenID, BillingSourceWallet, 0)

	RefundTaskQuota(ctx, task, "task failed: upstream error")

	// User quota should increase by preConsumed
	assert.Equal(t, initQuota+preConsumed, getUserQuota(t, userID))

	// Token remain_quota should increase, used_quota should decrease
	assert.Equal(t, tokenRemain+preConsumed, getTokenRemainQuota(t, tokenID))
	assert.Equal(t, -preConsumed, getTokenUsedQuota(t, tokenID))

	// A refund log should be created
	log := getLastLog(t)
	require.NotNil(t, log)
	assert.Equal(t, model.LogTypeRefund, log.Type)
	assert.Equal(t, preConsumed, log.Quota)
	assert.Equal(t, "test-model", log.ModelName)
}

func TestRefundTaskQuota_Subscription(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID, tokenID, channelID, subID = 2, 2, 2, 1
	const preConsumed = 2000
	const subTotal, subUsed int64 = 100000, 50000
	const tokenRemain = 8000

	seedUser(t, userID, 0)
	seedToken(t, tokenID, userID, "sk-sub-key", tokenRemain)
	seedChannel(t, channelID)
	seedSubscription(t, subID, userID, subTotal, subUsed)

	task := makeTask(userID, channelID, preConsumed, tokenID, BillingSourceSubscription, subID)

	RefundTaskQuota(ctx, task, "subscription task failed")

	// Subscription used should decrease by preConsumed
	assert.Equal(t, subUsed-int64(preConsumed), getSubscriptionUsed(t, subID))

	// Token should also be refunded
	assert.Equal(t, tokenRemain+preConsumed, getTokenRemainQuota(t, tokenID))

	log := getLastLog(t)
	require.NotNil(t, log)
	assert.Equal(t, model.LogTypeRefund, log.Type)
}

func TestRefundTaskQuota_SubscriptionDoesNotCrossPeriod(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID, tokenID, channelID, subID = 20, 20, 20, 20
	const preConsumed = 2000
	const tokenRemain = 8000

	seedUser(t, userID, 0)
	seedToken(t, tokenID, userID, "sk-cross-period", tokenRemain)
	seedChannel(t, channelID)
	sub := &model.UserSubscription{
		Id:            subID,
		UserId:        userID,
		AmountTotal:   100000,
		AmountUsed:    3000,
		Status:        "active",
		StartTime:     50,
		EndTime:       time.Now().Add(30 * 24 * time.Hour).Unix(),
		LastResetTime: 200,
	}
	require.NoError(t, model.DB.Create(sub).Error)

	task := makeTask(userID, channelID, preConsumed, tokenID, BillingSourceSubscription, subID)
	task.SubmitTime = 150
	task.PrivateData.SubscriptionPeriodStart = 100

	RefundTaskQuota(ctx, task, "old-period task failed")

	// The expired subscription period must not refund into the new period.
	assert.Equal(t, int64(3000), getSubscriptionUsed(t, subID))
	// Token quota is lifetime-scoped and still receives its task refund.
	assert.Equal(t, tokenRemain+preConsumed, getTokenRemainQuota(t, tokenID))
	assert.Equal(t, 0, task.Quota)

	log := getLastLog(t)
	require.NotNil(t, log)
	var other map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(log.Other), &other))
	assert.Equal(t, true, other["subscription_period_expired"])
}

func TestRefundTaskQuota_ZeroQuota(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID = 3
	seedUser(t, userID, 5000)

	task := makeTask(userID, 0, 0, 0, BillingSourceWallet, 0)

	RefundTaskQuota(ctx, task, "zero quota task")

	// No change to user quota
	assert.Equal(t, 5000, getUserQuota(t, userID))

	// No log created
	assert.Equal(t, int64(0), countLogs(t))
}

func TestRefundTaskQuota_NoToken(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID, channelID = 4, 4
	const initQuota, preConsumed = 10000, 1500

	seedUser(t, userID, initQuota)
	seedChannel(t, channelID)

	task := makeTask(userID, channelID, preConsumed, 0, BillingSourceWallet, 0) // TokenId=0

	RefundTaskQuota(ctx, task, "no token task failed")

	// User quota refunded
	assert.Equal(t, initQuota+preConsumed, getUserQuota(t, userID))

	// Log created
	log := getLastLog(t)
	require.NotNil(t, log)
	assert.Equal(t, model.LogTypeRefund, log.Type)
}

// ===========================================================================
// RecalculateTaskQuota tests
// ===========================================================================

func TestRecalculate_PositiveDelta(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID, tokenID, channelID = 10, 10, 10
	const initQuota, preConsumed = 10000, 2000
	const actualQuota = 3000 // under-charged by 1000
	const tokenRemain = 5000

	seedUser(t, userID, initQuota)
	seedToken(t, tokenID, userID, "sk-recalc-pos", tokenRemain)
	seedChannel(t, channelID)

	task := makeTask(userID, channelID, preConsumed, tokenID, BillingSourceWallet, 0)

	RecalculateTaskQuota(ctx, task, actualQuota, "adaptor adjustment")

	// User quota should decrease by the delta (1000 additional charge)
	assert.Equal(t, initQuota-(actualQuota-preConsumed), getUserQuota(t, userID))

	// Token should also be charged the delta
	assert.Equal(t, tokenRemain-(actualQuota-preConsumed), getTokenRemainQuota(t, tokenID))

	// task.Quota should be updated to actualQuota
	assert.Equal(t, actualQuota, task.Quota)

	// Log type should be Consume (additional charge)
	log := getLastLog(t)
	require.NotNil(t, log)
	assert.Equal(t, model.LogTypeConsume, log.Type)
	assert.Equal(t, actualQuota-preConsumed, log.Quota)
}

func TestRecalculate_NegativeDelta(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID, tokenID, channelID = 11, 11, 11
	const initQuota, preConsumed = 10000, 5000
	const actualQuota = 3000 // over-charged by 2000
	const tokenRemain = 5000

	seedUser(t, userID, initQuota)
	seedToken(t, tokenID, userID, "sk-recalc-neg", tokenRemain)
	seedChannel(t, channelID)

	task := makeTask(userID, channelID, preConsumed, tokenID, BillingSourceWallet, 0)

	RecalculateTaskQuota(ctx, task, actualQuota, "adaptor adjustment")

	// User quota should increase by abs(delta) = 2000 (refund overpayment)
	assert.Equal(t, initQuota+(preConsumed-actualQuota), getUserQuota(t, userID))

	// Token should be refunded the difference
	assert.Equal(t, tokenRemain+(preConsumed-actualQuota), getTokenRemainQuota(t, tokenID))

	// task.Quota updated
	assert.Equal(t, actualQuota, task.Quota)

	// Log type should be Refund
	log := getLastLog(t)
	require.NotNil(t, log)
	assert.Equal(t, model.LogTypeRefund, log.Type)
	assert.Equal(t, preConsumed-actualQuota, log.Quota)
}

func TestRecalculate_ZeroDelta(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID = 12
	const initQuota, preConsumed = 10000, 3000

	seedUser(t, userID, initQuota)

	task := makeTask(userID, 0, preConsumed, 0, BillingSourceWallet, 0)

	RecalculateTaskQuota(ctx, task, preConsumed, "exact match")

	// No change to user quota
	assert.Equal(t, initQuota, getUserQuota(t, userID))

	// No log created (delta is zero)
	assert.Equal(t, int64(0), countLogs(t))
}

func TestRecalculate_ActualQuotaZero(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID = 13
	const initQuota = 10000

	seedUser(t, userID, initQuota)

	task := makeTask(userID, 0, 5000, 0, BillingSourceWallet, 0)

	RecalculateTaskQuota(ctx, task, 0, "zero actual")

	// No change (early return)
	assert.Equal(t, initQuota, getUserQuota(t, userID))
	assert.Equal(t, int64(0), countLogs(t))
}

func TestRecalculate_Subscription_NegativeDelta(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID, tokenID, channelID, subID = 14, 14, 14, 2
	const preConsumed = 5000
	const actualQuota = 2000 // over-charged by 3000
	const subTotal, subUsed int64 = 100000, 50000
	const tokenRemain = 8000

	seedUser(t, userID, 0)
	seedToken(t, tokenID, userID, "sk-sub-recalc", tokenRemain)
	seedChannel(t, channelID)
	seedSubscription(t, subID, userID, subTotal, subUsed)

	task := makeTask(userID, channelID, preConsumed, tokenID, BillingSourceSubscription, subID)

	RecalculateTaskQuota(ctx, task, actualQuota, "subscription over-charge")

	// Subscription used should decrease by delta (refund 3000)
	assert.Equal(t, subUsed-int64(preConsumed-actualQuota), getSubscriptionUsed(t, subID))

	// Token refunded
	assert.Equal(t, tokenRemain+(preConsumed-actualQuota), getTokenRemainQuota(t, tokenID))

	assert.Equal(t, actualQuota, task.Quota)

	log := getLastLog(t)
	require.NotNil(t, log)
	assert.Equal(t, model.LogTypeRefund, log.Type)
}

// ===========================================================================
// CAS + Billing integration tests
// Simulates the flow in updateVideoSingleTask (service/task_polling.go)
// ===========================================================================

// simulatePollBilling reproduces the CAS + billing logic from updateVideoSingleTask.
// It takes a persisted task (already in DB), applies the new status, and performs
// the conditional update + billing exactly as the polling loop does.
func simulatePollBilling(ctx context.Context, task *model.Task, newStatus model.TaskStatus, actualQuota int) {
	snap := task.Snapshot()

	shouldRefund := false
	shouldSettle := false
	quota := task.Quota

	task.Status = newStatus
	switch string(newStatus) {
	case model.TaskStatusSuccess:
		task.Progress = "100%"
		task.FinishTime = 9999
		shouldSettle = true
	case model.TaskStatusFailure:
		task.Progress = "100%"
		task.FinishTime = 9999
		task.FailReason = "upstream error"
		if quota != 0 {
			shouldRefund = true
		}
	default:
		task.Progress = "50%"
	}

	isDone := task.Status == model.TaskStatus(model.TaskStatusSuccess) || task.Status == model.TaskStatus(model.TaskStatusFailure)
	if isDone && snap.Status != task.Status {
		won, err := task.UpdateWithStatus(snap.Status)
		if err != nil {
			shouldRefund = false
			shouldSettle = false
		} else if !won {
			shouldRefund = false
			shouldSettle = false
		}
	} else if !snap.Equal(task.Snapshot()) {
		_, _ = task.UpdateWithStatus(snap.Status)
	}

	if shouldSettle && actualQuota > 0 {
		RecalculateTaskQuota(ctx, task, actualQuota, "test settle")
	}
	if shouldRefund {
		RefundTaskQuota(ctx, task, task.FailReason)
	}
}

func TestCASGuardedRefund_Win(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID, tokenID, channelID = 20, 20, 20
	const initQuota, preConsumed = 10000, 4000
	const tokenRemain = 6000

	seedUser(t, userID, initQuota)
	seedToken(t, tokenID, userID, "sk-cas-refund-win", tokenRemain)
	seedChannel(t, channelID)

	task := makeTask(userID, channelID, preConsumed, tokenID, BillingSourceWallet, 0)
	task.Status = model.TaskStatus(model.TaskStatusInProgress)
	require.NoError(t, model.DB.Create(task).Error)

	simulatePollBilling(ctx, task, model.TaskStatus(model.TaskStatusFailure), 0)

	// CAS wins: task in DB should now be FAILURE
	var reloaded model.Task
	require.NoError(t, model.DB.First(&reloaded, task.ID).Error)
	assert.EqualValues(t, model.TaskStatusFailure, reloaded.Status)

	// Refund should have happened
	assert.Equal(t, initQuota+preConsumed, getUserQuota(t, userID))
	assert.Equal(t, tokenRemain+preConsumed, getTokenRemainQuota(t, tokenID))

	log := getLastLog(t)
	require.NotNil(t, log)
	assert.Equal(t, model.LogTypeRefund, log.Type)
}

func TestCASGuardedRefund_Lose(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID, tokenID, channelID = 21, 21, 21
	const initQuota, preConsumed = 10000, 4000
	const tokenRemain = 6000

	seedUser(t, userID, initQuota)
	seedToken(t, tokenID, userID, "sk-cas-refund-lose", tokenRemain)
	seedChannel(t, channelID)

	// Create task with IN_PROGRESS in DB
	task := makeTask(userID, channelID, preConsumed, tokenID, BillingSourceWallet, 0)
	task.Status = model.TaskStatus(model.TaskStatusInProgress)
	require.NoError(t, model.DB.Create(task).Error)

	// Simulate another process already transitioning to FAILURE
	model.DB.Model(&model.Task{}).Where("id = ?", task.ID).Update("status", model.TaskStatusFailure)

	// Our process still has the old in-memory state (IN_PROGRESS) and tries to transition
	// task.Status is still IN_PROGRESS in the snapshot
	simulatePollBilling(ctx, task, model.TaskStatus(model.TaskStatusFailure), 0)

	// CAS lost: user quota should NOT change (no double refund)
	assert.Equal(t, initQuota, getUserQuota(t, userID))
	assert.Equal(t, tokenRemain, getTokenRemainQuota(t, tokenID))

	// No billing log should be created
	assert.Equal(t, int64(0), countLogs(t))
}

func TestCASGuardedSettle_Win(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID, tokenID, channelID = 22, 22, 22
	const initQuota, preConsumed = 10000, 5000
	const actualQuota = 3000 // over-charged, should get partial refund
	const tokenRemain = 8000

	seedUser(t, userID, initQuota)
	seedToken(t, tokenID, userID, "sk-cas-settle-win", tokenRemain)
	seedChannel(t, channelID)

	task := makeTask(userID, channelID, preConsumed, tokenID, BillingSourceWallet, 0)
	task.Status = model.TaskStatus(model.TaskStatusInProgress)
	require.NoError(t, model.DB.Create(task).Error)

	simulatePollBilling(ctx, task, model.TaskStatus(model.TaskStatusSuccess), actualQuota)

	// CAS wins: task should be SUCCESS
	var reloaded model.Task
	require.NoError(t, model.DB.First(&reloaded, task.ID).Error)
	assert.EqualValues(t, model.TaskStatusSuccess, reloaded.Status)

	// Settlement should refund the over-charge (5000 - 3000 = 2000 back to user)
	assert.Equal(t, initQuota+(preConsumed-actualQuota), getUserQuota(t, userID))
	assert.Equal(t, tokenRemain+(preConsumed-actualQuota), getTokenRemainQuota(t, tokenID))

	// task.Quota should be updated to actualQuota
	assert.Equal(t, actualQuota, task.Quota)
}

func TestNonTerminalUpdate_NoBilling(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID, channelID = 23, 23
	const initQuota, preConsumed = 10000, 3000

	seedUser(t, userID, initQuota)
	seedChannel(t, channelID)

	task := makeTask(userID, channelID, preConsumed, 0, BillingSourceWallet, 0)
	task.Status = model.TaskStatus(model.TaskStatusInProgress)
	task.Progress = "20%"
	require.NoError(t, model.DB.Create(task).Error)

	// Simulate a non-terminal poll update (still IN_PROGRESS, progress changed)
	simulatePollBilling(ctx, task, model.TaskStatus(model.TaskStatusInProgress), 0)

	// User quota should NOT change
	assert.Equal(t, initQuota, getUserQuota(t, userID))

	// No billing log
	assert.Equal(t, int64(0), countLogs(t))

	// Task progress should be updated in DB
	var reloaded model.Task
	require.NoError(t, model.DB.First(&reloaded, task.ID).Error)
	assert.Equal(t, "50%", reloaded.Progress)
}

// ===========================================================================
// Mock adaptor for settleTaskBillingOnComplete tests
// ===========================================================================

type mockAdaptor struct {
	adjustReturn int
}

func (m *mockAdaptor) Init(_ *relaycommon.RelayInfo) {}
func (m *mockAdaptor) FetchTask(string, string, map[string]any, string) (*http.Response, error) {
	return nil, nil
}
func (m *mockAdaptor) ParseTaskResult([]byte) (*relaycommon.TaskInfo, error) { return nil, nil }
func (m *mockAdaptor) AdjustBillingOnComplete(_ *model.Task, _ *relaycommon.TaskInfo) int {
	return m.adjustReturn
}

// ===========================================================================
// PerCallBilling tests — settleTaskBillingOnComplete
// ===========================================================================

func TestSettle_PerCallBilling_SkipsAdaptorAdjust(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID, tokenID, channelID = 30, 30, 30
	const initQuota, preConsumed = 10000, 5000
	const tokenRemain = 8000

	seedUser(t, userID, initQuota)
	seedToken(t, tokenID, userID, "sk-percall-adaptor", tokenRemain)
	seedChannel(t, channelID)

	task := makeTask(userID, channelID, preConsumed, tokenID, BillingSourceWallet, 0)
	task.PrivateData.BillingContext.PerCallBilling = true

	adaptor := &mockAdaptor{adjustReturn: 2000}
	taskResult := &relaycommon.TaskInfo{Status: model.TaskStatusSuccess}

	settleTaskBillingOnComplete(ctx, adaptor, task, taskResult)

	// Per-call: no adjustment despite adaptor returning 2000
	assert.Equal(t, initQuota, getUserQuota(t, userID))
	assert.Equal(t, tokenRemain, getTokenRemainQuota(t, tokenID))
	assert.Equal(t, preConsumed, task.Quota)
	assert.Equal(t, int64(0), countLogs(t))
}

func TestSettle_PerCallBilling_SkipsTotalTokens(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID, tokenID, channelID = 31, 31, 31
	const initQuota, preConsumed = 10000, 4000
	const tokenRemain = 7000

	seedUser(t, userID, initQuota)
	seedToken(t, tokenID, userID, "sk-percall-tokens", tokenRemain)
	seedChannel(t, channelID)

	task := makeTask(userID, channelID, preConsumed, tokenID, BillingSourceWallet, 0)
	task.PrivateData.BillingContext.PerCallBilling = true

	adaptor := &mockAdaptor{adjustReturn: 0}
	taskResult := &relaycommon.TaskInfo{Status: model.TaskStatusSuccess, TotalTokens: 9999}

	settleTaskBillingOnComplete(ctx, adaptor, task, taskResult)

	// Per-call: no recalculation by tokens
	assert.Equal(t, initQuota, getUserQuota(t, userID))
	assert.Equal(t, tokenRemain, getTokenRemainQuota(t, tokenID))
	assert.Equal(t, preConsumed, task.Quota)
	assert.Equal(t, int64(0), countLogs(t))
}

func TestSettle_NonPerCall_AdaptorAdjustWorks(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID, tokenID, channelID = 32, 32, 32
	const initQuota, preConsumed = 10000, 5000
	const adaptorQuota = 3000
	const tokenRemain = 8000

	seedUser(t, userID, initQuota)
	seedToken(t, tokenID, userID, "sk-nonpercall-adj", tokenRemain)
	seedChannel(t, channelID)

	task := makeTask(userID, channelID, preConsumed, tokenID, BillingSourceWallet, 0)
	// PerCallBilling defaults to false

	adaptor := &mockAdaptor{adjustReturn: adaptorQuota}
	taskResult := &relaycommon.TaskInfo{Status: model.TaskStatusSuccess}

	settleTaskBillingOnComplete(ctx, adaptor, task, taskResult)

	// Non-per-call: adaptor adjustment applies (refund 2000)
	assert.Equal(t, initQuota+(preConsumed-adaptorQuota), getUserQuota(t, userID))
	assert.Equal(t, tokenRemain+(preConsumed-adaptorQuota), getTokenRemainQuota(t, tokenID))
	assert.Equal(t, adaptorQuota, task.Quota)

	log := getLastLog(t)
	require.NotNil(t, log)
	assert.Equal(t, model.LogTypeRefund, log.Type)
}
