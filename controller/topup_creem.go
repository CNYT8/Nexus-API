package controller

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"

	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"github.com/thanhpk/randstr"
)

const CreemSignatureHeader = "creem-signature"

var creemAdaptor = &CreemAdaptor{}

// 生成HMAC-SHA256签名
func generateCreemSignature(payload string, secret string) string {
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(payload))
	return hex.EncodeToString(h.Sum(nil))
}

// 验证Creem webhook签名
func verifyCreemSignature(payload string, signature string, secret string) bool {
	if strings.TrimSpace(secret) == "" {
		logger.LogWarn(context.Background(), "Creem webhook secret 未配置")
		return false
	}
	if strings.TrimSpace(signature) == "" {
		return false
	}

	expectedSignature := generateCreemSignature(payload, secret)
	return hmac.Equal([]byte(signature), []byte(expectedSignature))
}

type CreemPayRequest struct {
	ProductId     string `json:"product_id"`
	PaymentMethod string `json:"payment_method"`
}

type CreemProduct struct {
	ProductId string  `json:"productId"`
	Name      string  `json:"name"`
	Price     float64 `json:"price"`
	Currency  string  `json:"currency"`
	Quota     int64   `json:"quota"`
}

type CreemAdaptor struct {
}

func (*CreemAdaptor) RequestPay(c *gin.Context, req *CreemPayRequest) {
	if req.PaymentMethod != model.PaymentMethodCreem {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "不支持的支付渠道"})
		return
	}

	if req.ProductId == "" {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "请选择产品"})
		return
	}

	// 解析产品列表
	var products []CreemProduct
	err := json.Unmarshal([]byte(setting.CreemProducts), &products)
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Creem 产品配置解析失败 user_id=%d error=%q", c.GetInt("id"), err.Error()))
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "产品配置错误"})
		return
	}

	// 查找对应的产品
	var selectedProduct *CreemProduct
	for _, product := range products {
		if product.ProductId == req.ProductId {
			selectedProduct = &product
			break
		}
	}

	if selectedProduct == nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "产品不存在"})
		return
	}

	id := c.GetInt("id")
	user, err := model.GetUserById(id, false)
	if err != nil || user == nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "用户不存在"})
		return
	}
	if rejectInvalidDirectQuota(c, id, selectedProduct.Quota) || rejectInvalidTopUpMoney(c, selectedProduct.Price) {
		return
	}

	// 生成唯一的订单引用ID
	reference := fmt.Sprintf("creem-api-ref-%d-%d-%s", user.Id, time.Now().UnixMilli(), randstr.String(4))
	referenceId := "ref_" + common.Sha1([]byte(reference))

	// 先创建订单记录，使用产品配置的金额和充值额度
	topUp := &model.TopUp{
		UserId:          id,
		Amount:          selectedProduct.Quota, // 充值额度
		Money:           selectedProduct.Price, // 支付金额
		TradeNo:         referenceId,
		PaymentMethod:   model.PaymentMethodCreem,
		PaymentProvider: model.PaymentProviderCreem,
		CreateTime:      time.Now().Unix(),
		Status:          common.TopUpStatusPending,
	}
	err = topUp.Insert()
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Creem 创建充值订单失败 user_id=%d trade_no=%s product_id=%s error=%q", id, referenceId, selectedProduct.ProductId, err.Error()))
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "创建订单失败"})
		return
	}

	// 创建支付链接，传入用户邮箱
	checkoutUrl, err := genCreemLink(c.Request.Context(), referenceId, selectedProduct, user.Email, user.Username, true)
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Creem 创建支付链接失败 user_id=%d trade_no=%s product_id=%s error=%q", id, referenceId, selectedProduct.ProductId, err.Error()))
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "拉起支付失败"})
		return
	}

	logger.LogInfo(c.Request.Context(), fmt.Sprintf("Creem 充值订单创建成功 user_id=%d trade_no=%s product_id=%s product_name=%q quota=%d money=%.2f", id, referenceId, selectedProduct.ProductId, selectedProduct.Name, selectedProduct.Quota, selectedProduct.Price))

	c.JSON(http.StatusOK, gin.H{
		"message": "success",
		"data": gin.H{
			"checkout_url": checkoutUrl,
			"order_id":     referenceId,
		},
	})
}

func RequestCreemPay(c *gin.Context) {
	var req CreemPayRequest

	// 保留原始 body 供 HMAC 验签和后续 JSON 解析共用。
	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Creem 支付请求读取失败 error=%q", err.Error()))
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "read query error"})
		return
	}

	logger.LogInfo(c.Request.Context(), fmt.Sprintf("Creem 支付请求已收到 user_id=%d payload_bytes=%d payload_sha256=%s", c.GetInt("id"), len(bodyBytes), paymentPayloadFingerprint(bodyBytes)))

	// 重新设置body供后续的ShouldBindJSON使用
	c.Request.Body = io.NopCloser(bytes.NewReader(bodyBytes))

	err = c.ShouldBindJSON(&req)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "参数错误"})
		return
	}
	creemAdaptor.RequestPay(c, &req)
}

// 新的Creem Webhook结构体，匹配实际的webhook数据格式
type CreemWebhookEvent struct {
	Id        string `json:"id"`
	EventType string `json:"eventType"`
	CreatedAt int64  `json:"created_at"`
	Object    struct {
		Id             string `json:"id"`
		Object         string `json:"object"`
		RequestId      string `json:"request_id"`
		Amount         int64  `json:"amount"`
		Currency       string `json:"currency"`
		RefundAmount   int64  `json:"refund_amount"`
		RefundCurrency string `json:"refund_currency"`
		Transaction    struct {
			Id             string `json:"id"`
			Order          string `json:"order"`
			AmountPaid     int64  `json:"amount_paid"`
			Currency       string `json:"currency"`
			Status         string `json:"status"`
			RefundedAmount *int64 `json:"refunded_amount"`
		} `json:"transaction"`
		Order struct {
			Object      string `json:"object"`
			Id          string `json:"id"`
			Customer    string `json:"customer"`
			Product     string `json:"product"`
			Amount      int    `json:"amount"`
			Currency    string `json:"currency"`
			SubTotal    int    `json:"sub_total"`
			TaxAmount   int    `json:"tax_amount"`
			AmountDue   int    `json:"amount_due"`
			AmountPaid  int    `json:"amount_paid"`
			Status      string `json:"status"`
			Type        string `json:"type"`
			Transaction string `json:"transaction"`
			CreatedAt   string `json:"created_at"`
			UpdatedAt   string `json:"updated_at"`
			Mode        string `json:"mode"`
		} `json:"order"`
		Product struct {
			Id                string  `json:"id"`
			Object            string  `json:"object"`
			Name              string  `json:"name"`
			Description       string  `json:"description"`
			Price             int     `json:"price"`
			Currency          string  `json:"currency"`
			BillingType       string  `json:"billing_type"`
			BillingPeriod     string  `json:"billing_period"`
			Status            string  `json:"status"`
			TaxMode           string  `json:"tax_mode"`
			TaxCategory       string  `json:"tax_category"`
			DefaultSuccessUrl *string `json:"default_success_url"`
			CreatedAt         string  `json:"created_at"`
			UpdatedAt         string  `json:"updated_at"`
			Mode              string  `json:"mode"`
		} `json:"product"`
		Units    int `json:"units"`
		Customer struct {
			Id        string `json:"id"`
			Object    string `json:"object"`
			Email     string `json:"email"`
			Name      string `json:"name"`
			Country   string `json:"country"`
			CreatedAt string `json:"created_at"`
			UpdatedAt string `json:"updated_at"`
			Mode      string `json:"mode"`
		} `json:"customer"`
		Status   string            `json:"status"`
		Metadata map[string]string `json:"metadata"`
		Mode     string            `json:"mode"`
	} `json:"object"`
}

func CreemWebhook(c *gin.Context) {
	webhookPath := c.Request.URL.Path
	if !isCreemWebhookEnabled() {
		logger.LogWarn(c.Request.Context(), fmt.Sprintf("Creem webhook 被拒绝 reason=webhook_disabled path=%q client_ip=%s", webhookPath, c.ClientIP()))
		c.AbortWithStatus(http.StatusForbidden)
		return
	}

	// 读取body内容用于打印，同时保留原始数据供后续使用
	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Creem webhook 读取请求体失败 path=%q client_ip=%s error=%q", webhookPath, c.ClientIP(), err.Error()))
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}

	// 获取签名头
	signature := c.GetHeader(CreemSignatureHeader)
	payloadFingerprint := paymentPayloadFingerprint(bodyBytes)
	logger.LogInfo(c.Request.Context(), fmt.Sprintf("Creem webhook 收到请求 path=%q client_ip=%s signature_present=%t payload_bytes=%d payload_sha256=%s", webhookPath, c.ClientIP(), signature != "", len(bodyBytes), payloadFingerprint))
	if signature == "" {
		logger.LogWarn(c.Request.Context(), fmt.Sprintf("Creem webhook 缺少签名 path=%q client_ip=%s payload_sha256=%s", webhookPath, c.ClientIP(), payloadFingerprint))
		c.AbortWithStatus(http.StatusUnauthorized)
		return
	}

	// 验证签名
	if !verifyCreemSignature(string(bodyBytes), signature, setting.CreemWebhookSecret) {
		logger.LogWarn(c.Request.Context(), fmt.Sprintf("Creem webhook 验签失败 path=%q client_ip=%s payload_sha256=%s", webhookPath, c.ClientIP(), payloadFingerprint))
		c.AbortWithStatus(http.StatusUnauthorized)
		return
	}

	logger.LogInfo(c.Request.Context(), fmt.Sprintf("Creem webhook 验签成功 path=%q client_ip=%s payload_sha256=%s", webhookPath, c.ClientIP(), payloadFingerprint))

	// 重新设置body供后续的ShouldBindJSON使用
	c.Request.Body = io.NopCloser(bytes.NewReader(bodyBytes))

	// 解析新格式的webhook数据
	var webhookEvent CreemWebhookEvent
	if err := c.ShouldBindJSON(&webhookEvent); err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Creem webhook 解析失败 path=%q client_ip=%s error=%q payload_sha256=%s", webhookPath, c.ClientIP(), err.Error(), payloadFingerprint))
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}

	logger.LogInfo(c.Request.Context(), fmt.Sprintf("Creem webhook 解析成功 event_type=%s event_id=%s request_id=%s order_id=%s order_status=%s", webhookEvent.EventType, webhookEvent.Id, webhookEvent.Object.RequestId, webhookEvent.Object.Order.Id, webhookEvent.Object.Order.Status))

	// 根据事件类型处理不同的webhook
	switch webhookEvent.EventType {
	case "checkout.completed":
		handleCheckoutCompleted(c, &webhookEvent)
	case "refund.created", "dispute.created":
		reversals, err := creemReversalsFromEvent(&webhookEvent)
		if err == nil {
			for _, reversal := range reversals {
				if err = model.ApplyVerifiedTopUpReversal(model.DB, reversal); err != nil {
					break
				}
			}
		}
		if err != nil {
			logger.LogError(c.Request.Context(), fmt.Sprintf("Creem 支付冲正未完成 event_type=%s event_id=%s error=%q", webhookEvent.EventType, webhookEvent.Id, err.Error()))
			c.AbortWithStatus(http.StatusInternalServerError)
			return
		}
		c.Status(http.StatusOK)
	default:
		logger.LogInfo(c.Request.Context(), fmt.Sprintf("Creem webhook 忽略事件 event_type=%s event_id=%s", webhookEvent.EventType, webhookEvent.Id))
		c.Status(http.StatusOK)
	}
}

// creemReversalsFromEvent 只接受验签后的现金事件，不使用客户端元数据定位充值。
func creemReversalsFromEvent(event *CreemWebhookEvent) ([]model.VerifiedTopUpReversal, error) {
	if event == nil || (event.EventType != "refund.created" && event.EventType != "dispute.created") {
		return nil, model.ErrTopUpReversalConflict
	}
	object := &event.Object
	transaction := &object.Transaction
	if event.Id == "" || len(event.Id) > 191 || object.Id == "" || event.CreatedAt < 1000 ||
		transaction.Id == "" || transaction.Order == "" || transaction.AmountPaid <= 0 ||
		object.Order.Id != transaction.Order || object.Order.Transaction != transaction.Id ||
		int64(object.Order.AmountPaid) != transaction.AmountPaid ||
		strings.TrimSpace(transaction.Currency) == "" || !strings.EqualFold(object.Order.Currency, transaction.Currency) {
		return nil, model.ErrTopUpReversalConflict
	}
	reversal := model.VerifiedTopUpReversal{
		Provider:      model.PaymentProviderCreem,
		PaymentID:     transaction.Order,
		EventID:       event.Id,
		ObjectID:      object.Id,
		Currency:      transaction.Currency,
		OriginalMinor: transaction.AmountPaid,
		OccurredAt:    event.CreatedAt / 1000,
	}
	if event.EventType == "dispute.created" {
		if object.Object != "dispute" || transaction.Status != "chargeback" ||
			object.Amount <= 0 || object.Amount > transaction.AmountPaid || !strings.EqualFold(object.Currency, transaction.Currency) {
			return nil, model.ErrTopUpReversalConflict
		}
		// 官方仅定义创建事件；不猜测胜诉状态，更不把退款累计字段当争议结果。
		reversal.Kind, reversal.AmountMinor = "dispute", object.Amount
		return []model.VerifiedTopUpReversal{reversal}, nil
	}
	if object.Object != "refund" || object.Status != "succeeded" ||
		object.RefundAmount <= 0 || object.RefundAmount > transaction.AmountPaid ||
		!strings.EqualFold(object.RefundCurrency, transaction.Currency) {
		return nil, model.ErrTopUpReversalConflict
	}
	reversal.Kind, reversal.AmountMinor, reversal.Terminal = "refund_item", object.RefundAmount, true
	itemKey, err := model.NewRebateIdempotencyKey("creem-refund-item-event-v1", event.Id)
	if err != nil {
		return nil, err
	}
	reversal.EventID = itemKey
	if transaction.RefundedAmount == nil {
		return []model.VerifiedTopUpReversal{reversal}, nil
	}
	if *transaction.RefundedAmount < object.RefundAmount || *transaction.RefundedAmount > transaction.AmountPaid {
		return nil, model.ErrTopUpReversalConflict
	}
	// 同时保存单笔不可变金额与渠道累计数；模型取两种表示的最大值，禁止双扣。
	total := reversal
	total.Kind, total.ObjectID, total.AmountMinor = "refund_total", transaction.Id, *transaction.RefundedAmount
	total.EventID, err = model.NewRebateIdempotencyKey("creem-refund-total-event-v1", event.Id)
	if err != nil {
		return nil, err
	}
	return []model.VerifiedTopUpReversal{total, reversal}, nil
}

// 处理支付完成事件
func handleCheckoutCompleted(c *gin.Context, event *CreemWebhookEvent) {
	// 验证订单状态
	if event.Object.Order.Status != "paid" {
		logger.LogInfo(c.Request.Context(), fmt.Sprintf("Creem 订单状态未支付，忽略处理 request_id=%s order_id=%s order_status=%s", event.Object.RequestId, event.Object.Order.Id, event.Object.Order.Status))
		c.Status(http.StatusOK)
		return
	}

	// 获取引用ID（这是我们创建订单时传递的request_id）
	referenceId := event.Object.RequestId
	if referenceId == "" {
		logger.LogWarn(c.Request.Context(), fmt.Sprintf("Creem webhook 缺少 request_id event_id=%s order_id=%s", event.Id, event.Object.Order.Id))
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(event.Object.Order.Currency) == "" || strings.TrimSpace(event.Object.Product.Currency) == "" {
		logger.LogWarn(c.Request.Context(), fmt.Sprintf("Creem webhook 缺少币种 trade_no=%s event_id=%s order_id=%s", referenceId, event.Id, event.Object.Order.Id))
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}

	// Try complete subscription order first
	LockOrder(referenceId)
	defer UnlockOrder(referenceId)
	confirmation := model.PaymentConfirmation{
		Amount: decimal.NewFromInt(int64(event.Object.Order.AmountPaid)).
			Shift(-2).
			String(),
		Currency:         event.Object.Order.Currency,
		ExpectedCurrency: event.Object.Product.Currency,
		DecimalPlaces:    2,
		AllowOverpayment: true,
	}
	if err := model.CompleteSubscriptionOrderWithConfirmation(referenceId, common.GetJsonString(event), model.PaymentProviderCreem, "", confirmation); err == nil {
		logger.LogInfo(c.Request.Context(), fmt.Sprintf("Creem 订阅订单处理成功 trade_no=%s creem_order_id=%s", referenceId, event.Object.Order.Id))
		c.Status(http.StatusOK)
		return
	} else if err != nil && !errors.Is(err, model.ErrSubscriptionOrderNotFound) {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Creem 订阅订单处理失败 trade_no=%s creem_order_id=%s error=%q", referenceId, event.Object.Order.Id, err.Error()))
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	// 验证订单类型，目前只处理一次性付款（充值）
	if event.Object.Order.Type != "onetime" {
		logger.LogInfo(c.Request.Context(), fmt.Sprintf("Creem 暂不支持该订单类型，忽略处理 request_id=%s creem_order_id=%s order_type=%s", referenceId, event.Object.Order.Id, event.Object.Order.Type))
		c.Status(http.StatusOK)
		return
	}

	logger.LogInfo(c.Request.Context(), fmt.Sprintf("Creem 支付完成回调 trade_no=%s creem_order_id=%s amount_paid=%d currency=%s product_id=%s customer_email_present=%t customer_name_present=%t", referenceId, event.Object.Order.Id, event.Object.Order.AmountPaid, event.Object.Order.Currency, event.Object.Product.Id, event.Object.Customer.Email != "", event.Object.Customer.Name != ""))

	identity := model.TopUpPaymentIdentity{Provider: model.PaymentProviderCreem, CheckoutID: event.Object.Id, PaymentID: event.Object.Order.Id, Currency: event.Object.Order.Currency, PaidMinor: int64(event.Object.Order.AmountPaid)}
	err := model.RechargeCreem(referenceId, c.ClientIP(), confirmation, identity)
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Creem 充值处理失败 trade_no=%s creem_order_id=%s client_ip=%s error=%q", referenceId, event.Object.Order.Id, c.ClientIP(), err.Error()))
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	logger.LogInfo(c.Request.Context(), fmt.Sprintf("Creem 充值成功 trade_no=%s creem_order_id=%s client_ip=%s", referenceId, event.Object.Order.Id, c.ClientIP()))
	c.Status(http.StatusOK)
}

type CreemCheckoutRequest struct {
	ProductId string `json:"product_id"`
	RequestId string `json:"request_id"`
	Customer  struct {
		Email string `json:"email"`
	} `json:"customer"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

type CreemCheckoutResponse struct {
	CheckoutUrl string `json:"checkout_url"`
	Id          string `json:"id"`
}

func genCreemLink(ctx context.Context, referenceId string, product *CreemProduct, email string, username string, registerTopUp ...bool) (string, error) {
	if setting.CreemApiKey == "" {
		return "", fmt.Errorf("未配置Creem API密钥")
	}

	// 根据测试模式选择 API 端点
	apiUrl := "https://api.creem.io/v1/checkouts"
	if setting.CreemTestMode {
		apiUrl = "https://test-api.creem.io/v1/checkouts"
		logger.LogInfo(ctx, fmt.Sprintf("Creem 使用测试环境 api_url=%s", apiUrl))
	}

	// 构建请求数据，确保包含用户邮箱
	requestData := CreemCheckoutRequest{
		ProductId: product.ProductId,
		RequestId: referenceId, // 这个作为订单ID传递给Creem
		Customer: struct {
			Email string `json:"email"`
		}{
			Email: email, // 用户邮箱会在支付页面预填充
		},
		Metadata: map[string]string{
			"username":     username,
			"reference_id": referenceId,
			"product_name": product.Name,
			"quota":        fmt.Sprintf("%d", product.Quota),
		},
	}

	// 序列化请求数据
	jsonData, err := json.Marshal(requestData)
	if err != nil {
		return "", fmt.Errorf("序列化请求数据失败: %v", err)
	}

	// 创建 HTTP 请求
	req, err := http.NewRequest("POST", apiUrl, bytes.NewBuffer(jsonData))
	if err != nil {
		return "", fmt.Errorf("创建HTTP请求失败: %v", err)
	}

	// 设置请求头
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", setting.CreemApiKey)

	logger.LogInfo(ctx, fmt.Sprintf("Creem 支付请求已发送 api_url=%s product_id=%s email_present=%t trade_no=%s", apiUrl, product.ProductId, email != "", referenceId))

	// 发送请求
	client := &http.Client{
		Timeout: 30 * time.Second,
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("发送HTTP请求失败: %v", err)
	}
	defer resp.Body.Close()

	// 读取响应
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("读取响应失败: %v", err)
	}

	logger.LogInfo(ctx, fmt.Sprintf("Creem API 响应已收到 trade_no=%s status_code=%d payload_bytes=%d payload_sha256=%s", referenceId, resp.StatusCode, len(body), paymentPayloadFingerprint(body)))

	// 检查响应状态
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("Creem API http status %d ", resp.StatusCode)
	}
	// 解析响应
	var checkoutResp CreemCheckoutResponse
	err = json.Unmarshal(body, &checkoutResp)
	if err != nil {
		return "", fmt.Errorf("解析响应失败: %v", err)
	}

	if checkoutResp.CheckoutUrl == "" {
		return "", fmt.Errorf("Creem API resp no checkout url ")
	}

	if len(registerTopUp) == 1 && registerTopUp[0] {
		if err := model.RegisterTopUpCheckout(referenceId, model.PaymentProviderCreem, checkoutResp.Id); err != nil {
			return "", err
		}
	}
	logger.LogInfo(ctx, fmt.Sprintf("Creem 支付链接创建成功 trade_no=%s response_id=%s", referenceId, checkoutResp.Id))
	return checkoutResp.CheckoutUrl, nil
}
