package model

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	ch "github.com/ClickHouse/clickhouse-go/v2"
	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	chgorm "gorm.io/driver/clickhouse"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func IsClickHouseLogDB() bool {
	return LOG_DB != nil && LOG_DB.Dialector.Name() == common.DatabaseTypeClickHouse
}

func isClickHouseDSN(dsn string) bool {
	return strings.HasPrefix(strings.ToLower(dsn), "clickhouse://")
}

// Activation, retention and the historical ID floor are deployment settings,
// never mutable application options. A floor of 0 is valid for a new database;
// a cutover must supply at least the highest ID in ALL retained old log stores.
func clickHouseLogConfig() (ttlDays int, idFloor int64, err error) {
	if os.Getenv("LOG_SQL_CLICKHOUSE_ENABLED") != "true" {
		return 0, 0, errors.New("ClickHouse logs require LOG_SQL_CLICKHOUSE_ENABLED=true")
	}
	ttl := os.Getenv("LOG_SQL_CLICKHOUSE_TTL_DAYS")
	if ttl == "" {
		ttl = "0"
	}
	ttlDays, err = strconv.Atoi(ttl)
	if err != nil || ttlDays < 0 || ttlDays > 36500 {
		return 0, 0, errors.New("LOG_SQL_CLICKHOUSE_TTL_DAYS must be 0 or 31..36500")
	}
	// Preserve the maximum 30-day empty-response scan and complete prior day.
	if ttlDays > 0 && ttlDays < 31 {
		return 0, 0, errors.New("ClickHouse log TTL must retain at least 31 days")
	}
	idFloor, err = strconv.ParseInt(os.Getenv("LOG_SQL_CLICKHOUSE_ID_FLOOR"), 10, 64)
	if err != nil || idFloor < 0 || idFloor >= maxLogID {
		return 0, 0, errors.New("LOG_SQL_CLICKHOUSE_ID_FLOOR must explicitly specify 0 for a fresh store or the historical maximum log ID")
	}
	if strconv.IntSize != 64 {
		return 0, 0, errors.New("ClickHouse logs require a 64-bit process to preserve integer cursors")
	}
	return ttlDays, idFloor, nil
}

func openClickHouseLogDB(dsn string) (*gorm.DB, error) {
	if _, _, err := clickHouseLogConfig(); err != nil {
		return nil, err
	}
	options, err := ch.ParseDSN(dsn)
	if err != nil {
		// Driver parse errors can contain credentials from the DSN.
		return nil, errors.New("invalid ClickHouse log DSN")
	}
	if len(options.Addr) != 1 || options.Protocol != ch.Native {
		return nil, errors.New("ClickHouse logs require one native endpoint; load-balanced or distributed stores are unsupported")
	}
	options.DialTimeout = 5 * time.Second
	options.ReadTimeout = 10 * time.Second
	options.MaxOpenConns = common.GetEnvOrDefault("SQL_MAX_OPEN_CONNS", 1000)
	options.MaxIdleConns = common.GetEnvOrDefault("SQL_MAX_IDLE_CONNS", 100)
	options.ConnMaxLifetime = time.Second * time.Duration(common.GetEnvOrDefault("SQL_MAX_LIFETIME", 60))
	conn := ch.OpenDB(options)
	config := newGormConfig(false)
	config.DisableAutomaticPing = true
	// Keep GORM's default create transaction: this driver's Commit sends the
	// prepared insert batch. It is not a relational transaction for mutations.
	db, err := gorm.Open(chgorm.New(chgorm.Config{Conn: conn, SkipInitializeWithVersion: true}), config)
	if err != nil {
		_ = conn.Close()
	}
	return db, err
}

func openLogMetaDB() (*gorm.DB, error) {
	dsn := os.Getenv("LOG_META_SQL_DSN")
	if dsn == "" {
		return DB, nil
	}
	if isClickHouseDSN(dsn) || strings.HasPrefix(dsn, "http://") || strings.HasPrefix(dsn, "https://") {
		return nil, errors.New("LOG_META_SQL_DSN must be SQLite, MySQL or PostgreSQL")
	}
	// Do not use chooseDB: it changes global primary/log dialect flags.
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		return gorm.Open(postgres.New(postgres.Config{DSN: dsn, PreferSimpleProtocol: true}), newGormConfig(false))
	}
	if strings.HasPrefix(dsn, "local") {
		return gorm.Open(sqlite.Open(common.SQLitePath), newGormConfig(true))
	}
	if !strings.Contains(dsn, "parseTime") {
		separator := "?"
		if strings.Contains(dsn, "?") {
			separator = "&"
		}
		dsn += separator + "parseTime=true"
	}
	return gorm.Open(mysql.Open(dsn), newGormConfig(true))
}

// Only logs belong in ClickHouse. No AutoMigrate call is made on that handle.
const clickHouseLogsDDL = "CREATE TABLE IF NOT EXISTS logs (" +
	"id Int64, user_id Int64, created_at Int64, type Int64, content String, " +
	"username String, token_name String, model_name String, quota Int64, " +
	"prompt_tokens Int64, completion_tokens Int64, use_time Int64, is_stream Bool, " +
	"channel_id Int64, token_id Int64, `group` String, ip String, client String, " +
	"request_id String, upstream_request_id String, other String, " +
	"CONSTRAINT positive_log_id CHECK id > 0) " +
	"ENGINE = MergeTree() PARTITION BY toYYYYMM(toDateTime(created_at)) ORDER BY (created_at, id)"

func initClickHouseLogs() error {
	ttlDays, floor, err := clickHouseLogConfig()
	if err != nil {
		return err
	}
	LogMetaDB, err = openLogMetaDB()
	if err != nil {
		return err
	}
	if LogMetaDB == nil || LogMetaDB.Dialector.Name() == common.DatabaseTypeClickHouse {
		return errors.New("ClickHouse logs require a relational LogMetaDB")
	}
	if LogMetaDB.Dialector.Name() == common.DatabaseTypeMySQL {
		if err := checkMySQLChineseSupport(LogMetaDB); err != nil {
			return err
		}
	}
	if LogMetaDB != DB {
		pool, err := LogMetaDB.DB()
		if err != nil {
			return err
		}
		pool.SetMaxIdleConns(common.GetEnvOrDefault("SQL_MAX_IDLE_CONNS", 100))
		pool.SetMaxOpenConns(common.GetEnvOrDefault("SQL_MAX_OPEN_CONNS", 1000))
		pool.SetConnMaxLifetime(time.Minute)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if common.IsMasterNode {
		if err := migrateAdditive(LogMetaDB.WithContext(ctx), &ModelMonitorSample{}, &LogCleanupLease{}, &LogCleanupCheckpoint{}, &LogIDSequence{}); err != nil {
			return err
		}
		if err := LOG_DB.WithContext(ctx).Exec(clickHouseLogsDDL).Error; err != nil {
			return err
		}
	}
	binding, createSQL, err := validateClickHouseLogs(ctx)
	if err != nil {
		return err
	}
	if err := initializeLogIDSequence(ctx, binding, floor, common.IsMasterNode); err != nil {
		return err
	}
	if common.IsMasterNode {
		if err := syncClickHouseLogTTL(ctx, ttlDays, createSQL); err != nil {
			return err
		}
	} else if ttlDays == 0 && clickHouseLogHasTTL(createSQL) {
		return errors.New("ClickHouse log TTL is still active; the master must remove it before starting with TTL=0")
	}
	common.SysLog(fmt.Sprintf("ClickHouse logs enabled; relational metadata=%s; TTL=%d days (0 disables); historical ID floor=%d; historical migration and rollback require explicit backfill", LogMetaDB.Dialector.Name(), ttlDays, floor))
	return nil
}

func validateClickHouseLogs(ctx context.Context) (binding, createSQL string, err error) {
	var table struct {
		Database string
		Engine   string
		UUID     string `gorm:"column:uuid"`
		DDL      string `gorm:"column:ddl"`
	}
	result := LOG_DB.WithContext(ctx).Raw("SELECT database, engine, toString(uuid) AS uuid, create_table_query AS ddl FROM system.tables WHERE database = currentDatabase() AND name = 'logs'").Scan(&table)
	if result.Error != nil {
		return "", "", result.Error
	}
	if table.Engine != "MergeTree" || table.UUID == "" || table.UUID == "00000000-0000-0000-0000-000000000000" {
		return "", "", errors.New("ClickHouse logs require a plain MergeTree table in an Atomic database with a stable UUID")
	}
	columns, _, err := loadDatabaseTableColumnsReadOnly(LOG_DB.WithContext(ctx), common.DatabaseTypeClickHouse)
	if err != nil {
		return "", "", err
	}
	for _, column := range []string{"id", "user_id", "created_at", "type", "quota", "prompt_tokens", "completion_tokens", "use_time", "channel_id", "token_id"} {
		if columns["logs"][column] != "Int64" {
			return "", "", fmt.Errorf("ClickHouse logs.%s must be Int64; migrate existing data explicitly", column)
		}
	}
	for _, column := range []string{"content", "username", "token_name", "model_name", "group", "ip", "client", "request_id", "upstream_request_id", "other"} {
		if columns["logs"][column] != "String" {
			return "", "", fmt.Errorf("ClickHouse logs.%s must be String", column)
		}
	}
	if kind := columns["logs"]["is_stream"]; kind != "Bool" && kind != "UInt8" {
		return "", "", errors.New("ClickHouse logs.is_stream must be Bool or UInt8")
	}
	return table.Database + "/" + table.UUID, table.DDL, nil
}

func clickHouseLogHasTTL(createSQL string) bool {
	for _, token := range strings.Fields(createSQL) {
		if strings.EqualFold(token, "TTL") {
			return true
		}
	}
	return false
}

func clickHouseLogTTLStatement(days int, createSQL string) (string, error) {
	if days == 0 {
		if clickHouseLogHasTTL(createSQL) {
			return "ALTER TABLE logs REMOVE TTL", nil
		}
		return "", nil
	}
	if days < 31 || days > 36500 {
		return "", errors.New("invalid ClickHouse log TTL")
	}
	// Only a validated integer is interpolated; table/columns are constants.
	return fmt.Sprintf("ALTER TABLE logs MODIFY TTL toDateTime(created_at) + INTERVAL %d DAY DELETE", days), nil
}

func syncClickHouseLogTTL(ctx context.Context, days int, createSQL string) error {
	statement, err := clickHouseLogTTLStatement(days, createSQL)
	if err != nil || statement == "" {
		return err
	}
	if err := LOG_DB.WithContext(ctx).Exec(statement).Error; err != nil {
		return err
	}
	if days == 0 {
		var rows []struct {
			DDL string `gorm:"column:ddl"`
		}
		if err := LOG_DB.WithContext(ctx).Raw("SELECT create_table_query AS ddl FROM system.tables WHERE database = currentDatabase() AND name = 'logs'").Scan(&rows).Error; err != nil {
			return err
		}
		if len(rows) != 1 || clickHouseLogHasTTL(rows[0].DDL) {
			return errors.New("ClickHouse log TTL removal could not be verified")
		}
	}
	return nil
}
