package controller

import (
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

func allowAccountSecurityAction(c *gin.Context, userID int, action string) bool {
	return service.AllowAccountSecurityAction(c, userID, action)
}
