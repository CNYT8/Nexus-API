package middleware

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

// BodyStorageCleanup 请求体存储清理中间件
// 在请求处理完成后自动清理磁盘/内存缓存
func BodyStorageCleanup() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Register before entering handlers so panic/abort paths also release files.
		defer service.CleanupFileSources(c)
		defer common.CleanupBodyStorage(c)
		c.Next()
	}
}
