package relay

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// setupMJPendingLedgerTest owns an isolated wallet-ledger database. When
// ledgerActive is false the baseline is missing, mirroring a pre-ledger
// deployment; otherwise a paid wallet lot exists.
func setupMJPendingLedgerTest(t *testing.T, ledgerActive bool, tokenQuota int) (*gorm.DB, *model.User, *model.Token) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	originalDB := model.DB
	originalLogDB := model.LOG_DB
	originalUsingSQLite := common.UsingSQLite
	originalRedisEnabled := common.RedisEnabled
	originalUnit := common.QuotaPerUnit
	originalBatch := common.BatchUpdateEnabled
	common.UsingSQLite = true
	common.RedisEnabled = false
	common.QuotaPerUnit = 500000
	common.BatchUpdateEnabled = false

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	model.DB = db
	model.LOG_DB = db
	require.NoError(t, db.AutoMigrate(
		&model.User{},
		&model.Token{},
		&model.Log{},
		&model.RebateSystemState{},
		&model.WalletCreditLot{},
		&model.WalletConsumptionSettlement{},
		&model.WalletConsumptionAllocation{},
		&model.WalletReservation{},
		&model.WalletLotHold{},
		&model.WalletDebtRepayment{},
		&model.RebateRecord{},
		&model.RebateCapBucket{},
		&model.UserSubscription{},
	))
	t.Cleanup(func() {
		model.DB = originalDB
		model.LOG_DB = originalLogDB
		common.UsingSQLite = originalUsingSQLite
		common.RedisEnabled = originalRedisEnabled
		common.QuotaPerUnit = originalUnit
		common.BatchUpdateEnabled = originalBatch
		if cleanupDB, dbErr := db.DB(); dbErr == nil {
			_ = cleanupDB.Close()
		}
	})

	if ledgerActive {
		require.NoError(t, db.Create(&model.RebateSystemState{
			Key:       "wallet_credit_baseline_v1",
			Value:     "initialized:mj-submit-test",
			UpdatedAt: time.Now().UTC(),
		}).Error)
	}
	user := model.User{Username: "mj-submit-user", AffCode: "mj-submit-aff", Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&user).Error)
	if ledgerActive {
		credited, err := model.CreditTopUpTx(db, user.Id, "mj-submit-topup", 10000)
		require.NoError(t, err)
		require.True(t, credited)
	} else {
		require.NoError(t, db.Model(&model.User{}).Where("id = ?", user.Id).Update("quota", 10000).Error)
	}
	token := model.Token{UserId: user.Id, Key: "mj-submit-token", RemainQuota: tokenQuota, Status: common.TokenStatusEnabled}
	require.NoError(t, db.Create(&token).Error)
	return db, &user, &token
}

func mjSubmitUserQuota(t *testing.T, db *gorm.DB, userID int) int {
	t.Helper()
	var user model.User
	require.NoError(t, db.Select("quota").First(&user, userID).Error)
	return user.Quota
}

func mjSubmitTokenRemain(t *testing.T, db *gorm.DB, tokenID int) int {
	t.Helper()
	var token model.Token
	require.NoError(t, db.Select("remain_quota").First(&token, tokenID).Error)
	return token.RemainQuota
}

func TestPrepareMidjourneyWalletSubmitLedgerInactive(t *testing.T) {
	db, user, token := setupMJPendingLedgerTest(t, false, 10000)
	info := &relaycommon.RelayInfo{
		UserId:        user.Id,
		TokenId:       token.Id,
		TokenKey:      token.Key,
		TaskRelayInfo: &relaycommon.TaskRelayInfo{},
	}
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())

	key, ledger, err := prepareMidjourneyWalletSubmit(ctx, info, 1000)
	require.NoError(t, err)
	require.False(t, ledger)
	require.Empty(t, key)

	var count int64
	require.NoError(t, db.Model(&model.WalletReservation{}).Count(&count).Error)
	require.Zero(t, count)
	require.Equal(t, 10000, mjSubmitUserQuota(t, db, user.Id))
	require.Equal(t, 10000, mjSubmitTokenRemain(t, db, token.Id))
}

func TestPrepareMidjourneyWalletSubmitSubscriptionFailsClosed(t *testing.T) {
	db, user, token := setupMJPendingLedgerTest(t, true, 10000)
	info := &relaycommon.RelayInfo{
		UserId:        user.Id,
		TokenId:       token.Id,
		TokenKey:      token.Key,
		TaskRelayInfo: &relaycommon.TaskRelayInfo{},
		UserSetting:   dto.UserSetting{BillingPreference: "subscription_only"},
	}
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())

	key, ledger, err := prepareMidjourneyWalletSubmit(ctx, info, 1000)
	require.NoError(t, err)
	require.False(t, ledger)
	require.Empty(t, key)

	// Midjourney subscription billing stays fail-closed through the existing
	// PreConsumeBilling wallet-ledger guard; no wallet reservation may exist.
	guardErr := service.PreConsumeBilling(ctx, 1000, info)
	require.NotNil(t, guardErr)
	require.ErrorIs(t, guardErr.Err, service.ErrWalletBillingIdentityRequired)
	require.Nil(t, info.Billing)

	var count int64
	require.NoError(t, db.Model(&model.WalletReservation{}).Count(&count).Error)
	require.Zero(t, count)
	require.Equal(t, 10000, mjSubmitUserQuota(t, db, user.Id))
}

func TestPrepareMidjourneyWalletSubmitWalletReserves(t *testing.T) {
	db, user, token := setupMJPendingLedgerTest(t, true, 10000)
	info := &relaycommon.RelayInfo{
		UserId:        user.Id,
		TokenId:       token.Id,
		TokenKey:      token.Key,
		TaskRelayInfo: &relaycommon.TaskRelayInfo{},
	}
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())

	key, ledger, err := prepareMidjourneyWalletSubmit(ctx, info, 1000)
	require.NoError(t, err)
	require.True(t, ledger)
	require.True(t, model.IsCanonicalWalletBillingKey(key))

	var reservation model.WalletReservation
	require.NoError(t, db.Where("reservation_key = ? AND user_id = ?", key, user.Id).First(&reservation).Error)
	require.Equal(t, model.WalletReservationReserved, reservation.State)
	require.EqualValues(t, 1000, reservation.ReservedQuota)
	require.Equal(t, 9000, mjSubmitUserQuota(t, db, user.Id))
	require.Equal(t, 9000, mjSubmitTokenRemain(t, db, token.Id))
}

func TestPrepareMidjourneyWalletSubmitTokenFailureRefundsReservation(t *testing.T) {
	db, user, token := setupMJPendingLedgerTest(t, true, 100)
	info := &relaycommon.RelayInfo{
		UserId:        user.Id,
		TokenId:       token.Id,
		TokenKey:      token.Key,
		TaskRelayInfo: &relaycommon.TaskRelayInfo{},
	}
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())

	key, ledger, err := prepareMidjourneyWalletSubmit(ctx, info, 1000)
	require.Error(t, err)
	require.False(t, ledger)
	require.Empty(t, key)

	// The wallet reservation made before the token check must be refunded.
	var reservation model.WalletReservation
	require.NoError(t, db.Where("user_id = ?", user.Id).First(&reservation).Error)
	require.Equal(t, model.WalletReservationRefunded, reservation.State)
	require.Equal(t, 10000, mjSubmitUserQuota(t, db, user.Id))
	require.Equal(t, 100, mjSubmitTokenRemain(t, db, token.Id))
}

func TestRefundMidjourneyWalletSubmitRestoresWalletAndToken(t *testing.T) {
	db, user, token := setupMJPendingLedgerTest(t, true, 10000)
	info := &relaycommon.RelayInfo{
		UserId:        user.Id,
		TokenId:       token.Id,
		TokenKey:      token.Key,
		TaskRelayInfo: &relaycommon.TaskRelayInfo{},
	}
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())

	key, ledger, err := prepareMidjourneyWalletSubmit(ctx, info, 1000)
	require.NoError(t, err)
	require.True(t, ledger)
	require.Equal(t, 9000, mjSubmitUserQuota(t, db, user.Id))
	require.Equal(t, 9000, mjSubmitTokenRemain(t, db, token.Id))

	refundMidjourneyWalletSubmit(info, key, 1000)
	require.Equal(t, 10000, mjSubmitUserQuota(t, db, user.Id))
	require.Equal(t, 10000, mjSubmitTokenRemain(t, db, token.Id))
	var reservation model.WalletReservation
	require.NoError(t, db.Where("reservation_key = ?", key).First(&reservation).Error)
	require.Equal(t, model.WalletReservationRefunded, reservation.State)
}
