package controller

import (
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/thanhpk/randstr"
	waffo "github.com/waffo-com/waffo-go"
	"github.com/waffo-com/waffo-go/config"
	"github.com/waffo-com/waffo-go/core"
	"github.com/waffo-com/waffo-go/types/order"
)

func getWaffoSDK() (*waffo.Waffo, error) {
	env := config.Sandbox
	apiKey := setting.WaffoSandboxApiKey
	privateKey := setting.WaffoSandboxPrivateKey
	publicKey := setting.WaffoSandboxPublicCert
	if !setting.WaffoSandbox {
		env = config.Production
		apiKey = setting.WaffoApiKey
		privateKey = setting.WaffoPrivateKey
		publicKey = setting.WaffoPublicCert
	}
	builder := config.NewConfigBuilder().
		APIKey(apiKey).
		PrivateKey(privateKey).
		WaffoPublicKey(publicKey).
		Environment(env)
	if setting.WaffoMerchantId != "" {
		builder = builder.MerchantID(setting.WaffoMerchantId)
	}
	cfg, err := builder.Build()
	if err != nil {
		return nil, err
	}
	return waffo.New(cfg), nil
}

func getWaffoUserEmail(user *model.User) string {
	return fmt.Sprintf("%d@examples.com", user.Id)
}

func getWaffoCurrency() string {
	if setting.WaffoCurrency != "" {
		return setting.WaffoCurrency
	}
	return "USD"
}

func buildWaffoTopUpGoodsInfo(amount int64) *order.GoodsInfo {
	appName := strings.TrimSpace(common.SystemName)
	if appName == "" {
		appName = "New API"
	}
	return &order.GoodsInfo{
		GoodsName: fmt.Sprintf("Recharge %d credits", amount),
		AppName:   appName,
	}
}

// zeroDecimalCurrencies 零小数位币种，金额不能带小数点
var zeroDecimalCurrencies = map[string]bool{
	"IDR": true, "JPY": true, "KRW": true, "VND": true,
}

func formatWaffoAmount(amount float64, currency string) string {
	if zeroDecimalCurrencies[strings.ToUpper(strings.TrimSpace(currency))] {
		return fmt.Sprintf("%.0f", amount)
	}
	return fmt.Sprintf("%.2f", amount)
}

func waffoCurrencyDecimalPlaces(currency string) int32 {
	if zeroDecimalCurrencies[strings.ToUpper(strings.TrimSpace(currency))] {
		return 0
	}
	return 2
}

func waffoCurrencyFromTradeNo(tradeNo string) (string, bool) {
	parts := strings.SplitN(strings.TrimSpace(tradeNo), "-", 4)
	if len(parts) != 4 || parts[0] != "WAFFO" || len(parts[1]) != 3 {
		return "", false
	}
	for _, char := range parts[1] {
		if char < 'A' || char > 'Z' {
			return "", false
		}
	}
	return parts[1], true
}

func isWaffoOrderTerminalFailure(status string) bool {
	return status == core.OrderStatusOrderClose
}

func isConfiguredWaffoMerchant(merchantInfo map[string]interface{}) bool {
	expectedMerchantID := strings.TrimSpace(setting.WaffoMerchantId)
	if expectedMerchantID == "" {
		return true
	}
	merchantID, ok := merchantInfo["merchantId"].(string)
	return ok && strings.TrimSpace(merchantID) == expectedMerchantID
}

// getWaffoPayMoney converts the user-facing amount to USD for Waffo payment.
// Waffo only accepts USD, so this function handles the conversion from different
// display types (USD/CNY/TOKENS) to the actual USD amount to charge.
func getWaffoPayMoney(amount float64, group string) float64 {
	if !validTopUpPricing(group, setting.WaffoUnitPrice, int64(amount)) {
		return math.NaN()
	}
	originalAmount := amount
	if operation_setting.GetQuotaDisplayType() == operation_setting.QuotaDisplayTypeTokens {
		amount = amount / common.QuotaPerUnit
	}
	topupGroupRatio := common.GetTopupGroupRatio(group)
	if topupGroupRatio == 0 {
		topupGroupRatio = 1
	}
	discount := 1.0
	if ds, ok := operation_setting.GetPaymentSetting().AmountDiscount[int(originalAmount)]; ok {
		if ds > 0 {
			discount = ds
		}
	}
	return amount * setting.WaffoUnitPrice * topupGroupRatio * discount
}

type WaffoPayRequest struct {
	Amount         int64  `json:"amount"`
	PayMethodIndex *int   `json:"pay_method_index"` // 服务端支付方式列表的索引，nil 表示由 Waffo 自动选择
	PayMethodType  string `json:"pay_method_type"`  // Deprecated: 兼容旧前端，优先使用 pay_method_index
	PayMethodName  string `json:"pay_method_name"`  // Deprecated: 兼容旧前端，优先使用 pay_method_index
}

func RequestWaffoAmount(c *gin.Context) {
	if setting.WaffoMinTopUp < 0 {
		rejectTopUpQuota(c, model.ErrInvalidTopUpQuota)
		return
	}
	var req WaffoPayRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "参数错误"})
		return
	}

	waffoMinTopup := int64(setting.WaffoMinTopUp)
	if req.Amount < waffoMinTopup {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": fmt.Sprintf("充值数量不能小于 %d", waffoMinTopup)})
		return
	}

	id := c.GetInt("id")
	amount, err := normalizeWaffoTopUpAmount(req.Amount)
	if rejectTopUpQuota(c, err) || rejectInvalidTopUpQuota(c, id, amount) {
		return
	}
	group, err := model.GetUserGroup(id, true)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "获取用户分组失败"})
		return
	}

	payMoney := getWaffoPayMoney(float64(req.Amount), group)
	if rejectInvalidTopUpMoney(c, payMoney) {
		return
	}
	if payMoney <= 0.01 {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "充值金额过低"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "success", "data": strconv.FormatFloat(payMoney, 'f', 2, 64)})
}

// RequestWaffoPay 创建 Waffo 支付订单
func RequestWaffoPay(c *gin.Context) {
	if setting.WaffoMinTopUp < 0 {
		rejectTopUpQuota(c, model.ErrInvalidTopUpQuota)
		return
	}
	if !setting.WaffoEnabled {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "Waffo 支付未启用"})
		return
	}

	var req WaffoPayRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "参数错误"})
		return
	}
	waffoMinTopup := int64(setting.WaffoMinTopUp)
	if req.Amount < waffoMinTopup {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": fmt.Sprintf("充值数量不能小于 %d", waffoMinTopup)})
		return
	}

	id := c.GetInt("id")
	amount, err := normalizeWaffoTopUpAmount(req.Amount)
	if rejectTopUpQuota(c, err) || rejectInvalidTopUpQuota(c, id, amount) {
		return
	}
	user, err := model.GetUserById(id, false)
	if err != nil || user == nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "用户不存在"})
		return
	}

	// 从服务端配置查找支付方式，客户端只传索引或旧字段
	var resolvedPayMethodType, resolvedPayMethodName string
	methods := setting.GetWaffoPayMethods()
	if req.PayMethodIndex != nil {
		// 新协议：按索引查找
		idx := *req.PayMethodIndex
		if idx < 0 || idx >= len(methods) {
			logger.LogWarn(c.Request.Context(), fmt.Sprintf("Waffo 支付方式索引无效 user_id=%d pay_method_index=%d method_count=%d", id, idx, len(methods)))
			c.JSON(http.StatusOK, gin.H{"message": "error", "data": "不支持的支付方式"})
			return
		}
		resolvedPayMethodType = methods[idx].PayMethodType
		resolvedPayMethodName = methods[idx].PayMethodName
	} else if req.PayMethodType != "" {
		// 兼容旧前端：验证客户端传的值在服务端列表中
		valid := false
		for _, m := range methods {
			if m.PayMethodType == req.PayMethodType && m.PayMethodName == req.PayMethodName {
				valid = true
				resolvedPayMethodType = m.PayMethodType
				resolvedPayMethodName = m.PayMethodName
				break
			}
		}
		if !valid {
			logger.LogWarn(c.Request.Context(), fmt.Sprintf("Waffo 支付方式无效 user_id=%d pay_method_type=%s pay_method_name=%q", id, req.PayMethodType, req.PayMethodName))
			c.JSON(http.StatusOK, gin.H{"message": "error", "data": "不支持的支付方式"})
			return
		}
	}
	// resolvedPayMethodType/Name 为空时，Waffo 自动选择支付方式

	group, _ := model.GetUserGroup(id, true)
	payMoney := getWaffoPayMoney(float64(req.Amount), group)
	if rejectInvalidTopUpMoney(c, payMoney) {
		return
	}
	if payMoney < 0.01 {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "充值金额过低"})
		return
	}

	// Snapshot currency in the external order ID without changing the database
	// schema, so later setting changes cannot invalidate an in-flight payment.
	currency := strings.ToUpper(strings.TrimSpace(getWaffoCurrency()))
	merchantOrderId := fmt.Sprintf("WAFFO-%s-%d-%d-%s", currency, id, time.Now().UnixMilli(), randstr.String(6))
	paymentRequestId := merchantOrderId

	// 创建本地订单
	topUp := &model.TopUp{
		UserId:          id,
		Amount:          amount,
		Money:           payMoney,
		TradeNo:         merchantOrderId,
		PaymentMethod:   model.PaymentMethodWaffo,
		PaymentProvider: model.PaymentProviderWaffo,
		CreateTime:      time.Now().Unix(),
		Status:          common.TopUpStatusPending,
	}
	if err := topUp.Insert(); err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Waffo 创建充值订单失败 user_id=%d trade_no=%s amount=%d error=%q", id, merchantOrderId, req.Amount, err.Error()))
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "创建订单失败"})
		return
	}

	sdk, err := getWaffoSDK()
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Waffo SDK 初始化失败 user_id=%d trade_no=%s error=%q", id, merchantOrderId, err.Error()))
		topUp.Status = common.TopUpStatusFailed
		_ = topUp.Update()
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "支付配置错误"})
		return
	}

	callbackAddr := service.GetCallbackAddress()
	notifyUrl := callbackAddr + "/api/waffo/webhook"
	if setting.WaffoNotifyUrl != "" {
		notifyUrl = setting.WaffoNotifyUrl
	}
	returnUrl := paymentReturnPath("/console/topup?show_history=true")
	if setting.WaffoReturnUrl != "" {
		returnUrl = setting.WaffoReturnUrl
	}

	goodsInfo := buildWaffoTopUpGoodsInfo(req.Amount)
	createParams := &order.CreateOrderParams{
		PaymentRequestID: paymentRequestId,
		MerchantOrderID:  merchantOrderId,
		OrderAmount:      formatWaffoAmount(payMoney, currency),
		OrderCurrency:    currency,
		OrderDescription: goodsInfo.GoodsName,
		OrderRequestedAt: time.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
		NotifyURL:        notifyUrl,
		MerchantInfo: &order.MerchantInfo{
			MerchantID: setting.WaffoMerchantId,
		},
		UserInfo: &order.UserInfo{
			UserID:       strconv.Itoa(user.Id),
			UserEmail:    getWaffoUserEmail(user),
			UserTerminal: "WEB",
		},
		PaymentInfo: &order.PaymentInfo{
			ProductName:   "ONE_TIME_PAYMENT",
			PayMethodType: resolvedPayMethodType,
			PayMethodName: resolvedPayMethodName,
		},
		GoodsInfo:          goodsInfo,
		SuccessRedirectURL: returnUrl,
		FailedRedirectURL:  returnUrl,
	}
	resp, err := sdk.Order().Create(c.Request.Context(), createParams, nil)
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Waffo 创建订单失败 user_id=%d trade_no=%s error=%q", id, merchantOrderId, err.Error()))
		topUp.Status = common.TopUpStatusFailed
		_ = topUp.Update()
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "拉起支付失败"})
		return
	}
	if !resp.IsSuccess() {
		logger.LogWarn(c.Request.Context(), fmt.Sprintf("Waffo 创建订单业务失败 user_id=%d trade_no=%s code=%s message=%q response=%q", id, merchantOrderId, resp.Code, resp.Message, common.GetJsonString(resp)))
		topUp.Status = common.TopUpStatusFailed
		_ = topUp.Update()
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "拉起支付失败"})
		return
	}

	orderData := resp.GetData()
	if orderData.PaymentRequestID != paymentRequestId || orderData.MerchantOrderID != merchantOrderId {
		logger.LogError(c.Request.Context(), "Waffo 创建响应订单身份不匹配")
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "支付身份不匹配"})
		return
	}
	if err := model.RegisterTopUpCheckout(merchantOrderId, model.PaymentProviderWaffo, orderData.PaymentRequestID); err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Waffo 保存支付身份失败 trade_no=%s error=%q", merchantOrderId, err.Error()))
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "保存支付身份失败"})
		return
	}
	logger.LogInfo(c.Request.Context(), fmt.Sprintf("Waffo 充值订单创建成功 user_id=%d trade_no=%s amount=%d money=%.2f pay_method_type=%s pay_method_name=%q", id, merchantOrderId, req.Amount, payMoney, resolvedPayMethodType, resolvedPayMethodName))

	paymentUrl := orderData.FetchRedirectURL()
	if paymentUrl == "" {
		paymentUrl = orderData.OrderAction
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "success",
		"data": gin.H{
			"payment_url": paymentUrl,
			"order_id":    merchantOrderId,
		},
	})
}

// webhookPayloadWithSubInfo 扩展 PAYMENT_NOTIFICATION，包含 SDK 未定义的 subscriptionInfo 字段
type webhookPayloadWithSubInfo struct {
	EventType string `json:"eventType"`
	Result    struct {
		core.PaymentNotificationResult
		SubscriptionInfo *webhookSubscriptionInfo `json:"subscriptionInfo,omitempty"`
	} `json:"result"`
}

type webhookSubscriptionInfo struct {
	Period              string `json:"period,omitempty"`
	MerchantRequest     string `json:"merchantRequest,omitempty"`
	SubscriptionID      string `json:"subscriptionId,omitempty"`
	SubscriptionRequest string `json:"subscriptionRequest,omitempty"`
}

const waffoEventChargeback = "CHARGEBACK_NOTIFICATION"

// Waffo's chargeback notification result has the same fields as inquiry data.
// SDK v1.3.2 does not define this event yet.
type waffoChargebackResult struct {
	ChargebackID             string `json:"chargebackId"`
	OriginalOrderID          string `json:"originalOrderId"`
	OriginalPaymentRequestID string `json:"originalPaymentRequestId"`
	MerchantID               string `json:"merchantId"`
	ChargebackStatus         string `json:"chargebackStatus"`
	Amount                   string `json:"amount"`
	Currency                 string `json:"currency"`
	ChargebackDateTime       string `json:"chargebackDateTime"`
	LifeCycle                string `json:"lifeCycle"`
	RelevantChargebackID     string `json:"relevantChargebackId"`
}

func validWaffoReversalID(id string) bool {
	return id != "" && len(id) <= 191 && strings.TrimSpace(id) == id
}

func getWaffoReversalBinding(paymentID string) (*model.TopUpPaymentBinding, error) {
	if model.DB == nil || !validWaffoReversalID(paymentID) {
		return nil, model.ErrTopUpPaymentUnbound
	}
	key, err := model.NewRebateIdempotencyKey("provider-payment-v1", model.PaymentProviderWaffo, paymentID)
	if err != nil {
		return nil, err
	}
	var binding model.TopUpPaymentBinding
	if err := model.DB.Where("payment_key = ?", key).First(&binding).Error; err != nil {
		return nil, err
	}
	return &binding, nil
}

func waffoReversalPayment(binding *model.TopUpPaymentBinding, paymentID, checkoutID string) (model.VerifiedTopUpReversal, error) {
	if binding == nil || !validWaffoReversalID(paymentID) || !validWaffoReversalID(checkoutID) ||
		binding.Provider != model.PaymentProviderWaffo || binding.PaymentID != paymentID || binding.CheckoutID != checkoutID ||
		binding.PaidMinor <= 0 || binding.CreditedQuota <= 0 || len(binding.Currency) != 3 {
		return model.VerifiedTopUpReversal{}, model.ErrTopUpReversalConflict
	}
	for _, char := range binding.Currency {
		if char < 'A' || char > 'Z' {
			return model.VerifiedTopUpReversal{}, model.ErrPaymentCurrencyMismatch
		}
	}
	return model.VerifiedTopUpReversal{
		Provider: model.PaymentProviderWaffo, PaymentID: paymentID,
		Currency: binding.Currency, OriginalMinor: binding.PaidMinor,
	}, nil
}

func finishWaffoReversal(event model.VerifiedTopUpReversal, eventType, status, amount, occurredAt string) (model.VerifiedTopUpReversal, error) {
	if !validWaffoReversalID(event.ObjectID) {
		return model.VerifiedTopUpReversal{}, model.ErrTopUpReversalConflict
	}
	minor, err := model.TopUpProviderMinorAmount(amount, waffoCurrencyDecimalPlaces(event.Currency))
	if err != nil || minor > event.OriginalMinor {
		return model.VerifiedTopUpReversal{}, model.ErrTopUpReversalConflict
	}
	occurred, err := time.Parse(time.RFC3339Nano, occurredAt)
	if err != nil || occurred.Unix() <= 0 {
		return model.VerifiedTopUpReversal{}, model.ErrTopUpReversalConflict
	}
	event.AmountMinor, event.OccurredAt = minor, occurred.Unix()
	// There is no official eventId. Scope a stable receipt to payment, object
	// and status; changed cash/time/group fields must conflict on replay.
	event.EventID, err = model.NewRebateIdempotencyKey("waffo-reversal-event-v1", eventType, event.PaymentID, event.ObjectID, status)
	return event, err
}

func waffoRefundReversal(result *core.RefundNotificationResult, binding *model.TopUpPaymentBinding) (model.VerifiedTopUpReversal, error) {
	if result == nil || (result.RefundStatus != core.RefundStatusPartiallyRefunded && result.RefundStatus != core.RefundStatusFullyRefunded) {
		return model.VerifiedTopUpReversal{}, model.ErrTopUpReversalConflict
	}
	event, err := waffoReversalPayment(binding, result.AcquiringOrderID, result.OrigPaymentRequestID)
	if err != nil {
		return model.VerifiedTopUpReversal{}, err
	}
	// refundAmount is one refund in the ORIGINAL order currency. userCurrency,
	// finalDealAmount and remainingRefundAmount describe different amounts.
	event.ObjectID, event.Kind, event.Terminal = result.AcquiringRefundOrderID, "refund_item", true
	return finishWaffoReversal(event, core.EventRefund, result.RefundStatus, result.RefundAmount, result.RefundCompletedAt)
}

func waffoChargebackReversal(result *waffoChargebackResult, binding *model.TopUpPaymentBinding) (model.VerifiedTopUpReversal, error) {
	if result == nil || strings.TrimSpace(setting.WaffoMerchantId) == "" || result.MerchantID != strings.TrimSpace(setting.WaffoMerchantId) {
		return model.VerifiedTopUpReversal{}, model.ErrTopUpReversalConflict
	}
	event, err := waffoReversalPayment(binding, result.OriginalOrderID, result.OriginalPaymentRequestID)
	if err != nil {
		return model.VerifiedTopUpReversal{}, err
	}
	if strings.ToUpper(strings.TrimSpace(result.Currency)) != event.Currency {
		return model.VerifiedTopUpReversal{}, model.ErrPaymentCurrencyMismatch
	}
	event.ObjectID, event.Kind = result.ChargebackID, "dispute"
	switch result.LifeCycle {
	case "FIRST_CYCLE":
		event.GroupID = result.ChargebackID
	case "SECOND_CYCLE":
		if !validWaffoReversalID(result.RelevantChargebackID) || result.RelevantChargebackID == result.ChargebackID {
			return model.VerifiedTopUpReversal{}, model.ErrTopUpReversalConflict
		}
		event.GroupID = result.RelevantChargebackID
	default:
		return model.VerifiedTopUpReversal{}, model.ErrTopUpReversalConflict
	}
	switch result.ChargebackStatus {
	case "ACTION_REQUIRED", "UNDER_REVIEW", "EVIDENCE_REQUIRED", "SECOND_CYCLE_RESPONSE_REQUIRED", "ESCALATE_TO_2ND_CYCLE":
		// Escalation keeps the same group's principal; the model takes the max
		// across its rounds and validates that the root belongs to this payment.
	case "CASE_WON", "CANCELED", "SETTLED":
		event.Terminal, event.Released = true, true
	case "CASE_LOST", "ACCEPTED", "EXPIRED":
		event.Terminal = true
	default:
		return model.VerifiedTopUpReversal{}, model.ErrTopUpReversalConflict
	}
	// chargebackDateTime is the official creation time, not an update time.
	// Terminal outcomes dominate earlier/open deliveries in the model.
	return finishWaffoReversal(event, waffoEventChargeback, result.ChargebackStatus, result.Amount, result.ChargebackDateTime)
}

// Called only after the SDK verifies the signature over the original body.
func waffoReversalFromWebhook(body []byte, eventType string) (model.VerifiedTopUpReversal, error) {
	switch eventType {
	case core.EventRefund:
		var payload core.RefundNotification
		if err := common.Unmarshal(body, &payload); err != nil {
			return model.VerifiedTopUpReversal{}, err
		}
		if payload.Result == nil {
			return model.VerifiedTopUpReversal{}, model.ErrTopUpReversalConflict
		}
		binding, err := getWaffoReversalBinding(payload.Result.AcquiringOrderID)
		if err != nil {
			return model.VerifiedTopUpReversal{}, err
		}
		return waffoRefundReversal(payload.Result, binding)
	case waffoEventChargeback:
		var payload struct {
			Result *waffoChargebackResult `json:"result"`
		}
		if err := common.Unmarshal(body, &payload); err != nil {
			return model.VerifiedTopUpReversal{}, err
		}
		if payload.Result == nil {
			return model.VerifiedTopUpReversal{}, model.ErrTopUpReversalConflict
		}
		binding, err := getWaffoReversalBinding(payload.Result.OriginalOrderID)
		if err != nil {
			return model.VerifiedTopUpReversal{}, err
		}
		return waffoChargebackReversal(payload.Result, binding)
	default:
		return model.VerifiedTopUpReversal{}, model.ErrTopUpReversalConflict
	}
}

// WaffoWebhook 处理 Waffo 回调通知（支付/退款/拒付）
func WaffoWebhook(c *gin.Context) {
	webhookPath := c.Request.URL.Path
	if !isWaffoWebhookEnabled() {
		logger.LogWarn(c.Request.Context(), fmt.Sprintf("Waffo webhook 被拒绝 reason=webhook_disabled path=%q client_ip=%s", webhookPath, c.ClientIP()))
		c.AbortWithStatus(http.StatusForbidden)
		return
	}

	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Waffo webhook 读取请求体失败 path=%q client_ip=%s error=%q", webhookPath, c.ClientIP(), err.Error()))
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}

	sdk, err := getWaffoSDK()
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Waffo webhook SDK 初始化失败 path=%q client_ip=%s error=%q", webhookPath, c.ClientIP(), err.Error()))
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	wh := sdk.Webhook()
	bodyStr := string(bodyBytes)
	signature := c.GetHeader("X-SIGNATURE")
	payloadFingerprint := paymentPayloadFingerprint(bodyBytes)
	logger.LogInfo(c.Request.Context(), fmt.Sprintf("Waffo webhook 收到请求 path=%q client_ip=%s signature_present=%t payload_bytes=%d payload_sha256=%s", webhookPath, c.ClientIP(), signature != "", len(bodyBytes), payloadFingerprint))
	if signature == "" {
		logger.LogWarn(c.Request.Context(), fmt.Sprintf("Waffo webhook 缺少签名 path=%q client_ip=%s payload_sha256=%s", webhookPath, c.ClientIP(), payloadFingerprint))
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}

	// 验证请求签名
	if !wh.VerifySignature(bodyStr, signature) {
		logger.LogWarn(c.Request.Context(), fmt.Sprintf("Waffo webhook 验签失败 path=%q client_ip=%s payload_sha256=%s", webhookPath, c.ClientIP(), payloadFingerprint))
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}

	var event core.WebhookEvent
	if err := common.Unmarshal(bodyBytes, &event); err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Waffo webhook 解析失败 path=%q client_ip=%s error=%q payload_sha256=%s", webhookPath, c.ClientIP(), err.Error(), payloadFingerprint))
		sendWaffoWebhookResponse(c, wh, false, "invalid payload")
		return
	}

	switch event.EventType {
	case core.EventPayment:
		// 解析为扩展类型，区分普通支付和订阅支付
		var payload webhookPayloadWithSubInfo
		if err := common.Unmarshal(bodyBytes, &payload); err != nil {
			logger.LogError(c.Request.Context(), fmt.Sprintf("Waffo 支付回调载荷解析失败 event_type=%s client_ip=%s error=%q payload_sha256=%s", event.EventType, c.ClientIP(), err.Error(), payloadFingerprint))
			sendWaffoWebhookResponse(c, wh, false, "invalid payment payload")
			return
		}
		logger.LogInfo(c.Request.Context(), fmt.Sprintf("Waffo webhook 验签并解析成功 event_type=%s merchant_order_id=%s order_status=%s client_ip=%s", event.EventType, payload.Result.MerchantOrderID, payload.Result.OrderStatus, c.ClientIP()))
		handleWaffoPayment(c, wh, &payload.Result.PaymentNotificationResult)
	case core.EventRefund, waffoEventChargeback:
		reversal, err := waffoReversalFromWebhook(bodyBytes, event.EventType)
		if err == nil {
			err = model.ApplyVerifiedTopUpReversal(model.DB, reversal)
		}
		if err != nil {
			logger.LogError(c.Request.Context(), fmt.Sprintf("Waffo 退款/拒付处理失败 event_type=%s payload_sha256=%s error=%q", event.EventType, payloadFingerprint, err.Error()))
			sendWaffoWebhookResponse(c, wh, false, "reversal processing failed")
			return
		}
		sendWaffoWebhookResponse(c, wh, true, "")
	default:
		logger.LogWarn(c.Request.Context(), fmt.Sprintf("Waffo webhook 未知事件 event_type=%s client_ip=%s", event.EventType, c.ClientIP()))
		sendWaffoWebhookResponse(c, wh, false, "unsupported event")
	}
}

// handleWaffoPayment 处理支付完成通知
func handleWaffoPayment(c *gin.Context, wh *core.WebhookHandler, result *core.PaymentNotificationResult) {
	if result == nil {
		logger.LogWarn(c.Request.Context(), fmt.Sprintf("Waffo 支付回调缺少 result client_ip=%s", c.ClientIP()))
		sendWaffoWebhookResponse(c, wh, false, "missing payment result")
		return
	}
	if !isConfiguredWaffoMerchant(result.MerchantInfo) {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Waffo 支付回调商户不匹配 trade_no=%s client_ip=%s", result.MerchantOrderID, c.ClientIP()))
		sendWaffoWebhookResponse(c, wh, false, "merchant mismatch")
		return
	}
	if result.OrderStatus != core.OrderStatusPaySuccess {
		logger.LogInfo(c.Request.Context(), fmt.Sprintf("Waffo 订单状态非成功，忽略充值 trade_no=%s order_status=%s client_ip=%s", result.MerchantOrderID, result.OrderStatus, c.ClientIP()))
		// Only ORDER_CLOSE is terminal. In-progress and authorization states must
		// stay pending so a later PAY_SUCCESS notification can still fulfill it.
		if isWaffoOrderTerminalFailure(result.OrderStatus) && result.MerchantOrderID != "" {
			if err := model.UpdatePendingTopUpStatus(result.MerchantOrderID, model.PaymentProviderWaffo, common.TopUpStatusFailed); err != nil &&
				!errors.Is(err, model.ErrTopUpNotFound) &&
				!errors.Is(err, model.ErrTopUpStatusInvalid) {
				logger.LogError(c.Request.Context(), fmt.Sprintf("Waffo 标记失败订单状态失败 trade_no=%s error=%q", result.MerchantOrderID, err.Error()))
				sendWaffoWebhookResponse(c, wh, false, "order update failed")
				return
			}
		}
		sendWaffoWebhookResponse(c, wh, true, "")
		return
	}

	merchantOrderId := strings.TrimSpace(result.MerchantOrderID)
	if merchantOrderId == "" {
		logger.LogWarn(c.Request.Context(), fmt.Sprintf("Waffo 支付成功回调缺少订单号 client_ip=%s", c.ClientIP()))
		sendWaffoWebhookResponse(c, wh, false, "missing merchant order id")
		return
	}
	if paymentRequestID := strings.TrimSpace(result.PaymentRequestID); paymentRequestID != "" && paymentRequestID != merchantOrderId {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Waffo 支付回调订单关联不匹配 trade_no=%s payment_request_id=%s client_ip=%s", merchantOrderId, paymentRequestID, c.ClientIP()))
		sendWaffoWebhookResponse(c, wh, false, "payment request mismatch")
		return
	}

	callbackCurrency := strings.ToUpper(strings.TrimSpace(result.OrderCurrency))
	if callbackCurrency == "" {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Waffo 支付回调缺少币种 trade_no=%s client_ip=%s", merchantOrderId, c.ClientIP()))
		sendWaffoWebhookResponse(c, wh, false, "missing order currency")
		return
	}
	expectedCurrency, ok := waffoCurrencyFromTradeNo(merchantOrderId)
	if !ok {
		// Legacy order IDs did not carry a currency snapshot. Their signed
		// callback currency remains authoritative for backward compatibility.
		expectedCurrency = callbackCurrency
	}
	confirmation := model.PaymentConfirmation{
		Amount:             result.OrderAmount,
		Currency:           callbackCurrency,
		ExpectedCurrency:   expectedCurrency,
		DecimalPlaces:      waffoCurrencyDecimalPlaces(expectedCurrency),
		UseGoFloatRounding: true,
	}

	LockOrder(merchantOrderId)
	defer UnlockOrder(merchantOrderId)

	paidMinor, err := model.TopUpProviderMinorAmount(result.OrderAmount, confirmation.DecimalPlaces)
	if err != nil {
		sendWaffoWebhookResponse(c, wh, false, "invalid payment amount")
		return
	}
	identity := model.TopUpPaymentIdentity{Provider: model.PaymentProviderWaffo, CheckoutID: result.PaymentRequestID, PaymentID: result.AcquiringOrderID, Currency: callbackCurrency, PaidMinor: paidMinor}
	if err := model.RechargeWaffo(merchantOrderId, c.ClientIP(), confirmation, identity); err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Waffo 充值处理失败 trade_no=%s amount=%s currency=%s client_ip=%s error=%q", merchantOrderId, result.OrderAmount, result.OrderCurrency, c.ClientIP(), err.Error()))
		sendWaffoWebhookResponse(c, wh, false, err.Error())
		return
	}

	logger.LogInfo(c.Request.Context(), fmt.Sprintf("Waffo 充值成功 trade_no=%s client_ip=%s", merchantOrderId, c.ClientIP()))
	sendWaffoWebhookResponse(c, wh, true, "")
}

// sendWaffoWebhookResponse 发送签名响应
func sendWaffoWebhookResponse(c *gin.Context, wh *core.WebhookHandler, success bool, msg string) {
	var body, sig string
	if success {
		body, sig = wh.BuildSuccessResponse()
	} else {
		body, sig = wh.BuildFailedResponse(msg)
	}
	c.Header("X-SIGNATURE", sig)
	status := http.StatusOK
	if !success {
		status = http.StatusInternalServerError
	}
	c.Data(status, "application/json", []byte(body))
}
