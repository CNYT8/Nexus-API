package model

import (
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/utils/tests"
)

func setupSubscriptionPurchaseLimitTest(t *testing.T) {
	t.Helper()
	truncateTables(t)
	require.NoError(t, DB.Exec("DELETE FROM subscription_orders").Error)
	require.NoError(t, DB.Exec("DELETE FROM user_subscriptions").Error)
	require.NoError(t, DB.Exec("DELETE FROM subscription_plans").Error)
	require.NoError(t, DB.Exec("DELETE FROM users").Error)
}

func createLimitedSubscriptionPlan(t *testing.T, maxPurchase int) *SubscriptionPlan {
	t.Helper()
	plan := &SubscriptionPlan{
		Title:              "purchase-limit-plan",
		PriceAmount:        10,
		Currency:           "USD",
		DurationUnit:       SubscriptionDurationMonth,
		DurationValue:      1,
		Enabled:            true,
		MaxPurchasePerUser: maxPurchase,
		TotalAmount:        100,
		QuotaResetPeriod:   SubscriptionResetNever,
	}
	require.NoError(t, DB.Create(plan).Error)
	// 测试库会复用自增 id,必须清掉可能残留的旧套餐缓存快照。
	InvalidateSubscriptionPlanCache(plan.Id)
	return plan
}

func createPurchaseLimitUser(t *testing.T, name string) *User {
	t.Helper()
	user := &User{
		Username: name,
		Quota:    1000,
		Status:   common.UserStatusEnabled,
		Role:     common.RoleCommonUser,
	}
	require.NoError(t, DB.Create(user).Error)
	return user
}

func createPendingSubscriptionOrder(t *testing.T, user *User, plan *SubscriptionPlan, tradeNo string) {
	t.Helper()
	order := &SubscriptionOrder{
		UserId:          user.Id,
		PlanId:          plan.Id,
		Money:           plan.PriceAmount,
		TradeNo:         tradeNo,
		PaymentMethod:   "epay",
		PaymentProvider: "epay",
		Status:          common.TopUpStatusPending,
		CreateTime:      common.GetTimestamp(),
	}
	require.NoError(t, DB.Create(order).Error)
}

func purchaseLimitConfirmation(amount string) PaymentConfirmation {
	return PaymentConfirmation{
		Amount:           amount,
		Currency:         "USD",
		ExpectedCurrency: "USD",
		DecimalPlaces:    2,
	}
}

// MaxPurchasePerUser=1 时,同一用户的两笔订单只能成功一笔;失败订单保持
// pending,且不会产生第二条订阅。
func TestCompleteSubscriptionOrderEnforcesMaxPurchasePerUser(t *testing.T) {
	setupSubscriptionPurchaseLimitTest(t)

	user := createPurchaseLimitUser(t, "purchase-limit-user")
	plan := createLimitedSubscriptionPlan(t, 1)
	createPendingSubscriptionOrder(t, user, plan, "trade-limit-a")
	createPendingSubscriptionOrder(t, user, plan, "trade-limit-b")

	tradeNos := []string{"trade-limit-a", "trade-limit-b"}
	var successCount atomic.Int32
	var limitErrorCount atomic.Int32
	var wg sync.WaitGroup
	for _, tradeNo := range tradeNos {
		wg.Add(1)
		go func(tradeNo string) {
			defer wg.Done()
			err := CompleteSubscriptionOrderWithConfirmation(tradeNo, "", "epay", "", purchaseLimitConfirmation("10.00"))
			switch {
			case err == nil:
				successCount.Add(1)
			case strings.Contains(err.Error(), "购买上限"):
				limitErrorCount.Add(1)
			default:
				t.Errorf("unexpected completion error for %s: %v", tradeNo, err)
			}
		}(tradeNo)
	}
	wg.Wait()

	require.Equal(t, int32(1), successCount.Load())
	require.Equal(t, int32(1), limitErrorCount.Load())

	var subscriptionCount int64
	require.NoError(t, DB.Model(&UserSubscription{}).
		Where("user_id = ? AND plan_id = ?", user.Id, plan.Id).
		Count(&subscriptionCount).Error)
	require.Equal(t, int64(1), subscriptionCount)

	var successOrders int64
	require.NoError(t, DB.Model(&SubscriptionOrder{}).
		Where("trade_no IN ? AND status = ?", tradeNos, common.TopUpStatusSuccess).
		Count(&successOrders).Error)
	require.Equal(t, int64(1), successOrders)

	var pendingOrders int64
	require.NoError(t, DB.Model(&SubscriptionOrder{}).
		Where("trade_no IN ? AND status = ?", tradeNos, common.TopUpStatusPending).
		Count(&pendingOrders).Error)
	require.Equal(t, int64(1), pendingOrders)
}

// 管理员绑定同样使用套餐购买上限检查,并且先锁用户行。
func TestAdminBindSubscriptionEnforcesMaxPurchasePerUser(t *testing.T) {
	setupSubscriptionPurchaseLimitTest(t)

	user := createPurchaseLimitUser(t, "admin-bind-limit-user")
	plan := createLimitedSubscriptionPlan(t, 1)

	_, err := AdminBindSubscription(user.Id, plan.Id, "first")
	require.NoError(t, err)

	_, err = AdminBindSubscription(user.Id, plan.Id, "second")
	require.Error(t, err)
	require.Contains(t, err.Error(), "购买上限")

	var subscriptionCount int64
	require.NoError(t, DB.Model(&UserSubscription{}).
		Where("user_id = ? AND plan_id = ?", user.Id, plan.Id).
		Count(&subscriptionCount).Error)
	require.Equal(t, int64(1), subscriptionCount)
}

// AdminBindSubscription 事务必须声明用户 FOR UPDATE 行锁。这里只捕获 GORM
// clause 意图；SQLite 方言会省略 SQL 中的 FOR UPDATE，不是真实行锁验证。
func TestAdminBindSubscriptionLocksUserRowForUpdate(t *testing.T) {
	setupSubscriptionPurchaseLimitTest(t)

	user := createPurchaseLimitUser(t, "admin-bind-lock-user")
	plan := createLimitedSubscriptionPlan(t, 5)

	previousSQLite := common.UsingSQLite
	common.UsingSQLite = false
	t.Cleanup(func() { common.UsingSQLite = previousSQLite })

	var captured atomic.Bool
	callbackName := "test:capture-admin-bind-user-lock"
	require.NoError(t, DB.Callback().Query().Before("gorm:query").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement == nil || tx.Statement.Clauses == nil {
			return
		}
		if _, ok := tx.Statement.Clauses["FOR"]; !ok {
			return
		}
		if _, ok := tx.Statement.Model.(*User); ok {
			captured.Store(true)
		}
	}))
	defer DB.Callback().Query().Remove(callbackName)

	_, err := AdminBindSubscription(user.Id, plan.Id, "lock-check")
	require.NoError(t, err)

	require.True(t, captured.Load(), "AdminBindSubscription must lock the user row with FOR UPDATE")
}

func TestSubscriptionUserLockPrecedesPlanAndPurchaseCount(t *testing.T) {
	for _, orderCompletion := range []bool{false, true} {
		name := "admin"
		if orderCompletion {
			name = "order"
		}
		t.Run(name, func(t *testing.T) {
			setupSubscriptionPurchaseLimitTest(t)
			user := createPurchaseLimitUser(t, "lock-order-user")
			plan := createLimitedSubscriptionPlan(t, 1)
			createPendingSubscriptionOrder(t, user, plan, "lock-order-trade")
			// Force a plan cache miss. In MySQL REPEATABLE READ, querying the
			// plan before locking the user would pin a stale purchase-count snapshot.
			InvalidateSubscriptionPlanCache(plan.Id)
			previousSQLite := common.UsingSQLite
			common.UsingSQLite = false
			t.Cleanup(func() { common.UsingSQLite = previousSQLite })
			var queries []string
			callback := "test:subscription-lock-order"
			require.NoError(t, DB.Callback().Query().Before("gorm:query").Register(callback, func(tx *gorm.DB) {
				table := tx.Statement.Table
				if table == "users" && tx.Statement.Selects != nil && len(tx.Statement.Selects) == 1 && tx.Statement.Selects[0] == "id" {
					_, locked := tx.Statement.Clauses["FOR"]
					assert.True(t, locked, "user lookup must be a locking read")
				}
				queries = append(queries, table)
			}))
			t.Cleanup(func() { DB.Callback().Query().Remove(callback) })
			// SQLite omits FOR UPDATE in generated SQL; the callback checks intent.
			if orderCompletion {
				require.NoError(t, CompleteSubscriptionOrderWithConfirmation("lock-order-trade", "", "epay", "", purchaseLimitConfirmation("10.00")))
				require.GreaterOrEqual(t, len(queries), 4)
				assert.Equal(t, []string{"subscription_orders", "users", "subscription_plans", "user_subscriptions"}, queries[:4])
			} else {
				_, err := AdminBindSubscription(user.Id, plan.Id, "test")
				require.NoError(t, err)
				require.GreaterOrEqual(t, len(queries), 3)
				// Admin loads the plan outside the transaction.
				assert.Equal(t, []string{"subscription_plans", "users", "user_subscriptions"}, queries[:3])
			}
		})
	}
}

// lockForUpdate 对 users 行锁生成的 SQL 形态保护。
func TestSubscriptionUserLockQueryShape(t *testing.T) {
	previousSQLite := common.UsingSQLite
	common.UsingSQLite = false
	t.Cleanup(func() { common.UsingSQLite = previousSQLite })

	dummyDB, err := gorm.Open(tests.DummyDialector{}, &gorm.Config{DryRun: true})
	require.NoError(t, err)

	var userRow User
	sql := lockForUpdate(dummyDB).
		Select("id").
		Where("id = ?", 1).
		First(&userRow).Statement.SQL.String()
	assert.Contains(t, strings.ToUpper(sql), "FOR UPDATE")
	assert.Contains(t, sql, "users")
}
