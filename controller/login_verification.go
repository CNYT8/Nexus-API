package controller

import (
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

// Every primary identity provider enters the same local MFA gate.
func completePrimaryLogin(user *model.User, c *gin.Context, provider string) {
	if user == nil || user.Status != common.UserStatusEnabled {
		common.ApiErrorI18n(c, i18n.MsgAuthUserBanned)
		return
	}
	enabled, err := model.IsTwoFAEnabled(user.Id)
	if err != nil {
		common.ApiErrorI18n(c, i18n.MsgDatabaseError)
		return
	}
	if enabled {
		if err := service.BeginLoginVerification(c, user, provider); err != nil {
			common.ApiErrorI18n(c, i18n.MsgUserSessionSaveFailed)
			return
		}
		c.JSON(http.StatusOK, gin.H{"success": true, "message": i18n.T(c, i18n.MsgUserRequire2FA), "data": gin.H{"require_2fa": true}})
		return
	}
	setupLogin(user, c)
}
