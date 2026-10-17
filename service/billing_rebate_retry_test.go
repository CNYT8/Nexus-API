package service

import (
	"errors"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/stretchr/testify/require"
)

type retryableUsageTestFunding struct {
	calls   int
	failure error
}

func (f *retryableUsageTestFunding) Source() string       { return BillingSourceWallet }
func (f *retryableUsageTestFunding) PreConsume(int) error { return nil }
func (f *retryableUsageTestFunding) Settle(int) error {
	return errors.New("difference settlement must not be called")
}
func (f *retryableUsageTestFunding) Refund() error {
	return errors.New("successful usage must not be refunded")
}
func (f *retryableUsageTestFunding) CanRetrySettlement() bool { return true }
func (f *retryableUsageTestFunding) SettleUsage(_, _ int) error {
	f.calls++
	if f.calls == 1 {
		return f.failure
	}
	return nil
}

func TestBillingSessionRetriesOnlyDurableFundingWithIdenticalUsage(t *testing.T) {
	for _, actual := range []int{25, 40} {
		t.Run(string(rune('a'+actual)), func(t *testing.T) {
			failure := errors.New("transaction rolled back")
			funding := &retryableUsageTestFunding{failure: failure}
			session := &BillingSession{
				relayInfo: &relaycommon.RelayInfo{IsPlayground: true},
				funding:   funding, preConsumedQuota: 25,
			}
			require.ErrorIs(t, session.Settle(actual), failure)
			require.True(t, session.settled)
			require.False(t, session.NeedsRefund())
			require.Error(t, session.Settle(actual+1))
			require.Equal(t, 1, funding.calls)
			require.NoError(t, session.Settle(actual))
			require.True(t, session.fundingSettled)
			require.NoError(t, session.Settle(actual))
			require.Equal(t, 2, funding.calls)
		})
	}
}

func TestBillingSessionPlaygroundWalletReservationNeedsRefund(t *testing.T) {
	session := &BillingSession{
		relayInfo: &relaycommon.RelayInfo{IsPlayground: true},
		funding:   &WalletFunding{userId: 1, consumed: 10},
	}
	require.True(t, session.NeedsRefund())
	session.settled = true
	require.False(t, session.NeedsRefund())
}
