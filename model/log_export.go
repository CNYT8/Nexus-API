package model

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const UserLogMaxSpanSeconds int64 = 31 * 24 * 60 * 60
const LogExportMaxRows = 100000
const LogExportBatchSize = 200

var ErrLogExportInProgress = errors.New("该用户已有日志导出任务正在进行")
var logExportUsers sync.Map

const logExportLeaseTTL = 60 * time.Second
const renewLogExportLease = `if redis.call('get', KEYS[1]) == ARGV[1] then return redis.call('pexpire', KEYS[1], ARGV[2]) else return 0 end`
const releaseLogExportLease = `if redis.call('get', KEYS[1]) == ARGV[1] then return redis.call('del', KEYS[1]) else return 0 end`

// AcquireLogExportLease shares one user lock across self/all exports. The
// derived context is canceled on lease loss and must be used for every batch.
func AcquireLogExportLease(parent context.Context, userID int) (context.Context, func(), error) {
	if userID <= 0 {
		return nil, nil, errors.New("无效的导出用户")
	}
	if err := parent.Err(); err != nil {
		return nil, nil, err
	}
	if _, loaded := logExportUsers.LoadOrStore(userID, struct{}{}); loaded {
		return nil, nil, ErrLogExportInProgress
	}
	client := common.RDB
	useRedis := common.RedisEnabled
	if useRedis && client == nil {
		logExportUsers.Delete(userID)
		return nil, nil, errors.New("日志导出锁服务未就绪，请稍后重试")
	}
	key, owner := "nexus:log_export:"+strconv.Itoa(userID), common.GetUUID()
	if useRedis {
		ctx, cancel := context.WithTimeout(parent, 5*time.Second)
		acquired, err := client.SetNX(ctx, key, owner, logExportLeaseTTL).Result()
		cancel()
		if err != nil || !acquired {
			logExportUsers.Delete(userID)
			if err != nil {
				return nil, nil, errors.New("无法取得日志导出锁，请稍后重试")
			}
			return nil, nil, ErrLogExportInProgress
		}
	}
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	if useRedis {
		go func() {
			defer close(done)
			ticker := time.NewTicker(logExportLeaseTTL / 3)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					renewCtx, stop := context.WithTimeout(ctx, 5*time.Second)
					updated, err := client.Eval(renewCtx, renewLogExportLease, []string{key}, owner, logExportLeaseTTL.Milliseconds()).Int()
					stop()
					if err != nil || updated != 1 {
						cancel()
						return
					}
				}
			}
		}()
	} else {
		close(done)
	}
	var once sync.Once
	release := func() {
		once.Do(func() {
			cancel()
			<-done
			if useRedis {
				releaseCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
				_ = client.Eval(releaseCtx, releaseLogExportLease, []string{key}, owner).Err()
				stop()
			}
			logExportUsers.Delete(userID)
		})
	}
	return ctx, release, nil
}

// NormalizeUserLogWindow applies the same bounded interval to lists, exports
// and statistics. Missing endpoints select the latest 31-day window.
func NormalizeUserLogWindow(start, end int64) (int64, int64, error) {
	if start < 0 || end < 0 {
		return 0, 0, errors.New("日志查询时间不能为负数")
	}
	if end == 0 {
		end = time.Now().Unix()
	}
	settings := operation_setting.GetLogSetting()
	if start == 0 {
		start = end - int64(settings.DefaultRangeDays())*86400
		if start < 0 {
			start = 0
		}
	}
	if start > end || end-start > settings.MaxRangeSeconds() {
		return 0, 0, fmt.Errorf("日志查询跨度不能超过%d天，且开始时间不能晚于结束时间", settings.MaxRangeDays)
	}
	return start, end, nil
}

type LogExportFilter struct {
	UserID            int
	Type              int
	Start             int64
	End               int64
	ModelName         string
	Username          string
	TokenName         string
	Channel           int
	Group             string
	RequestID         string
	UpstreamRequestID string
}

func applyLogExportTextFilter(tx *gorm.DB, column, value string) (*gorm.DB, error) {
	if value == "" {
		return tx, nil
	}
	if !strings.Contains(value, "%") {
		return tx.Where(clause.Eq{Column: clause.Column{Table: "logs", Name: column}, Value: value}), nil
	}
	pattern, err := sanitizeLikePattern(value)
	condition := " LIKE ? ESCAPE '!'"
	if tx.Dialector.Name() == "clickhouse" {
		pattern, err = sanitizeClickHouseLikePattern(value)
		condition = " LIKE ?"
	}
	if err != nil {
		return nil, err
	}
	return tx.Where(clause.Expr{SQL: "?" + condition, Vars: []interface{}{clause.Column{Table: "logs", Name: column}, pattern}}), nil
}

// GetLogExportBatch returns raw rows; the controller must apply its existing
// role-specific formatting before writing any bytes. Cursor uses stored IDs,
// since user formatting intentionally replaces IDs with sequential numbers.
func GetLogExportBatch(ctx context.Context, filter LogExportFilter, beforeID, limit int) ([]*Log, error) {
	if limit <= 0 || limit > 1000 {
		limit = LogExportBatchSize
	}
	if LOG_DB == nil {
		return nil, errors.New("日志数据库未初始化")
	}
	tx := LOG_DB.WithContext(ctx).Model(&Log{})
	if filter.UserID > 0 {
		tx = tx.Where("logs.user_id = ?", filter.UserID)
	}
	if beforeID > 0 {
		tx = tx.Where("logs.id < ?", beforeID)
	}
	if filter.Type != LogTypeUnknown {
		tx = tx.Where("logs.type = ?", filter.Type)
	}
	var err error
	if tx, err = applyLogExportTextFilter(tx, "model_name", filter.ModelName); err != nil {
		return nil, err
	}
	if filter.UserID == 0 {
		if tx, err = applyLogExportTextFilter(tx, "username", filter.Username); err != nil {
			return nil, err
		}
		if filter.Channel != 0 {
			tx = tx.Where("logs.channel_id = ?", filter.Channel)
		}
	}
	for column, value := range map[string]string{
		"token_name": filter.TokenName, "request_id": filter.RequestID,
		"upstream_request_id": filter.UpstreamRequestID, "group": filter.Group,
	} {
		if value != "" {
			tx = tx.Where(clause.Eq{Column: clause.Column{Table: "logs", Name: column}, Value: value})
		}
	}
	if filter.Start != 0 {
		tx = tx.Where("logs.created_at >= ?", filter.Start)
	}
	if filter.End != 0 {
		tx = tx.Where("logs.created_at <= ?", filter.End)
	}
	var logs []*Log
	err = tx.Order("logs.id DESC").Limit(limit).Find(&logs).Error
	stripNonConsumeLogClients(logs)
	return logs, err
}

// PopulateLogExportChannelNames queries the primary database separately from
// LOG_DB. Never join channels onto logs: the two may use different dialects.
// Call only for viewers permitted to see channel information.
func PopulateLogExportChannelNames(ctx context.Context, logs []*Log) error {
	ids := make([]int, 0)
	seen := make(map[int]bool)
	for _, entry := range logs {
		if entry != nil && entry.ChannelId > 0 && !seen[entry.ChannelId] {
			ids = append(ids, entry.ChannelId)
			seen[entry.ChannelId] = true
		}
	}
	if len(ids) == 0 {
		return nil
	}
	if DB == nil {
		return errors.New("主数据库未初始化")
	}
	var channels []struct {
		Id   int
		Name string
	}
	if err := DB.WithContext(ctx).Table("channels").Select("id", "name").Where("id IN ?", ids).Find(&channels).Error; err != nil {
		return err
	}
	names := make(map[int]string, len(channels))
	for _, channel := range channels {
		names[channel.Id] = channel.Name
	}
	for _, entry := range logs {
		if entry != nil {
			entry.ChannelName = names[entry.ChannelId]
		}
	}
	return nil
}

func FormatUserLogExport(logs []*Log, start int) {
	formatUserLogs(logs, start)
	for _, entry := range logs {
		if entry == nil {
			continue
		}
		// Error bodies and arbitrary diagnostic metadata can embed channel
		// names, upstream URLs/models and credentials. Export fails closed,
		// independently of the optional error-mask configuration.
		if entry.Type == LogTypeError {
			entry.Content = "请求失败，请联系管理员并提供请求 ID"
			entry.ModelName = ""
			entry.Other = "{}"
		}
	}
}
