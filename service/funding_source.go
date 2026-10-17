package service

import (
	"errors"
	"time"

	"github.com/QuantumNous/new-api/model"
)

// ---------------------------------------------------------------------------
// FundingSource — 资金来源接口（钱包 or 订阅）
// ---------------------------------------------------------------------------

// FundingSource 抽象了预扣费的资金来源。
type FundingSource interface {
	// Source 返回资金来源标识："wallet" 或 "subscription"
	Source() string
	// PreConsume 从该资金来源预扣 amount 额度
	PreConsume(amount int) error
	// Settle 根据差额调整资金来源（正数补扣，负数退还）
	Settle(delta int) error
	// Refund 退还所有预扣费
	Refund() error
}

// ---------------------------------------------------------------------------
// WalletFunding — 钱包资金来源实现
// ---------------------------------------------------------------------------

type WalletFunding struct {
	userId        int
	consumed      int    // 实际预扣的用户额度
	settlementKey string // 服务端生成的钱包最终结算身份（为空时走旧差额路径）

	// reservationActive 表示本次预扣已写入持久 reservation/hold；此时退款、
	// 增扣和最终结算都必须走账本 API，绝不能直接加减 quota。
	reservationActive bool
	// durableSettlement 表示最终结算拥有持久幂等身份，失败后可安全重试。
	durableSettlement bool
}

func (w *WalletFunding) Source() string { return BillingSourceWallet }

// CanRetrySettlement 仅在结算具备服务端持久身份时返回 true；旧差额路径在
// 提交结果未知的情况下重试可能重复扣费，必须保持 fail-closed。
func (w *WalletFunding) CanRetrySettlement() bool { return w.durableSettlement }

func mapWalletReserveError(err error) error {
	if err == nil {
		return nil
	}
	// 统一成上层可识别的余额不足信号，保留其它账务错误原样上报。
	if errors.Is(err, model.ErrInsufficientQuota) || errors.Is(err, model.ErrWalletInsufficientQuota) {
		return model.ErrInsufficientQuota
	}
	return err
}

func (w *WalletFunding) PreConsume(amount int) error {
	if amount <= 0 {
		return nil
	}
	active, err := model.WalletCreditLedgerActive(model.DB)
	if err != nil {
		return err
	}
	if active {
		// Ledger-backed wallets must reserve through the durable identity; a
		// direct quota debit here would drift quota away from the source lots.
		if w.settlementKey == "" {
			return ErrWalletBillingIdentityRequired
		}
		if err := model.ReserveWalletQuota(model.DB, w.settlementKey, w.userId, int64(amount)); err != nil {
			return mapWalletReserveError(err)
		}
		w.consumed = amount
		w.reservationActive = true
		w.durableSettlement = true
		return nil
	}
	if err := model.ReserveUserQuota(w.userId, amount); err != nil {
		return err
	}
	w.consumed = amount
	return nil
}

// ReserveMore 在同一会话内追加预扣到新的绝对总量。
func (w *WalletFunding) ReserveMore(delta int) error {
	if delta <= 0 {
		return nil
	}
	if w.reservationActive {
		if err := model.ReserveWalletQuota(model.DB, w.settlementKey, w.userId, int64(w.consumed+delta)); err != nil {
			return mapWalletReserveError(err)
		}
		w.consumed += delta
		return nil
	}
	active, err := model.WalletCreditLedgerActive(model.DB)
	if err != nil {
		return err
	}
	if active {
		if w.settlementKey == "" {
			return ErrWalletBillingIdentityRequired
		}
		if err := model.ReserveWalletQuota(model.DB, w.settlementKey, w.userId, int64(w.consumed+delta)); err != nil {
			return mapWalletReserveError(err)
		}
		w.consumed += delta
		w.reservationActive = true
		w.durableSettlement = true
		return nil
	}
	if err := model.ReserveUserQuota(w.userId, delta); err != nil {
		return err
	}
	w.consumed += delta
	return nil
}

// ReleaseReserved 把本次会话的持久预扣回退到更小的绝对总量（CAS 校验）。
func (w *WalletFunding) ReleaseReserved(delta int) error {
	if delta <= 0 {
		return nil
	}
	if w.consumed < delta {
		return errors.New("wallet release exceeds reserved quota")
	}
	if w.reservationActive {
		if err := model.ReleaseWalletReservationTo(model.DB, w.settlementKey, w.userId, int64(w.consumed), int64(w.consumed-delta)); err != nil {
			return err
		}
		w.consumed -= delta
		return nil
	}
	if err := model.IncreaseUserQuotaDirect(w.userId, delta); err != nil {
		return err
	}
	w.consumed -= delta
	return nil
}

func (w *WalletFunding) Settle(delta int) error {
	if delta == 0 {
		return nil
	}
	if delta > 0 {
		// Settlement records actual usage even when it exceeds the remaining
		// wallet balance. The resulting debt blocks later atomic reservations.
		return model.DecreaseUserQuotaDirect(w.userId, delta)
	}
	return model.IncreaseUserQuotaDirect(w.userId, -delta)
}

// SettleUsage finalizes a wallet request with the exact reserved and final
// usage. When the wallet ledger is initialized it records the settlement,
// consumes source lots, and applies the wallet delta atomically; identical
// retries are idempotent. Otherwise the legacy difference-only path is kept.
func (w *WalletFunding) SettleUsage(preConsumed, actual int) error {
	active, err := model.WalletCreditLedgerActive(model.DB)
	if err != nil {
		return err
	}
	if !active {
		if err := w.Settle(actual - preConsumed); err != nil {
			return err
		}
		// The request succeeded: the funding adjustment is final and must never
		// be refunded by a later cleanup path.
		w.consumed = 0
		return nil
	}
	// Ledger-backed wallets must settle through the durable identity; the
	// legacy difference-only write would drift quota away from the source lots.
	if w.settlementKey == "" {
		return ErrWalletBillingIdentityRequired
	}
	w.durableSettlement = true
	if w.reservationActive {
		// 持久屏障必须独立提交：即使随后的最终结算失败，也无法再退款已成功的用量。
		if err := model.PrepareWalletFinalization(model.DB, w.settlementKey, w.userId, int64(preConsumed), int64(actual)); err != nil {
			return err
		}
	}
	if _, err := model.FinalizeWalletConsumption(model.DB, w.settlementKey, w.userId, int64(preConsumed), int64(actual)); err != nil {
		return err
	}
	w.consumed = 0
	w.reservationActive = false
	return nil
}

func (w *WalletFunding) Refund() error {
	if w.consumed <= 0 {
		return nil
	}
	if w.reservationActive {
		if err := model.RefundWalletReservation(model.DB, w.settlementKey, w.userId); err != nil {
			return err
		}
		w.consumed = 0
		w.reservationActive = false
		return nil
	}
	// 钱包退款是 quota += N：仅在数据库成功后清零本地预扣状态，
	// 使同一 Funding 实例的重复调用保持幂等；失败时保留状态供上层恢复。
	if err := model.IncreaseUserQuotaDirect(w.userId, w.consumed); err != nil {
		return err
	}
	w.consumed = 0
	return nil
}

// ---------------------------------------------------------------------------
// SubscriptionFunding — 订阅资金来源实现
// ---------------------------------------------------------------------------

type SubscriptionFunding struct {
	requestId         string
	userId            int
	modelName         string
	amount            int64 // 预扣的订阅额度（subConsume）
	subscriptionId    int
	preConsumed       int64
	periodStart       int64
	operationTime     int64
	lastSettleApplied bool
	// 以下字段在 PreConsume 成功后填充，供 RelayInfo 同步使用
	AmountTotal     int64
	AmountUsedAfter int64
	PlanId          int
	PlanTitle       string
}

func (s *SubscriptionFunding) Source() string { return BillingSourceSubscription }

func (s *SubscriptionFunding) PreConsume(_ int) error {
	// amount 参数被忽略，使用内部 s.amount（已在构造时根据 preConsumedQuota 计算）
	res, err := model.PreConsumeUserSubscription(s.requestId, s.userId, s.modelName, 0, s.amount)
	if err != nil {
		return err
	}
	s.subscriptionId = res.UserSubscriptionId
	s.preConsumed = res.PreConsumed
	s.periodStart = res.PeriodStart
	s.AmountTotal = res.AmountTotal
	s.AmountUsedAfter = res.AmountUsedAfter
	// 获取订阅计划信息
	if planInfo, err := model.GetSubscriptionPlanInfoByUserSubscriptionId(res.UserSubscriptionId); err == nil && planInfo != nil {
		s.PlanId = planInfo.PlanId
		s.PlanTitle = planInfo.PlanTitle
	}
	return nil
}

func (s *SubscriptionFunding) Settle(delta int) error {
	if delta == 0 {
		return nil
	}
	applied, err := model.SettleUserSubscriptionDelta(
		s.subscriptionId,
		int64(delta),
		s.periodStart,
		s.operationTime,
	)
	s.lastSettleApplied = applied
	return err
}

func (s *SubscriptionFunding) Refund() error {
	if s.preConsumed <= 0 {
		return nil
	}
	return refundWithRetry(func() error {
		return model.RefundSubscriptionPreConsume(s.requestId)
	})
}

// refundWithRetry 尝试多次执行退款操作以提高成功率，只能用于基于事务的退款函数！！！！！！
// try to refund with retries, only for refund functions based on transactions!!!
func refundWithRetry(fn func() error) error {
	if fn == nil {
		return nil
	}
	const maxAttempts = 3
	var lastErr error
	for i := 0; i < maxAttempts; i++ {
		if err := fn(); err == nil {
			return nil
		} else {
			lastErr = err
		}
		if i < maxAttempts-1 {
			time.Sleep(time.Duration(200*(i+1)) * time.Millisecond)
		}
	}
	return lastErr
}
