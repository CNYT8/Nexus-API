package service

import (
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type searchBillingProbe struct {
	held, actual, settlements, refunds int
	err                                error
}

func (b *searchBillingProbe) Reserve(target int) error {
	if target > b.held {
		b.held = target
	}
	return nil
}
func (b *searchBillingProbe) Settle(actual int) error {
	b.actual = actual
	b.settlements++
	return b.err
}
func (b *searchBillingProbe) Refund(*gin.Context)      { b.refunds++ }
func (b *searchBillingProbe) NeedsRefund() bool        { return b.settlements == 0 }
func (b *searchBillingProbe) GetPreConsumedQuota() int { return b.held }

func TestSearchSettlement(t *testing.T) {
	for _, tc := range []struct {
		name        string
		billable    bool
		price, held int
		want        int
		source      string
	}{
		{name: "wallet exact call", billable: true, price: 25, held: 100, want: 25, source: BillingSourceWallet},
		{name: "subscription exact call", billable: true, price: 25, held: 100, want: 25, source: BillingSourceSubscription},
		{name: "empty search refunds full reserve", price: 25, held: 100, source: BillingSourceWallet},
		{name: "empty subscription refunds full reserve", price: 25, held: 100, source: BillingSourceSubscription},
		{name: "free retry releases old group reserve", billable: true, held: 100, source: BillingSourceWallet},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", "/v1/tools/search", nil)
			billing := &searchBillingProbe{held: tc.held}
			info := &relaycommon.RelayInfo{Billing: billing, BillingSource: tc.source,
				FinalPreConsumedQuota: tc.held, PriceData: types.PriceData{UsePrice: true, Quota: tc.price}}
			quota, err := settleSearchBilling(c, info, tc.billable)
			if err != nil || quota != tc.want || billing.actual != tc.want || billing.settlements != 1 || billing.refunds != 0 {
				t.Fatalf("quota=%d, error=%v, billing=%+v", quota, err, billing)
			}
		})
	}
}

func TestSearchSettlementRequiresSession(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	for _, info := range []*relaycommon.RelayInfo{
		{PriceData: types.PriceData{Quota: 1}},
		{FinalPreConsumedQuota: 1},
		{PriceData: types.PriceData{Quota: -1}},
	} {
		if _, err := settleSearchBilling(c, info, true); err == nil {
			t.Fatal("invalid paid settlement was accepted without a session")
		}
	}
	if quota, err := settleSearchBilling(c, &relaycommon.RelayInfo{}, true); err != nil || quota != 0 {
		t.Fatalf("free call quota=%d, error=%v", quota, err)
	}
}

func TestSearchBillingSessionWalletAndToken(t *testing.T) {
	for _, billable := range []bool{true, false} {
		name := "empty result"
		if billable {
			name = "one billable call"
		}
		t.Run(name, func(t *testing.T) {
			truncate(t)
			const userID, tokenID, balance, reserve = 701, 701, 1000, 100
			seedUser(t, userID, balance)
			seedToken(t, tokenID, userID, "sk-search-wallet", balance)
			c, info := newWalletBillingTestContext(userID, tokenID, "sk-search-wallet", balance)
			c.Request = httptest.NewRequest("POST", dto.DeepSeekSearchPath, nil)
			info.PriceData = types.PriceData{UsePrice: true, Quota: 25, QuotaToPreConsume: reserve}
			require.Nil(t, PreConsumeBilling(c, reserve, info))
			require.Equal(t, balance-reserve, getUserQuota(t, userID))
			require.Equal(t, balance-reserve, getTokenRemainQuota(t, tokenID))
			want := 0
			if billable {
				want = 25
			}
			for i := 0; i < 2; i++ {
				quota, err := settleSearchBilling(c, info, billable)
				require.NoError(t, err)
				require.Equal(t, want, quota)
			}
			info.Billing.Refund(c)
			require.False(t, info.Billing.NeedsRefund())
			require.Equal(t, balance-want, getUserQuota(t, userID))
			require.Equal(t, balance-want, getTokenRemainQuota(t, tokenID))
			require.Equal(t, want, getTokenUsedQuota(t, tokenID))
		})
	}
}

func TestSearchBillingSessionFailureBarrier(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", dto.DeepSeekFetchPath, nil)
	want := errors.New("search settlement storage unavailable")
	funding := &failingSettlementFunding{settleErr: want}
	info := &relaycommon.RelayInfo{IsPlayground: true, PriceData: types.PriceData{UsePrice: true, Quota: 25}}
	session := &BillingSession{relayInfo: info, funding: funding, preConsumedQuota: 100, tokenConsumed: 100}
	info.Billing = session
	_, err := settleSearchBilling(c, info, true)
	require.ErrorIs(t, err, want)
	session.Refund(c)
	require.False(t, session.NeedsRefund())
	require.Zero(t, funding.refundCalls)
}

func TestSearchSettlementUsageLog(t *testing.T) {
	truncate(t)
	const userID, channelID = 702, 702
	seedUser(t, userID, 1000)
	seedChannel(t, channelID)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", dto.DeepSeekSearchPath, nil)
	billing := &searchBillingProbe{held: 100}
	info := &relaycommon.RelayInfo{UserId: userID, ChannelMeta: &relaycommon.ChannelMeta{ChannelId: channelID},
		OriginModelName: dto.DeepSeekSearchModel, Billing: billing, StartTime: time.Now(),
		PriceData: types.PriceData{UsePrice: true, Quota: 25, ModelPrice: 0.01, GroupRatioInfo: types.GroupRatioInfo{GroupRatio: 1}},
	}
	PostSearchConsumeQuota(c, info, true)
	require.Equal(t, 25, billing.actual)
	var user model.User
	require.NoError(t, model.DB.First(&user, userID).Error)
	require.Equal(t, 25, user.UsedQuota)
	log := getLastLog(t)
	require.NotNil(t, log)
	require.Equal(t, 25, log.Quota)
	require.Contains(t, log.Other, `"usage_semantic":"search_call"`)
	require.Contains(t, log.Other, `"search_call_count":1`)
}

func TestSearchSettlementFailureDoesNotRefund(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/tools/search", nil)
	want := errors.New("settlement storage unavailable")
	billing := &searchBillingProbe{held: 100, err: want}
	info := &relaycommon.RelayInfo{Billing: billing, PriceData: types.PriceData{Quota: 25}}
	quota, err := settleSearchBilling(c, info, true)
	if !errors.Is(err, want) || quota != 25 || billing.refunds != 0 || billing.NeedsRefund() {
		t.Fatalf("quota=%d, error=%v, billing=%+v", quota, err, billing)
	}
}
