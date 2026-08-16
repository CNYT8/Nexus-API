package service

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type failingSettlementFunding struct {
	preConsumeErr error
	settleErr     error
	refundCalls   int
}

func (f *failingSettlementFunding) Source() string { return BillingSourceWallet }
func (f *failingSettlementFunding) PreConsume(_ int) error {
	return f.preConsumeErr
}
func (f *failingSettlementFunding) Settle(_ int) error { return f.settleErr }
func (f *failingSettlementFunding) Refund() error {
	f.refundCalls++
	return nil
}

type capturingBillingSettler struct {
	preConsumed int
	settled     []int
}

func (s *capturingBillingSettler) Settle(actual int) error {
	s.settled = append(s.settled, actual)
	return nil
}
func (s *capturingBillingSettler) Refund(*gin.Context)      {}
func (s *capturingBillingSettler) NeedsRefund() bool        { return false }
func (s *capturingBillingSettler) GetPreConsumedQuota() int { return s.preConsumed }
func (s *capturingBillingSettler) Reserve(target int) error {
	if target > s.preConsumed {
		s.preConsumed = target
	}
	return nil
}

func TestConservativeSettlementQuotaKeepsKnownFreeGroupFree(t *testing.T) {
	billing := &capturingBillingSettler{preConsumed: 300}
	info := &relaycommon.RelayInfo{Billing: billing, FinalPreConsumedQuota: 300}
	info.PriceData.ModelRatio = 1
	info.PriceData.GroupRatioInfo.GroupRatio = 0
	require.Equal(t, 0, conservativeSettlementQuota(info))
}

func TestPostTextConsumeQuotaMissingUsageRetainsPreConsumedQuota(t *testing.T) {
	truncate(t)
	gin.SetMode(gin.TestMode)
	const userID, channelID = 107, 107
	const preConsumed = 300
	seedUser(t, userID, 0)
	seedChannel(t, channelID)

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	billing := &capturingBillingSettler{preConsumed: preConsumed}
	info := &relaycommon.RelayInfo{
		UserId:                userID,
		ChannelMeta:           &relaycommon.ChannelMeta{ChannelId: channelID},
		OriginModelName:       "missing-usage-model",
		Billing:               billing,
		FinalPreConsumedQuota: preConsumed,
		StartTime:             time.Now(),
	}

	PostTextConsumeQuota(ctx, info, nil, nil)

	require.Equal(t, []int{preConsumed}, billing.settled)
	var user model.User
	require.NoError(t, model.DB.First(&user, userID).Error)
	require.Equal(t, preConsumed, user.UsedQuota)
	log := getLastLog(t)
	require.NotNil(t, log)
	require.Equal(t, preConsumed, log.Quota)
}

func TestBillingSessionSettlementFailureCannotRefundSuccessfulUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	settleErr := errors.New("database unavailable")
	funding := &failingSettlementFunding{settleErr: settleErr}
	session := &BillingSession{
		relayInfo:        &relaycommon.RelayInfo{IsPlayground: true},
		funding:          funding,
		preConsumedQuota: 100,
		tokenConsumed:    100,
	}

	require.ErrorIs(t, session.Settle(200), settleErr)
	require.ErrorIs(t, session.Settle(200), settleErr)
	require.True(t, session.settled)
	require.False(t, session.fundingSettled)

	// A deferred cleanup may still call Refund, but the settlement barrier must
	// retain the original reservation after upstream usage has succeeded.
	session.Refund(ctx)
	require.Equal(t, 0, funding.refundCalls)
	require.False(t, session.refunded)
}

func TestBillingSessionPreConsumeFundingFailureRollsBackToken(t *testing.T) {
	truncate(t)
	gin.SetMode(gin.TestMode)
	const userID, tokenID = 105, 105
	const initialQuota = 100
	const tokenKey = "sk-security-rollback"
	seedUser(t, userID, initialQuota)
	seedToken(t, tokenID, userID, tokenKey, initialQuota)

	ctx, info := newWalletBillingTestContext(userID, tokenID, tokenKey, initialQuota)
	fundingErr := errors.New("funding reserve failed")
	session := &BillingSession{
		relayInfo: info,
		funding:   &failingSettlementFunding{preConsumeErr: fundingErr},
	}

	apiErr := session.preConsume(ctx, 50)
	require.NotNil(t, apiErr)
	require.ErrorIs(t, apiErr.Err, fundingErr)
	require.Equal(t, initialQuota, getTokenRemainQuota(t, tokenID))
	require.Equal(t, 0, getTokenUsedQuota(t, tokenID))
	require.Equal(t, 0, session.tokenConsumed)
}

func TestWalletFundingRefundIsIdempotentWithinSession(t *testing.T) {
	truncate(t)
	const userID = 104
	seedUser(t, userID, 0)
	funding := &WalletFunding{userId: userID, consumed: 100}

	require.NoError(t, funding.Refund())
	require.NoError(t, funding.Refund())
	require.Equal(t, 100, getUserQuota(t, userID))
	require.Equal(t, 0, funding.consumed)
}

func TestPostConsumeQuotaSubscriptionRefundDoesNotCrossPeriod(t *testing.T) {
	truncate(t)
	const userID, tokenID, subID = 102, 102, 102
	const tokenRemain = 5000
	seedUser(t, userID, 0)
	seedToken(t, tokenID, userID, "sk-legacy-period", tokenRemain)
	sub := &model.UserSubscription{
		Id:            subID,
		UserId:        userID,
		AmountTotal:   10000,
		AmountUsed:    3000,
		Status:        "active",
		StartTime:     50,
		EndTime:       time.Now().Add(time.Hour).Unix(),
		LastResetTime: 200,
	}
	require.NoError(t, model.DB.Create(sub).Error)

	info := &relaycommon.RelayInfo{
		UserId:                  userID,
		TokenId:                 tokenID,
		TokenKey:                "sk-legacy-period",
		BillingSource:           BillingSourceSubscription,
		SubscriptionId:          subID,
		SubscriptionPeriodStart: 100,
		StartTime:               time.Unix(150, 0),
	}
	require.NoError(t, PostConsumeQuota(info, -1000, 0, false))
	require.Equal(t, int64(3000), getSubscriptionUsed(t, subID))
	require.Equal(t, tokenRemain+1000, getTokenRemainQuota(t, tokenID))
	require.Equal(t, int64(0), info.SubscriptionPostDelta)
}

func newWalletBillingTestContext(userID int, tokenID int, tokenKey string, userQuota int) (*gin.Context, *relaycommon.RelayInfo) {
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	return ctx, &relaycommon.RelayInfo{
		UserId:         userID,
		TokenId:        tokenID,
		TokenKey:       tokenKey,
		UserQuota:      userQuota,
		TokenUnlimited: false,
		UserSetting: dto.UserSetting{
			BillingPreference: "wallet_only",
		},
	}
}

func TestBillingSessionSubscriptionOverageIsRecorded(t *testing.T) {
	truncate(t)
	const subID = 106
	sub := &model.UserSubscription{
		Id:               subID,
		UserId:           106,
		PlanId:           1,
		AmountTotal:      100,
		AmountUsed:       80,
		Status:           "active",
		StartTime:        50,
		EndTime:          time.Now().Add(time.Hour).Unix(),
		QuotaPeriodStart: 100,
	}
	require.NoError(t, model.DB.Create(sub).Error)
	info := &relaycommon.RelayInfo{IsPlayground: true, SubscriptionId: subID, SubscriptionPeriodStart: 100}
	funding := &SubscriptionFunding{subscriptionId: subID, periodStart: 100, operationTime: 120}
	session := &BillingSession{
		relayInfo:        info,
		funding:          funding,
		preConsumedQuota: 20,
	}

	require.NoError(t, session.Settle(70))
	require.Equal(t, int64(130), getSubscriptionUsed(t, subID))
	require.Equal(t, int64(50), info.SubscriptionPostDelta)
}

func TestBillingSessionHighBalanceStillPreConsumes(t *testing.T) {
	truncate(t)
	gin.SetMode(gin.TestMode)

	const userID, tokenID = 101, 101
	const initialQuota = 1_000_000
	const reserve = 1_000
	const tokenKey = "sk-security-preconsume"
	seedUser(t, userID, initialQuota)
	seedToken(t, tokenID, userID, tokenKey, initialQuota)

	ctx, info := newWalletBillingTestContext(userID, tokenID, tokenKey, initialQuota)
	session, apiErr := NewBillingSession(ctx, info, reserve)
	require.Nil(t, apiErr)
	require.NotNil(t, session)
	require.Equal(t, reserve, session.GetPreConsumedQuota())
	require.Equal(t, initialQuota-reserve, getUserQuota(t, userID))
	require.Equal(t, initialQuota-reserve, getTokenRemainQuota(t, tokenID))

	require.NoError(t, session.Settle(reserve))
}

func TestBillingSessionOverageIsRecordedAsWalletAndTokenDebt(t *testing.T) {
	truncate(t)
	gin.SetMode(gin.TestMode)

	const userID, tokenID = 103, 103
	const initialQuota = 100
	const reserve = 80
	const actualQuota = 150
	const tokenKey = "sk-security-overage"
	seedUser(t, userID, initialQuota)
	seedToken(t, tokenID, userID, tokenKey, initialQuota)

	ctx, info := newWalletBillingTestContext(userID, tokenID, tokenKey, initialQuota)
	session, apiErr := NewBillingSession(ctx, info, reserve)
	require.Nil(t, apiErr)
	require.NotNil(t, session)
	require.NoError(t, session.Settle(actualQuota))

	require.Equal(t, initialQuota-actualQuota, getUserQuota(t, userID))
	require.Equal(t, initialQuota-actualQuota, getTokenRemainQuota(t, tokenID))
	var token model.Token
	require.NoError(t, model.DB.First(&token, tokenID).Error)
	require.Equal(t, actualQuota, token.UsedQuota)

	secondCtx, secondInfo := newWalletBillingTestContext(userID, tokenID, tokenKey, initialQuota-actualQuota)
	secondSession, secondErr := NewBillingSession(secondCtx, secondInfo, 1)
	require.Nil(t, secondSession)
	require.NotNil(t, secondErr)
}
