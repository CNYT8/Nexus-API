package controller

import (
	"errors"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

const webRiskVerifyBodyLimit = 4 << 10

var verifyWebRiskTurnstile = service.VerifyTurnstile

type webRiskVerifyRequest struct {
	Turnstile string `json:"turnstile"`
}

func GetWebRiskStatus(c *gin.Context) {
	if !common.TurnstileConfigured() {
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"data": gin.H{
				"required":   false,
				"configured": false,
			},
		})
		return
	}
	if c.GetBool("use_access_token") {
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"data": gin.H{
				"required":           false,
				"configured":         true,
				"turnstile_site_key": common.TurnstileSiteKey,
			},
		})
		return
	}

	status, err := model.ObserveWebRiskIP(c.GetInt("id"), c.ClientIP())
	if err != nil {
		common.SysLog("failed to observe web risk IP from status endpoint: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": common.TranslateMessage(c, i18n.MsgDatabaseError),
		})
		return
	}
	middleware.UpdateWebRiskChallengeCache(c.GetInt("id"), status.Challenged)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"required":           status.Challenged,
			"configured":         true,
			"turnstile_site_key": common.TurnstileSiteKey,
		},
	})
}

func VerifyWebRisk(c *gin.Context) {
	if !common.TurnstileConfigured() {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": common.TranslateMessage(c, i18n.MsgWebRiskNotConfigured),
		})
		return
	}

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, webRiskVerifyBodyLimit)
	var request webRiskVerifyRequest
	if err := c.ShouldBindJSON(&request); err != nil || strings.TrimSpace(request.Turnstile) == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": common.TranslateMessage(c, i18n.MsgInvalidParams),
		})
		return
	}

	err := verifyWebRiskTurnstile(c.Request.Context(), request.Turnstile, c.ClientIP())
	if err != nil {
		status := http.StatusBadRequest
		message := common.TranslateMessage(c, i18n.MsgWebRiskVerificationFailed)
		if !errors.Is(err, service.ErrTurnstileTokenRequired) &&
			!errors.Is(err, service.ErrTurnstileVerificationFailed) &&
			!errors.Is(err, service.ErrTurnstileNotConfigured) {
			status = http.StatusBadGateway
			common.SysLog("web risk Turnstile verification request failed: " + err.Error())
		}
		c.JSON(status, gin.H{"success": false, "message": message})
		return
	}

	userId := c.GetInt("id")
	if err := model.ResetWebRisk(userId); err != nil {
		common.SysLog("failed to reset web risk state: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": common.TranslateMessage(c, i18n.MsgDatabaseError),
		})
		return
	}
	middleware.ClearWebRiskChallengeCache(userId)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": common.TranslateMessage(c, i18n.MsgWebRiskVerificationSuccess),
	})
}
