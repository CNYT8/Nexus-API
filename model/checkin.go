package model

import (
	"errors"
	"math/rand"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"gorm.io/gorm"
)

// Checkin 签到记录
type Checkin struct {
	Id           int    `json:"id" gorm:"primaryKey;autoIncrement"`
	UserId       int    `json:"user_id" gorm:"not null;uniqueIndex:idx_user_checkin_date"`
	CheckinDate  string `json:"checkin_date" gorm:"type:varchar(10);not null;uniqueIndex:idx_user_checkin_date"` // 格式: YYYY-MM-DD
	QuotaAwarded int    `json:"quota_awarded" gorm:"not null"`
	CreatedAt    int64  `json:"created_at" gorm:"bigint"`
}

// CheckinRecord 用于API返回的签到记录（不包含敏感字段）
type CheckinRecord struct {
	CheckinDate  string `json:"checkin_date"`
	QuotaAwarded int    `json:"quota_awarded"`
}

type CheckinConditionStatus struct {
	Enabled          bool   `json:"enabled"`
	Eligible         bool   `json:"eligible"`
	Reason           string `json:"reason,omitempty"`
	Message          string `json:"message,omitempty"`
	Date             string `json:"date"`
	RequestThreshold int    `json:"request_threshold"`
	TokenThreshold   int    `json:"token_threshold"`
	RequestCount     int64  `json:"request_count"`
	TokenCount       int64  `json:"token_count"`
	StageMode        bool   `json:"stage_mode"`
	MatchedStage     int    `json:"-"`
	StageMinQuota    int    `json:"-"`
	StageMaxQuota    int    `json:"-"`
}

func (Checkin) TableName() string {
	return "checkins"
}

// GetUserCheckinRecords 获取用户在指定日期范围内的签到记录
func GetUserCheckinRecords(userId int, startDate, endDate string) ([]Checkin, error) {
	var records []Checkin
	err := DB.Where("user_id = ? AND checkin_date >= ? AND checkin_date <= ?",
		userId, startDate, endDate).
		Order("checkin_date DESC").
		Find(&records).Error
	return records, err
}

// HasCheckedInToday 检查用户今天是否已签到
func HasCheckedInToday(userId int) (bool, error) {
	today := time.Now().Format("2006-01-02")
	var count int64
	err := DB.Model(&Checkin{}).
		Where("user_id = ? AND checkin_date = ?", userId, today).
		Count(&count).Error
	return count > 0, err
}

func GetCheckinConditionStatus(userId int, setting *operation_setting.CheckinSetting) (*CheckinConditionStatus, error) {
	status, _, _, err := getCheckinConditionStatusWithQuota(userId, setting)
	return status, err
}

func getCheckinConditionStatusWithQuota(userId int, setting *operation_setting.CheckinSetting) (*CheckinConditionStatus, int, int, error) {
	yesterday := time.Now().AddDate(0, 0, -1)
	minQuota, maxQuota := normalizeCheckinQuotaRange(setting.MinQuota, setting.MaxQuota)
	status := &CheckinConditionStatus{
		Enabled:          setting.ConditionEnabled,
		Eligible:         true,
		Date:             yesterday.Format("2006-01-02"),
		RequestThreshold: setting.RequestThreshold,
		TokenThreshold:   setting.TokenThreshold,
		MatchedStage:     -1,
		StageMinQuota:    minQuota,
		StageMaxQuota:    maxQuota,
	}

	if !setting.ConditionEnabled {
		return status, minQuota, maxQuota, nil
	}

	startAt := time.Date(yesterday.Year(), yesterday.Month(), yesterday.Day(), 0, 0, 0, 0, time.Local)
	endAt := startAt.AddDate(0, 0, 1)
	var usage struct {
		RequestCount int64
		TokenCount   int64
	}
	if err := LOG_DB.Model(&Log{}).
		Select("COUNT(*) AS request_count, COALESCE(SUM(prompt_tokens + completion_tokens), 0) AS token_count").
		Where("user_id = ? AND type = ? AND created_at >= ? AND created_at < ?", userId, LogTypeConsume, startAt.Unix(), endAt.Unix()).
		Scan(&usage).Error; err != nil {
		return nil, 0, 0, err
	}

	status.RequestCount = usage.RequestCount
	status.TokenCount = usage.TokenCount

	if len(setting.StageRules) > 0 {
		status.StageMode = true
		for index, rule := range setting.StageRules {
			if !checkinStageRuleMatches(rule, usage.RequestCount, usage.TokenCount) {
				continue
			}
			status.MatchedStage = index
			status.RequestThreshold = rule.RequestThreshold
			status.TokenThreshold = rule.TokenThreshold
			stageMinQuota, stageMaxQuota := normalizeCheckinQuotaRange(rule.MinQuota, rule.MaxQuota)
			status.StageMinQuota = stageMinQuota
			status.StageMaxQuota = stageMaxQuota
			if !rule.AllowCheckin {
				status.Eligible = false
				status.Reason = "stage_disabled"
				status.Message = "当前阶段无法签到"
				return status, 0, 0, nil
			}
			return status, stageMinQuota, stageMaxQuota, nil
		}
		status.Eligible = false
		status.Reason = "stage_no_match"
		status.Message = "前一天用量未达到阶段签到要求"
		return status, 0, 0, nil
	}

	if setting.RequestThreshold > 0 && usage.RequestCount <= int64(setting.RequestThreshold) {
		status.Eligible = false
		status.Reason = "request_count"
		status.Message = "前一天调用量未达到签到要求"
		return status, 0, 0, nil
	}
	if setting.TokenThreshold > 0 && usage.TokenCount <= int64(setting.TokenThreshold) {
		status.Eligible = false
		status.Reason = "token_count"
		status.Message = "前一天用量未达到签到要求"
		return status, 0, 0, nil
	}

	return status, minQuota, maxQuota, nil
}

func checkinStageRuleMatches(rule operation_setting.CheckinStageRule, requestCount int64, tokenCount int64) bool {
	if rule.RequestThreshold <= 0 && rule.TokenThreshold <= 0 {
		return true
	}
	return (rule.RequestThreshold > 0 && requestCount > int64(rule.RequestThreshold)) ||
		(rule.TokenThreshold > 0 && tokenCount > int64(rule.TokenThreshold))
}

func normalizeCheckinQuotaRange(minQuota int, maxQuota int) (int, int) {
	if minQuota < 0 {
		minQuota = 0
	}
	if maxQuota < minQuota {
		maxQuota = minQuota
	}
	return minQuota, maxQuota
}

func randomCheckinQuota(minQuota int, maxQuota int) int {
	if maxQuota > minQuota {
		return minQuota + rand.Intn(maxQuota-minQuota+1)
	}
	return minQuota
}

// UserCheckin 执行用户签到
// MySQL 和 PostgreSQL 使用事务保证原子性
// SQLite 不支持嵌套事务，使用顺序操作 + 手动回滚
func UserCheckin(userId int) (*Checkin, error) {
	setting := operation_setting.GetCheckinSetting()
	if !setting.Enabled {
		return nil, errors.New("签到功能未启用")
	}

	// 检查今天是否已签到
	hasChecked, err := HasCheckedInToday(userId)
	if err != nil {
		return nil, err
	}
	if hasChecked {
		return nil, errors.New("今日已签到")
	}

	condition, minQuota, maxQuota, err := getCheckinConditionStatusWithQuota(userId, setting)
	if err != nil {
		return nil, err
	}
	if !condition.Eligible {
		return nil, errors.New(condition.Message)
	}

	quotaAwarded := randomCheckinQuota(minQuota, maxQuota)

	today := time.Now().Format("2006-01-02")
	checkin := &Checkin{
		UserId:       userId,
		CheckinDate:  today,
		QuotaAwarded: quotaAwarded,
		CreatedAt:    time.Now().Unix(),
	}

	// 根据数据库类型选择不同的策略
	if common.UsingSQLite {
		// SQLite 不支持嵌套事务，使用顺序操作 + 手动回滚
		return userCheckinWithoutTransaction(checkin, userId, quotaAwarded)
	}

	// MySQL 和 PostgreSQL 支持事务，使用事务保证原子性
	return userCheckinWithTransaction(checkin, userId, quotaAwarded)
}

// userCheckinWithTransaction 使用事务执行签到（适用于 MySQL 和 PostgreSQL）
func userCheckinWithTransaction(checkin *Checkin, userId int, quotaAwarded int) (*Checkin, error) {
	err := DB.Transaction(func(tx *gorm.DB) error {
		// 步骤1: 创建签到记录
		// 数据库有唯一约束 (user_id, checkin_date)，可以防止并发重复签到
		if err := tx.Create(checkin).Error; err != nil {
			return errors.New("签到失败，请稍后重试")
		}

		// 步骤2: 在事务中增加用户额度
		if err := tx.Model(&User{}).Where("id = ?", userId).
			Update("quota", gorm.Expr("quota + ?", quotaAwarded)).Error; err != nil {
			return errors.New("签到失败：更新额度出错")
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	// 事务成功后，异步更新缓存
	go func() {
		_ = cacheIncrUserQuota(userId, int64(quotaAwarded))
	}()

	return checkin, nil
}

// userCheckinWithoutTransaction 不使用事务执行签到（适用于 SQLite）
func userCheckinWithoutTransaction(checkin *Checkin, userId int, quotaAwarded int) (*Checkin, error) {
	// 步骤1: 创建签到记录
	// 数据库有唯一约束 (user_id, checkin_date)，可以防止并发重复签到
	if err := DB.Create(checkin).Error; err != nil {
		return nil, errors.New("签到失败，请稍后重试")
	}

	// 步骤2: 增加用户额度
	// 使用 db=true 强制直接写入数据库，不使用批量更新
	if err := IncreaseUserQuota(userId, quotaAwarded, true); err != nil {
		// 如果增加额度失败，需要回滚签到记录
		DB.Delete(checkin)
		return nil, errors.New("签到失败：更新额度出错")
	}

	return checkin, nil
}

// GetUserCheckinStats 获取用户签到统计信息
func GetUserCheckinStats(userId int, month string) (map[string]interface{}, error) {
	// 获取指定月份的所有签到记录
	startDate := month + "-01"
	endDate := month + "-31"

	records, err := GetUserCheckinRecords(userId, startDate, endDate)
	if err != nil {
		return nil, err
	}

	// 转换为不包含敏感字段的记录
	checkinRecords := make([]CheckinRecord, len(records))
	for i, r := range records {
		checkinRecords[i] = CheckinRecord{
			CheckinDate:  r.CheckinDate,
			QuotaAwarded: r.QuotaAwarded,
		}
	}

	// 检查今天是否已签到
	hasCheckedToday, _ := HasCheckedInToday(userId)
	condition, err := GetCheckinConditionStatus(userId, operation_setting.GetCheckinSetting())
	if err != nil {
		return nil, err
	}

	// 获取用户所有时间的签到统计
	var totalCheckins int64
	var totalQuota int64
	DB.Model(&Checkin{}).Where("user_id = ?", userId).Count(&totalCheckins)
	DB.Model(&Checkin{}).Where("user_id = ?", userId).Select("COALESCE(SUM(quota_awarded), 0)").Scan(&totalQuota)

	return map[string]interface{}{
		"total_quota":      totalQuota,      // 所有时间累计获得的额度
		"total_checkins":   totalCheckins,   // 所有时间累计签到次数
		"checkin_count":    len(records),    // 本月签到次数
		"checked_in_today": hasCheckedToday, // 今天是否已签到
		"condition":        condition,       // 阶段签到状态
		"records":          checkinRecords,  // 本月签到记录详情（不含id和user_id）
	}, nil
}
