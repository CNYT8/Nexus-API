package middleware

import (
	"net/http"

	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

// Mount after UserAuth/TokenOrUserAuth on authenticated sensitive routes.
func AccountSecurityRateLimit(action string) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := c.GetInt("id")
		if userID <= 0 {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		if service.AllowAccountSecurityAction(c, userID, action) {
			c.Next()
		}
	}
}
