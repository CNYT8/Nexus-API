package controller

import (
	"net/http"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

// rebateAdminRequest is the body shared by the administrative rebate mutation
// endpoints. Both fields are required and bounded so one request can neither
// mutate an unbounded number of financial rows nor hide its audit reason.
type rebateAdminRequest struct {
	IDs    []int64 `json:"ids" binding:"required"`
	Reason string  `json:"reason" binding:"required"`
}

// VoidUserRebates cancels unpaid rebate obligations (pending or held). Paid
// records are returned in "skipped" and must use the clawback endpoint.
func VoidUserRebates(c *gin.Context) {
	operatorID := c.GetInt("id")
	var req rebateAdminRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorMsg(c, "参数错误")
		return
	}
	voided, skipped, err := model.VoidWalletRebates(model.DB, req.IDs, operatorID, req.Reason)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"voided":  voided,
			"skipped": skipped,
		},
	})
}

// AdminListUserRebates lists rebate records for fraud investigation and for
// locating the ids used by the void/clawback endpoints. Filters are validated
// and pagination is bounded server-side.
func AdminListUserRebates(c *gin.Context) {
	inviterID, _ := strconv.Atoi(c.DefaultQuery("inviter_id", "0"))
	if inviterID < 0 {
		inviterID = 0
	}
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
	records, total, err := model.AdminListWalletRebates(model.DB, model.AdminRebateRecordFilter{
		InviterID: inviterID,
		Status:    c.Query("status"),
	}, (page-1)*pageSize, pageSize)
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

// ClawbackUserRebates reverses already-paid rewards after confirmed fraud. It
// debits the inviter's wallet with an idempotent ledger adjustment, so a retry
// never debits twice, and returns every record it could not claw back.
func ClawbackUserRebates(c *gin.Context) {
	operatorID := c.GetInt("id")
	var req rebateAdminRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorMsg(c, "参数错误")
		return
	}
	clawed, skipped, err := model.ClawbackPaidWalletRebates(model.DB, req.IDs, operatorID, req.Reason)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"clawed_back": clawed,
			"skipped":     skipped,
		},
	})
}
