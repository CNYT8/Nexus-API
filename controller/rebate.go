package controller

import (
	"net/http"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

// maxRebateRecordPage bounds (page-1)*page_size to a constant offset. With the
// 100-row page-size cap this limits the deepest scan to 100k rows per request.
const maxRebateRecordPage = 1000

// GetUserRebateSummary returns the authenticated user's invitation rebate
// snapshot. It never exposes other users' records or settings values that are
// not part of the user-facing rebate view.
func GetUserRebateSummary(c *gin.Context) {
	userID := c.GetInt("id")
	summary, err := model.GetWalletRebateSummary(model.DB, userID)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    summary,
	})
}

// GetUserRebateRecords returns a bounded page of the authenticated user's own
// rebate records. Page and page size are clamped before any offset arithmetic
// so a hostile page number can neither overflow nor force an unbounded scan.
func GetUserRebateRecords(c *gin.Context) {
	userID := c.GetInt("id")
	page, err := strconv.Atoi(c.DefaultQuery("page", "1"))
	if err != nil || page < 1 {
		page = 1
	}
	if page > maxRebateRecordPage {
		page = maxRebateRecordPage
	}
	pageSize, err := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	if err != nil || pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	records, total, err := model.GetWalletRebateRecords(model.DB, userID, (page-1)*pageSize, pageSize)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"items":     records,
			"total":     total,
			"page":      page,
			"page_size": pageSize,
		},
	})
}
