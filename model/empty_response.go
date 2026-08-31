package model

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const emptyResponseMaxBatchSize = 500

var emptyResponseSyncMu sync.Mutex

var ErrEmptyResponseCycleExpired = errors.New("empty response cycle expired")
var ErrEmptyResponseFeatureDisabled = errors.New("empty response compensation disabled")

type EmptyResponseRecord struct {
	Id                   int    `json:"id" gorm:"primaryKey"`
	UserId               int    `json:"user_id" gorm:"not null;index:idx_empty_response_user_cycle,priority:1;index:idx_empty_response_user_created"`
	RequestId            string `json:"request_id" gorm:"type:varchar(64);not null;uniqueIndex"`
	LogId                int    `json:"log_id" gorm:"not null;index"`
	CycleStart           int64  `json:"cycle_start" gorm:"not null;index:idx_empty_response_user_cycle,priority:2"`
	CycleEnd             int64  `json:"cycle_end" gorm:"not null"`
	CreatedAt            int64  `json:"created_at" gorm:"autoCreateTime;index:idx_empty_response_user_created"`
	LogCreatedAt         int64  `json:"log_created_at" gorm:"not null"`
	ModelName            string `json:"model_name" gorm:"type:varchar(255);not null"`
	PromptTokens         int    `json:"prompt_tokens" gorm:"not null"`
	Quota                int    `json:"quota" gorm:"not null"`
	RefundPercent        int    `json:"refund_percent" gorm:"not null"`
	RefundQuota          int    `json:"refund_quota" gorm:"not null"`
	Status               string `json:"status" gorm:"type:varchar(32);not null;default:'pending';index"`
	RefundedAt           int64  `json:"refunded_at" gorm:"default:0"`
	RefundLogId          int    `json:"refund_log_id" gorm:"default:0"`
	UserNotified         bool   `json:"user_notified" gorm:"default:false"`
	BillingSource        string `json:"billing_source" gorm:"type:varchar(32);default:''"`
	SubscriptionConsumed int64  `json:"subscription_consumed" gorm:"default:0"`
}

type EmptyResponseLogSnapshot struct {
	Id                   int
	CreatedAt            int64
	RequestId            string
	ModelName            string
	PromptTokens         int
	CompletionTokens     int
	Quota                int
	Other                string
	BillingSource        string
	SubscriptionConsumed int64
}

func EmptyResponseCycleWindow(now int64, periodDays int) (int64, int64) {
	setting := operation_setting.NormalizeEmptyResponseSetting(operation_setting.EmptyResponseSetting{PeriodDays: periodDays})
	if now <= 0 {
		now = common.GetTimestamp()
	}
	return now - int64(setting.PeriodDays)*24*3600, now
}

func positiveOtherValue(otherMap map[string]interface{}, keys ...string) bool {
	for _, key := range keys {
		value, ok := otherMap[key]
		if !ok {
			continue
		}
		switch number := value.(type) {
		case float64:
			if number > 0 {
				return true
			}
		case int:
			if number > 0 {
				return true
			}
		case int64:
			if number > 0 {
				return true
			}
		}
	}
	return false
}

func isExcludedEmptyResponseLog(other string) bool {
	otherMap, err := common.StrToMap(other)
	if err != nil || otherMap == nil {
		return false
	}
	for _, key := range []string{
		"image",
		"audio",
		"ws",
		"image_generation_call",
		"audio_input_seperate_price",
		"web_search",
		"file_search",
		"is_task",
		"violation_fee_marker",
		"tool_calls",
		"function_call",
		"tool_use",
	} {
		if value, ok := otherMap[key].(bool); ok && value {
			return true
		}
	}
	if rejectReason, _ := otherMap["reject_reason"].(string); strings.TrimSpace(rejectReason) != "" {
		return true
	}
	requestPath, _ := otherMap["request_path"].(string)
	requestPath = strings.ToLower(strings.SplitN(requestPath, "?", 2)[0])
	for _, marker := range []string{
		"/embeddings",
		":embedcontent",
		":batchembedcontents",
		"/rerank",
		"/audio/",
		"/images/",
		"/moderations",
	} {
		if strings.Contains(requestPath, marker) {
			return true
		}
	}
	if streamStatus, ok := otherMap["stream_status"].(map[string]interface{}); ok {
		if status, _ := streamStatus["status"].(string); status == "error" {
			return true
		}
		if positiveOtherValue(streamStatus, "error_count") {
			return true
		}
	}
	return false
}

func hasEmptyResponseInput(promptTokens int, other string) bool {
	if promptTokens > 0 {
		return true
	}
	otherMap, err := common.StrToMap(other)
	if err != nil || otherMap == nil {
		return false
	}
	return positiveOtherValue(otherMap,
		"input_tokens_total",
		"input_tokens",
		"cache_tokens",
		"cache_creation_tokens",
		"cache_write_tokens",
		"audio_input",
		"text_input",
	)
}

func scanEmptyResponseLogs(userId int, since int64, until int64, limit int, excludedLogIds map[int]struct{}) ([]EmptyResponseLogSnapshot, error) {
	if userId <= 0 || since <= 0 || until < since {
		return nil, errors.New("invalid empty response scan window")
	}
	if limit <= 0 || limit > emptyResponseMaxBatchSize {
		limit = emptyResponseMaxBatchSize
	}
	type emptyResponseLogRow struct {
		Id               int
		CreatedAt        int64
		RequestId        string
		ModelName        string
		PromptTokens     int
		CompletionTokens int
		Quota            int
		Other            string
	}

	result := make([]EmptyResponseLogSnapshot, 0, limit)
	for offset := 0; len(result) < limit; offset += emptyResponseMaxBatchSize {
		rows := make([]emptyResponseLogRow, 0, emptyResponseMaxBatchSize)
		err := LOG_DB.Model(&Log{}).
			Select("id", "created_at", "request_id", "model_name", "prompt_tokens", "completion_tokens", "quota", "other").
			Where("user_id = ? AND type = ? AND created_at >= ? AND created_at <= ?", userId, LogTypeConsume, since, until).
			Where("completion_tokens <= 0 AND quota > 0").
			Order("created_at asc, id asc").
			Offset(offset).
			Limit(emptyResponseMaxBatchSize).
			Scan(&rows).Error
		if err != nil {
			return nil, err
		}
		if len(rows) == 0 {
			break
		}

		for _, row := range rows {
			if _, excluded := excludedLogIds[row.Id]; excluded {
				continue
			}
			if isExcludedEmptyResponseLog(row.Other) || !hasEmptyResponseInput(row.PromptTokens, row.Other) {
				continue
			}
			otherMap, _ := common.StrToMap(row.Other)
			billingSource, _ := otherMap["billing_source"].(string)
			var subscriptionConsumed int64
			if value, ok := otherMap["subscription_consumed"].(float64); ok {
				subscriptionConsumed = int64(value)
			}
			result = append(result, EmptyResponseLogSnapshot{
				Id:                   row.Id,
				CreatedAt:            row.CreatedAt,
				RequestId:            row.RequestId,
				ModelName:            row.ModelName,
				PromptTokens:         row.PromptTokens,
				CompletionTokens:     row.CompletionTokens,
				Quota:                row.Quota,
				Other:                row.Other,
				BillingSource:        billingSource,
				SubscriptionConsumed: subscriptionConsumed,
			})
			if len(result) >= limit {
				break
			}
		}
		if len(rows) < emptyResponseMaxBatchSize {
			break
		}
	}
	return result, nil
}

func emptyResponseRequestID(userId int, logSnapshot EmptyResponseLogSnapshot) string {
	// Use the local user/log identity instead of the upstream request ID. Upstream
	// request IDs are not guaranteed to be unique across users or providers.
	return fmt.Sprintf("empty-response-log-%d-%d", userId, logSnapshot.Id)
}

// syncEmptyResponseRecords discovers eligible consume logs without issuing a refund.
// Keeping discovery separate from claiming makes the page useful before the user clicks claim.
func syncEmptyResponseRecords(userId int, now int64) error {
	emptyResponseSyncMu.Lock()
	defer emptyResponseSyncMu.Unlock()

	setting := operation_setting.GetEmptyResponseSetting()
	if !setting.Enabled {
		return nil
	}
	if userId <= 0 {
		return errors.New("invalid user id")
	}
	if now <= 0 {
		now = common.GetTimestamp()
	}
	cycleStart := now - int64(setting.PeriodDays)*24*3600
	var existingLogIds []int
	if err := DB.Model(&EmptyResponseRecord{}).
		Where("user_id = ?", userId).
		Pluck("log_id", &existingLogIds).Error; err != nil {
		return err
	}
	excludedLogIds := make(map[int]struct{}, len(existingLogIds))
	for _, logId := range existingLogIds {
		excludedLogIds[logId] = struct{}{}
	}
	logs, err := scanEmptyResponseLogs(userId, cycleStart, now, emptyResponseMaxBatchSize, excludedLogIds)
	if err != nil {
		return err
	}

	return DB.Transaction(func(tx *gorm.DB) error {
		var user User
		if err := lockForUpdate(tx).Where("id = ?", userId).First(&user).Error; err != nil {
			return err
		}
		if err := tx.Where("user_id = ? AND status = ? AND log_created_at < ?", userId, "pending", cycleStart).
			Delete(&EmptyResponseRecord{}).Error; err != nil {
			return err
		}

		for _, logSnapshot := range logs {
			var existing EmptyResponseRecord
			result := tx.Where("user_id = ? AND log_id = ?", userId, logSnapshot.Id).Limit(1).Find(&existing)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected > 0 {
				continue
			}

			refundQuota := operation_setting.CalcEmptyResponseRefundQuota(logSnapshot.Quota, setting.RefundPercent)
			if refundQuota <= 0 {
				continue
			}
			record := EmptyResponseRecord{
				UserId:               userId,
				RequestId:            emptyResponseRequestID(userId, logSnapshot),
				LogId:                logSnapshot.Id,
				CycleStart:           cycleStart,
				CycleEnd:             now,
				LogCreatedAt:         logSnapshot.CreatedAt,
				ModelName:            logSnapshot.ModelName,
				PromptTokens:         logSnapshot.PromptTokens,
				Quota:                logSnapshot.Quota,
				RefundPercent:        setting.RefundPercent,
				RefundQuota:          refundQuota,
				Status:               "pending",
				BillingSource:        logSnapshot.BillingSource,
				SubscriptionConsumed: logSnapshot.SubscriptionConsumed,
			}
			result = tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&record)
			if result.Error != nil {
				return result.Error
			}
		}
		return nil
	})
}

func ListEmptyResponses(userId int, status string, page *common.PageInfo) ([]EmptyResponseRecord, int64, error) {
	if userId <= 0 {
		return nil, 0, errors.New("invalid user id")
	}
	if err := syncEmptyResponseRecords(userId, common.GetTimestamp()); err != nil {
		return nil, 0, err
	}
	query := DB.Model(&EmptyResponseRecord{}).Where("user_id = ?", userId)
	if status != "" {
		query = query.Where("status = ?", status)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var records []EmptyResponseRecord
	query = query.Order("created_at desc")
	if page != nil {
		query = query.Limit(page.GetPageSize()).Offset(page.GetStartIdx())
	}
	if err := query.Find(&records).Error; err != nil {
		return nil, 0, err
	}
	return records, total, nil
}

func ClaimEmptyResponseCompensation(userId int, now int64) (int, int, error) {
	setting := operation_setting.GetEmptyResponseSetting()
	if !setting.Enabled {
		return 0, 0, ErrEmptyResponseFeatureDisabled
	}
	if userId <= 0 {
		return 0, 0, errors.New("invalid user id")
	}
	if now <= 0 {
		now = common.GetTimestamp()
	}
	cycleStart := now - int64(setting.PeriodDays)*24*3600
	if err := syncEmptyResponseRecords(userId, now); err != nil {
		return 0, 0, err
	}

	refundedCount := 0
	refundTotal := 0

	err := DB.Transaction(func(tx *gorm.DB) error {
		var user User
		if err := lockForUpdate(tx).Where("id = ?", userId).First(&user).Error; err != nil {
			return err
		}

		if err := tx.Where("user_id = ? AND status = ? AND log_created_at < ?", userId, "pending", cycleStart).
			Delete(&EmptyResponseRecord{}).Error; err != nil {
			return err
		}

		pendingQuery := tx.Model(&EmptyResponseRecord{}).
			Where("user_id = ? AND status = ? AND log_created_at >= ?", userId, "pending", cycleStart)
		if err := pendingQuery.Select("COALESCE(SUM(refund_quota), 0)").Scan(&refundTotal).Error; err != nil {
			return err
		}
		var pendingCount int64
		if err := pendingQuery.Count(&pendingCount).Error; err != nil {
			return err
		}
		refundedCount = int(pendingCount)

		if refundTotal > 0 {
			if err := addUserQuotaTx(tx, userId, refundTotal); err != nil {
				return err
			}
			if err := tx.Model(&EmptyResponseRecord{}).
				Where("user_id = ? AND status = ? AND log_created_at >= ?", userId, "pending", cycleStart).
				Updates(map[string]interface{}{"status": "refunded", "refunded_at": now}).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return 0, 0, err
	}

	if refundTotal > 0 {
		RecordLog(userId, LogTypeRefund, fmt.Sprintf("空回赔付成功，记录 %d 条，返还额度 %s", refundedCount, logger.LogQuota(refundTotal)))
	}
	return refundedCount, refundTotal, nil
}

func GetEmptyResponseStatus(userId int, now int64) (int64, int, int, error) {
	setting := operation_setting.GetEmptyResponseSetting()
	if userId <= 0 {
		return 0, 0, 0, errors.New("invalid user id")
	}
	if now <= 0 {
		now = common.GetTimestamp()
	}
	if err := syncEmptyResponseRecords(userId, now); err != nil {
		return 0, 0, 0, err
	}
	since := now - int64(setting.PeriodDays)*24*3600
	var pendingCount int64
	var pendingQuota int
	var refundedCount int64
	if err := DB.Model(&EmptyResponseRecord{}).
		Where("user_id = ? AND status = ? AND log_created_at >= ?", userId, "pending", since).
		Count(&pendingCount).Error; err != nil {
		return 0, 0, 0, err
	}
	if err := DB.Model(&EmptyResponseRecord{}).
		Where("user_id = ? AND status = ? AND log_created_at >= ?", userId, "pending", since).
		Select("COALESCE(SUM(refund_quota), 0)").Scan(&pendingQuota).Error; err != nil {
		return 0, 0, 0, err
	}
	if err := DB.Model(&EmptyResponseRecord{}).
		Where("user_id = ? AND status = ? AND log_created_at >= ?", userId, "refunded", since).
		Count(&refundedCount).Error; err != nil {
		return 0, 0, 0, err
	}
	return pendingCount, pendingQuota, int(refundedCount), nil
}

func PruneEmptyResponseRecords(userId int, now int64) error {
	if userId <= 0 {
		return errors.New("invalid user id")
	}
	setting := operation_setting.GetEmptyResponseSetting()
	cutoff := now - int64(setting.PeriodDays)*24*3600
	if now <= 0 {
		cutoff = common.GetTimestamp() - int64(setting.PeriodDays)*24*3600
	}
	return DB.Where("user_id = ? AND status = ? AND log_created_at < ?", userId, "pending", cutoff).
		Delete(&EmptyResponseRecord{}).Error
}

func StartEmptyResponseCleanup(userId int) {
	go func() {
		if err := PruneEmptyResponseRecords(userId, time.Now().Unix()); err != nil {
			common.SysLog("failed to prune empty response records: " + err.Error())
		}
	}()
}
