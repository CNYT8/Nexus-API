package middleware

import (
	"errors"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
)

func TurnstileCheck() gin.HandlerFunc {
	return func(c *gin.Context) {
		if common.TurnstileCheckEnabled {
			if c.Request.URL.Path == "/api/user/register" && !common.RegisterEnabled {
				c.Next()
				return
			}
			session := sessions.Default(c)
			turnstileChecked := session.Get("turnstile")
			if turnstileChecked != nil {
				c.Next()
				return
			}
			response := c.Query("turnstile")
			if response == "" {
				c.JSON(http.StatusOK, gin.H{
					"success": false,
					"message": "Turnstile token 为空",
				})
				c.Abort()
				return
			}
			err := service.VerifyTurnstile(c.Request.Context(), response, c.ClientIP())
			if err != nil {
				common.SysLog(err.Error())
				message := err.Error()
				if errors.Is(err, service.ErrTurnstileVerificationFailed) {
					message = "Turnstile 校验失败，请刷新重试！"
				}
				c.JSON(http.StatusOK, gin.H{
					"success": false,
					"message": message,
				})
				c.Abort()
				return
			}
			session.Set("turnstile", true)
			err = session.Save()
			if err != nil {
				c.JSON(http.StatusOK, gin.H{
					"message": "无法保存会话信息，请重试",
					"success": false,
				})
				return
			}
		}
		c.Next()
	}
}
