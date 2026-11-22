package controller

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/gin-gonic/gin"
)

func GetAllLogs(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)
	logType, _ := strconv.Atoi(c.Query("type"))
	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	username := c.Query("username")
	tokenName := c.Query("token_name")
	modelName := c.Query("model_name")
	channel, _ := strconv.Atoi(c.Query("channel"))
	group := c.Query("group")
	requestId := c.Query("request_id")
	upstreamRequestId := c.Query("upstream_request_id")
	logs, total, err := model.GetAllLogs(logType, startTimestamp, endTimestamp, modelName, username, tokenName, pageInfo.GetStartIdx(), pageInfo.GetPageSize(), channel, group, requestId, upstreamRequestId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	restricted, err := shouldRestrictAdminLogChannelInfo(c)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if c.GetInt("role") == common.RoleRootUser {
		model.FormatRootLogs(logs)
	} else {
		model.FormatAdminLogs(logs)
	}
	if restricted {
		model.StripChannelRestrictedAdminLogFields(logs)
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(logs)
	common.ApiSuccess(c, pageInfo)
	return
}

func shouldRestrictAdminLogChannelInfo(c *gin.Context) (bool, error) {
	if c.GetInt("role") != common.RoleAdminUser {
		return false, nil
	}
	allowed, err := model.IsAdminPermissionAllowed(c.GetInt("id"), model.AdminPermissionChannel)
	if err != nil {
		return false, err
	}
	return !allowed, nil
}

func GetUserLogs(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)
	userId := c.GetInt("id")
	logType, _ := strconv.Atoi(c.Query("type"))
	startTimestamp, endTimestamp, err := parseLogWindow(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
		return
	}
	tokenName := c.Query("token_name")
	modelName := c.Query("model_name")
	group := c.Query("group")
	requestId := c.Query("request_id")
	upstreamRequestId := c.Query("upstream_request_id")
	logs, total, err := model.GetUserLogs(userId, logType, startTimestamp, endTimestamp, modelName, tokenName, pageInfo.GetStartIdx(), pageInfo.GetPageSize(), group, requestId, upstreamRequestId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	service.ApplyErrorMaskToUserLogs(logs)
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(logs)
	common.ApiSuccess(c, pageInfo)
	return
}

// Deprecated: SearchAllLogs 已废弃，前端未使用该接口。
func SearchAllLogs(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"success": false,
		"message": "该接口已废弃",
	})
}

// Deprecated: SearchUserLogs 已废弃，前端未使用该接口。
func SearchUserLogs(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"success": false,
		"message": "该接口已废弃",
	})
}

func GetLogByKey(c *gin.Context) {
	tokenId := c.GetInt("token_id")
	if tokenId == 0 {
		c.JSON(200, gin.H{
			"success": false,
			"message": "无效的令牌",
		})
		return
	}
	logs, err := model.GetLogByTokenId(tokenId)
	if err != nil {
		c.JSON(200, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	service.ApplyErrorMaskToUserLogs(logs)
	c.JSON(200, gin.H{
		"success": true,
		"message": "",
		"data":    logs,
	})
}

func GetLogsStat(c *gin.Context) {
	logType, _ := strconv.Atoi(c.Query("type"))
	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	tokenName := c.Query("token_name")
	username := c.Query("username")
	modelName := c.Query("model_name")
	channel, _ := strconv.Atoi(c.Query("channel"))
	group := c.Query("group")
	stat, err := model.SumUsedQuota(logType, startTimestamp, endTimestamp, modelName, username, tokenName, channel, group)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	//tokenNum := model.SumUsedToken(logType, startTimestamp, endTimestamp, modelName, username, "")
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"quota": stat.Quota,
			"rpm":   stat.Rpm,
			"tpm":   stat.Tpm,
		},
	})
	return
}

func GetLogsSelfStat(c *gin.Context) {
	userID := c.GetInt("id")
	logType, _ := strconv.Atoi(c.Query("type"))
	startTimestamp, endTimestamp, err := parseLogWindow(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
		return
	}
	tokenName := c.Query("token_name")
	modelName := c.Query("model_name")
	group := c.Query("group")
	quotaNum, err := model.SumUserUsedQuota(userID, logType, startTimestamp, endTimestamp, modelName, tokenName, group)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	//tokenNum := model.SumUsedToken(logType, startTimestamp, endTimestamp, modelName, username, tokenName)
	c.JSON(200, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"quota": quotaNum.Quota,
			"rpm":   quotaNum.Rpm,
			"tpm":   quotaNum.Tpm,
			//"token": tokenNum,
		},
	})
	return
}

func DeleteHistoryLogs(c *gin.Context) {
	// Relational clients retain the synchronous response. ClickHouse mutations
	// always return a durable task; affected-row counts are not exact.
	if model.IsClickHouseLogDB() || c.Query("async") == "true" {
		CreateLogCleanupTask(c)
		return
	}
	targetTimestamp, _ := strconv.ParseInt(c.Query("target_timestamp"), 10, 64)
	if targetTimestamp == 0 {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "target timestamp is required",
		})
		return
	}
	count, err := model.DeleteOldLog(c.Request.Context(), targetTimestamp, 100)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    count,
	})
	return
}

// GetLogSettings is safe for authenticated users and exposes no admin options.
func GetLogSettings(c *gin.Context) {
	if c.GetInt("id") <= 0 {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	settings := operation_setting.GetLogSetting()
	c.Header("Cache-Control", "no-store")
	common.ApiSuccess(c, gin.H{
		"max_range_days": settings.MaxRangeDays, "default_range_days": settings.DefaultRangeDays(),
		"user_export_enabled": settings.UserExportEnabled,
		"export_enabled":      settings.UserExportEnabled || c.GetInt("role") >= common.RoleAdminUser,
		"export_max_rows":     model.LogExportMaxRows, "export_batch_size": model.LogExportBatchSize,
		"export_concurrency_per_user": 1,
	})
}

func parseLogWindow(c *gin.Context) (int64, int64, error) {
	values := [2]int64{}
	query := c.Request.URL.Query()
	for i, key := range []string{"start_timestamp", "end_timestamp"} {
		if raw, present := query[key]; present {
			if len(raw) != 1 || raw[0] == "" {
				return 0, 0, errors.New("日志查询时间必须为单个非负整数秒时间戳")
			}
			parsed, err := strconv.ParseInt(raw[0], 10, 64)
			if err != nil || parsed < 0 {
				return 0, 0, errors.New("日志查询时间必须为非负整数秒时间戳")
			}
			values[i] = parsed
		}
	}
	return model.NormalizeUserLogWindow(values[0], values[1])
}

func ExportUserLogs(c *gin.Context) { exportLogs(c, true) }
func ExportAllLogs(c *gin.Context)  { exportLogs(c, false) }

func exportLogs(c *gin.Context, self bool) {
	if c.GetInt("id") <= 0 || (!self && c.GetInt("role") < common.RoleAdminUser) {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	filter := model.LogExportFilter{ModelName: c.Query("model_name"), Username: c.Query("username"), TokenName: c.Query("token_name"), Group: c.Query("group"), RequestID: c.Query("request_id"), UpstreamRequestID: c.Query("upstream_request_id")}
	filter.Type, _ = strconv.Atoi(c.Query("type"))
	filter.Channel, _ = strconv.Atoi(c.Query("channel"))
	if c.GetInt("role") < common.RoleAdminUser && !operation_setting.GetLogSetting().UserExportEnabled {
		c.JSON(http.StatusForbidden, gin.H{"success": false, "message": "管理员已关闭普通用户日志导出"})
		return
	}
	var err error
	filter.Start, filter.End, err = parseLogWindow(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
		return
	}
	if self {
		filter.UserID = c.GetInt("id")
	}
	restricted := false
	if !self {
		restricted, err = shouldRestrictAdminLogChannelInfo(c)
		if err != nil {
			common.ApiError(c, err)
			return
		}
	}
	exportContext, release, err := model.AcquireLogExportLease(c.Request.Context(), c.GetInt("id"))
	if err != nil {
		status := http.StatusServiceUnavailable
		if errors.Is(err, model.ErrLogExportInProgress) {
			status = http.StatusTooManyRequests
		}
		c.JSON(status, gin.H{"success": false, "message": err.Error()})
		return
	}
	defer release()
	writeLogExport(c, exportContext, filter, self, restricted, model.LogExportMaxRows)
}

func writeLogExport(c *gin.Context, exportContext context.Context, filter model.LogExportFilter, self, restricted bool, maxRows int) {
	includeSummary := c.Query("include_summary") == "true"
	beforeID, emitted := 0, 0
	encoder := json.NewEncoder(c.Writer)
	start := func() {
		c.Header("Content-Type", "application/x-ndjson; charset=utf-8")
		c.Header("Content-Disposition", "attachment; filename=logs.ndjson")
		c.Header("Cache-Control", "no-store")
		c.Header("X-Accel-Buffering", "no")
		c.Header("X-Export-Max-Rows", strconv.Itoa(maxRows))
		c.Header("Trailer", "X-Export-Status")
		if includeSummary {
			c.Header("X-Export-Summary", "1")
		}
	}
	finish := func(status string) {
		if exportContext.Err() != nil {
			status = "failed"
		}
		c.Header("X-Export-Status", status)
		if includeSummary {
			// An opt-in final record leaves every log row unchanged. A missing
			// record, mismatched count or non-complete status means incomplete.
			_ = encoder.Encode(gin.H{"_export": gin.H{
				"version": 1, "status": status, "count": emitted, "max_rows": maxRows,
			}})
		}
		c.Writer.Flush()
	}
	if includeSummary {
		// Once accepted, even a first-batch failure has the negotiated summary.
		start()
	}
	for emitted < maxRows {
		limit := min(model.LogExportBatchSize, maxRows-emitted)
		logs, err := model.GetLogExportBatch(exportContext, filter, beforeID, limit)
		if err == nil && !self && !restricted {
			err = model.PopulateLogExportChannelNames(exportContext, logs)
		}
		if err != nil {
			if emitted == 0 && !includeSummary {
				common.ApiError(c, errors.New("日志导出失败，请稍后重试"))
			} else {
				finish("failed")
			}
			return
		}
		if emitted == 0 && !includeSummary {
			start()
		}
		if len(logs) == 0 {
			finish("complete")
			return
		}
		beforeID = logs[len(logs)-1].Id
		if self {
			service.ApplyErrorMaskToUserLogs(logs)
			model.FormatUserLogExport(logs, emitted)
		} else if c.GetInt("role") == common.RoleRootUser {
			model.FormatRootLogs(logs)
		} else {
			model.FormatAdminLogs(logs)
		}
		if !self && restricted {
			model.StripChannelRestrictedAdminLogFields(logs)
		}
		for _, item := range logs {
			if exportContext.Err() != nil {
				finish("failed")
				return
			}
			if err := encoder.Encode(item); err != nil {
				return
			}
			emitted++
		}
		c.Writer.Flush()
	}
	// Probe with the same owner, filters and cursor. Exactly maxRows is complete;
	// only an additional matching row proves truncation.
	remaining, err := model.GetLogExportBatch(exportContext, filter, beforeID, 1)
	if err != nil {
		finish("failed")
	} else if len(remaining) == 0 {
		finish("complete")
	} else {
		finish("limit_reached")
	}
}

func CreateLogCleanupTask(c *gin.Context) {
	if c.GetInt("role") < common.RoleAdminUser {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	target, _ := strconv.ParseInt(c.Query("target_timestamp"), 10, 64)
	task, err := model.CreateLogCleanupTask(c.GetInt("id"), target)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	go service.RunLogCleanupTask(task.ID)
	common.ApiSuccess(c, task)
}

func GetLogCleanupTask(c *gin.Context) {
	if c.GetInt("role") < common.RoleAdminUser {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}
	task, err := model.GetSystemTask(id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if task.Kind != "log_cleanup" || (c.GetInt("role") != common.RoleRootUser && task.UserID != c.GetInt("id")) {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	common.ApiSuccess(c, task)
}
