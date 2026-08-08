package middleware

import (
	"fmt"
	"net/http"
	"runtime/debug"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

func RelayPanicRecover() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if err := recover(); err != nil {
				common.SysLog(fmt.Sprintf("panic detected: %v", err))
				common.SysLog(fmt.Sprintf("stacktrace from panic: %s", string(debug.Stack())))
				statusCode, message := service.ApplyErrorMaskToMessage(
					c,
					http.StatusInternalServerError,
					common.MessageWithRequestId("internal server error", c.GetString(common.RequestIdKey)),
					"internal_server_error",
					"new_api_panic",
				)
				c.JSON(statusCode, gin.H{
					"error": gin.H{
						"message": message,
						"type":    "new_api_panic",
						"code":    "internal_server_error",
					},
				})
				c.Abort()
			}
		}()
		c.Next()
	}
}
