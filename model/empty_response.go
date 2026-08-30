package model

import (
	"errors"
	"fmt"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const emptyResponseMaxBatchSize = 500

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

func scanEmptyResponseLogs(userId int, since int64, until int64, limit int) ([]EmptyResponseLogSnapshot, error) {
	if userId <= 0 || since <= 0 || until <= since {
		return nil, errors.New("invalid empty response scan window")
	}
	if limit <= 0 || limit > emptyResponseMaxBatchSize {
		limit = emptyResponseMaxBatchSize
	}
	rows := make([]struct {
		Id               int
		CreatedAt        int64
		RequestId        string
		ModelName        string
		PromptTokens     int
		CompletionTokens int
		Quota            int
		Other            string
	}, 0)
	err := LOG_DB.Model(&Log{}).
		Select("id", "created_at", "request_id", "model_name", "prompt_tokens", "completion_tokens", "quota", "other").
		Where("user_id = ? AND type = ? AND created_at >= ? AND created_at < ?", userId, LogTypeConsume, since, until).
		Where("prompt_tokens > 0 AND completion_tokens = 0 AND quota > 0 AND request_id <> ''").
		Order("created_at asc").
		Limit(limit).
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	result := make([]EmptyResponseLogSnapshot, 0, len(rows))
	for _, row := range rows {
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
	}
	return result, nil
}

func ListEmptyResponses(userId int, status string, page *common.PageInfo) ([]EmptyResponseRecord, int64, error) {
	if userId <= 0 {
		return nil, 0, errors.New("invalid user id")
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
	created := 0
	refundTotal := 0

	logs, err := scanEmptyResponseLogs(userId, cycleStart, now, emptyResponseMaxBatchSize)
	if err != nil {
		return 0, 0, err
	}

	err = DB.Transaction(func(tx *gorm.DB) error {
		var user User
		if err := lockForUpdate(tx).Where("id = ?", userId).First(&user).Error; err != nil {
			return err
		}

		if err := tx.Where("user_id = ? AND status = ? AND created_at < ?", userId, "pending", cycleStart).
			Delete(&EmptyResponseRecord{}).Error; err != nil {
			return err
		}

		for _, logSnapshot := range logs {
			record := EmptyResponseRecord{
				UserId:               userId,
				RequestId:            logSnapshot.RequestId,
				LogId:                logSnapshot.Id,
				CycleStart:           cycleStart,
				CycleEnd:             now,
				LogCreatedAt:         logSnapshot.CreatedAt,
				ModelName:            logSnapshot.ModelName,
				PromptTokens:         logSnapshot.PromptTokens,
				Quota:                logSnapshot.Quota,
				RefundPercent:        setting.RefundPercent,
				RefundQuota:          operation_setting.CalcEmptyResponseRefundQuota(logSnapshot.Quota, setting.RefundPercent),
				Status:               "pending",
				BillingSource:        logSnapshot.BillingSource,
				SubscriptionConsumed: logSnapshot.SubscriptionConsumed,
			}
			if record.RefundQuota <= 0 {
				continue
			}
			result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&record)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected == 0 {
				continue
			}
			created++
			refundTotal += record.RefundQuota
		}

		if refundTotal > 0 {
			if err := addUserQuotaTx(tx, userId, refundTotal); err != nil {
				return err
			}
			if err := tx.Model(&EmptyResponseRecord{}).
				Where("user_id = ? AND status = ? AND created_at >= ?", userId, "pending", cycleStart).
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
		RecordLog(userId, LogTypeRefund, fmt.Sprintf("空回赔付成功，记录 %d 条，返还额度 %s", created, logger.LogQuota(refundTotal)))
	}
	return created, refundTotal, nil
}

func GetEmptyResponseStatus(userId int, now int64) (int64, int, int, error) {
	setting := operation_setting.GetEmptyResponseSetting()
	if userId <= 0 {
		return 0, 0, 0, errors.New("invalid user id")
	}
	if now <= 0 {
		now = common.GetTimestamp()
	}
	since := now - int64(setting.PeriodDays)*24*3600
	var pendingCount int64
	var pendingQuota int
	var refundedCount int64
	if err := DB.Model(&EmptyResponseRecord{}).
		Where("user_id = ? AND status = ? AND created_at >= ?", userId, "pending", since).
		Count(&pendingCount).Error; err != nil {
		return 0, 0, 0, err
	}
	if err := DB.Model(&EmptyResponseRecord{}).
		Where("user_id = ? AND status = ? AND created_at >= ?", userId, "pending", since).
		Select("COALESCE(SUM(refund_quota), 0)").Scan(&pendingQuota).Error; err != nil {
		return 0, 0, 0, err
	}
	if err := DB.Model(&EmptyResponseRecord{}).
		Where("user_id = ? AND status = ? AND created_at >= ?", userId, "refunded", since).
		Count(&refundedCount).Error; err != nil {
		return 0, 0, 0, err
	}
	return pendingCount + refundedCount, pendingQuota, int(pendingCount), nil
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
	return DB.Where("user_id = ? AND created_at < ?", userId, cutoff).
		Delete(&EmptyResponseRecord{}).Error
}

func StartEmptyResponseCleanup(userId int) {
	go func() {
		if err := PruneEmptyResponseRecords(userId, time.Now().Unix()); err != nil {
			common.SysLog("failed to prune empty response records: " + err.Error())
		}
	}()
}
