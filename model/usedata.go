package model

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// QuotaData 柱状图数据
type QuotaData struct {
	Id        int    `json:"id"`
	UserID    int    `json:"user_id" gorm:"index"`
	Username  string `json:"username" gorm:"index:idx_qdt_model_user_name,priority:2;size:64;default:''"`
	ModelName string `json:"model_name" gorm:"index:idx_qdt_model_user_name,priority:1;size:64;default:''"`
	CreatedAt int64  `json:"created_at" gorm:"bigint;index:idx_qdt_created_at,priority:2"`
	TokenUsed int    `json:"token_used" gorm:"default:0"`
	Count     int    `json:"count" gorm:"default:0"`
	Quota     int    `json:"quota" gorm:"default:0"`
}

func UpdateQuotaData() {
	for {
		if common.DataExportEnabled {
			common.SysLog("正在更新数据看板数据...")
			SaveQuotaDataCache()
		}
		time.Sleep(time.Duration(common.DataExportInterval) * time.Minute)
	}
}

var CacheQuotaData = make(map[string]*QuotaData)
var CacheQuotaDataLock = sync.Mutex{}

func logQuotaDataCache(userId int, username string, modelName string, quota int, createdAt int64, tokenUsed int) {
	key := fmt.Sprintf("%d-%s-%s-%d", userId, username, modelName, createdAt)
	quotaData, ok := CacheQuotaData[key]
	if ok {
		quotaData.Count += 1
		quotaData.Quota += quota
		quotaData.TokenUsed += tokenUsed
	} else {
		quotaData = &QuotaData{
			UserID:    userId,
			Username:  username,
			ModelName: modelName,
			CreatedAt: createdAt,
			Count:     1,
			Quota:     quota,
			TokenUsed: tokenUsed,
		}
	}
	CacheQuotaData[key] = quotaData
}

func LogQuotaData(userId int, username string, modelName string, quota int, createdAt int64, tokenUsed int) {
	// 只精确到小时
	createdAt = createdAt - (createdAt % 3600)

	CacheQuotaDataLock.Lock()
	defer CacheQuotaDataLock.Unlock()
	logQuotaDataCache(userId, username, modelName, quota, createdAt, tokenUsed)
}

// SaveQuotaDataCache preserves the periodic updater's existing entry point.
func SaveQuotaDataCache() {
	if err := FlushQuotaDataCache(context.Background()); err != nil {
		common.SysError("保存数据看板数据失败，未保存项保留在缓存中: " + err.Error())
	}
}

// FlushQuotaDataCache holds the existing cache lock through each DB transaction
// and removes only committed entries. Both lock acquisition and DB work honor ctx.
// An ambiguous database commit cannot be made exactly-once without a durable
// idempotency key; this in-memory cache does not provide crash recovery.
func FlushQuotaDataCache(ctx context.Context) error {
	if err := lockQuotaDataCache(ctx); err != nil {
		return err
	}
	defer CacheQuotaDataLock.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(CacheQuotaData) == 0 {
		return nil
	}
	if DB == nil {
		return errors.New("quota data database is not initialized")
	}

	var flushErrors []error
	saved := 0
	for key, quotaData := range CacheQuotaData {
		if err := ctx.Err(); err != nil {
			flushErrors = append(flushErrors, err)
			break
		}
		if quotaData == nil {
			flushErrors = append(flushErrors, fmt.Errorf("quota data cache %q is nil", key))
			continue
		}
		// Create may assign an ID before a transaction rolls back. Keep those
		// mutations away from the pending cache entry so retry starts cleanly.
		pending := *quotaData
		err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			var existing QuotaData
			query := tx.Where("user_id = ? and username = ? and model_name = ? and created_at = ?",
				pending.UserID, pending.Username, pending.ModelName, pending.CreatedAt).First(&existing)
			if errors.Is(query.Error, gorm.ErrRecordNotFound) {
				pending.Id = 0
				created := tx.Create(&pending)
				if created.Error != nil {
					return created.Error
				}
				if created.RowsAffected != 1 {
					return fmt.Errorf("quota data insert affected %d rows", created.RowsAffected)
				}
				return nil
			}
			if query.Error != nil {
				return query.Error
			}
			updated := tx.Model(&QuotaData{}).Where("id = ?", existing.Id).Updates(map[string]interface{}{
				"count":      gorm.Expr("count + ?", pending.Count),
				"quota":      gorm.Expr("quota + ?", pending.Quota),
				"token_used": gorm.Expr("token_used + ?", pending.TokenUsed),
			})
			if updated.Error != nil {
				return updated.Error
			}
			if updated.RowsAffected != 1 {
				return fmt.Errorf("quota data update affected %d rows", updated.RowsAffected)
			}
			return nil
		})
		if err != nil {
			flushErrors = append(flushErrors, fmt.Errorf("flush quota data %q: %w", key, err))
			continue
		}
		// Delete immediately after a successful commit, even if ctx just expired.
		// A later failed item must not make this delta run a second time.
		delete(CacheQuotaData, key)
		saved++
	}
	if len(flushErrors) == 0 {
		common.SysLog(fmt.Sprintf("保存数据看板数据成功，共保存%d条数据", saved))
	}
	return errors.Join(flushErrors...)
}

func lockQuotaDataCache(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if ctx.Done() == nil {
		CacheQuotaDataLock.Lock()
		return nil
	}
	if CacheQuotaDataLock.TryLock() {
		return nil
	}
	// Do not leave a goroutine waiting on Lock after the shutdown deadline.
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if CacheQuotaDataLock.TryLock() {
				return nil
			}
		}
	}
}

func GetQuotaDataByUsername(username string, startTime int64, endTime int64) (quotaData []*QuotaData, err error) {
	var quotaDatas []*QuotaData
	// 从quota_data表中查询数据
	err = DB.Table("quota_data").Where("username = ? and created_at >= ? and created_at <= ?", username, startTime, endTime).Find(&quotaDatas).Error
	return quotaDatas, err
}

func GetQuotaDataByUserId(userId int, startTime int64, endTime int64) (quotaData []*QuotaData, err error) {
	var quotaDatas []*QuotaData
	// 从quota_data表中查询数据
	err = DB.Table("quota_data").Where("user_id = ? and created_at >= ? and created_at <= ?", userId, startTime, endTime).Find(&quotaDatas).Error
	return quotaDatas, err
}

func GetQuotaDataGroupByUser(startTime int64, endTime int64) (quotaData []*QuotaData, err error) {
	var quotaDatas []*QuotaData
	err = DB.Table("quota_data").
		Select("username, created_at, sum(count) as count, sum(quota) as quota, sum(token_used) as token_used").
		Where("created_at >= ? and created_at <= ?", startTime, endTime).
		Group("username, created_at").
		Find(&quotaDatas).Error
	return quotaDatas, err
}

func GetAllQuotaDates(startTime int64, endTime int64, username string) (quotaData []*QuotaData, err error) {
	if username != "" {
		return GetQuotaDataByUsername(username, startTime, endTime)
	}
	var quotaDatas []*QuotaData
	// 从quota_data表中查询数据
	// only select model_name, sum(count) as count, sum(quota) as quota, model_name, created_at from quota_data group by model_name, created_at;
	//err = DB.Table("quota_data").Where("created_at >= ? and created_at <= ?", startTime, endTime).Find(&quotaDatas).Error
	err = DB.Table("quota_data").Select("model_name, sum(count) as count, sum(quota) as quota, sum(token_used) as token_used, created_at").Where("created_at >= ? and created_at <= ?", startTime, endTime).Group("model_name, created_at").Find(&quotaDatas).Error
	return quotaDatas, err
}
