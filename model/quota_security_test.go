package model

import (
	"errors"
	"math"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type legacyUserSubscriptionForQuotaMigration struct {
	Id            int `gorm:"primaryKey"`
	UserId        int
	StartTime     int64
	LastResetTime int64
}

func (legacyUserSubscriptionForQuotaMigration) TableName() string {
	return "user_subscriptions"
}

func TestUserSubscriptionQuotaPeriodMigrationIsAdditive(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:quota-period-migration?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, db.AutoMigrate(&legacyUserSubscriptionForQuotaMigration{}))
	require.NoError(t, db.Create(&legacyUserSubscriptionForQuotaMigration{
		Id:            1,
		UserId:        1,
		StartTime:     50,
		LastResetTime: 100,
	}).Error)

	require.NoError(t, db.AutoMigrate(&UserSubscription{}))
	var migrated UserSubscription
	require.NoError(t, db.First(&migrated, 1).Error)
	require.Equal(t, int64(0), migrated.QuotaPeriodStart)
	require.Equal(t, int64(100), currentSubscriptionQuotaPeriodStart(&migrated))
}

func TestReserveUserQuotaConcurrentNeverOverdraws(t *testing.T) {
	truncateTables(t)
	user := &User{Username: "quota-reserve-user", Quota: 100, Status: 1, Role: 1}
	require.NoError(t, DB.Create(user).Error)

	var successes atomic.Int32
	var insufficient atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := ReserveUserQuota(user.Id, 100)
			switch {
			case err == nil:
				successes.Add(1)
			case errors.Is(err, ErrInsufficientQuota):
				insufficient.Add(1)
			default:
				t.Errorf("unexpected reserve error: %v", err)
			}
		}()
	}
	wg.Wait()

	require.Equal(t, int32(1), successes.Load())
	require.Equal(t, int32(1), insufficient.Load())
	var saved User
	require.NoError(t, DB.First(&saved, user.Id).Error)
	require.Equal(t, 0, saved.Quota)
}

func TestReserveTokenQuotaConcurrentNeverOverdraws(t *testing.T) {
	truncateTables(t)
	token := &Token{
		UserId:      1,
		Key:         "quota-reserve-token",
		Name:        "quota-reserve-token",
		Status:      1,
		RemainQuota: 100,
	}
	require.NoError(t, DB.Create(token).Error)

	var successes atomic.Int32
	var insufficient atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := ReserveTokenQuota(token.Id, token.Key, 100)
			switch {
			case err == nil:
				successes.Add(1)
			case errors.Is(err, ErrInsufficientQuota):
				insufficient.Add(1)
			default:
				t.Errorf("unexpected reserve error: %v", err)
			}
		}()
	}
	wg.Wait()

	require.Equal(t, int32(1), successes.Load())
	require.Equal(t, int32(1), insufficient.Load())
	var saved Token
	require.NoError(t, DB.First(&saved, token.Id).Error)
	require.Equal(t, 0, saved.RemainQuota)
	require.Equal(t, 100, saved.UsedQuota)
}

func TestDirectSettlementDebitsRecordDebtAndBlockNewReservations(t *testing.T) {
	truncateTables(t)
	user := &User{Username: "quota-debt-user", Quota: 50, Status: 1, Role: 1}
	require.NoError(t, DB.Create(user).Error)
	token := &Token{
		UserId:      user.Id,
		Key:         "quota-debt-token",
		Name:        "quota-debt-token",
		Status:      1,
		RemainQuota: 50,
	}
	require.NoError(t, DB.Create(token).Error)

	require.NoError(t, DecreaseUserQuotaDirect(user.Id, 100))
	require.NoError(t, DecreaseTokenQuotaDirect(token.Id, token.Key, 100))
	require.ErrorIs(t, ReserveUserQuota(user.Id, 1), ErrInsufficientQuota)
	require.ErrorIs(t, ReserveTokenQuota(token.Id, token.Key, 1), ErrInsufficientQuota)

	var savedUser User
	require.NoError(t, DB.First(&savedUser, user.Id).Error)
	require.Equal(t, -50, savedUser.Quota)
	var savedToken Token
	require.NoError(t, DB.First(&savedToken, token.Id).Error)
	require.Equal(t, -50, savedToken.RemainQuota)
	require.Equal(t, 100, savedToken.UsedQuota)
}

func TestPreConsumeUserSubscriptionRequestIdIsIdempotent(t *testing.T) {
	truncateTables(t)
	now := GetDBTimestamp()
	plan := &SubscriptionPlan{
		Title:            "quota-security-plan",
		DurationUnit:     SubscriptionDurationMonth,
		DurationValue:    1,
		Enabled:          true,
		TotalAmount:      100,
		QuotaResetPeriod: SubscriptionResetNever,
	}
	require.NoError(t, DB.Create(plan).Error)
	sub := &UserSubscription{
		UserId:      1,
		PlanId:      plan.Id,
		AmountTotal: 100,
		AmountUsed:  0,
		Status:      "active",
		StartTime:   now - 10,
		EndTime:     now + 3600,
	}
	require.NoError(t, DB.Create(sub).Error)

	first, err := PreConsumeUserSubscription("idempotent-request", 1, "model", 0, 60)
	require.NoError(t, err)
	require.Equal(t, int64(60), first.PreConsumed)
	require.NotZero(t, first.PeriodStart)
	var firstRecord SubscriptionPreConsumeRecord
	require.NoError(t, DB.Where("request_id = ?", "idempotent-request").First(&firstRecord).Error)
	require.Equal(t, first.PeriodStart, firstRecord.PeriodStart)

	// A retry keeps the original reservation even if the caller supplies a
	// different amount; it must not debit the subscription twice.
	second, err := PreConsumeUserSubscription("idempotent-request", 1, "model", 0, 90)
	require.NoError(t, err)
	require.Equal(t, first.UserSubscriptionId, second.UserSubscriptionId)
	require.Equal(t, int64(60), second.PreConsumed)

	_, err = PreConsumeUserSubscription("idempotent-request", 2, "model", 0, 60)
	require.ErrorContains(t, err, "belongs to another user")

	var saved UserSubscription
	require.NoError(t, DB.First(&saved, sub.Id).Error)
	require.Equal(t, int64(60), saved.AmountUsed)
}

func TestSettleUserSubscriptionDeltaRecordsOverage(t *testing.T) {
	truncateTables(t)
	sub := &UserSubscription{
		UserId:        1,
		PlanId:        1,
		AmountTotal:   100,
		AmountUsed:    80,
		Status:        "active",
		StartTime:     50,
		EndTime:       1000,
		LastResetTime: 100,
	}
	require.NoError(t, DB.Create(sub).Error)

	applied, err := SettleUserSubscriptionDelta(sub.Id, 50, 100, 120)
	require.NoError(t, err)
	require.True(t, applied)

	var saved UserSubscription
	require.NoError(t, DB.First(&saved, sub.Id).Error)
	require.Equal(t, int64(130), saved.AmountUsed)
}

func TestSettleUserSubscriptionDeltaRejectsOverflow(t *testing.T) {
	truncateTables(t)
	sub := &UserSubscription{
		UserId:           1,
		PlanId:           1,
		AmountTotal:      0,
		AmountUsed:       math.MaxInt64,
		Status:           "active",
		StartTime:        50,
		EndTime:          1000,
		QuotaPeriodStart: 100,
	}
	require.NoError(t, DB.Create(sub).Error)

	applied, err := SettleUserSubscriptionDelta(sub.Id, 1, 100, 120)
	require.False(t, applied)
	require.ErrorContains(t, err, "overflow")

	var saved UserSubscription
	require.NoError(t, DB.First(&saved, sub.Id).Error)
	require.Equal(t, int64(math.MaxInt64), saved.AmountUsed)
}

func TestRefundSubscriptionPreConsumeDoesNotCrossPeriods(t *testing.T) {
	truncateTables(t)
	sub := &UserSubscription{
		UserId:        1,
		PlanId:        1,
		AmountTotal:   100,
		AmountUsed:    30,
		Status:        "active",
		StartTime:     50,
		EndTime:       1000,
		LastResetTime: 200,
	}
	require.NoError(t, DB.Create(sub).Error)
	record := &SubscriptionPreConsumeRecord{
		RequestId:          "old-period-request",
		UserId:             1,
		UserSubscriptionId: sub.Id,
		PreConsumed:        50,
		PeriodStart:        100,
		Status:             "consumed",
		CreatedAt:          150,
		UpdatedAt:          150,
	}
	require.NoError(t, DB.Create(record).Error)

	require.NoError(t, RefundSubscriptionPreConsume(record.RequestId))

	var savedSub UserSubscription
	require.NoError(t, DB.First(&savedSub, sub.Id).Error)
	require.Equal(t, int64(30), savedSub.AmountUsed)
	var savedRecord SubscriptionPreConsumeRecord
	require.NoError(t, DB.First(&savedRecord, record.Id).Error)
	require.Equal(t, "period_expired", savedRecord.Status)

	// Idempotent retry must remain a no-op.
	require.NoError(t, RefundSubscriptionPreConsume(record.RequestId))
	require.NoError(t, DB.First(&savedSub, sub.Id).Error)
	require.Equal(t, int64(30), savedSub.AmountUsed)
}

func TestRefundSubscriptionPreConsumeCurrentPeriod(t *testing.T) {
	truncateTables(t)
	sub := &UserSubscription{
		UserId:        1,
		PlanId:        1,
		AmountTotal:   100,
		AmountUsed:    80,
		Status:        "active",
		StartTime:     50,
		EndTime:       1000,
		LastResetTime: 200,
	}
	require.NoError(t, DB.Create(sub).Error)
	record := &SubscriptionPreConsumeRecord{
		RequestId:          "current-period-request",
		UserId:             1,
		UserSubscriptionId: sub.Id,
		PreConsumed:        50,
		PeriodStart:        200,
		Status:             "consumed",
		CreatedAt:          220,
		UpdatedAt:          220,
	}
	require.NoError(t, DB.Create(record).Error)

	require.NoError(t, RefundSubscriptionPreConsume(record.RequestId))

	var savedSub UserSubscription
	require.NoError(t, DB.First(&savedSub, sub.Id).Error)
	require.Equal(t, int64(30), savedSub.AmountUsed)
	var savedRecord SubscriptionPreConsumeRecord
	require.NoError(t, DB.First(&savedRecord, record.Id).Error)
	require.Equal(t, "refunded", savedRecord.Status)
}

func TestScheduledSubscriptionResetAdvancesQuotaPeriod(t *testing.T) {
	truncateTables(t)
	plan := &SubscriptionPlan{
		QuotaResetPeriod:        SubscriptionResetCustom,
		QuotaResetCustomSeconds: 100,
	}
	sub := &UserSubscription{
		UserId:           1,
		PlanId:           1,
		AmountTotal:      100,
		AmountUsed:       50,
		Status:           "active",
		StartTime:        100,
		EndTime:          1000,
		LastResetTime:    100,
		NextResetTime:    200,
		QuotaPeriodStart: 100,
	}
	require.NoError(t, DB.Create(sub).Error)

	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		var locked UserSubscription
		if err := lockForUpdate(tx).First(&locked, sub.Id).Error; err != nil {
			return err
		}
		return maybeResetUserSubscriptionWithPlanTx(tx, &locked, plan, 250)
	}))

	var saved UserSubscription
	require.NoError(t, DB.First(&saved, sub.Id).Error)
	require.Equal(t, int64(0), saved.AmountUsed)
	require.Equal(t, int64(200), saved.LastResetTime)
	require.Equal(t, int64(300), saved.NextResetTime)
	require.Equal(t, int64(200), saved.QuotaPeriodStart)
}

func TestManualSubscriptionResetAdvancesQuotaPeriodWithoutChangingSchedule(t *testing.T) {
	truncateTables(t)
	plan := &SubscriptionPlan{QuotaResetPeriod: SubscriptionResetMonthly}
	sub := &UserSubscription{
		UserId:           1,
		PlanId:           1,
		AmountTotal:      100,
		AmountUsed:       50,
		Status:           "active",
		StartTime:        50,
		EndTime:          1000,
		LastResetTime:    100,
		NextResetTime:    300,
		QuotaPeriodStart: 100,
	}
	require.NoError(t, DB.Create(sub).Error)
	record := &SubscriptionPreConsumeRecord{
		RequestId:          "manual-reset-old-period",
		UserId:             1,
		UserSubscriptionId: sub.Id,
		PreConsumed:        20,
		PeriodStart:        100,
		Status:             "consumed",
	}
	require.NoError(t, DB.Create(record).Error)

	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		var locked UserSubscription
		if err := lockForUpdate(tx).First(&locked, sub.Id).Error; err != nil {
			return err
		}
		return resetUserSubscriptionTx(tx, &locked, plan, 200, false)
	}))

	var resetSub UserSubscription
	require.NoError(t, DB.First(&resetSub, sub.Id).Error)
	require.Equal(t, int64(0), resetSub.AmountUsed)
	require.Equal(t, int64(100), resetSub.LastResetTime)
	require.Equal(t, int64(300), resetSub.NextResetTime)
	require.Equal(t, int64(200), resetSub.QuotaPeriodStart)

	// Simulate usage in the freshly reset period, then deliver the old refund.
	require.NoError(t, DB.Model(&UserSubscription{}).Where("id = ?", sub.Id).Update("amount_used", 30).Error)
	require.NoError(t, RefundSubscriptionPreConsume(record.RequestId))
	require.NoError(t, DB.First(&resetSub, sub.Id).Error)
	require.Equal(t, int64(30), resetSub.AmountUsed)
	var savedRecord SubscriptionPreConsumeRecord
	require.NoError(t, DB.First(&savedRecord, record.Id).Error)
	require.Equal(t, "period_expired", savedRecord.Status)
}

func TestSubscriptionPeriodMatchesLegacyRecords(t *testing.T) {
	sub := &UserSubscription{LastResetTime: 200}
	require.False(t, subscriptionPeriodMatches(0, 150, sub))
	require.True(t, subscriptionPeriodMatches(0, 200, sub))
	require.True(t, subscriptionPeriodMatches(200, 150, sub))
	require.False(t, subscriptionPeriodMatches(100, 250, sub))
	sub.QuotaPeriodStart = 100
	require.False(t, subscriptionPeriodMatches(100, 250, sub))
	require.True(t, subscriptionPeriodMatches(200, 150, sub))
	sub.QuotaPeriodStart = 300
	require.False(t, subscriptionPeriodMatches(200, 350, sub))
	require.True(t, subscriptionPeriodMatches(300, 250, sub))
	require.False(t, subscriptionPeriodMatches(0, 250, sub))
	require.True(t, subscriptionPeriodMatches(0, 300, sub))
	require.True(t, subscriptionPeriodMatches(0, 0, &UserSubscription{}))
}

func TestReserveUserSubscriptionDeltaRejectsPeriodChange(t *testing.T) {
	truncateTables(t)
	sub := &UserSubscription{
		UserId:        1,
		PlanId:        1,
		AmountTotal:   100,
		AmountUsed:    10,
		Status:        "active",
		StartTime:     50,
		EndTime:       1000,
		LastResetTime: 200,
	}
	require.NoError(t, DB.Create(sub).Error)

	err := ReserveUserSubscriptionDelta(sub.Id, 10, 100)
	require.ErrorIs(t, err, ErrSubscriptionPeriodChanged)

	var saved UserSubscription
	require.NoError(t, DB.First(&saved, sub.Id).Error)
	require.Equal(t, int64(10), saved.AmountUsed)
}
