package controller

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/Calcium-Ion/go-epay/epay"
	"github.com/gin-gonic/gin"
	"github.com/samber/lo"
	"github.com/shopspring/decimal"
)

func GetTopUpInfo(c *gin.Context) {
	complianceConfirmed := operation_setting.IsPaymentComplianceConfirmed()

	// 获取支付方式
	payMethods := operation_setting.PayMethods
	if !complianceConfirmed {
		payMethods = []map[string]string{}
	}

	// 如果启用了 Stripe 支付，添加到支付方法列表
	if isStripeTopUpEnabled() {
		// 检查是否已经包含 Stripe
		hasStripe := false
		for _, method := range payMethods {
			if method["type"] == "stripe" {
				hasStripe = true
				break
			}
		}

		if !hasStripe {
			stripeMethod := map[string]string{
				"name":      "Stripe",
				"type":      "stripe",
				"color":     "rgba(var(--semi-purple-5), 1)",
				"min_topup": strconv.Itoa(setting.StripeMinTopUp),
			}
			payMethods = append(payMethods, stripeMethod)
		}
	}

	// Waffo Pancake displayed above the legacy Waffo gateway.
	enableWaffoPancake := isWaffoPancakeTopUpEnabled()
	if enableWaffoPancake {
		hasWaffoPancake := false
		for _, method := range payMethods {
			if method["type"] == model.PaymentMethodWaffoPancake {
				hasWaffoPancake = true
				break
			}
		}

		if !hasWaffoPancake {
			payMethods = append(payMethods, map[string]string{
				"name":      "Waffo Pancake",
				"type":      model.PaymentMethodWaffoPancake,
				"color":     "rgba(var(--semi-orange-5), 1)",
				"min_topup": strconv.Itoa(setting.WaffoPancakeMinTopUp),
			})
		}
	}

	// 如果启用了 Waffo 支付，添加到支付方法列表
	enableWaffo := isWaffoTopUpEnabled()
	if enableWaffo {
		hasWaffo := false
		for _, method := range payMethods {
			if method["type"] == model.PaymentMethodWaffo {
				hasWaffo = true
				break
			}
		}

		if !hasWaffo {
			waffoMethod := map[string]string{
				"name":      "Waffo (Global Payment)",
				"type":      model.PaymentMethodWaffo,
				"color":     "rgba(var(--semi-blue-5), 1)",
				"min_topup": strconv.Itoa(setting.WaffoMinTopUp),
			}
			payMethods = append(payMethods, waffoMethod)
		}
	}

	data := gin.H{
		"enable_online_topup":              isEpayTopUpEnabled(),
		"enable_stripe_topup":              isStripeTopUpEnabled(),
		"enable_creem_topup":               isCreemTopUpEnabled(),
		"enable_waffo_topup":               enableWaffo,
		"enable_waffo_pancake_topup":       enableWaffoPancake,
		"enable_redemption":                complianceConfirmed,
		"payment_compliance_confirmed":     complianceConfirmed,
		"payment_compliance_terms_version": operation_setting.CurrentComplianceTermsVersion,
		"waffo_pay_methods": func() interface{} {
			if enableWaffo {
				return setting.GetWaffoPayMethods()
			}
			return nil
		}(),
		"creem_products":          setting.CreemProducts,
		"pay_methods":             payMethods,
		"min_topup":               operation_setting.MinTopUp,
		"stripe_min_topup":        setting.StripeMinTopUp,
		"waffo_min_topup":         setting.WaffoMinTopUp,
		"waffo_pancake_min_topup": setting.WaffoPancakeMinTopUp,
		"amount_options":          operation_setting.GetPaymentSetting().AmountOptions,
		"discount":                operation_setting.GetPaymentSetting().AmountDiscount,
		"topup_link":              common.TopUpLink,
	}
	common.ApiSuccess(c, data)
}

type EpayRequest struct {
	Amount        int64  `json:"amount"`
	PaymentMethod string `json:"payment_method"`
}

type AmountRequest struct {
	Amount int64 `json:"amount"`
}

func GetEpayClient() *epay.Client {
	if operation_setting.PayAddress == "" || operation_setting.EpayId == "" || operation_setting.EpayKey == "" {
		return nil
	}
	withUrl, err := epay.NewClient(&epay.Config{
		PartnerID: operation_setting.EpayId,
		Key:       operation_setting.EpayKey,
	}, operation_setting.PayAddress)
	if err != nil {
		return nil
	}
	return withUrl
}

func getPayMoney(amount int64, group string) float64 {
	if !validTopUpPricing(group, operation_setting.Price, amount) {
		return math.NaN()
	}
	dAmount := decimal.NewFromInt(amount)
	// 充值金额以“展示类型”为准：
	// - USD/CNY: 前端传 amount 为金额单位；TOKENS: 前端传 tokens，需要换成 USD 金额
	if operation_setting.GetQuotaDisplayType() == operation_setting.QuotaDisplayTypeTokens {
		dQuotaPerUnit := decimal.NewFromFloat(common.QuotaPerUnit)
		dAmount = dAmount.Div(dQuotaPerUnit)
	}

	topupGroupRatio := common.GetTopupGroupRatio(group)
	if topupGroupRatio == 0 {
		topupGroupRatio = 1
	}

	dTopupGroupRatio := decimal.NewFromFloat(topupGroupRatio)
	dPrice := decimal.NewFromFloat(operation_setting.Price)
	// apply optional preset discount by the original request amount (if configured), default 1.0
	discount := 1.0
	if ds, ok := operation_setting.GetPaymentSetting().AmountDiscount[int(amount)]; ok {
		if ds > 0 {
			discount = ds
		}
	}
	dDiscount := decimal.NewFromFloat(discount)

	payMoney := dAmount.Mul(dPrice).Mul(dTopupGroupRatio).Mul(dDiscount)

	return payMoney.InexactFloat64()
}

// Pricing guards reject unusable configuration without changing the legacy
// zero group-ratio / non-positive discount fallbacks or any charge formula.
func validTopUpPricing(group string, price float64, amount int64) bool {
	ratio := common.GetTopupGroupRatio(group)
	discount := operation_setting.GetPaymentSetting().AmountDiscount[int(amount)]
	_, err := model.TopUpQuotaPerUnit()
	return err == nil && price > 0 && ratio >= 0 &&
		finiteTopUpNumber(price) && finiteTopUpNumber(ratio) && finiteTopUpNumber(discount)
}

func finiteTopUpNumber(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

func rejectInvalidTopUpMoney(c *gin.Context, money float64) bool {
	if money > 0 && finiteTopUpNumber(money) {
		return false
	}
	return rejectTopUpQuota(c, model.ErrPaymentConfirmationInvalid)
}

// checkedTopUpInt64 preserves truncation, but never lets IntPart wrap around.
func checkedTopUpInt64(value decimal.Decimal) (int64, error) {
	value = value.Truncate(0)
	if value.IsNegative() || value.GreaterThan(decimal.NewFromInt(math.MaxInt64)) {
		return 0, model.ErrTopUpQuotaOutOfRange
	}
	return value.IntPart(), nil
}

func getMinTopup() (int64, error) {
	qpu, err := model.TopUpQuotaPerUnit()
	if err != nil || operation_setting.MinTopUp < 0 {
		return 0, model.ErrInvalidTopUpQuota
	}
	minimum := decimal.NewFromInt(int64(operation_setting.MinTopUp))
	if operation_setting.GetQuotaDisplayType() == operation_setting.QuotaDisplayTypeTokens {
		minimum = minimum.Mul(qpu)
	}
	return checkedTopUpInt64(minimum)
}

// normalizeEpayTopUpAmount mirrors the amount stored for Epay orders. It keeps
// the legacy truncation (no minimum clamp) so the advisory quota pre-check
// validates exactly the amount the settlement path later converts.
func normalizeEpayTopUpAmount(amount int64) (int64, error) {
	qpu, err := model.TopUpQuotaPerUnit()
	if err != nil || amount <= 0 {
		return 0, model.ErrInvalidTopUpQuota
	}
	if operation_setting.GetQuotaDisplayType() == operation_setting.QuotaDisplayTypeTokens {
		return checkedTopUpInt64(decimal.NewFromInt(amount).Div(qpu))
	}
	return amount, nil
}

// normalizeWaffoTopUpAmount mirrors the float division and minimum of 1 used by
// Waffo order creation, so the pre-check validates the same amount that will be
// stored and later converted by the settlement path.
func normalizeWaffoTopUpAmount(amount int64) (int64, error) {
	if _, err := model.TopUpQuotaPerUnit(); err != nil || amount <= 0 {
		return 0, model.ErrInvalidTopUpQuota
	}
	if operation_setting.GetQuotaDisplayType() == operation_setting.QuotaDisplayTypeTokens {
		normalized := float64(amount) / common.QuotaPerUnit
		// float64(MaxInt64) rounds UP to 2^63, which is already out of range.
		if math.IsNaN(normalized) || math.IsInf(normalized, 0) || normalized >= 0x1p63 {
			return 0, model.ErrTopUpQuotaOutOfRange
		}
		amount = int64(normalized)
		if amount < 1 {
			amount = 1
		}
	}
	return amount, nil
}

// maxTopUpAmount is a currency-mode hint only, not a validation rule. Use an
// exact quotient and saturate before IntPart even with subnormal QPU values.
func maxTopUpAmount() int64 {
	qpu, err := model.TopUpQuotaPerUnit()
	if err != nil {
		return 0
	}
	maximum, _ := decimal.NewFromInt(int64(model.MaxStoredUserQuota())).QuoRem(qpu, 0)
	if maximum.GreaterThan(decimal.NewFromInt(math.MaxInt64)) {
		return math.MaxInt64
	}
	return maximum.IntPart()
}

// rejectTopUpQuota writes the unified top-up rejection response (HTTP 200 with
// {"message":"error","data":...}) and reports whether the request was rejected.
func rejectTopUpQuota(c *gin.Context, err error) bool {
	if err == nil {
		return false
	}
	// Never expose wrapped database errors (or their SQL/credentials) to users.
	message := "充值校验失败，请稍后重试"
	for _, publicErr := range []error{model.ErrInvalidTopUpQuota, model.ErrTopUpQuotaOutOfRange, model.ErrTopUpUserNotFound, model.ErrTopUpQuotaLimitExceeded} {
		if errors.Is(err, publicErr) {
			message = publicErr.Error()
			break
		}
	}
	var limitErr *topUpAmountLimitError
	if errors.As(err, &limitErr) {
		message = fmt.Sprintf("单笔充值数量不能大于 %d", limitErr.maximum)
	}
	logger.LogError(c.Request.Context(), fmt.Sprintf("充值校验失败 user_id=%d error=%q", c.GetInt("id"), err.Error()))
	c.JSON(http.StatusOK, gin.H{"message": "error", "data": message})
	return true
}

type topUpAmountLimitError struct{ maximum int64 }

func (err *topUpAmountLimitError) Error() string {
	return fmt.Sprintf("单笔充值数量不能大于 %d", err.maximum)
}

// validateTopUpQuota converts the normalized amount with the settlement
// conversion and verifies the user's remaining wallet capacity.
func validateTopUpQuota(userId int, amount int64) error {
	creditedQuota, err := model.TopUpQuotaFromAmount(amount)
	if err != nil {
		// Normalized currency units are not the requested TOKENS quantity.
		if operation_setting.GetQuotaDisplayType() != operation_setting.QuotaDisplayTypeTokens {
			if maxAmount := maxTopUpAmount(); maxAmount > 0 && amount > maxAmount {
				return &topUpAmountLimitError{maximum: maxAmount}
			}
		}
		return err
	}
	return model.ValidateTopUpQuotaCapacity(userId, creditedQuota)
}

func rejectInvalidTopUpQuota(c *gin.Context, userId int, amount int64) bool {
	return rejectTopUpQuota(c, validateTopUpQuota(userId, amount))
}

// rejectInvalidCreditedQuota validates an already-computed credited quota
// (for example Stripe's amount multiplied by the group ratio).
func rejectInvalidCreditedQuota(c *gin.Context, userId int, creditedQuota int, quotaErr error) bool {
	if quotaErr != nil {
		return rejectTopUpQuota(c, quotaErr)
	}
	return rejectTopUpQuota(c, model.ValidateTopUpQuotaCapacity(userId, creditedQuota))
}

// rejectInvalidDirectQuota validates a direct quota product (Creem) before the
// order is created.
func rejectInvalidDirectQuota(c *gin.Context, userId int, amount int64) bool {
	creditedQuota, err := model.DirectTopUpQuota(amount)
	if err != nil {
		return rejectTopUpQuota(c, err)
	}
	return rejectTopUpQuota(c, model.ValidateTopUpQuotaCapacity(userId, creditedQuota))
}

func RequestEpay(c *gin.Context) {
	var req EpayRequest
	err := c.ShouldBindJSON(&req)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "参数错误"})
		return
	}
	minimum, err := getMinTopup()
	if rejectTopUpQuota(c, err) {
		return
	}
	if req.Amount < minimum {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": fmt.Sprintf("充值数量不能小于 %d", minimum)})
		return
	}

	id := c.GetInt("id")
	amount, err := normalizeEpayTopUpAmount(req.Amount)
	if rejectTopUpQuota(c, err) || rejectInvalidTopUpQuota(c, id, amount) {
		return
	}

	group, err := model.GetUserGroup(id, true)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "获取用户分组失败"})
		return
	}
	payMoney := getPayMoney(req.Amount, group)
	if rejectInvalidTopUpMoney(c, payMoney) {
		return
	}
	if payMoney < 0.01 {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "充值金额过低"})
		return
	}

	if !operation_setting.ContainsPayMethod(req.PaymentMethod) {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "支付方式不存在"})
		return
	}

	callBackAddress := service.GetCallbackAddress()
	returnUrl, _ := url.Parse(paymentReturnPath("/console/log"))
	notifyUrl, _ := url.Parse(callBackAddress + "/api/user/epay/notify")
	tradeNo := fmt.Sprintf("%s%d", common.GetRandomString(6), time.Now().Unix())
	tradeNo = fmt.Sprintf("USR%dNO%s", id, tradeNo)
	client := GetEpayClient()
	if client == nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "当前管理员未配置支付信息"})
		return
	}
	uri, params, err := client.Purchase(&epay.PurchaseArgs{
		Type:           req.PaymentMethod,
		ServiceTradeNo: tradeNo,
		Name:           fmt.Sprintf("TUC%d", req.Amount),
		Money:          strconv.FormatFloat(payMoney, 'f', 2, 64),
		Device:         epay.PC,
		NotifyUrl:      notifyUrl,
		ReturnUrl:      returnUrl,
	})
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("易支付 拉起支付失败 user_id=%d trade_no=%s payment_method=%s amount=%d error=%q", id, tradeNo, req.PaymentMethod, req.Amount, err.Error()))
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "拉起支付失败"})
		return
	}
	topUp := &model.TopUp{
		UserId:          id,
		Amount:          amount,
		Money:           payMoney,
		TradeNo:         tradeNo,
		PaymentMethod:   req.PaymentMethod,
		PaymentProvider: model.PaymentProviderEpay,
		CreateTime:      time.Now().Unix(),
		Status:          common.TopUpStatusPending,
	}
	err = topUp.Insert()
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("易支付 创建充值订单失败 user_id=%d trade_no=%s payment_method=%s amount=%d error=%q", id, tradeNo, req.PaymentMethod, req.Amount, err.Error()))
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "创建订单失败"})
		return
	}
	logger.LogInfo(c.Request.Context(), fmt.Sprintf("易支付 充值订单创建成功 user_id=%d trade_no=%s payment_method=%s amount=%d money=%.2f", id, tradeNo, req.PaymentMethod, req.Amount, payMoney))
	c.JSON(http.StatusOK, gin.H{"message": "success", "data": params, "url": uri})
}

// tradeNo lock
var orderLocks sync.Map
var createLock sync.Mutex

// refCountedMutex 带引用计数的互斥锁，确保最后一个使用者才从 map 中删除
type refCountedMutex struct {
	mu       sync.Mutex
	refCount int
}

// LockOrder 尝试对给定订单号加锁
func LockOrder(tradeNo string) {
	createLock.Lock()
	var rcm *refCountedMutex
	if v, ok := orderLocks.Load(tradeNo); ok {
		rcm = v.(*refCountedMutex)
	} else {
		rcm = &refCountedMutex{}
		orderLocks.Store(tradeNo, rcm)
	}
	rcm.refCount++
	createLock.Unlock()
	rcm.mu.Lock()
}

// UnlockOrder 释放给定订单号的锁
func UnlockOrder(tradeNo string) {
	v, ok := orderLocks.Load(tradeNo)
	if !ok {
		return
	}
	rcm := v.(*refCountedMutex)
	rcm.mu.Unlock()

	createLock.Lock()
	rcm.refCount--
	if rcm.refCount == 0 {
		orderLocks.Delete(tradeNo)
	}
	createLock.Unlock()
}

func EpayNotify(c *gin.Context) {
	webhookPath := c.Request.URL.Path
	if !isEpayWebhookEnabled() {
		logger.LogWarn(c.Request.Context(), fmt.Sprintf("易支付 webhook 被拒绝 reason=webhook_disabled path=%q client_ip=%s", webhookPath, c.ClientIP()))
		_, _ = c.Writer.Write([]byte("fail"))
		return
	}

	var params map[string]string

	if c.Request.Method == "POST" {
		// POST 请求：从 POST body 解析参数
		if err := c.Request.ParseForm(); err != nil {
			logger.LogError(c.Request.Context(), fmt.Sprintf("易支付 webhook POST 表单解析失败 path=%q client_ip=%s error=%q", webhookPath, c.ClientIP(), err.Error()))
			_, _ = c.Writer.Write([]byte("fail"))
			return
		}
		params = lo.Reduce(lo.Keys(c.Request.PostForm), func(r map[string]string, t string, i int) map[string]string {
			r[t] = c.Request.PostForm.Get(t)
			return r
		}, map[string]string{})
	} else {
		// GET 请求：从 URL Query 解析参数
		params = lo.Reduce(lo.Keys(c.Request.URL.Query()), func(r map[string]string, t string, i int) map[string]string {
			r[t] = c.Request.URL.Query().Get(t)
			return r
		}, map[string]string{})
	}
	logger.LogInfo(c.Request.Context(), fmt.Sprintf("易支付 webhook 收到请求 path=%q client_ip=%s method=%s param_count=%d sign_present=%t", webhookPath, c.ClientIP(), c.Request.Method, len(params), params["sign"] != ""))

	if len(params) == 0 {
		logger.LogWarn(c.Request.Context(), fmt.Sprintf("易支付 webhook 参数为空 path=%q client_ip=%s", webhookPath, c.ClientIP()))
		_, _ = c.Writer.Write([]byte("fail"))
		return
	}
	client := GetEpayClient()
	if client == nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("易支付 client 未初始化 path=%q client_ip=%s", webhookPath, c.ClientIP()))
		_, err := c.Writer.Write([]byte("fail"))
		if err != nil {
			logger.LogError(c.Request.Context(), fmt.Sprintf("易支付 webhook 响应写入失败 path=%q client_ip=%s error=%q", webhookPath, c.ClientIP(), err.Error()))
		}
		return
	}
	verifyInfo, err := client.Verify(params)
	if err != nil || verifyInfo == nil || !verifyInfo.VerifyStatus {
		_, _ = c.Writer.Write([]byte("fail"))
		if err != nil {
			logger.LogWarn(c.Request.Context(), fmt.Sprintf("易支付 webhook 验签失败 path=%q client_ip=%s verify_error=%q", webhookPath, c.ClientIP(), err.Error()))
		} else {
			logger.LogWarn(c.Request.Context(), fmt.Sprintf("易支付 webhook 验签失败 path=%q client_ip=%s verify_status=false", webhookPath, c.ClientIP()))
		}
		return
	}

	logger.LogInfo(c.Request.Context(), fmt.Sprintf("易支付 webhook 验签成功 trade_no=%s callback_type=%s trade_status=%s money=%s client_ip=%s", verifyInfo.ServiceTradeNo, verifyInfo.Type, verifyInfo.TradeStatus, verifyInfo.Money, c.ClientIP()))
	if verifyInfo.TradeStatus != epay.StatusTradeSuccess {
		logger.LogInfo(c.Request.Context(), fmt.Sprintf("易支付 webhook 忽略事件 trade_no=%s callback_type=%s trade_status=%s client_ip=%s", verifyInfo.ServiceTradeNo, verifyInfo.Type, verifyInfo.TradeStatus, c.ClientIP()))
		_, _ = c.Writer.Write([]byte("success"))
		return
	}

	LockOrder(verifyInfo.ServiceTradeNo)
	defer UnlockOrder(verifyInfo.ServiceTradeNo)
	confirmation := model.PaymentConfirmation{
		Amount:             verifyInfo.Money,
		DecimalPlaces:      2,
		UseGoFloatRounding: true,
	}
	if err := model.RechargeEpay(verifyInfo.ServiceTradeNo, verifyInfo.Type, c.ClientIP(), confirmation); err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("易支付 充值处理失败 trade_no=%s callback_type=%s money=%s client_ip=%s error=%q", verifyInfo.ServiceTradeNo, verifyInfo.Type, verifyInfo.Money, c.ClientIP(), err.Error()))
		_, _ = c.Writer.Write([]byte("fail"))
		return
	}

	_, err = c.Writer.Write([]byte("success"))
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("易支付 webhook 响应写入失败 trade_no=%s client_ip=%s error=%q", verifyInfo.ServiceTradeNo, c.ClientIP(), err.Error()))
	}
}

func RequestAmount(c *gin.Context) {
	var req AmountRequest
	err := c.ShouldBindJSON(&req)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "参数错误"})
		return
	}

	minimum, err := getMinTopup()
	if rejectTopUpQuota(c, err) {
		return
	}
	if req.Amount < minimum {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": fmt.Sprintf("充值数量不能小于 %d", minimum)})
		return
	}
	id := c.GetInt("id")
	amount, err := normalizeEpayTopUpAmount(req.Amount)
	if rejectTopUpQuota(c, err) || rejectInvalidTopUpQuota(c, id, amount) {
		return
	}
	group, err := model.GetUserGroup(id, true)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "获取用户分组失败"})
		return
	}
	payMoney := getPayMoney(req.Amount, group)
	if rejectInvalidTopUpMoney(c, payMoney) {
		return
	}
	if payMoney <= 0.01 {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "充值金额过低"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "success", "data": strconv.FormatFloat(payMoney, 'f', 2, 64)})
}

func GetUserTopUps(c *gin.Context) {
	userId := c.GetInt("id")
	pageInfo := common.GetPageQuery(c)
	keyword := c.Query("keyword")

	var (
		topups []*model.TopUp
		total  int64
		err    error
	)
	if keyword != "" {
		topups, total, err = model.SearchUserTopUps(userId, keyword, pageInfo)
	} else {
		topups, total, err = model.GetUserTopUps(userId, pageInfo)
	}
	if err != nil {
		common.ApiError(c, err)
		return
	}

	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(topups)
	common.ApiSuccess(c, pageInfo)
}

// GetAllTopUps 管理员获取全平台充值记录
func GetAllTopUps(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)
	keyword := c.Query("keyword")

	var (
		topups []*model.TopUp
		total  int64
		err    error
	)
	if keyword != "" {
		topups, total, err = model.SearchAllTopUps(keyword, pageInfo)
	} else {
		topups, total, err = model.GetAllTopUps(pageInfo)
	}
	if err != nil {
		common.ApiError(c, err)
		return
	}

	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(topups)
	common.ApiSuccess(c, pageInfo)
}

type AdminCompleteTopupRequest struct {
	TradeNo string `json:"trade_no"`
}

// AdminCompleteTopUp 管理员补单接口
func AdminCompleteTopUp(c *gin.Context) {
	var req AdminCompleteTopupRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.TradeNo == "" {
		common.ApiErrorMsg(c, "参数错误")
		return
	}

	// 订单级互斥，防止并发补单
	LockOrder(req.TradeNo)
	defer UnlockOrder(req.TradeNo)

	if err := model.ManualCompleteTopUp(req.TradeNo, c.ClientIP()); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, nil)
}
