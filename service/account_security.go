package service

import (
	"net/http"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

// The common account bucket bounds attempts even when callers switch actions.
// Context marking avoids charging twice when a route and its handler both check.
func AllowAccountSecurityAction(c *gin.Context, userID int, action string) bool {
	if checked, ok := c.Get("account_security_checked"); ok && checked == userID {
		return true
	}
	allowed, err := model.TakeAccountSecurityLimit(userID, "sensitive", 30, 5*time.Minute)
	if err == nil && allowed {
		allowed, err = model.TakeAccountSecurityLimit(userID, action, 10, 5*time.Minute)
	}
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "message": common.TranslateMessage(c, i18n.MsgDatabaseError)})
		c.Abort()
		return false
	}
	if !allowed {
		c.Header("Retry-After", "300")
		c.JSON(http.StatusTooManyRequests, gin.H{"success": false, "message": "操作过于频繁，请稍后重试"})
		c.Abort()
		return false
	}
	c.Set("account_security_checked", userID)
	return true
}
