package service

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	// ContextKeyTaskWalletBillingKey carries the durable pending wallet
	// reservation key of the current asynchronous task submission. It is set
	// before the upstream submit and persisted onto the task row on success so
	// polling completion can finalize exactly one settlement.
	ContextKeyTaskWalletBillingKey = "task_wallet_billing_key"
	// ContextKeyTaskWalletBillingQuota carries the amount reserved for
	// ContextKeyTaskWalletBillingKey. The task row is inserted with this amount,
	// then aligned to the final submit quota via ReserveTaskWalletQuota.
	ContextKeyTaskWalletBillingQuota = "task_wallet_billing_quota"
)

// LogTaskConsumption 记录任务消费日志和统计信息（仅记录，不涉及实际扣费）。
// 实际扣费已由 BillingSession（PreConsumeBilling + SettleBilling）完成。
func LogTaskConsumption(c *gin.Context, info *relaycommon.RelayInfo) {
	tokenName := c.GetString("token_name")
	logContent := fmt.Sprintf("操作 %s", info.Action)
	// 支持任务仅按次计费
	if common.StringsContains(constant.TaskPricePatches, info.OriginModelName) {
		logContent = fmt.Sprintf("%s，按次计费", logContent)
	} else {
		if len(info.PriceData.OtherRatios) > 0 {
			var contents []string
			for key, ra := range info.PriceData.OtherRatios {
				if 1.0 != ra {
					contents = append(contents, fmt.Sprintf("%s: %.2f", key, ra))
				}
			}
			if len(contents) > 0 {
				logContent = fmt.Sprintf("%s, 计算参数：%s", logContent, strings.Join(contents, ", "))
			}
		}
	}
	other := make(map[string]interface{})
	other["is_task"] = true
	other["request_path"] = c.Request.URL.Path
	other["model_price"] = info.PriceData.ModelPrice
	if info.PriceData.ModelRatio > 0 {
		other["model_ratio"] = info.PriceData.ModelRatio
	}
	other["group_ratio"] = info.PriceData.GroupRatioInfo.GroupRatio
	if info.PriceData.GroupRatioInfo.HasSpecialRatio {
		other["user_group_ratio"] = info.PriceData.GroupRatioInfo.GroupSpecialRatio
	}
	if info.IsModelMapped {
		other["is_model_mapped"] = true
		other["upstream_model_name"] = info.UpstreamModelName
	}
	attachQuotaSaturation(c, info, other)
	model.RecordConsumeLog(c, info.UserId, model.RecordConsumeLogParams{
		ChannelId: info.ChannelId,
		ModelName: info.OriginModelName,
		TokenName: tokenName,
		Quota:     info.PriceData.Quota,
		Content:   logContent,
		TokenId:   info.TokenId,
		Group:     info.UsingGroup,
		Other:     other,
	})
	model.UpdateUserUsedQuotaAndRequestCount(info.UserId, info.PriceData.Quota)
	model.UpdateChannelUsedQuota(info.ChannelId, info.PriceData.Quota)
}

// ---------------------------------------------------------------------------
// 异步任务计费辅助函数
// ---------------------------------------------------------------------------

// TaskWalletBillingKey identifies the durable wallet settlement identity of a
// task. A persisted BillingKey (created before the upstream submit) always
// wins; historical tasks without one keep the legacy task.ID derivation.
func TaskWalletBillingKey(task *model.Task) (string, error) {
	if task == nil || task.UserId <= 0 {
		return "", ErrWalletBillingIdentityRequired
	}
	if key := task.PrivateData.BillingKey; key != "" {
		if !model.IsCanonicalWalletBillingKey(key) {
			return "", fmt.Errorf("%w: persisted task billing key is malformed", ErrWalletBillingIdentityRequired)
		}
		return key, nil
	}
	if task.ID <= 0 {
		return "", ErrWalletBillingIdentityRequired
	}
	return model.NewWalletFinalSettlementKey(task.UserId, "task", strconv.FormatInt(task.ID, 10))
}

// ReservePendingTaskWalletQuota creates a server-owned durable reservation
// identity BEFORE the upstream submit and BEFORE any task row exists. The key
// must be persisted on the task row after a successful submit; submission
// failure must refund it through RefundPendingTaskWalletQuota. A crash between
// this call and the task insert intentionally leaves the reservation to the
// existing stale-reservation TTL cleanup; no consumption or reward can be
// produced without a persisted task identity.
func ReservePendingTaskWalletQuota(userID int, targetTotal int) (string, error) {
	if userID <= 0 {
		return "", ErrWalletBillingIdentityRequired
	}
	if targetTotal < 0 {
		return "", errors.New("task reservation quota cannot be negative")
	}
	key, err := model.NewWalletFinalSettlementKey(userID, "task-submit", common.GetUUID())
	if err != nil {
		return "", err
	}
	if err := model.ReserveWalletQuota(model.DB, key, userID, int64(targetTotal)); err != nil {
		return "", err
	}
	return key, nil
}

// RefundPendingTaskWalletQuota refunds a pending reservation that never reached
// a persisted task row. Refunding an already-refunded reservation is a no-op;
// a finalizing or finalized reservation returns a conflict instead of money.
func RefundPendingTaskWalletQuota(key string, userID int) error {
	if userID <= 0 || !model.IsCanonicalWalletBillingKey(key) {
		return ErrWalletBillingIdentityRequired
	}
	return model.RefundWalletReservation(model.DB, key, userID)
}

// TaskWalletLedgerUsesSubscription mirrors NewBillingSession's initial funding
// decision for asynchronous task submissions. The wallet ledger guard must
// only block wallet-funded tasks; subscription sessions are created directly
// by the task submit owner without changing subscription behavior.
func TaskWalletLedgerUsesSubscription(info *relaycommon.RelayInfo) (bool, error) {
	if info == nil {
		return false, errors.New("relayInfo is nil")
	}
	switch common.NormalizeBillingPreference(info.UserSetting.BillingPreference) {
	case "subscription_only":
		return true, nil
	case "subscription_first":
		return model.HasActiveUserSubscription(info.UserId)
	default:
		// wallet_only and wallet_first start from the wallet. wallet_first may
		// still fall back to the subscription when the wallet is short.
		return false, nil
	}
}

// IsTaskWalletInsufficientQuota reports whether err is the wallet shortfall
// that permits the wallet_first fallback to subscription.
func IsTaskWalletInsufficientQuota(err error) bool {
	return errors.Is(err, model.ErrInsufficientQuota) || errors.Is(err, model.ErrWalletInsufficientQuota)
}

// TaskWalletReserveError maps a pending reservation failure onto the same API
// error surface as BillingSession.preConsume so task submit responses keep
// their established status codes and codes.
func TaskWalletReserveError(err error) *types.NewAPIError {
	mapped := mapWalletReserveError(err)
	if errors.Is(mapped, model.ErrInsufficientQuota) {
		return types.NewErrorWithStatusCode(fmt.Errorf("用户额度不足"), types.ErrorCodeInsufficientUserQuota, http.StatusForbidden, types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog())
	}
	return types.NewError(mapped, types.ErrorCodeUpdateDataError, types.ErrOptionWithSkipRetry())
}

// TaskWalletTokenPreConsumeError maps a token pre-consume failure onto the same
// API error surface as BillingSession.preConsume.
func TaskWalletTokenPreConsumeError(err error) *types.NewAPIError {
	return types.NewErrorWithStatusCode(err, types.ErrorCodePreConsumeTokenQuotaFailed, http.StatusForbidden, types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog())
}

// TaskWalletQuotaClampError preserves PreConsumeBilling's quota-overflow guard
// for the ledger task path, which skips PreConsumeBilling.
func TaskWalletQuotaClampError(clamp *common.QuotaClamp) *types.NewAPIError {
	if clamp == nil {
		return nil
	}
	return types.NewErrorWithStatusCode(clamp, types.ErrorCodeModelPriceError, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
}

// lockTaskBillingTx reloads the authoritative identity and serializes token,
// quota and wallet changes. Do not save a stale polling snapshot over this row.
func lockTaskBillingTx(tx *gorm.DB, task *model.Task) (*model.Task, string, error) {
	key, err := TaskWalletBillingKey(task)
	if err != nil {
		return nil, "", err
	}
	query := tx.Where("id = ? AND user_id = ?", task.ID, task.UserId)
	if tx.Dialector.Name() == "sqlite" {
		r := query.Model(&model.Task{}).UpdateColumn("quota", gorm.Expr("quota"))
		if r.Error != nil {
			return nil, "", r.Error
		}
		if r.RowsAffected != 1 {
			return nil, "", ErrWalletBillingIdentityRequired
		}
	} else {
		query = query.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	var stored model.Task
	if err := query.First(&stored).Error; err != nil {
		return nil, "", err
	}
	if stored.TaskID != task.TaskID || taskIsSubscription(&stored) || stored.Quota < 0 {
		return nil, "", ErrWalletBillingIdentityRequired
	}
	return &stored, key, nil
}

// adjustTaskTokenTx is part of the same transaction as the reservation. A
// deleted token is no longer spendable; it must not prevent a wallet refund.
// A present token with different ownership, or a failed write, aborts everything.
func adjustTaskTokenTx(tx *gorm.DB, task *model.Task, delta int, reserve bool) error {
	if task.PrivateData.TokenId <= 0 || delta == 0 {
		return nil
	}
	var token model.Token
	q := tx.Where("id = ?", task.PrivateData.TokenId)
	if tx.Dialector.Name() != "sqlite" {
		q = q.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	if err := q.First(&token).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) && !reserve {
			return nil
		}
		return err
	}
	if token.UserId != task.UserId {
		return errors.New("task token ownership mismatch")
	}
	if (delta > 0 && (token.RemainQuota < math.MinInt+delta || token.UsedQuota > math.MaxInt-delta)) ||
		(delta < 0 && (token.RemainQuota > math.MaxInt+delta || token.UsedQuota < math.MinInt-delta)) {
		return errors.New("task token quota adjustment overflows")
	}
	remaining, used := token.RemainQuota-delta, token.UsedQuota+delta
	if reserve && !token.UnlimitedQuota && remaining < 0 {
		return model.ErrInsufficientQuota
	}
	r := tx.Model(&model.Token{}).
		Where("id = ? AND user_id = ? AND remain_quota = ? AND used_quota = ?", token.Id, task.UserId, token.RemainQuota, token.UsedQuota).
		Updates(map[string]interface{}{"remain_quota": remaining, "used_quota": used, "accessed_time": common.GetTimestamp()})
	if r.Error != nil {
		return r.Error
	}
	if r.RowsAffected != 1 {
		return errors.New("task token quota changed during billing")
	}
	return nil
}

func updateTaskBillingQuotaTx(tx *gorm.DB, task *model.Task, quota int) error {
	if task.Quota == quota {
		return nil
	}
	r := tx.Model(&model.Task{}).Where("id = ? AND user_id = ? AND quota = ?", task.ID, task.UserId, task.Quota).UpdateColumn("quota", quota)
	if r.Error != nil {
		return r.Error
	}
	if r.RowsAffected != 1 {
		return errors.New("task quota changed during billing")
	}
	return nil
}

func invalidateTaskBillingCache(task *model.Task) {
	// Invalidate, never apply a cache delta on an idempotent retry. A cache
	// failure after commit is logged, not returned as a failed financial write.
	if err := model.InvalidateUserCache(task.UserId); err != nil {
		common.SysError(fmt.Sprintf("task wallet cache invalidation: %v", err))
	}
	if task.PrivateData.TokenId > 0 {
		if err := model.InvalidateUserTokensCache(task.UserId); err != nil {
			common.SysError(fmt.Sprintf("task token cache invalidation: %v", err))
		}
	}
}

// ReserveTaskWalletQuota is the durable submit adapter. The submit owner must
// insert a task before calling it and persist TokenId/BillingSource first. The
// absolute total is retryable; an existing legacy debit is never reclassified
// as a reservation. This operation cannot create a consumption or a reward.
func ReserveTaskWalletQuota(task *model.Task, targetTotal int) error {
	if targetTotal < 0 {
		return errors.New("task reservation quota cannot be negative")
	}
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		stored, key, err := lockTaskBillingTx(tx, task)
		if err != nil {
			return err
		}
		if stored.Status == model.TaskStatusSuccess || stored.Status == model.TaskStatusFailure {
			return errors.New("cannot reserve quota for a terminal task")
		}
		var reservation model.WalletReservation
		reservationExists := false
		err = tx.Where("reservation_key = ? AND user_id = ?", key, stored.UserId).First(&reservation).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			if stored.Quota != 0 {
				return ErrWalletBillingIdentityRequired
			}
		} else if err != nil {
			return err
		} else {
			reservationExists = true
			if int64(stored.Quota) != reservation.ReservedQuota {
				return model.ErrWalletReservationConflict
			}
		}
		if reservationExists && int64(targetTotal) < reservation.ReservedQuota {
			// Downward alignment must release holds under an exact CAS target;
			// ReserveWalletQuotaTx only grows reservations.
			if err := model.ReleaseWalletReservationToTx(tx, key, stored.UserId, reservation.ReservedQuota, int64(targetTotal)); err != nil {
				return err
			}
		} else if err := model.ReserveWalletQuotaTx(tx, key, stored.UserId, int64(targetTotal)); err != nil {
			return err
		}
		if err := adjustTaskTokenTx(tx, stored, targetTotal-stored.Quota, true); err != nil {
			return err
		}
		return updateTaskBillingQuotaTx(tx, stored, targetTotal)
	})
	if err != nil {
		return err
	}
	task.Quota = targetTotal
	invalidateTaskBillingCache(task)
	return nil
}

// prepareTaskWalletFinalization commits the durable finalizing barrier before
// the final accounting transaction. Without it, a crash or transient DB error
// between a terminal task status and finalization would leave the reservation
// refundable by the stale-reservation TTL. Recovery replays finalizing wallet
// settlements; token/quota rows remain request-local as documented there.
func prepareTaskWalletFinalization(task *model.Task, actual int) error {
	key, err := TaskWalletBillingKey(task)
	if err != nil {
		return err
	}
	// Free or zero-reservation tasks have no wallet debit to finalize.
	if task.Quota == 0 && actual == 0 {
		return nil
	}
	var stored model.Task
	if err := model.DB.Where("id = ? AND user_id = ?", task.ID, task.UserId).First(&stored).Error; err != nil {
		return err
	}
	if stored.Status != model.TaskStatusSuccess || taskIsSubscription(&stored) || stored.Quota < 0 {
		return errors.New("task status does not permit this billing outcome")
	}
	var r model.WalletReservation
	if err := model.DB.Where("reservation_key = ? AND user_id = ?", key, stored.UserId).First(&r).Error; err != nil {
		return fmt.Errorf("task wallet reservation unavailable: %w", err)
	}
	// An identical retry after a committed finalization must be a clean no-op;
	// the final transaction returns the existing settlement unchanged.
	if r.State == model.WalletReservationFinalized && r.FinalQuota == int64(actual) {
		return nil
	}
	if int64(stored.Quota) != r.ReservedQuota {
		return model.ErrWalletReservationConflict
	}
	return model.PrepareWalletFinalization(model.DB, key, stored.UserId, r.ReservedQuota, int64(actual))
}

// settleTaskWalletQuota atomically terminates a task reservation together with
// its token and quota. A retry uses the reservation's original pre-consumption,
// not task.Quota (which now holds the final total). Changed final totals conflict
// rather than turning adjustments into additional reward-bearing consumptions.
func settleTaskWalletQuota(task *model.Task, actual int, refund bool) (bool, int, error) {
	if actual < 0 {
		return false, 0, errors.New("actual task quota cannot be negative")
	}
	// Finalization is two-phase: the durable finalizing barrier must commit
	// before the accounting transaction, so a crash can be replayed safely.
	if !refund {
		if err := prepareTaskWalletFinalization(task, actual); err != nil {
			return false, 0, err
		}
	}
	applied, delta, paidInviterID := false, 0, 0
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		stored, key, err := lockTaskBillingTx(tx, task)
		if err != nil {
			return err
		}
		if (!refund && stored.Status != model.TaskStatusSuccess) || (refund && stored.Status == model.TaskStatusSuccess) {
			return errors.New("task status does not permit this billing outcome")
		}
		var r model.WalletReservation
		if err := tx.Where("reservation_key = ? AND user_id = ?", key, stored.UserId).First(&r).Error; err != nil {
			// A free or legacy task has no wallet debit to settle or refund.
			if errors.Is(err, gorm.ErrRecordNotFound) && stored.Quota == 0 && actual == 0 && !refund {
				return nil
			}
			return fmt.Errorf("task wallet reservation unavailable: %w", err)
		}
		if r.ReservedQuota < 0 || r.ReservedQuota > int64(math.MaxInt) {
			return model.ErrWalletReservationConflict
		}
		if refund {
			if err := model.RefundWalletReservationTx(tx, key, stored.UserId); err != nil {
				return err
			}
			if r.State == model.WalletReservationRefunded {
				return nil
			}
			delta = -int(r.ReservedQuota)
		} else {
			res, err := model.FinalizeWalletConsumptionTx(tx, key, stored.UserId, r.ReservedQuota, int64(actual))
			if err != nil {
				return err
			}
			if res != nil && res.PaidInviterID > 0 {
				paidInviterID = res.PaidInviterID
			}
			if r.State == model.WalletReservationFinalized {
				return nil
			}
			delta = actual - int(r.ReservedQuota)
		}
		if stored.Quota != int(r.ReservedQuota) {
			return model.ErrWalletReservationConflict
		}
		if err := adjustTaskTokenTx(tx, stored, delta, false); err != nil {
			return err
		}
		if err := updateTaskBillingQuotaTx(tx, stored, actual); err != nil {
			return err
		}
		applied = true
		return nil
	})
	if err != nil {
		return false, 0, err
	}
	task.Quota = actual
	invalidateTaskBillingCache(task)
	if paidInviterID > 0 {
		if err := model.InvalidateUserCache(paidInviterID); err != nil {
			common.SysError(fmt.Sprintf("task rebate inviter cache invalidation: %v", err))
		}
	}
	return applied, delta, nil
}

// quotaNotifySender consumes a post-settlement user snapshot. Never pass this
// snapshot to a delta-based checker or the successful usage is subtracted twice.
var quotaNotifySender = checkAndSendRemainingQuotaNotify

// FinalizeTaskWalletConsumption commits the no-refund barrier before accounting.
// Only the transaction that creates the settlement may notify; idempotent
// replays are silent. Recovery can complete a failed accounting transaction.
func FinalizeTaskWalletConsumption(ctx context.Context, key string, userID, reserved, actual int) error {
	if userID <= 0 || reserved < 0 || actual < 0 || !model.IsCanonicalWalletBillingKey(key) {
		return ErrWalletBillingIdentityRequired
	}
	if err := model.PrepareWalletFinalization(model.DB, key, userID, int64(reserved), int64(actual)); err != nil {
		return err
	}
	result, err := model.FinalizeWalletConsumption(model.DB, key, userID, int64(reserved), int64(actual))
	if err != nil {
		return err
	}
	if result.Applied {
		NotifyTaskQuotaConsumed(ctx, userID, actual)
	}
	return nil
}

// NotifyTaskQuotaConsumed mirrors the legacy settlement notification for
// ledger-backed tasks whose final accounting happens in the polling loop. The
// user row is reloaded so the remaining balance and notification preferences
// are current instead of a stale submit-time snapshot.
func NotifyTaskQuotaConsumed(ctx context.Context, userID, actualQuota int) {
	if userID <= 0 || actualQuota <= 0 {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var user model.User
	if err := model.DB.Select("id", "email", "quota", "setting").First(&user, userID).Error; err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("task quota notification skipped for user %d: %v", userID, err))
		return
	}
	info := &relaycommon.RelayInfo{
		UserId:      user.Id,
		UserEmail:   user.Email,
		UserQuota:   user.Quota,
		UserSetting: user.GetSetting(),
	}
	quotaNotifySender(info)
}

// resolveTokenKey 通过 TokenId 运行时获取令牌 Key（用于 Redis 缓存操作）。
// 如果令牌已被删除或查询失败，返回空字符串。
func resolveTokenKey(ctx context.Context, tokenId int, taskID string) string {
	token, err := model.GetTokenById(tokenId)
	if err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("获取令牌 key 失败 (tokenId=%d, task=%s): %s", tokenId, taskID, err.Error()))
		return ""
	}
	return token.Key
}

// taskIsSubscription 判断任务是否通过订阅计费。
func taskIsSubscription(task *model.Task) bool {
	return task.PrivateData.BillingSource == BillingSourceSubscription && task.PrivateData.SubscriptionId > 0
}

// taskAdjustFunding 调整任务的资金来源（钱包或订阅），delta > 0 表示扣费，delta < 0 表示退还。
// applied=false 表示订阅已进入新额度周期，旧周期退款被安全丢弃。
func taskAdjustFunding(task *model.Task, delta int) (applied bool, err error) {
	if taskIsSubscription(task) {
		operationTime := task.SubmitTime
		if operationTime <= 0 {
			operationTime = task.CreatedAt
		}
		if delta > 0 {
			return model.SettleUserSubscriptionDelta(
				task.PrivateData.SubscriptionId,
				int64(delta),
				task.PrivateData.SubscriptionPeriodStart,
				operationTime,
			)
		}
		return model.RefundUserSubscriptionDelta(
			task.PrivateData.SubscriptionId,
			int64(-delta),
			task.PrivateData.SubscriptionPeriodStart,
			operationTime,
		)
	}
	if delta > 0 {
		return true, model.DecreaseUserQuotaDirect(task.UserId, delta)
	}
	return true, model.IncreaseUserQuotaDirect(task.UserId, -delta)
}

// taskAdjustTokenQuota 调整任务的令牌额度，delta > 0 表示扣费，delta < 0 表示退还。
// 需要通过 resolveTokenKey 运行时获取 key（不从 PrivateData 中读取）。
func taskAdjustTokenQuota(ctx context.Context, task *model.Task, delta int) {
	if task.PrivateData.TokenId <= 0 || delta == 0 {
		return
	}
	tokenKey := resolveTokenKey(ctx, task.PrivateData.TokenId, task.TaskID)
	if tokenKey == "" {
		return
	}
	var err error
	if delta > 0 {
		err = model.DecreaseTokenQuotaDirect(task.PrivateData.TokenId, tokenKey, delta)
	} else {
		err = model.IncreaseTokenQuotaDirect(task.PrivateData.TokenId, tokenKey, -delta)
	}
	if err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("调整令牌额度失败 (delta=%d, task=%s): %s", delta, task.TaskID, err.Error()))
	}
}

// taskBillingOther 从 task 的 BillingContext 构建日志 Other 字段。
func taskBillingOther(task *model.Task) map[string]interface{} {
	other := make(map[string]interface{})
	if bc := task.PrivateData.BillingContext; bc != nil {
		other["model_price"] = bc.ModelPrice
		if bc.ModelRatio > 0 {
			other["model_ratio"] = bc.ModelRatio
		}
		other["group_ratio"] = bc.GroupRatio
		if len(bc.OtherRatios) > 0 {
			for k, v := range bc.OtherRatios {
				other[k] = v
			}
		}
	}
	props := task.Properties
	if props.UpstreamModelName != "" && props.UpstreamModelName != props.OriginModelName {
		other["is_model_mapped"] = true
		other["upstream_model_name"] = props.UpstreamModelName
	}
	return other
}

// taskModelName 从 BillingContext 或 Properties 中获取模型名称。
func taskModelName(task *model.Task) string {
	if bc := task.PrivateData.BillingContext; bc != nil && bc.OriginModelName != "" {
		return bc.OriginModelName
	}
	return task.Properties.OriginModelName
}

// RefundTaskQuota 统一的任务失败退款逻辑。
// 当异步任务失败时，将预扣的 quota 退还给用户（支持钱包和订阅），并退还令牌额度。
func RefundTaskQuota(ctx context.Context, task *model.Task, reason string) {
	if task == nil || task.Quota < 0 {
		return
	}
	quota := task.Quota
	if quota == 0 {
		return
	}
	if !taskIsSubscription(task) {
		active, err := model.WalletCreditLedgerActive(model.DB)
		if err != nil {
			logger.LogError(ctx, fmt.Sprintf("查询任务钱包账本失败: %v", err))
			return
		}
		if active {
			applied, delta, err := settleTaskWalletQuota(task, 0, true)
			if err != nil {
				logger.LogError(ctx, fmt.Sprintf("任务钱包退款失败 task %s: %v", task.TaskID, err))
				return
			}
			if applied && delta != 0 {
				other := taskBillingOther(task)
				other["task_id"], other["reason"] = task.TaskID, reason
				model.RecordTaskBillingLog(model.RecordTaskBillingLogParams{UserId: task.UserId, LogType: model.LogTypeRefund, ChannelId: task.ChannelId, ModelName: taskModelName(task), Quota: -delta, TokenId: task.PrivateData.TokenId, Group: task.Group, Other: other})
			}
			return
		}
	}

	// 1. 退还资金来源（钱包或订阅）
	fundingRefunded, err := taskAdjustFunding(task, -quota)
	if err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("退还资金来源失败 task %s: %s", task.TaskID, err.Error()))
		return
	}
	if !fundingRefunded {
		logger.LogInfo(ctx, fmt.Sprintf("任务 %s 属于已结束的订阅额度周期，不向新周期退还额度", task.TaskID))
	}

	// 2. 退还令牌额度
	taskAdjustTokenQuota(ctx, task, -quota)

	// 3. 记录日志
	other := taskBillingOther(task)
	other["task_id"] = task.TaskID
	other["reason"] = reason
	if !fundingRefunded {
		other["subscription_period_expired"] = true
	}
	model.RecordTaskBillingLog(model.RecordTaskBillingLogParams{
		UserId:    task.UserId,
		LogType:   model.LogTypeRefund,
		Content:   "",
		ChannelId: task.ChannelId,
		ModelName: taskModelName(task),
		Quota:     quota,
		TokenId:   task.PrivateData.TokenId,
		Group:     task.Group,
		Other:     other,
	})

	task.Quota = 0
	if err := task.UpdateQuota(); err != nil {
		logger.LogError(ctx, fmt.Sprintf("退款成功但清除 task quota 失败 task %s: %s", task.TaskID, err.Error()))
	}
}

// RecalculateTaskQuota 通用的异步差额结算。
// actualQuota 是任务完成后的实际应扣额度，与预扣额度 (task.Quota) 做差额结算。
// reason 用于日志记录（例如 "token重算" 或 "adaptor调整"）。
func RecalculateTaskQuota(ctx context.Context, task *model.Task, actualQuota int, reason string, clamps ...*common.QuotaClamp) {
	if task == nil || task.Quota < 0 || actualQuota < 0 {
		return
	}
	if actualQuota == 0 && task.Quota == 0 {
		return
	}
	if !taskIsSubscription(task) {
		active, err := model.WalletCreditLedgerActive(model.DB)
		if err != nil {
			logger.LogError(ctx, fmt.Sprintf("查询任务钱包账本失败: %v", err))
			return
		}
		if active {
			applied, delta, err := settleTaskWalletQuota(task, actualQuota, false)
			if err != nil {
				logger.LogError(ctx, fmt.Sprintf("任务钱包最终结算失败 task %s: %v", task.TaskID, err))
				return
			}
			if applied && delta != 0 {
				logType, logQuota := model.LogTypeConsume, delta
				if delta < 0 {
					logType, logQuota = model.LogTypeRefund, -delta
				} else {
					model.UpdateUserUsedQuotaAndRequestCount(task.UserId, delta)
					model.UpdateChannelUsedQuota(task.ChannelId, delta)
				}
				other := taskBillingOther(task)
				other["task_id"], other["actual_quota"], other["pre_consumed_quota"] = task.TaskID, actualQuota, actualQuota-delta
				for _, clamp := range clamps {
					if clamp != nil {
						attachQuotaSaturationToOther(other, clamp)
						break
					}
				}
				model.RecordTaskBillingLog(model.RecordTaskBillingLogParams{UserId: task.UserId, LogType: logType, Content: reason, ChannelId: task.ChannelId, ModelName: taskModelName(task), Quota: logQuota, TokenId: task.PrivateData.TokenId, Group: task.Group, Other: other})
			}
			if applied {
				// The submit path no longer settles ledger wallet tasks, so notify only
				// after the new settlement transaction actually charged the wallet.
				NotifyTaskQuotaConsumed(ctx, task.UserId, actualQuota)
			}
			return
		}
	}
	if actualQuota == 0 {
		return
	}
	preConsumedQuota := task.Quota
	quotaDelta := actualQuota - preConsumedQuota

	if quotaDelta == 0 {
		logger.LogInfo(ctx, fmt.Sprintf("任务 %s 预扣费准确（%s，%s）",
			task.TaskID, logger.LogQuota(actualQuota), reason))
		return
	}

	logger.LogInfo(ctx, fmt.Sprintf("任务 %s 差额结算：delta=%s（实际：%s，预扣：%s，%s）",
		task.TaskID,
		logger.LogQuota(quotaDelta),
		logger.LogQuota(actualQuota),
		logger.LogQuota(preConsumedQuota),
		reason,
	))

	// 调整资金来源
	fundingAdjusted, err := taskAdjustFunding(task, quotaDelta)
	if err != nil {
		logger.LogError(ctx, fmt.Sprintf("差额结算资金调整失败 task %s: %s", task.TaskID, err.Error()))
		return
	}
	if !fundingAdjusted && quotaDelta < 0 {
		logger.LogInfo(ctx, fmt.Sprintf("任务 %s 属于已结束的订阅额度周期，不向新周期返还差额", task.TaskID))
	}

	// 调整令牌额度
	taskAdjustTokenQuota(ctx, task, quotaDelta)

	task.Quota = actualQuota
	if err := task.UpdateQuota(); err != nil {
		logger.LogError(ctx, fmt.Sprintf("差额结算回写 quota 失败 task %s: %s", task.TaskID, err.Error()))
	}

	var logType int
	var logQuota int
	if quotaDelta > 0 {
		logType = model.LogTypeConsume
		logQuota = quotaDelta
		model.UpdateUserUsedQuotaAndRequestCount(task.UserId, quotaDelta)
		model.UpdateChannelUsedQuota(task.ChannelId, quotaDelta)
	} else {
		logType = model.LogTypeRefund
		logQuota = -quotaDelta
	}
	other := taskBillingOther(task)
	other["task_id"] = task.TaskID
	other["pre_consumed_quota"] = preConsumedQuota
	other["actual_quota"] = actualQuota
	if !fundingAdjusted {
		other["subscription_period_expired"] = true
	}
	for _, clamp := range clamps {
		if clamp != nil {
			attachQuotaSaturationToOther(other, clamp)
			break
		}
	}
	model.RecordTaskBillingLog(model.RecordTaskBillingLogParams{
		UserId:    task.UserId,
		LogType:   logType,
		Content:   reason,
		ChannelId: task.ChannelId,
		ModelName: taskModelName(task),
		Quota:     logQuota,
		TokenId:   task.PrivateData.TokenId,
		Group:     task.Group,
		Other:     other,
	})
}

// RecalculateTaskQuotaByTokens 根据实际 token 消耗重新计费（异步差额结算）。
// 当任务成功且返回了 totalTokens 时，根据模型倍率和分组倍率重新计算实际扣费额度，
// 与预扣费的差额进行补扣或退还。支持钱包和订阅计费来源。
func RecalculateTaskQuotaByTokens(ctx context.Context, task *model.Task, totalTokens int) {
	if totalTokens <= 0 {
		return
	}

	modelName := taskModelName(task)

	// 获取模型价格和倍率
	modelRatio, hasRatioSetting, _ := ratio_setting.GetModelRatio(modelName)
	// 只有配置了倍率(非固定价格)时才按 token 重新计费
	if !hasRatioSetting || modelRatio <= 0 {
		return
	}

	// 获取用户和组的倍率信息
	group := task.Group
	if group == "" {
		user, err := model.GetUserById(task.UserId, false)
		if err == nil {
			group = user.Group
		}
	}
	if group == "" {
		return
	}

	groupRatio := ratio_setting.GetGroupRatio(group)
	userGroupRatio, hasUserGroupRatio := ratio_setting.GetGroupGroupRatio(group, group)

	var finalGroupRatio float64
	if hasUserGroupRatio {
		finalGroupRatio = userGroupRatio
	} else {
		finalGroupRatio = groupRatio
	}
	finalGroupRatio, _, _ = model.ResolveUserGroupRatio(task.UserId, group, finalGroupRatio)

	// 计算 OtherRatios 乘积（视频折扣、时长等）
	otherMultiplier := 1.0
	if bc := task.PrivateData.BillingContext; bc != nil {
		for _, r := range bc.OtherRatios {
			if r != 1.0 && r > 0 {
				otherMultiplier *= r
			}
		}
	}

	// 计算实际应扣费额度: totalTokens * modelRatio * groupRatio * otherMultiplier
	actualQuota, clamp := common.QuotaFromFloatChecked(float64(totalTokens) * modelRatio * finalGroupRatio * otherMultiplier)

	reason := fmt.Sprintf("token重算：tokens=%d, modelRatio=%.2f, groupRatio=%.2f, otherMultiplier=%.4f", totalTokens, modelRatio, finalGroupRatio, otherMultiplier)
	RecalculateTaskQuota(ctx, task, actualQuota, reason, clamp)
}
