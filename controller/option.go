package controller

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/console_setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/setting/system_setting"

	"github.com/gin-gonic/gin"
)

var completionRatioMetaOptionKeys = []string{
	"ModelPrice",
	"ModelRatio",
	"CompletionRatio",
	"CacheRatio",
	"CreateCacheRatio",
	"ImageRatio",
	"AudioRatio",
	"AudioCompletionRatio",
}

func isPaymentComplianceOptionKey(key string) bool {
	return strings.HasPrefix(key, "payment_setting.compliance_")
}

func isPositiveOptionValue(value string) bool {
	intValue, err := strconv.Atoi(strings.TrimSpace(value))
	if err == nil {
		return intValue > 0
	}
	floatValue, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	return err == nil && floatValue > 0
}

func collectModelNamesFromOptionValue(raw string, modelNames map[string]struct{}) {
	if strings.TrimSpace(raw) == "" {
		return
	}

	var parsed map[string]any
	if err := common.UnmarshalJsonStr(raw, &parsed); err != nil {
		return
	}

	for modelName := range parsed {
		modelNames[modelName] = struct{}{}
	}
}

func buildCompletionRatioMetaValue(optionValues map[string]string) string {
	modelNames := make(map[string]struct{})
	for _, key := range completionRatioMetaOptionKeys {
		collectModelNamesFromOptionValue(optionValues[key], modelNames)
	}

	meta := make(map[string]ratio_setting.CompletionRatioInfo, len(modelNames))
	for modelName := range modelNames {
		meta[modelName] = ratio_setting.GetCompletionRatioInfo(modelName)
	}

	jsonBytes, err := common.Marshal(meta)
	if err != nil {
		return "{}"
	}
	return string(jsonBytes)
}

const maxOutputSensitiveConfigRequestBytes int64 = 8 << 20

func isOutputSensitiveOptionKey(key string) bool {
	return model.IsOutputSensitiveOptionKey(key)
}

func GetOptions(c *gin.Context) {
	var options []*model.Option
	optionValues := make(map[string]string)
	common.OptionMapRWMutex.Lock()
	for k, v := range common.OptionMap {
		value := common.Interface2String(v)
		isSensitiveKey := strings.HasSuffix(k, "Token") ||
			strings.HasSuffix(k, "Secret") ||
			strings.HasSuffix(k, "Key") ||
			strings.HasSuffix(k, "secret") ||
			strings.HasSuffix(k, "api_key") ||
			isOutputSensitiveOptionKey(k)
		if k == "TurnstileSecretKeyConfigured" {
			continue
		}
		if isSensitiveKey && k != "TurnstileSiteKey" {
			continue
		}
		options = append(options, &model.Option{
			Key:   k,
			Value: value,
		})
		for _, optionKey := range completionRatioMetaOptionKeys {
			if optionKey == k {
				optionValues[k] = value
				break
			}
		}
	}
	options = append(options, &model.Option{
		Key:   "TurnstileSecretKeyConfigured",
		Value: strconv.FormatBool(strings.TrimSpace(common.TurnstileSecretKey) != ""),
	})
	common.OptionMapRWMutex.Unlock()
	options = append(options, &model.Option{
		Key:   "CompletionRatioMeta",
		Value: buildCompletionRatioMetaValue(optionValues),
	})
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    options,
	})
}

type OutputSensitiveConfigRequest struct {
	Enabled      bool     `json:"enabled"`
	Action       string   `json:"action"`
	MatchPercent int      `json:"match_percent"`
	Patterns     []string `json:"patterns"`
}

func GetOutputSensitiveConfig(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    setting.GetOutputSensitiveConfig(),
	})
}

func UpdateOutputSensitiveConfig(c *gin.Context) {
	if c.Request.Body == nil {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxOutputSensitiveConfigRequestBytes)
	var request OutputSensitiveConfigRequest
	if err := common.DecodeJson(c.Request.Body, &request); err != nil {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	configValue := setting.OutputSensitiveConfig{
		Enabled:      request.Enabled,
		Action:       request.Action,
		MatchPercent: request.MatchPercent,
		Patterns:     request.Patterns,
	}
	if err := model.UpdateOutputSensitiveConfig(configValue); err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	recordManageAudit(c, "option.update", map[string]interface{}{
		"key": "OutputSensitiveConfig",
	})
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
	})
}

type RebateConfigRequest struct {
	Fields   map[string]string `json:"fields"`
	Revision string            `json:"revision"`
}

func GetRebateConfig(c *gin.Context) {
	snapshot, err := model.GetRebateConfig()
	if err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": snapshot})
}

func UpdateRebateConfig(c *gin.Context) {
	if c.Request.Body == nil {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	var request RebateConfigRequest
	if err := decoder.Decode(&request); err != nil {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	snapshot, err := model.UpdateRebateConfig(request.Fields, request.Revision)
	if err != nil {
		if errors.Is(err, model.ErrRebateConfigConflict) {
			c.JSON(http.StatusConflict, gin.H{"success": false, "message": err.Error()})
		} else {
			common.ApiErrorMsg(c, err.Error())
		}
		return
	}
	recordManageAudit(c, "option.update", map[string]interface{}{"key": "RebateConfig"})
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": snapshot})
}

type OptionUpdateRequest struct {
	Key   string `json:"key"`
	Value any    `json:"value"`
}

var turnstileOptionUpdateMu sync.Mutex

func UpdateOption(c *gin.Context) {
	var option OptionUpdateRequest
	err := common.DecodeJson(c.Request.Body, &option)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "无效的参数",
		})
		return
	}
	if isOutputSensitiveOptionKey(option.Key) {
		common.ApiErrorMsg(c, "输出敏感词设置只能通过独立设置页修改")
		return
	}
	if strings.EqualFold(strings.TrimSpace(option.Key), "TicketEncryptionSecret") {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	switch option.Value.(type) {
	case bool:
		option.Value = common.Interface2String(option.Value.(bool))
	case float64:
		option.Value = common.Interface2String(option.Value.(float64))
	case int:
		option.Value = common.Interface2String(option.Value.(int))
	default:
		option.Value = fmt.Sprintf("%v", option.Value)
	}
	switch option.Key {
	case "QuotaForInviter", "QuotaForInvitee":
		if isPositiveOptionValue(option.Value.(string)) && !operation_setting.IsPaymentComplianceConfirmed() {
			common.ApiErrorI18n(c, i18n.MsgPaymentComplianceRequired)
			return
		}
	default:
		if isPaymentComplianceOptionKey(option.Key) {
			common.ApiErrorMsg(c, "合规确认字段不允许通过通用设置接口修改")
			return
		}
	}
	if option.Key == "TurnstileCheckEnabled" || option.Key == "TurnstileSiteKey" || option.Key == "TurnstileSecretKey" {
		// 同一实例内把凭据校验与保存串行化，避免清空和启用请求交错。
		turnstileOptionUpdateMu.Lock()
		defer turnstileOptionUpdateMu.Unlock()
	}

	switch option.Key {
	case "GitHubOAuthEnabled":
		if option.Value == "true" && common.GitHubClientId == "" {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "无法启用 GitHub OAuth，请先填入 GitHub Client Id 以及 GitHub Client Secret！",
			})
			return
		}
	case "discord.enabled":
		if option.Value == "true" && system_setting.GetDiscordSettings().ClientId == "" {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "无法启用 Discord OAuth，请先填入 Discord Client Id 以及 Discord Client Secret！",
			})
			return
		}
	case "oidc.enabled":
		if option.Value == "true" && system_setting.GetOIDCSettings().ClientId == "" {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "无法启用 OIDC 登录，请先填入 OIDC Client Id 以及 OIDC Client Secret！",
			})
			return
		}
	case "LinuxDOOAuthEnabled":
		if option.Value == "true" && common.LinuxDOClientId == "" {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "无法启用 LinuxDO OAuth，请先填入 LinuxDO Client Id 以及 LinuxDO Client Secret！",
			})
			return
		}
	case "EmailDomainRestrictionEnabled":
		if option.Value == "true" && len(common.EmailDomainWhitelist) == 0 {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "无法启用邮箱域名限制，请先填入限制的邮箱域名！",
			})
			return
		}
	case "WeChatAuthEnabled":
		if option.Value == "true" && common.WeChatServerAddress == "" {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "无法启用微信登录，请先填入微信登录相关配置信息！",
			})
			return
		}
	case "TurnstileSecretKeyConfigured":
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	case "TurnstileSiteKey", "TurnstileSecretKey":
		option.Value = strings.TrimSpace(option.Value.(string))
		common.OptionMapRWMutex.RLock()
		turnstileEnabled := common.TurnstileCheckEnabled
		common.OptionMapRWMutex.RUnlock()
		if turnstileEnabled && option.Value == "" {
			common.ApiErrorMsg(c, "无法启用 Turnstile 校验，请先填入 Turnstile 校验相关配置信息！")
			return
		}
	case "TurnstileCheckEnabled":
		common.OptionMapRWMutex.RLock()
		turnstileConfigured := strings.TrimSpace(common.TurnstileSiteKey) != "" && strings.TrimSpace(common.TurnstileSecretKey) != ""
		common.OptionMapRWMutex.RUnlock()
		if option.Value == "true" && !turnstileConfigured {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "无法启用 Turnstile 校验，请先填入 Turnstile 校验相关配置信息！",
			})

			return
		}
	case "TelegramOAuthEnabled":
		if option.Value == "true" && common.TelegramBotToken == "" {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "无法启用 Telegram OAuth，请先填入 Telegram Bot Token！",
			})
			return
		}
	case "fetch_setting.allowed_ports":
		var ports []string
		if err := common.Unmarshal([]byte(option.Value.(string)), &ports); err != nil {
			common.ApiErrorMsg(c, "允许端口必须是字符串数组")
			return
		}
		if _, err := common.NewSSRFProtectionFromFetchSetting(false, false, false, nil, nil, ports, true); err != nil {
			common.ApiErrorMsg(c, err.Error())
			return
		}
	case "theme.frontend":
		if option.Value != "default" && option.Value != "classic" {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "无效的主题值，可选值：default（新版前端）、classic（经典前端）",
			})
			return
		}
	case "GroupRatio":
		err = ratio_setting.CheckGroupRatio(option.Value.(string))
		if err != nil {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": err.Error(),
			})
			return
		}
	case "ImageRatio":
		err = ratio_setting.UpdateImageRatioByJSONString(option.Value.(string))
		if err != nil {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "图片倍率设置失败: " + err.Error(),
			})
			return
		}
	case "AudioRatio":
		err = ratio_setting.UpdateAudioRatioByJSONString(option.Value.(string))
		if err != nil {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "音频倍率设置失败: " + err.Error(),
			})
			return
		}
	case "AudioCompletionRatio":
		err = ratio_setting.UpdateAudioCompletionRatioByJSONString(option.Value.(string))
		if err != nil {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "音频补全倍率设置失败: " + err.Error(),
			})
			return
		}
	case "CreateCacheRatio":
		err = ratio_setting.UpdateCreateCacheRatioByJSONString(option.Value.(string))
		if err != nil {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "缓存创建倍率设置失败: " + err.Error(),
			})
			return
		}
	case "ModelRequestRateLimitGroup":
		err = setting.CheckModelRequestRateLimitGroup(option.Value.(string))
		if err != nil {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": err.Error(),
			})
			return
		}
	case "register_setting.default_group":
		groupName := option.Value.(string)
		if groupName != "" && groupName != "default" {
			if _, ok := ratio_setting.GetGroupRatioCopy()[groupName]; !ok {
				c.JSON(http.StatusOK, gin.H{
					"success": false,
					"message": "分组 " + groupName + " 不存在",
				})
				return
			}
		}
	case "AutomaticDisableStatusCodes":
		_, err = operation_setting.ParseHTTPStatusCodeRanges(option.Value.(string))
		if err != nil {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": err.Error(),
			})
			return
		}
	case "AutomaticRetryStatusCodes":
		_, err = operation_setting.ParseHTTPStatusCodeRanges(option.Value.(string))
		if err != nil {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": err.Error(),
			})
			return
		}
	case "checkin_setting.stage_rules":
		_, err = operation_setting.ParseCheckinStageRules(option.Value.(string))
		if err != nil {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": err.Error(),
			})
			return
		}
	case "console_setting.api_info":
		err = console_setting.ValidateConsoleSettings(option.Value.(string), "ApiInfo")
		if err != nil {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": err.Error(),
			})
			return
		}
	case "console_setting.announcements":
		err = console_setting.ValidateConsoleSettings(option.Value.(string), "Announcements")
		if err != nil {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": err.Error(),
			})
			return
		}
	case "console_setting.faq":
		err = console_setting.ValidateConsoleSettings(option.Value.(string), "FAQ")
		if err != nil {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": err.Error(),
			})
			return
		}
	case "console_setting.uptime_kuma_groups":
		err = console_setting.ValidateConsoleSettings(option.Value.(string), "UptimeKumaGroups")
		if err != nil {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": err.Error(),
			})
			return
		}
	}
	recordIpLogChanged := option.Key == "DefaultRecordIpLogEnabled" &&
		strconv.FormatBool(common.DefaultRecordIpLogEnabled) != option.Value.(string)
	if recordIpLogChanged {
		ts := strconv.FormatInt(common.GetTimestamp(), 10)
		// 先写时间戳；若失败则中止，避免在陈旧时间戳上翻转全局开关导致 last-writer-wins 判定错误。
		if err := model.UpdateOption("DefaultRecordIpLogUpdatedAt", ts); err != nil {
			common.ApiError(c, err)
			return
		}
	}
	err = model.UpdateOption(option.Key, option.Value.(string))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "option.update", map[string]interface{}{
		"key": option.Key,
	})
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
	})
}

func CheckDatabaseIntegrity(c *gin.Context) {
	report := model.CheckDatabaseIntegrity()
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    report,
	})
}

var walletLedgerInitMu sync.Mutex

func InitializeWalletLedger(c *gin.Context) {
	if !walletLedgerInitMu.TryLock() {
		common.ApiErrorMsg(c, "钱包账本初始化正在执行中，请勿重复提交")
		return
	}
	defer walletLedgerInitMu.Unlock()

	if model.DB == nil {
		common.ApiErrorMsg(c, "主数据库未初始化")
		return
	}

	active, err := model.WalletCreditLedgerActive(model.DB)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if active {
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"message": "钱包账本此前已完成初始化，无需重复操作",
			"data": gin.H{
				"already_initialized": true,
				"initialized":         true,
			},
		})
		return
	}

	service.SetRebateMaintenancePaused(true)
	defer service.SetRebateMaintenancePaused(false)
	defer middleware.EndWalletMaintenanceGate()
	if !middleware.BeginWalletMaintenanceGate(3*time.Second) || !service.WaitForRebateTasksIdle(2*time.Second) {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"success": false,
			"message": "钱包账本初始化已中止：仍有请求或后台账务任务未完成，请稍后重试",
		})
		return
	}

	if err := model.InitializeWalletCreditBaseline(model.DB); err != nil {
		if errors.Is(err, model.ErrWalletBaselineBusy) {
			c.Header("Retry-After", "5")
			c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "message": err.Error()})
		} else {
			common.ApiError(c, err)
		}
		return
	}

	afterUserID := 0
	const batchSize = 2000
	var mismatches int
	for {
		batch, next, done, recErr := model.ReconcileWalletCreditBatch(model.DB, afterUserID, batchSize)
		if recErr != nil {
			common.ApiError(c, recErr)
			return
		}
		mismatches += len(batch)
		if done {
			break
		}
		afterUserID = next
	}

	recordManageAudit(c, "option.wallet_ledger_init", map[string]interface{}{
		"mismatches": mismatches,
	})

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "钱包账本初始化成功",
		"data": gin.H{
			"already_initialized": false,
			"initialized":         true,
			"mismatches":          mismatches,
		},
	})
}
