package controller

import (
	"errors"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type EmptyResponseSettingRequest struct {
	Enabled       bool `json:"enabled"`
	PeriodDays    int  `json:"period_days"`
	RefundPercent int  `json:"refund_percent"`
}

func emptyResponseError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, model.ErrEmptyResponseFeatureDisabled):
		common.ApiErrorMsg(c, "空回检测未开启")
	case errors.Is(err, gorm.ErrRecordNotFound):
		common.ApiErrorI18n(c, i18n.MsgUserNotExists)
	default:
		common.SysLog("empty response operation failed: " + err.Error())
		common.ApiErrorMsg(c, "空回检测操作失败，请稍后重试")
	}
}

func GetEmptyResponseStatus(c *gin.Context) {
	userId := c.GetInt("id")
	setting := operation_setting.GetEmptyResponseSetting()
	pendingCount, pendingQuota, refundedCount, err := model.GetEmptyResponseStatus(userId, common.GetTimestamp())
	if err != nil {
		emptyResponseError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{
		"enabled":        setting.Enabled,
		"period_days":    setting.PeriodDays,
		"refund_percent": setting.RefundPercent,
		"pending_count":  pendingCount,
		"pending_quota":  pendingQuota,
		"refunded_count": refundedCount,
	})
}

func ListEmptyResponses(c *gin.Context) {
	setting := operation_setting.GetEmptyResponseSetting()
	if !setting.Enabled {
		common.ApiErrorMsg(c, "空回检测未开启")
		return
	}
	pageInfo := common.GetPageQuery(c)
	status := c.Query("status")
	if status != "" && status != "pending" && status != "refunded" {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	records, total, err := model.ListEmptyResponses(c.GetInt("id"), status, pageInfo)
	if err != nil {
		emptyResponseError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(records)
	common.ApiSuccess(c, pageInfo)
}

func ClaimEmptyResponses(c *gin.Context) {
	userId := c.GetInt("id")
	created, refundQuota, err := model.ClaimEmptyResponseCompensation(userId, common.GetTimestamp())
	if err != nil {
		emptyResponseError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{
		"created":      created,
		"refund_quota": refundQuota,
	})
}

func GetEmptyResponseSettings(c *gin.Context) {
	common.ApiSuccess(c, operation_setting.GetEmptyResponseSetting())
}

func UpdateEmptyResponseSettings(c *gin.Context) {
	var request EmptyResponseSettingRequest
	if err := common.DecodeJson(c.Request.Body, &request); err != nil {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	setting := operation_setting.NormalizeEmptyResponseSetting(operation_setting.EmptyResponseSetting{
		Enabled:       request.Enabled,
		PeriodDays:    request.PeriodDays,
		RefundPercent: request.RefundPercent,
	})
	if err := operation_setting.ValidateEmptyResponseSetting(setting); err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	values := map[string]string{
		"empty_response_setting.enabled":        strconv.FormatBool(setting.Enabled),
		"empty_response_setting.period_days":    strconv.Itoa(setting.PeriodDays),
		"empty_response_setting.refund_percent": strconv.Itoa(setting.RefundPercent),
	}
	if err := model.UpdateOptionsBulk(values); err != nil {
		common.ApiError(c, err)
		return
	}
	if err := operation_setting.SetEmptyResponseSetting(setting); err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "empty_response.setting_update", map[string]interface{}{
		"enabled":        setting.Enabled,
		"period_days":    setting.PeriodDays,
		"refund_percent": setting.RefundPercent,
	})
	common.ApiSuccess(c, setting)
}
