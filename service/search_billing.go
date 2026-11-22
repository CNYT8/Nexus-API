package service

import (
	"errors"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	perfmetrics "github.com/QuantumNous/new-api/pkg/perf_metrics"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/bytedance/gopkg/util/gopool"
	"github.com/gin-gonic/gin"
)

// settleSearchBilling is deliberately separate from token settlement: the
// absence of token usage is expected, not a reason to retain a conservative
// token floor or add the tool price a second time. Settle(0) releases an empty
// tool result through the same wallet/subscription/token transaction lifecycle.
func settleSearchBilling(c *gin.Context, info *relaycommon.RelayInfo, billable bool) (int, error) {
	quota := 0
	if billable {
		quota = info.PriceData.Quota
	}
	if quota < 0 {
		return quota, errors.New("invalid search settlement quota")
	}
	if info.Billing == nil {
		if quota != 0 || info.FinalPreConsumedQuota != 0 {
			return quota, errors.New("search settlement requires a billing session")
		}
		return quota, nil
	}
	return quota, settleSearchQuota(c, info, quota)
}

// settleSearchQuota retries only a session with a durable CAS identity. This
// also retries a failed intent write, before background recovery can see it.
// Once the intent commits, existing recovery owns any remaining database error.
func settleSearchQuota(c *gin.Context, info *relaycommon.RelayInfo, quota int) error {
	err := SettleBilling(c, info, quota)
	session, ok := info.Billing.(*BillingSession)
	if err == nil || !ok {
		return err
	}
	session.mu.Lock()
	durable := session.durableBillingKey() != ""
	session.mu.Unlock()
	if durable {
		for attempt := 1; attempt < 3 && err != nil; attempt++ {
			err = SettleBilling(c, info, quota)
		}
	}
	return err
}

// RefundSearchBilling is used only after the last failed search attempt. A zero
// finalization persists the refund target so wallet/subscription and token
// adjustments can recover together even after dispatch. Never replace an
// already-established successful-usage target with zero.
func RefundSearchBilling(c *gin.Context, info *relaycommon.RelayInfo) error {
	if info == nil || info.Billing == nil {
		if info != nil && info.FinalPreConsumedQuota != 0 {
			return errors.New("search refund requires a billing session")
		}
		return nil
	}
	if session, ok := info.Billing.(*BillingSession); ok {
		session.mu.Lock()
		settled, actual, refunded := session.settled, session.actualQuota, session.refunded
		session.mu.Unlock()
		if refunded || (settled && actual != 0) {
			return nil
		}
		// A previous zero finalization can be retried even though NeedsRefund
		// is false: Settle has already installed its in-memory barrier.
	} else if !info.Billing.NeedsRefund() {
		return nil
	}
	return settleSearchQuota(c, info, 0)
}

// PostSearchConsumeQuota runs after a validated successful upstream result. A
// settlement storage failure is recorded, never returned as a retryable relay
// error: BillingSession has already established the successful-usage barrier.
func PostSearchConsumeQuota(c *gin.Context, info *relaycommon.RelayInfo, billable bool) {
	quota, err := settleSearchBilling(c, info, billable)
	if err != nil {
		logger.LogError(c, "error settling search billing: "+err.Error())
	}
	model.UpdateUserUsedQuotaAndRequestCount(info.UserId, quota)
	model.UpdateChannelUsedQuota(info.ChannelId, quota)
	other := GenerateTextOtherInfo(c, info, 0, info.PriceData.GroupRatioInfo.GroupRatio, 0, 0, 0, info.PriceData.ModelPrice, info.PriceData.GroupRatioInfo.GroupSpecialRatio)
	other["usage_semantic"] = "search_call"
	other["search_endpoint"] = c.Request.URL.Path
	other["search_call_count"] = 0
	if billable {
		other["search_call_count"] = 1
	}
	other["search_call_price"] = info.PriceData.ModelPrice
	other["search_empty_result"] = !billable
	if err != nil {
		model.SetLogOtherAdminField(other, "search_settlement_pending", true)
	}
	if reason := common.GetContextKeyString(c, constant.ContextKeyAdminRejectReason); reason != "" {
		model.SetLogOtherAdminField(other, "reject_reason", reason)
	}
	model.RecordConsumeLog(c, info.UserId, model.RecordConsumeLogParams{
		ChannelId: info.ChannelId, ModelName: info.OriginModelName,
		TokenName: c.GetString("token_name"), TokenId: info.TokenId,
		Quota: quota, Content: "search per-call settlement",
		UseTimeSeconds: int(time.Since(info.StartTime).Seconds()),
		IsStream:       false, Group: info.UsingGroup, Other: other,
	})
	gopool.Go(func() { perfmetrics.RecordRelaySample(info, true, 0, 0) })
}
