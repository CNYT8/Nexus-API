package service

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"

	"github.com/bytedance/gopkg/util/gopool"
	"github.com/gin-gonic/gin"
)

// ---------------------------------------------------------------------------
// BillingSession — 统一计费会话
// ---------------------------------------------------------------------------

// BillingSession 封装单次请求的预扣费/结算/退款生命周期。
// 实现 relaycommon.BillingSettler 接口。
type BillingSession struct {
	relayInfo        *relaycommon.RelayInfo
	funding          FundingSource
	preConsumedQuota int   // 实际预扣额度
	tokenConsumed    int   // 令牌额度实际扣减量
	extraReserved    int   // 发送前补充预扣的额度（订阅退款时需要单独回滚）
	fundingSettled   bool  // funding.Settle 已成功，资金来源已提交
	settled          bool  // 成功用量的不可退款屏障；持久幂等资金来源可重试失败的结算
	actualQuota      int   // 首次结算的不可变用量；重试不能改价
	settlementErr    error // 最近结算错误；非幂等资金来源保持首次结果
	refunded         bool  // Refund 已调用
	refundDone       chan struct{}
	refundErr        error
	leaseStop        chan struct{}
	mu               sync.Mutex
}

// usageSettler is implemented by funding sources that must observe the exact
// reserved and final usage instead of only their difference. Wallet ledger
// settlement needs both values to consume source lots even when the delta is
// zero.
type usageSettler interface {
	SettleUsage(preConsumed, actual int) error
}

// retryableFunding explicitly opts in only after a durable idempotency identity
// exists. A difference-only wallet or subscription must never be blindly retried
// after an ambiguous database commit.
type retryableFunding interface {
	CanRetrySettlement() bool
}

// Settle 根据实际消耗额度进行结算。
// 一旦开始结算，会先把会话标记为不可退款：上游已经成功产生用量时，
// 即使后续数据库调整失败，也绝不能由延迟清理逻辑退掉原预扣额度。
func (s *BillingSession) Settle(actualQuota int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.settled {
		if actualQuota != s.actualQuota {
			return errors.New("billing settlement retried with different actual quota")
		}
		if s.settlementErr == nil {
			return nil
		}
		if s.durableBillingKey() == "" {
			retry, ok := s.funding.(retryableFunding)
			if s.fundingSettled || !ok || !retry.CanRetrySettlement() {
				return s.settlementErr
			}
		}
	}
	if s.refunded {
		return errors.New("billing session already refunded")
	}
	if actualQuota < 0 {
		return errors.New("actual quota cannot be negative")
	}
	delta := actualQuota - s.preConsumedQuota
	// Finalization barrier: after a successful upstream response, Refund must
	// never run even when a settlement write returns an error.
	s.settled = true
	s.actualQuota = actualQuota
	s.settlementErr = nil
	if key := s.durableBillingKey(); key != "" {
		// Persist the successful-usage barrier separately. If the accounting
		// transaction fails, recovery commits funding and token together.
		if err := model.PrepareWalletFinalization(model.DB, key, s.relayInfo.UserId, int64(s.preConsumedQuota), int64(actualQuota)); err != nil {
			s.settlementErr = err
			return err
		}
		s.stopLeaseLocked()
		applied, err := model.FinalizeBillingReservation(model.DB, key, s.relayInfo.UserId, int64(s.preConsumedQuota), int64(actualQuota))
		s.settlementErr = err
		if err != nil {
			return err
		}
		s.fundingSettled = true
		if wallet, ok := s.funding.(*WalletFunding); ok {
			wallet.consumed = 0
			wallet.reservationActive = false
		}
		if _, ok := s.funding.(*SubscriptionFunding); ok && applied {
			s.relayInfo.SubscriptionPostDelta += int64(delta)
		}
		return nil
	}
	if delta == 0 {
		// Wallet ledger settlements must still consume source lots and prove the
		// wallet row exists even when reserved and final usage match.
		if usage, ok := s.funding.(usageSettler); ok && !s.fundingSettled {
			if err := usage.SettleUsage(s.preConsumedQuota, actualQuota); err != nil {
				s.settlementErr = err
				return err
			}
			s.fundingSettled = true
		}
		return nil
	}
	// 1) 调整资金来源（仅在尚未提交时执行，防止重复调用）
	if !s.fundingSettled {
		var settleErr error
		if usage, ok := s.funding.(usageSettler); ok {
			settleErr = usage.SettleUsage(s.preConsumedQuota, actualQuota)
		} else {
			settleErr = s.funding.Settle(delta)
		}
		if settleErr != nil {
			s.settlementErr = settleErr
			return settleErr
		}
		s.fundingSettled = true
	}
	// 2) 调整令牌额度
	var tokenErr error
	if !s.relayInfo.IsPlayground {
		if delta > 0 {
			tokenErr = model.DecreaseTokenQuotaDirect(s.relayInfo.TokenId, s.relayInfo.TokenKey, delta)
		} else {
			tokenErr = model.IncreaseTokenQuotaDirect(s.relayInfo.TokenId, s.relayInfo.TokenKey, -delta)
		}
		if tokenErr != nil {
			// 资金来源已提交，令牌调整失败只能记录日志；会话已被结算屏障保护，不能误退款。
			common.SysLog(fmt.Sprintf("error adjusting token quota after funding settled (userId=%d, tokenId=%d, delta=%d): %s",
				s.relayInfo.UserId, s.relayInfo.TokenId, delta, tokenErr.Error()))
		}
	}
	// 3) 更新 relayInfo 上的订阅 PostDelta（用于日志）。旧周期退款被
	// 丢弃时不能伪装成已冲减新周期用量。
	if subscription, ok := s.funding.(*SubscriptionFunding); ok && subscription.lastSettleApplied {
		s.relayInfo.SubscriptionPostDelta += int64(delta)
	}
	s.settlementErr = tokenErr
	return tokenErr
}

// Refund 退还所有预扣费，幂等安全，异步执行。
func (s *BillingSession) Refund(c *gin.Context) {
	_, run := s.prepareRefund(c)
	if run != nil {
		gopool.Go(run)
	}
}

// RefundSync 等待资金和令牌退款结束，也会等待已开始的异步退款。
func (s *BillingSession) RefundSync(c *gin.Context) error {
	done, run := s.prepareRefund(c)
	if run != nil {
		run()
	}
	if done == nil {
		return nil
	}
	<-done
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.refundErr
}

func (s *BillingSession) prepareRefund(c *gin.Context) (<-chan struct{}, func()) {
	s.mu.Lock()
	if s.refundDone != nil {
		done := s.refundDone
		s.mu.Unlock()
		return done, nil
	}
	if !s.needsRefundLocked() {
		s.mu.Unlock()
		return nil, nil
	}
	s.refunded = true
	s.stopLeaseLocked()
	billingKey := s.durableBillingKey()
	userId := s.relayInfo.UserId
	tokenId := s.relayInfo.TokenId
	tokenKey := s.relayInfo.TokenKey
	isPlayground := s.relayInfo.IsPlayground
	tokenConsumed := s.tokenConsumed
	extraReserved := s.extraReserved
	subscriptionId := s.relayInfo.SubscriptionId
	subscriptionPeriodStart := s.relayInfo.SubscriptionPeriodStart
	funding := s.funding
	done := make(chan struct{})
	s.refundDone = done
	s.mu.Unlock()

	logger.LogInfo(c, fmt.Sprintf("用户 %d 请求失败, 返还预扣费（token_quota=%s, funding=%s）",
		userId, logger.FormatQuota(tokenConsumed), funding.Source()))

	return done, func() {
		refundErr := errors.New("退款未完成")
		defer func() {
			s.mu.Lock()
			s.refundErr = refundErr
			s.mu.Unlock()
			close(done)
		}()
		if billingKey != "" {
			refundErr = model.RefundBillingReservation(model.DB, billingKey, userId)
			if refundErr != nil {
				common.SysLog("error refunding durable billing reservation: " + refundErr.Error())
			}
			return
		}
		// 非账本资金来源保持原有顺序；一次会话只执行一次退款。
		fundingErr := funding.Refund()
		if fundingErr != nil {
			common.SysLog("error refunding billing source: " + fundingErr.Error())
		}
		var subscriptionErr error
		if extraReserved > 0 && funding.Source() == BillingSourceSubscription && subscriptionId > 0 {
			_, subscriptionErr = model.RefundUserSubscriptionDelta(
				subscriptionId, int64(extraReserved), subscriptionPeriodStart, common.GetTimestamp())
			if subscriptionErr != nil {
				common.SysLog("error refunding subscription extra reserved quota: " + subscriptionErr.Error())
			}
		}
		var tokenErr error
		if tokenConsumed > 0 && !isPlayground {
			tokenErr = model.IncreaseTokenQuotaDirect(tokenId, tokenKey, tokenConsumed)
			if tokenErr != nil {
				common.SysLog("error refunding token quota: " + tokenErr.Error())
			}
		}
		refundErr = errors.Join(fundingErr, subscriptionErr, tokenErr)
	}
}

// NeedsRefund 返回是否存在需要退还的预扣状态。
func (s *BillingSession) NeedsRefund() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.needsRefundLocked()
}

func (s *BillingSession) needsRefundLocked() bool {
	if s.settled || s.refunded || s.fundingSettled {
		// fundingSettled 时资金来源已提交结算，不能再退预扣费
		return false
	}
	if s.durableBillingKey() != "" || s.tokenConsumed > 0 {
		return true
	}
	// Playground requests can reserve wallet quota without reserving a token.
	if wallet, ok := s.funding.(*WalletFunding); ok && wallet.consumed > 0 {
		return true
	}
	// 订阅可能在 tokenConsumed=0 时仍预扣了额度
	if sub, ok := s.funding.(*SubscriptionFunding); ok && sub.preConsumed > 0 {
		return true
	}
	return false
}

// GetPreConsumedQuota 返回实际预扣的额度。
func (s *BillingSession) GetPreConsumedQuota() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.preConsumedQuota
}

func (s *BillingSession) Reserve(targetQuota int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.settled || s.refunded || targetQuota <= s.preConsumedQuota {
		return nil
	}

	delta := targetQuota - s.preConsumedQuota
	if delta <= 0 {
		return nil
	}

	if key := s.durableBillingKey(); key != "" {
		if err := model.ResizeBillingReservation(model.DB, key, s.relayInfo.UserId, int64(s.preConsumedQuota), int64(targetQuota)); err != nil {
			return TaskWalletReserveError(err)
		}
		if wallet, ok := s.funding.(*WalletFunding); ok {
			wallet.consumed = targetQuota
		}
		s.preConsumedQuota = targetQuota
		if !s.relayInfo.IsPlayground {
			s.tokenConsumed += delta
		}
		s.extraReserved += delta
		s.syncRelayInfo()
		return nil
	}

	if err := s.reserveFunding(delta); err != nil {
		return err
	}
	if err := s.reserveToken(delta); err != nil {
		s.rollbackFundingReserve(delta)
		return err
	}

	s.preConsumedQuota += delta
	s.tokenConsumed += delta
	s.extraReserved += delta
	s.syncRelayInfo()
	return nil
}

func (s *BillingSession) durableBillingKey() string {
	switch funding := s.funding.(type) {
	case *WalletFunding:
		if funding.durableSettlement {
			return funding.settlementKey
		}
	case *SubscriptionFunding:
		return funding.billingKey
	}
	return ""
}

// MarkDispatched 在发送上游前保存屏障；写入失败时调用方必须停止发送。
func (s *BillingSession) MarkDispatched() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.settled || s.refunded {
		return errors.New("billing session already completed")
	}
	if key := s.durableBillingKey(); key != "" {
		return model.MarkBillingReservationDispatched(model.DB, key, s.relayInfo.UserId)
	}
	return nil
}

// TaskReservationKey transfers either funding source to task persistence.
func (s *BillingSession) TaskReservationKey() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.durableBillingKey()
}

// WalletReservationKey hands the same durable identity to asynchronous task
// persistence, including sessions that fell back from a subscription.
func (s *BillingSession) WalletReservationKey() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.funding.Source() == BillingSourceWallet {
		return s.durableBillingKey()
	}
	return ""
}

func (s *BillingSession) stopLeaseLocked() {
	if s.leaseStop != nil {
		close(s.leaseStop)
		s.leaseStop = nil
	}
}

func (s *BillingSession) startLease(c *gin.Context) {
	key := s.durableBillingKey()
	if key == "" {
		return
	}
	stop := make(chan struct{})
	s.leaseStop = stop
	db, userID := model.DB, s.relayInfo.UserId
	var done <-chan struct{}
	if c != nil && c.Request != nil {
		done = c.Request.Context().Done()
	}
	gopool.Go(func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-done:
				return
			case <-ticker.C:
				live, err := model.RenewBillingReservation(db, key, userID)
				if err != nil {
					common.SysError("billing reservation renewal failed: " + err.Error())
				} else if !live {
					return
				}
			}
		}
	})
}

// ---------------------------------------------------------------------------
// PreConsume — 统一预扣费入口
// ---------------------------------------------------------------------------

// preConsume 对持久资金来源原子预扣令牌和资金，兼容资金来源保留补偿路径。
// 所有付费请求都必须真实预扣，不能依赖进程内余额快照跳过预留。
func (s *BillingSession) preConsume(c *gin.Context, quota int) *types.NewAPIError {
	effectiveQuota := quota
	if effectiveQuota > 0 {
		logger.LogInfo(c, fmt.Sprintf("用户 %d 需要预扣费 %s (funding=%s)", s.relayInfo.UserId, logger.FormatQuota(effectiveQuota), s.funding.Source()))
	}

	// Durable sources own both balances in one transaction, including the
	// token identity needed by recovery before a task row is inserted.
	atomicFunding := false
	switch funding := s.funding.(type) {
	case *WalletFunding:
		atomicFunding = funding.settlementKey != ""
	case *SubscriptionFunding:
		atomicFunding = funding.billingKey != ""
	}
	// ---- 1) 兼容旧资金来源的令牌预扣 ----
	if effectiveQuota > 0 && !atomicFunding {
		if err := PreConsumeTokenQuota(s.relayInfo, effectiveQuota); err != nil {
			return types.NewErrorWithStatusCode(err, types.ErrorCodePreConsumeTokenQuotaFailed, http.StatusForbidden, types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog())
		}
		s.tokenConsumed = effectiveQuota
	}

	// ---- 2) 预扣资金来源 ----
	if err := s.funding.PreConsume(effectiveQuota); err != nil {
		// 预扣费失败，回滚令牌额度
		if s.tokenConsumed > 0 && !s.relayInfo.IsPlayground {
			if rollbackErr := model.IncreaseTokenQuotaDirect(s.relayInfo.TokenId, s.relayInfo.TokenKey, s.tokenConsumed); rollbackErr != nil {
				common.SysLog(fmt.Sprintf("error rolling back token quota (userId=%d, tokenId=%d, amount=%d, fundingErr=%s): %s",
					s.relayInfo.UserId, s.relayInfo.TokenId, s.tokenConsumed, err.Error(), rollbackErr.Error()))
			}
			s.tokenConsumed = 0
		}
		if errors.Is(err, model.ErrBillingTokenReserve) {
			return TaskWalletTokenPreConsumeError(err)
		}
		if errors.Is(err, model.ErrInsufficientQuota) {
			return types.NewErrorWithStatusCode(fmt.Errorf("用户额度不足"), types.ErrorCodeInsufficientUserQuota, http.StatusForbidden, types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog())
		}
		// TODO: model 层应定义哨兵错误（如 ErrNoActiveSubscription），用 errors.Is 替代字符串匹配
		errMsg := err.Error()
		if strings.Contains(errMsg, "no active subscription") || strings.Contains(errMsg, "subscription quota insufficient") {
			return types.NewErrorWithStatusCode(fmt.Errorf("订阅额度不足或未配置订阅: %s", errMsg), types.ErrorCodeInsufficientUserQuota, http.StatusForbidden, types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog())
		}
		return types.NewError(err, types.ErrorCodeUpdateDataError, types.ErrOptionWithSkipRetry())
	}

	s.preConsumedQuota = effectiveQuota
	if atomicFunding && !s.relayInfo.IsPlayground {
		s.tokenConsumed = effectiveQuota
	}

	// ---- 同步 RelayInfo 兼容字段 ----
	s.syncRelayInfo()
	s.startLease(c)

	return nil
}

func (s *BillingSession) reserveFunding(delta int) error {
	switch funding := s.funding.(type) {
	case *WalletFunding:
		if err := funding.ReserveMore(delta); err != nil {
			if errors.Is(err, model.ErrInsufficientQuota) {
				return types.NewErrorWithStatusCode(
					fmt.Errorf("用户额度不足"),
					types.ErrorCodeInsufficientUserQuota,
					http.StatusForbidden,
					types.ErrOptionWithSkipRetry(),
					types.ErrOptionWithNoRecordErrorLog(),
				)
			}
			return types.NewError(err, types.ErrorCodeUpdateDataError, types.ErrOptionWithSkipRetry())
		}
		return nil
	case *SubscriptionFunding:
		if err := model.ReserveUserSubscriptionDelta(funding.subscriptionId, int64(delta), funding.periodStart); err != nil {
			return types.NewErrorWithStatusCode(
				fmt.Errorf("订阅额度不足或未配置订阅: %s", err.Error()),
				types.ErrorCodeInsufficientUserQuota,
				http.StatusForbidden,
				types.ErrOptionWithSkipRetry(),
				types.ErrOptionWithNoRecordErrorLog(),
			)
		}
		return nil
	default:
		return types.NewError(fmt.Errorf("unsupported funding source: %s", s.funding.Source()), types.ErrorCodeUpdateDataError, types.ErrOptionWithSkipRetry())
	}
}

func (s *BillingSession) rollbackFundingReserve(delta int) {
	switch funding := s.funding.(type) {
	case *WalletFunding:
		if err := funding.ReleaseReserved(delta); err != nil {
			common.SysLog("error rolling back wallet funding reserve: " + err.Error())
		}
	case *SubscriptionFunding:
		if _, err := model.RefundUserSubscriptionDelta(
			funding.subscriptionId,
			int64(delta),
			funding.periodStart,
			funding.operationTime,
		); err != nil {
			common.SysLog("error rolling back subscription funding reserve: " + err.Error())
		}
	}
}

func (s *BillingSession) reserveToken(delta int) error {
	if delta <= 0 || s.relayInfo.IsPlayground {
		return nil
	}
	if err := PreConsumeTokenQuota(s.relayInfo, delta); err != nil {
		return types.NewErrorWithStatusCode(err, types.ErrorCodePreConsumeTokenQuotaFailed, http.StatusForbidden, types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog())
	}
	return nil
}

// syncRelayInfo 将 BillingSession 的状态同步到 RelayInfo 的兼容字段上。
func (s *BillingSession) syncRelayInfo() {
	info := s.relayInfo
	info.FinalPreConsumedQuota = s.preConsumedQuota
	info.BillingSource = s.funding.Source()

	if sub, ok := s.funding.(*SubscriptionFunding); ok {
		info.SubscriptionId = sub.subscriptionId
		info.SubscriptionPreConsumed = sub.preConsumed + int64(s.extraReserved)
		info.SubscriptionPeriodStart = sub.periodStart
		info.SubscriptionPostDelta = 0
		info.SubscriptionAmountTotal = sub.AmountTotal
		info.SubscriptionAmountUsedAfterPreConsume = sub.AmountUsedAfter + int64(s.extraReserved)
		info.SubscriptionPlanId = sub.PlanId
		info.SubscriptionPlanTitle = sub.PlanTitle
	} else {
		info.SubscriptionId = 0
		info.SubscriptionPreConsumed = 0
		info.SubscriptionPeriodStart = 0
	}
}

// ---------------------------------------------------------------------------
// NewBillingSession 工厂 — 根据计费偏好创建会话并处理回退
// ---------------------------------------------------------------------------

// NewBillingSession 根据用户计费偏好创建 BillingSession，处理 subscription_first / wallet_first 的回退。
func NewBillingSession(c *gin.Context, relayInfo *relaycommon.RelayInfo, preConsumedQuota int) (*BillingSession, *types.NewAPIError) {
	if relayInfo == nil {
		return nil, types.NewError(fmt.Errorf("relayInfo is nil"), types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
	}
	if preConsumedQuota < 0 {
		return nil, types.NewError(fmt.Errorf("pre-consume quota cannot be negative: %d", preConsumedQuota), types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
	}

	pref := common.NormalizeBillingPreference(relayInfo.UserSetting.BillingPreference)
	requestID := relayInfo.RequestId
	if requestID == "" {
		requestID = common.GetUUID()
		relayInfo.RequestId = requestID
	}
	tokenID := relayInfo.TokenId
	if relayInfo.IsPlayground {
		tokenID = 0
	}

	// 钱包路径需要先检查用户额度
	tryWallet := func() (*BillingSession, *types.NewAPIError) {
		userQuota, err := model.GetUserQuota(relayInfo.UserId, true)
		if err != nil {
			return nil, types.NewError(err, types.ErrorCodeQueryDataError, types.ErrOptionWithSkipRetry())
		}
		if userQuota <= 0 {
			return nil, types.NewErrorWithStatusCode(
				fmt.Errorf("用户额度不足, 剩余额度: %s", logger.FormatQuota(userQuota)),
				types.ErrorCodeInsufficientUserQuota, http.StatusForbidden,
				types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog())
		}
		if userQuota-preConsumedQuota < 0 {
			return nil, types.NewErrorWithStatusCode(
				fmt.Errorf("预扣费额度失败, 用户剩余额度: %s, 需要预扣费额度: %s", logger.FormatQuota(userQuota), logger.FormatQuota(preConsumedQuota)),
				types.ErrorCodeInsufficientUserQuota, http.StatusForbidden,
				types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog())
		}
		relayInfo.UserQuota = userQuota

		settlementKey, err := model.NewWalletFinalSettlementKey(relayInfo.UserId, "relay", requestID)
		if err != nil {
			return nil, types.NewError(err, types.ErrorCodeQueryDataError, types.ErrOptionWithSkipRetry())
		}
		walletFunding := &WalletFunding{userId: relayInfo.UserId, settlementKey: settlementKey, tokenId: tokenID}
		session := &BillingSession{
			relayInfo: relayInfo,
			funding:   walletFunding,
		}
		if apiErr := session.preConsume(c, preConsumedQuota); apiErr != nil {
			return nil, apiErr
		}
		return session, nil
	}

	trySubscription := func() (*BillingSession, *types.NewAPIError) {
		billingKey, err := model.NewWalletFinalSettlementKey(relayInfo.UserId, "subscription", requestID)
		if err != nil {
			return nil, types.NewError(err, types.ErrorCodeQueryDataError, types.ErrOptionWithSkipRetry())
		}
		subConsume := int64(preConsumedQuota)
		if subConsume <= 0 {
			subConsume = 1
		}
		session := &BillingSession{
			relayInfo: relayInfo,
			funding: &SubscriptionFunding{
				billingKey:    billingKey,
				tokenId:       tokenID,
				requestId:     relayInfo.RequestId,
				userId:        relayInfo.UserId,
				modelName:     relayInfo.OriginModelName,
				amount:        subConsume,
				operationTime: common.GetTimestamp(),
			},
		}
		// 必须传 subConsume 而非 preConsumedQuota，保证 SubscriptionFunding.amount、
		// preConsume 参数和 FinalPreConsumedQuota 三者一致，避免订阅多扣费。
		if apiErr := session.preConsume(c, int(subConsume)); apiErr != nil {
			return nil, apiErr
		}
		return session, nil
	}

	switch pref {
	case "subscription_only":
		return trySubscription()
	case "wallet_only":
		return tryWallet()
	case "wallet_first":
		session, err := tryWallet()
		if err != nil {
			if err.GetErrorCode() == types.ErrorCodeInsufficientUserQuota {
				return trySubscription()
			}
			return nil, err
		}
		return session, nil
	case "subscription_first":
		fallthrough
	default:
		hasSub, subCheckErr := model.HasActiveUserSubscription(relayInfo.UserId)
		if subCheckErr != nil {
			return nil, types.NewError(subCheckErr, types.ErrorCodeQueryDataError, types.ErrOptionWithSkipRetry())
		}
		if !hasSub {
			return tryWallet()
		}
		session, apiErr := trySubscription()
		if apiErr != nil {
			if apiErr.GetErrorCode() == types.ErrorCodeInsufficientUserQuota {
				allowed, policyErr := model.UserActiveSubscriptionsAllowWalletOverflow(relayInfo.UserId)
				if policyErr != nil {
					return nil, types.NewError(policyErr, types.ErrorCodeQueryDataError, types.ErrOptionWithSkipRetry())
				}
				if !allowed {
					return nil, apiErr
				}
				return tryWallet()
			}
			return nil, apiErr
		}
		return session, nil
	}
}
