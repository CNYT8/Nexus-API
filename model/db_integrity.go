package model

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

type DatabaseIntegrityReport struct {
	Healthy       bool     `json:"healthy"`
	CheckedAt     int64    `json:"checked_at"`
	MainDialect   string   `json:"main_dialect"`
	LogDialect    string   `json:"log_dialect"`
	SeparateLogDB bool     `json:"separate_log_db"`
	TotalTables   int      `json:"total_tables"`
	CheckedTables int      `json:"checked_tables"`
	Issues        []string `json:"issues"`
	Logs          []string `json:"logs"`
}

type modelSpec struct {
	Name  string
	Model interface{}
}

func primaryDBModelSpecs() []modelSpec {
	return []modelSpec{
		{Name: "Channel", Model: &Channel{}},
		{Name: "Token", Model: &Token{}},
		{Name: "User", Model: &User{}},
		{Name: "PasskeyCredential", Model: &PasskeyCredential{}},
		{Name: "Option", Model: &Option{}},
		{Name: "Redemption", Model: &Redemption{}},
		{Name: "Ability", Model: &Ability{}},
		{Name: "Log", Model: &Log{}},
		{Name: "Midjourney", Model: &Midjourney{}},
		{Name: "TopUp", Model: &TopUp{}},
		{Name: "QuotaData", Model: &QuotaData{}},
		{Name: "Task", Model: &Task{}},
		{Name: "Model", Model: &Model{}},
		{Name: "Vendor", Model: &Vendor{}},
		{Name: "PrefillGroup", Model: &PrefillGroup{}},
		{Name: "Setup", Model: &Setup{}},
		{Name: "TwoFA", Model: &TwoFA{}},
		{Name: "TwoFABackupCode", Model: &TwoFABackupCode{}},
		{Name: "Checkin", Model: &Checkin{}},
		{Name: "SubscriptionPlan", Model: &SubscriptionPlan{}},
		{Name: "SubscriptionOrder", Model: &SubscriptionOrder{}},
		{Name: "UserSubscription", Model: &UserSubscription{}},
		{Name: "SubscriptionPreConsumeRecord", Model: &SubscriptionPreConsumeRecord{}},
		{Name: "CustomOAuthProvider", Model: &CustomOAuthProvider{}},
		{Name: "UserOAuthBinding", Model: &UserOAuthBinding{}},
		{Name: "PerfMetric", Model: &PerfMetric{}},
		{Name: "ModelMonitorSample", Model: &ModelMonitorSample{}},
		{Name: "UserMembership", Model: &UserMembership{}},
		{Name: "MembershipQuotaGrant", Model: &MembershipQuotaGrant{}},
		{Name: "UserGroupRatioOverride", Model: &UserGroupRatioOverride{}},
		{Name: "Ticket", Model: &Ticket{}},
		{Name: "TicketMessage", Model: &TicketMessage{}},
		{Name: "WebRiskState", Model: &WebRiskState{}},
		{Name: "EmptyResponseRecord", Model: &EmptyResponseRecord{}},
		{Name: "WalletCreditLot", Model: &WalletCreditLot{}},
		{Name: "WalletConsumptionSettlement", Model: &WalletConsumptionSettlement{}},
		{Name: "WalletConsumptionAllocation", Model: &WalletConsumptionAllocation{}},
		{Name: "WalletReservation", Model: &WalletReservation{}},
		{Name: "WalletLotHold", Model: &WalletLotHold{}},
		{Name: "WalletDebtRepayment", Model: &WalletDebtRepayment{}},
		{Name: "RebateCapBucket", Model: &RebateCapBucket{}},
		{Name: "RebateSystemState", Model: &RebateSystemState{}},
		{Name: "RebateRecord", Model: &RebateRecord{}},
	}
}

func logDBModelSpecs() []modelSpec {
	return []modelSpec{
		{Name: "Log", Model: &Log{}},
		{Name: "ModelMonitorSample", Model: &ModelMonitorSample{}},
	}
}

// CheckDatabaseIntegrity performs a strictly read-only inspection of the connected
// SQL database(s) (MySQL / PostgreSQL / SQLite, excluding Redis). It never executes
// DDL or DML (no CREATE, ALTER, DROP, INSERT, UPDATE, DELETE).
func CheckDatabaseIntegrity() DatabaseIntegrityReport {
	now := time.Now().Unix()
	report := DatabaseIntegrityReport{
		Healthy:   true,
		CheckedAt: now,
		Issues:    make([]string, 0),
		Logs:      make([]string, 0),
	}

	addIssue := func(msg string) {
		report.Healthy = false
		report.Issues = append(report.Issues, msg)
		report.Logs = append(report.Logs, "[ERROR] "+msg)
	}
	addInfo := func(msg string) {
		report.Logs = append(report.Logs, "[OK] "+msg)
	}

	if DB == nil {
		addIssue("主数据库实例未初始化 (model.DB is nil)")
		return report
	}

	report.MainDialect = DB.Dialector.Name()
	separateLogDB := LOG_DB != nil && LOG_DB != DB
	report.SeparateLogDB = separateLogDB
	if separateLogDB {
		report.LogDialect = LOG_DB.Dialector.Name()
	} else {
		report.LogDialect = report.MainDialect
	}

	inspectDBTarget(DB, "主数据库 (SQL_DSN)", primaryDBModelSpecs(), &report, addInfo, addIssue)
	if separateLogDB {
		inspectDBTarget(LOG_DB, "日志数据库 (LOG_SQL_DSN)", logDBModelSpecs(), &report, addInfo, addIssue)
	}

	return report
}

func inspectDBTarget(
	db *gorm.DB,
	label string,
	specs []modelSpec,
	report *DatabaseIntegrityReport,
	addInfo func(string),
	addIssue func(string),
) {
	dialect := db.Dialector.Name()
	sqlDB, err := db.DB()
	if err != nil {
		addIssue(fmt.Sprintf("%s: 获取底层数据库连接失败: %v", label, err))
		return
	}
	if err := sqlDB.Ping(); err != nil {
		addIssue(fmt.Sprintf("%s (%s): 数据库连接 Ping 失败: %v", label, dialect, err))
		return
	}
	addInfo(fmt.Sprintf("%s (%s): 数据库连接正常", label, dialect))

	if dialect == "mysql" {
		if err := checkMySQLChineseSupport(db); err != nil {
			addIssue(fmt.Sprintf("%s (MySQL): 字符集/排序规则兼容性异常: %v", label, err))
		} else {
			addInfo(fmt.Sprintf("%s (MySQL): 字符集与中文排序规则兼容", label))
		}
	} else if dialect == "sqlite" {
		var integrityResult string
		if err := db.Raw("PRAGMA quick_check(1)").Scan(&integrityResult).Error; err != nil {
			addIssue(fmt.Sprintf("%s (SQLite): PRAGMA quick_check 执行失败: %v", label, err))
		} else if !strings.EqualFold(strings.TrimSpace(integrityResult), "ok") {
			addIssue(fmt.Sprintf("%s (SQLite): 数据库物理完整性校验异常: %s", label, integrityResult))
		} else {
			addInfo(fmt.Sprintf("%s (SQLite): PRAGMA quick_check 物理完整性正常", label))
		}
	}

	actualColumnsByTable, engineIssues, err := loadDatabaseTableColumnsReadOnly(db, dialect)
	if err != nil {
		addIssue(fmt.Sprintf("%s (%s): 读取数据库元数据表结构失败: %v", label, dialect, err))
		return
	}
	for _, engIssue := range engineIssues {
		addIssue(fmt.Sprintf("%s (%s): %s", label, dialect, engIssue))
	}

	expectedTables := make(map[string]struct{}, len(specs))
	var namer schema.Namer
	if db.Config != nil && db.Config.NamingStrategy != nil {
		namer = db.Config.NamingStrategy
	} else {
		namer = schema.NamingStrategy{}
	}

	for _, spec := range specs {
		report.TotalTables++
		s, parseErr := schema.Parse(spec.Model, &syncMapPool, namer)
		if parseErr != nil || s == nil {
			addIssue(fmt.Sprintf("%s: 解析系统模型 %s 失败: %v", label, spec.Name, parseErr))
			continue
		}
		tableName := s.Table
		expectedTables[strings.ToLower(tableName)] = struct{}{}

		colSet, tableExists := actualColumnsByTable[strings.ToLower(tableName)]
		if !tableExists {
			if !db.Migrator().HasTable(tableName) {
				addIssue(fmt.Sprintf("%s: 缺失系统核心表 `%s` (模型 %s)", label, tableName, spec.Name))
				continue
			}
			colSet = make(map[string]string)
		}

		// Check missing columns AND unrecognized extra columns against GORM schema
		var missingCols []string
		var selectCols []string
		expectedCols := make(map[string]struct{}, len(s.Fields))
		for _, field := range s.Fields {
			if field.DBName == "" {
				continue
			}
			colLower := strings.ToLower(field.DBName)
			expectedCols[colLower] = struct{}{}
			selectCols = append(selectCols, quoteIdentifierReadOnly(dialect, field.DBName))
			if len(colSet) > 0 {
				if _, ok := colSet[colLower]; !ok {
					missingCols = append(missingCols, field.DBName)
				}
			} else if !db.Migrator().HasColumn(spec.Model, field.DBName) {
				missingCols = append(missingCols, field.DBName)
			}
		}
		if len(missingCols) > 0 {
			addIssue(fmt.Sprintf("%s: 表 `%s` (模型 %s) 缺失字段: [%s]", label, tableName, spec.Name, strings.Join(missingCols, ", ")))
			continue
		}

		var unknownCols []string
		for actualColLower := range colSet {
			if _, ok := expectedCols[actualColLower]; !ok {
				unknownCols = append(unknownCols, actualColLower)
			}
		}
		sort.Strings(unknownCols)
		if len(unknownCols) > 0 {
			addIssue(fmt.Sprintf("%s: 表 `%s` (模型 %s) 存在系统无法识别的冗余/未知字段: [%s]", label, tableName, spec.Name, strings.Join(unknownCols, ", ")))
			continue
		}

		// Read-only probe query: verify table storage engine is not corrupted and all model columns are readable
		quotedTable := quoteIdentifierReadOnly(dialect, tableName)
		probeColExpr := "1"
		if len(selectCols) > 0 {
			probeColExpr = strings.Join(selectCols, ", ")
		}
		probeSQL := fmt.Sprintf("SELECT %s FROM %s LIMIT 1", probeColExpr, quotedTable)
		rows, queryErr := db.Raw(probeSQL).Rows()
		if queryErr != nil {
			addIssue(fmt.Sprintf("%s: 表 `%s` (模型 %s) 读取异常或表已损坏: %v", label, tableName, spec.Name, queryErr))
			continue
		}
		_ = rows.Close()

		// Dialect-specific physical / metadata corruption check (strictly read-only)
		if dialect == "mysql" {
			if checkErr := checkMySQLTableReadOnly(db, quotedTable); checkErr != nil {
				addIssue(fmt.Sprintf("%s: 表 `%s` (模型 %s) MySQL CHECK TABLE 检测到表或索引损坏: %v", label, tableName, spec.Name, checkErr))
				continue
			}
		} else if dialect == "postgres" {
			if checkErr := checkPostgresTableReadOnly(db, tableName); checkErr != nil {
				addIssue(fmt.Sprintf("%s: 表 `%s` (模型 %s) PostgreSQL 索引/约束完整性异常: %v", label, tableName, spec.Name, checkErr))
				continue
			}
		}

		// Dialect-specific column type compatibility checks (read-only)
		if tableName == "tokens" && (dialect == "mysql" || dialect == "postgres") {
			if dataType, ok := colSet["model_limits"]; ok && dataType != "" {
				dtLower := strings.ToLower(dataType)
				if !strings.Contains(dtLower, "text") && !strings.Contains(dtLower, "clob") {
					addIssue(fmt.Sprintf("%s: 表 `tokens` 字段 `model_limits` 类型为 `%s`（建议为 `text` 以兼容长模型列表）", label, dataType))
					continue
				}
			}
		}
		if tableName == "subscription_plans" && (dialect == "mysql" || dialect == "postgres") {
			if dataType, ok := colSet["price_amount"]; ok && dataType != "" {
				dtLower := strings.ToLower(dataType)
				if !strings.Contains(dtLower, "decimal") && !strings.Contains(dtLower, "numeric") {
					addIssue(fmt.Sprintf("%s: 表 `subscription_plans` 字段 `price_amount` 类型为 `%s`（期望 `decimal/numeric` 定点类型）", label, dataType))
					continue
				}
			}
		}

		report.CheckedTables++
		addInfo(fmt.Sprintf("%s: 表 `%s` (%s) 结构与读取校验正常 (%d 个字段)", label, tableName, spec.Name, len(selectCols)))
	}

	// Report any unknown tables in the schema that are not recognized by Nexus-API
	ignoredExtraTables := map[string]struct{}{
		"sqlite_sequence":   {},
		"schema_migrations": {},
		"gorm_migrations":   {},
	}
	var unknownTables []string
	for tblLower := range actualColumnsByTable {
		if _, expected := expectedTables[tblLower]; expected {
			continue
		}
		if _, ignored := ignoredExtraTables[tblLower]; ignored {
			continue
		}
		unknownTables = append(unknownTables, tblLower)
	}
	sort.Strings(unknownTables)
	if len(unknownTables) > 0 {
		addIssue(fmt.Sprintf("%s: 发现非本系统原生的数据表 (%d 个): [%s]", label, len(unknownTables), strings.Join(unknownTables, ", ")))
	}

	if db == DB {
		inspectWalletLedgerReadOnly(db, label, addInfo, addIssue)
	}
}

func inspectWalletLedgerReadOnly(
	db *gorm.DB,
	label string,
	addInfo func(string),
	addIssue func(string),
) {
	active, err := WalletCreditLedgerActive(db)
	if err != nil {
		addIssue(fmt.Sprintf("%s: 读取钱包账本初始化状态失败: %v", label, err))
		return
	}
	if !active {
		addInfo(fmt.Sprintf("%s: 钱包账本基线尚未初始化 (可在运维设置中点击“钱包账本初始化”)", label))
		return
	}

	afterUserID := 0
	const batchSize = 2000
	var mismatchCount int
	var sampleUsers []string
	for {
		batch, next, done, recErr := ReconcileWalletCreditBatch(db, afterUserID, batchSize)
		if recErr != nil {
			addIssue(fmt.Sprintf("%s: 钱包账本只读对账检查失败: %v", label, recErr))
			return
		}
		for _, item := range batch {
			mismatchCount++
			if len(sampleUsers) < 5 {
				sampleUsers = append(sampleUsers, fmt.Sprintf("user=%d(quota=%d,expected=%d)", item.UserID, item.Quota, item.ExpectedQuota))
			}
		}
		if done {
			break
		}
		afterUserID = next
	}
	if mismatchCount > 0 {
		addIssue(fmt.Sprintf("%s: 钱包账本存在 %d 个用户余额与来源批次不一致: [%s]", label, mismatchCount, strings.Join(sampleUsers, ", ")))
		return
	}
	addInfo(fmt.Sprintf("%s: 钱包账本基线已初始化且全部用户余额与账本批次一致", label))
}

var syncMapPool sync.Map

func quoteIdentifierReadOnly(dialect, ident string) string {
	safe := strings.ReplaceAll(ident, "`", "")
	safe = strings.ReplaceAll(safe, `"`, "")
	if dialect == "postgres" {
		return `"` + safe + `"`
	}
	return "`" + safe + "`"
}

func loadDatabaseTableColumnsReadOnly(db *gorm.DB, dialect string) (map[string]map[string]string, []string, error) {
	result := make(map[string]map[string]string)
	var engineIssues []string

	switch dialect {
	case "mysql":
		// Verify no corrupted tables with missing engine/comment in information_schema.TABLES
		type mysqlTableRow struct {
			TableName    string  `gorm:"column:TABLE_NAME"`
			Engine       *string `gorm:"column:ENGINE"`
			TableComment string  `gorm:"column:TABLE_COMMENT"`
		}
		var tableRows []mysqlTableRow
		if err := db.Raw(`SELECT TABLE_NAME, ENGINE, TABLE_COMMENT
			FROM information_schema.TABLES
			WHERE TABLE_SCHEMA = DATABASE() AND TABLE_TYPE = 'BASE TABLE'`).Scan(&tableRows).Error; err != nil {
			return nil, nil, err
		}
		for _, tr := range tableRows {
			tblLower := strings.ToLower(tr.TableName)
			if tr.Engine == nil || strings.TrimSpace(*tr.Engine) == "" {
				engineIssues = append(engineIssues, fmt.Sprintf("表 `%s` 存储引擎为空或已损坏 (%s)", tr.TableName, tr.TableComment))
			}
			if _, ok := result[tblLower]; !ok {
				result[tblLower] = make(map[string]string)
			}
		}

		type mysqlColRow struct {
			TableName  string `gorm:"column:TABLE_NAME"`
			ColumnName string `gorm:"column:COLUMN_NAME"`
			ColumnType string `gorm:"column:COLUMN_TYPE"`
		}
		var colRows []mysqlColRow
		if err := db.Raw(`SELECT TABLE_NAME, COLUMN_NAME, COLUMN_TYPE
			FROM information_schema.COLUMNS
			WHERE TABLE_SCHEMA = DATABASE()`).Scan(&colRows).Error; err != nil {
			return nil, engineIssues, err
		}
		for _, cr := range colRows {
			tblLower := strings.ToLower(cr.TableName)
			if _, ok := result[tblLower]; !ok {
				result[tblLower] = make(map[string]string)
			}
			result[tblLower][strings.ToLower(cr.ColumnName)] = cr.ColumnType
		}
		return result, engineIssues, nil

	case "postgres":
		type pgTableRow struct {
			TableName string `gorm:"column:table_name"`
		}
		var tableRows []pgTableRow
		if err := db.Raw(`SELECT table_name
			FROM information_schema.tables
			WHERE table_schema = current_schema() AND table_type = 'BASE TABLE'`).Scan(&tableRows).Error; err != nil {
			return nil, nil, err
		}
		for _, tr := range tableRows {
			tblLower := strings.ToLower(tr.TableName)
			if _, ok := result[tblLower]; !ok {
				result[tblLower] = make(map[string]string)
			}
		}

		type pgColRow struct {
			TableName  string `gorm:"column:table_name"`
			ColumnName string `gorm:"column:column_name"`
			DataType   string `gorm:"column:data_type"`
		}
		var colRows []pgColRow
		if err := db.Raw(`SELECT table_name, column_name, data_type
			FROM information_schema.columns
			WHERE table_schema = current_schema()`).Scan(&colRows).Error; err != nil {
			return nil, nil, err
		}
		for _, cr := range colRows {
			tblLower := strings.ToLower(cr.TableName)
			if _, ok := result[tblLower]; !ok {
				result[tblLower] = make(map[string]string)
			}
			result[tblLower][strings.ToLower(cr.ColumnName)] = cr.DataType
		}
		return result, nil, nil

	case "sqlite":
		type sqliteMasterRow struct {
			Name string `gorm:"column:name"`
		}
		var tables []sqliteMasterRow
		if err := db.Raw(`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`).Scan(&tables).Error; err != nil {
			return nil, nil, err
		}
		for _, t := range tables {
			tblLower := strings.ToLower(t.Name)
			result[tblLower] = make(map[string]string)
			var cols []struct {
				Name string `gorm:"column:name"`
				Type string `gorm:"column:type"`
			}
			safeName := strings.ReplaceAll(t.Name, "`", "")
			if err := db.Raw("PRAGMA table_info(`" + safeName + "`)").Scan(&cols).Error; err != nil {
				return nil, nil, err
			}
			for _, c := range cols {
				result[tblLower][strings.ToLower(c.Name)] = c.Type
			}
		}
		return result, nil, nil

	default:
		return result, nil, nil
	}
}

func checkMySQLTableReadOnly(db *gorm.DB, quotedTable string) error {
	type checkTableRow struct {
		Table   string `gorm:"column:Table"`
		Op      string `gorm:"column:Op"`
		MsgType string `gorm:"column:Msg_type"`
		MsgText string `gorm:"column:Msg_text"`
	}
	var rows []checkTableRow
	if err := db.Raw(fmt.Sprintf("CHECK TABLE %s FAST QUICK", quotedTable)).Scan(&rows).Error; err != nil {
		return err
	}
	for _, r := range rows {
		msgType := strings.ToLower(strings.TrimSpace(r.MsgType))
		msgText := strings.TrimSpace(r.MsgText)
		if msgType == "error" || (msgType == "status" && !strings.EqualFold(msgText, "ok") && !strings.Contains(strings.ToLower(msgText), "already up to date")) {
			return fmt.Errorf("%s: %s", r.MsgType, msgText)
		}
	}
	return nil
}

func checkPostgresTableReadOnly(db *gorm.DB, tableName string) error {
	type invalidIndexRow struct {
		IndexName string `gorm:"column:index_name"`
	}
	var invalidIndexes []invalidIndexRow
	if err := db.Raw(`
		SELECT c2.relname AS index_name
		FROM pg_index i
		JOIN pg_class c1 ON c1.oid = i.indrelid
		JOIN pg_class c2 ON c2.oid = i.indexrelid
		JOIN pg_namespace n ON n.oid = c1.relnamespace
		WHERE n.nspname = current_schema()
		  AND LOWER(c1.relname) = LOWER(?)
		  AND (NOT i.indisvalid OR NOT i.indisready)
	`, tableName).Scan(&invalidIndexes).Error; err != nil {
		return err
	}
	if len(invalidIndexes) > 0 {
		names := make([]string, 0, len(invalidIndexes))
		for _, idx := range invalidIndexes {
			names = append(names, idx.IndexName)
		}
		return fmt.Errorf("存在失效或未就绪的索引: [%s]", strings.Join(names, ", "))
	}

	type invalidConstraintRow struct {
		ConName string `gorm:"column:conname"`
	}
	var invalidConstraints []invalidConstraintRow
	if err := db.Raw(`
		SELECT con.conname AS conname
		FROM pg_constraint con
		JOIN pg_class rel ON rel.oid = con.conrelid
		JOIN pg_namespace n ON n.oid = rel.relnamespace
		WHERE n.nspname = current_schema()
		  AND LOWER(rel.relname) = LOWER(?)
		  AND NOT con.convalidated
	`, tableName).Scan(&invalidConstraints).Error; err != nil {
		return err
	}
	if len(invalidConstraints) > 0 {
		names := make([]string, 0, len(invalidConstraints))
		for _, c := range invalidConstraints {
			names = append(names, c.ConName)
		}
		return fmt.Errorf("存在未验证的约束: [%s]", strings.Join(names, ", "))
	}
	return nil
}
