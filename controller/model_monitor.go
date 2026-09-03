package controller

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
)

func GetModelMonitor(c *gin.Context) {
	summary, err := model.GetModelMonitorSummary()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, summary)
}

func GetModelMonitorAvailability(c *gin.Context) {
	availability, err := model.GetModelMonitorAvailability()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, availability)
}
