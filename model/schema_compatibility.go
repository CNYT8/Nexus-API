package model

import (
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

// Never rename the original tables: upstream and historical Nexus installations
// used the same names for incompatible credentials and plugin languages.
func (UserSession) TableName() string     { return "nexus_user_sessions" }
func (AuthFlow) TableName() string        { return "nexus_auth_flows" }
func (UserAccessToken) TableName() string { return "nexus_user_access_tokens" }
func (TaskPlugin) TableName() string      { return "nexus_task_plugins" }
func (SystemTask) TableName() string      { return "nexus_system_tasks" }

// This is a durable cutover receipt, not a cache. Deleting it could resurrect
// consumed/revoked/deleted records from the frozen archive on the next startup.
// Backups/restores MUST include it and the nexus_* tables together.
type SchemaCompatibilityMigration struct {
	Source      string `gorm:"type:varchar(64);primaryKey"`
	Destination string `gorm:"type:varchar(64);not null"`
	Layout      string `gorm:"type:varchar(32);not null"`
	Disposition string `gorm:"type:varchar(32);not null"`
	CopiedRows  int64  `gorm:"not null"`
	CompletedAt int64  `gorm:"not null"`
}

func (SchemaCompatibilityMigration) TableName() string { return "nexus_schema_compatibility" }

const (
	compatibilityNexus    = "nexus-v1"
	compatibilityUpstream = "upstream-v1"
	compatibilityAbsent   = "absent"
)

type compatibilitySpec struct {
	Source           string
	Model            interface{}
	UpstreamRequired string
	UpstreamIdentity string
}

func compatibilitySpecs() []compatibilitySpec {
	return []compatibilitySpec{
		{"user_sessions", &UserSession{}, "sid,user_id,version,user_auth_version,status,refresh_hash,expires_at", "sid"},
		{"auth_flows", &AuthFlow{}, "id,token_hash,purpose,session_id,expires_at,consumed_at", "token_hash"},
		{"user_access_tokens", &UserAccessToken{}, "id,user_id,token_hash,token_hint,scopes,expires_at", "token_hash"},
		{"task_plugins", &TaskPlugin{}, "id,key,version,api_version,source,source_hash,enabled,active,created_at", "source_hash"},
		{"system_tasks", &SystemTask{}, "id,task_id,type,status,payload,state,result,locked_by", "type"},
	}
}

func compatibilitySchema(db *gorm.DB, model interface{}) (*schema.Schema, error) {
	stmt := &gorm.Statement{DB: db}
	if err := stmt.Parse(model); err != nil {
		return nil, err
	}
	return stmt.Schema, nil
}

func compatibilityColumns(db *gorm.DB, table string) (map[string]gorm.ColumnType, error) {
	columns, err := db.Migrator().ColumnTypes(table)
	if err != nil {
		return nil, fmt.Errorf("inspect compatibility table %s: %w", table, err)
	}
	result := make(map[string]gorm.ColumnType, len(columns))
	for _, column := range columns {
		result[strings.ToLower(column.Name())] = column
	}
	return result, nil
}

func compatibilityColumnMatches(column gorm.ColumnType, field *schema.Field) bool {
	typ := strings.ToLower(column.DatabaseTypeName())
	switch field.DataType {
	case schema.Int, schema.Uint:
		return strings.Contains(typ, "int") || typ == "serial" || typ == "bigserial"
	case schema.Bool:
		return strings.Contains(typ, "bool") || strings.Contains(typ, "int") || typ == "numeric"
	default:
		return strings.Contains(typ, "char") || strings.Contains(typ, "text") || typ == "clob"
	}
}

// Only known non-authorizing historical additions can be absent. In particular
// nullable auth_version/revoked_at/consumed_at are NOT optional: guessing those
// could revive an invalid credential. Unknown extra columns/indexes are fine.
func optionalCompatibilityColumn(table, column string) bool {
	optional := map[string]string{
		"nexus_user_sessions":      ",created_at,verified_at,verified_method,login_method,ip,user_agent,",
		"nexus_auth_flows":         ",",
		"nexus_user_access_tokens": ",name,prefix,created_at,",
		"nexus_task_plugins":       ",created_at,retain_result,",
		"nexus_system_tasks":       ",error,created_at,updated_at,progress_version,progress_exact,log_backend,",
	}
	return strings.Contains(optional[table], ","+column+",")
}

// Unknown columns and indexes are legitimate forward/upstream extensions. Only
// validate columns we actually use; never ALTER their types or constraints.
func validateCompatibilityColumns(db *gorm.DB, table string, columns map[string]gorm.ColumnType, s *schema.Schema) error {
	for _, field := range s.Fields {
		if field.DBName == "" {
			continue
		}
		column, exists := columns[field.DBName]
		if !exists {
			if optionalCompatibilityColumn(s.Table, field.DBName) {
				continue
			}
			return fmt.Errorf("missing required column %s", field.DBName)
		}
		if !compatibilityColumnMatches(column, field) {
			return fmt.Errorf("incompatible column type for %s (%s)", field.DBName, column.DatabaseTypeName())
		}
	}
	primary, err := migrationPrimaryColumns(db, table, columns)
	if err != nil {
		return err
	}
	for _, field := range s.PrimaryFields {
		if !primary[field.DBName] {
			return fmt.Errorf("incompatible primary key %s", field.DBName)
		}
	}
	for name := range primary {
		field := s.LookUpField(name)
		if field == nil || !field.PrimaryKey {
			return fmt.Errorf("unexpected primary key %s", name)
		}
	}
	return nil
}

func classifyCompatibilityTable(db *gorm.DB, spec compatibilitySpec) (string, error) {
	if !db.Migrator().HasTable(spec.Source) {
		return compatibilityAbsent, nil
	}
	columns, err := compatibilityColumns(db, spec.Source)
	if err != nil {
		return "", err
	}
	s, err := compatibilitySchema(db, spec.Model)
	if err != nil {
		return "", err
	}
	identity := "secret_hash"
	if spec.Source == "task_plugins" {
		identity = "expression"
	} else if spec.Source == "system_tasks" {
		identity = "kind"
	}
	_, nexus := columns[identity]
	_, upstream := columns[spec.UpstreamIdentity]
	if nexus && upstream {
		return "", fmt.Errorf("mixed Nexus/upstream layout in %s; preserve both tables and reconcile explicitly", spec.Source)
	}
	if nexus {
		frozen, err := compatibilityV1Schema(s.Table)
		if err != nil {
			return "", err
		}
		if err := validateCompatibilityColumns(db, spec.Source, columns, frozen); err != nil {
			return "", fmt.Errorf("unsafe Nexus source %s: %w", spec.Source, err)
		}
		return compatibilityNexus, nil
	}
	if upstream {
		for _, name := range strings.Split(spec.UpstreamRequired, ",") {
			if _, exists := columns[name]; !exists {
				return "", fmt.Errorf("unrecognized upstream layout in %s: missing %s", spec.Source, name)
			}
		}
		primaryName := "id"
		if spec.Source == "user_sessions" {
			primaryName = "sid"
		}
		primary, err := migrationPrimaryColumns(db, spec.Source, columns)
		if err != nil {
			return "", err
		}
		if !primary[primaryName] || len(primary) != 1 {
			return "", fmt.Errorf("unrecognized upstream primary key in %s", spec.Source)
		}
		return compatibilityUpstream, nil
	}
	return "", fmt.Errorf("unrecognized layout in %s; no tables were renamed or dropped", spec.Source)
}

// Deliberately NOT AutoMigrate: SQLite AutoMigrate can rebuild tables, and an
// upstream NOT NULL/PK/time column must never be silently converted. New optional
// columns/indexes may be appended, but required additions need an explicit plan.
func ensureCompatibilityTable(db *gorm.DB, model interface{}) error {
	s, err := compatibilitySchema(db, model)
	if err != nil {
		return err
	}
	if !db.Migrator().HasTable(s.Table) {
		return db.Migrator().CreateTable(model)
	}
	columns, err := compatibilityColumns(db, s.Table)
	if err != nil {
		return err
	}
	frozen, err := compatibilityV1Schema(s.Table)
	if err != nil {
		return err
	}
	// Frozen authorization/state columns remain required even when nullable.
	// Current optional/defaulted additions are handled independently below.
	if err := validateCompatibilityColumns(db, s.Table, columns, frozen); err != nil {
		return fmt.Errorf("unsafe compatibility destination %s: %w", s.Table, err)
	}
	return migrateAdditive(db, model)
}

// MyISAM cannot roll back a partial import or its receipt. Fail before copying
// rather than pretend the cutover is atomic. We never change an existing engine.
func requireCompatibilityTransactions(db *gorm.DB, table string) error {
	if db.Dialector.Name() != "mysql" {
		return nil
	}
	var engine string
	if err := db.Raw("SELECT ENGINE FROM information_schema.TABLES WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ?", table).Scan(&engine).Error; err != nil {
		return err
	}
	if !strings.EqualFold(engine, "InnoDB") {
		return fmt.Errorf("compatibility table %s requires transactional InnoDB (found %s)", table, engine)
	}
	return nil
}

// Explicit IDs advance SQLite ROWID and MySQL AUTO_INCREMENT automatically,
// but not PostgreSQL sequences. Run after every successful receipt, including
// restarts after commit, and never move a sequence backwards.
func advanceCompatibilitySequence(db *gorm.DB, table string) error {
	if db.Dialector.Name() != "postgres" {
		return nil
	}
	var maximum int64
	if err := db.Table(table).Select("COALESCE(MAX(id), 0)").Scan(&maximum).Error; err != nil || maximum == 0 {
		return err
	}
	var sequence sql.NullString
	if err := db.Raw("SELECT pg_get_serial_sequence(?, 'id')", quoteIdentifierReadOnly("postgres", table)).Scan(&sequence).Error; err != nil {
		return err
	}
	if !sequence.Valid || sequence.String == "" {
		return fmt.Errorf("missing PostgreSQL ID sequence for %s", table)
	}
	return db.Exec(compatibilitySequenceSQL, sequence.String, sequence.String, maximum).Error
}

const compatibilitySequenceSQL = "SELECT setval(CAST(? AS regclass), GREATEST(COALESCE(pg_sequence_last_value(CAST(? AS regclass)), 0), ?), true)"

// Compare every persisted column, not just a hash/key. NULL remains NULL and
// maps avoid GORM's autoCreateTime/default callbacks changing historical rows.
func compatibilityValue(value interface{}) string {
	switch value := value.(type) {
	case nil:
		return "null:"
	case []byte:
		return "value:" + string(value)
	case bool:
		if value {
			return "value:1"
		}
		return "value:0"
	default:
		return "value:" + fmt.Sprint(value)
	}
}

func copyCompatibilityRows(tx *gorm.DB, spec compatibilitySpec, destination string) (int64, error) {
	columns, err := compatibilityColumns(tx, spec.Source)
	if err != nil {
		return 0, err
	}
	s, err := compatibilityV1Schema(destination)
	if err != nil {
		return 0, err
	}
	var selected, sourceSelected []string
	for _, field := range s.Fields {
		if field.DBName == "" {
			continue
		}
		quoted := quoteIdentifierReadOnly(tx.Dialector.Name(), field.DBName)
		selected = append(selected, quoted)
		if columns[field.DBName] != nil {
			sourceSelected = append(sourceSelected, quoted)
			continue
		}
		// Explicit defaults, including nil RetainResult; never let GORM stamp
		// historical CreatedAt/UpdatedAt with the upgrade time.
		value := "''"
		switch field.DataType {
		case schema.Int, schema.Uint:
			value = "0"
		case schema.Bool:
			value = "false"
		}
		if field.DBName == "retain_result" {
			value = "NULL"
		}
		sourceSelected = append(sourceSelected, value+" AS "+quoted)
	}
	var copied, after int64
	for {
		var batch []map[string]interface{}
		if err := tx.Table(spec.Source).Select(strings.Join(sourceSelected, ", ")).Where("id > ?", after).Order("id").Limit(256).Find(&batch).Error; err != nil {
			return 0, err
		}
		if len(batch) == 0 {
			return copied, nil
		}
		for _, row := range batch {
			id, err := strconv.ParseInt(strings.TrimPrefix(compatibilityValue(row["id"]), "value:"), 10, 64)
			if err != nil || id <= after {
				return 0, fmt.Errorf("invalid source ID in %s", spec.Source)
			}
			var existing []map[string]interface{}
			if err := tx.Table(destination).Select(strings.Join(selected, ", ")).Where("id = ?", id).Limit(1).Find(&existing).Error; err != nil {
				return 0, err
			}
			if len(existing) != 0 {
				for column, value := range row {
					if compatibilityValue(value) != compatibilityValue(existing[0][column]) {
						return 0, fmt.Errorf("compatibility conflict %s id=%d column=%s; neither row overwritten", spec.Source, id, column)
					}
				}
			} else if err := tx.Table(destination).Create(row).Error; err != nil {
				// Do not use ON CONFLICT/INSERT IGNORE: a different ID with the
				// same secret/revision must abort, not silently discard a record.
				return 0, fmt.Errorf("compatibility insert conflict in %s id=%d: %w", spec.Source, id, err)
			}
			copied++
			after = id
		}
	}
}

// MigrateSchemaCompatibility runs before any ordinary AutoMigrate and before
// serving traffic. Old binaries/writers must be stopped during cutover. Source
// tables become immutable archives: subsequent startups never replay them.
func MigrateSchemaCompatibility(db *gorm.DB) error {
	return migrateSchemaCompatibility(db, compatibilitySpecs())
}

func migrateSchemaCompatibility(db *gorm.DB, specs []compatibilitySpec) error {
	if db == nil {
		return errors.New("compatibility migration requires a database")
	}
	layouts := make(map[string]string, len(specs))
	for _, spec := range specs {
		layout, err := classifyCompatibilityTable(db, spec)
		if err != nil {
			return err
		}
		layouts[spec.Source] = layout
	}
	if err := ensureCompatibilityTable(db, &SchemaCompatibilityMigration{}); err != nil {
		return err
	}
	if err := requireCompatibilityTransactions(db, (SchemaCompatibilityMigration{}).TableName()); err != nil {
		return err
	}
	for _, spec := range specs {
		s, err := compatibilitySchema(db, spec.Model)
		if err != nil {
			return err
		}
		var receipt SchemaCompatibilityMigration
		err = db.Where("source = ?", spec.Source).First(&receipt).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err == nil {
			if err := validateCompatibilityReceipt(db, spec, receipt, layouts[spec.Source]); err != nil {
				return err
			}
		}
		if err := ensureCompatibilityTable(db, spec.Model); err != nil {
			return err
		}
		if err := requireCompatibilityTransactions(db, s.Table); err != nil {
			return err
		}
		if receipt.CompletedAt == 0 {
			if layouts[spec.Source] == compatibilityNexus {
				if err := requireCompatibilityTransactions(db, spec.Source); err != nil {
					return err
				}
			}
			err = db.Transaction(func(tx *gorm.DB) error {
				var copied int64
				if layouts[spec.Source] == compatibilityNexus {
					var invalid int64
					if err := tx.Table(spec.Source).Where("id <= 0 OR id IS NULL").Count(&invalid).Error; err != nil {
						return err
					}
					if invalid != 0 {
						return fmt.Errorf("non-positive source IDs in %s require explicit reconciliation", spec.Source)
					}
					var err error
					copied, err = copyCompatibilityRows(tx, spec, s.Table)
					if err != nil {
						return err
					}
				}
				return tx.Create(&SchemaCompatibilityMigration{Source: spec.Source, Destination: s.Table, Layout: layouts[spec.Source],
					Disposition: compatibilityDisposition(layouts[spec.Source]), CopiedRows: copied, CompletedAt: time.Now().Unix()}).Error
			})
			if err != nil {
				return err
			}
		}
		if err := advanceCompatibilitySequence(db, s.Table); err != nil {
			return err
		}
		if layouts[spec.Source] == compatibilityUpstream {
			common.SysLog("preserved upstream table " + spec.Source + ": see database integrity compatibility review; no automatic credential/plugin/task reinterpretation")
		}
	}
	return nil
}

// Migration and the read-only review share the same receipt and destination
// checks. A missing target must never be recreated once cutover was recorded.
func validateCompatibilityReceipt(db *gorm.DB, spec compatibilitySpec, receipt SchemaCompatibilityMigration, layout string) error {
	s, err := compatibilitySchema(db, spec.Model)
	if err != nil {
		return err
	}
	if receipt.Source != spec.Source || receipt.Destination != s.Table || receipt.Layout != layout || receipt.CompletedAt <= 0 || receipt.CopiedRows < 0 || receipt.Disposition != compatibilityDisposition(layout) {
		return fmt.Errorf("compatibility receipt/layout mismatch for %s; restore matching archives and nexus tables", spec.Source)
	}
	if !db.Migrator().HasTable(s.Table) {
		return fmt.Errorf("completed compatibility destination %s is missing; restore it, do not replay credentials", s.Table)
	}
	return nil
}

func compatibilityDisposition(layout string) string {
	switch layout {
	case compatibilityNexus:
		return "copied-once"
	case compatibilityUpstream:
		return "preserved-not-converted"
	case compatibilityAbsent:
		return "new-installation"
	default:
		return ""
	}
}

// A signed legacy cookie is not evidence of a live upstream JWT session. Never
// turn an unconvertible upstream session/flow/PAT into a fresh Nexus session.
func allowLegacySessionAdoption(db *gorm.DB) (bool, error) {
	if !db.Migrator().HasTable(&SchemaCompatibilityMigration{}) {
		return false, errors.New("authentication compatibility migration has not completed")
	}
	var receipts []SchemaCompatibilityMigration
	if err := db.Where("source IN ?", []string{"user_sessions", "auth_flows", "user_access_tokens"}).Find(&receipts).Error; err != nil {
		return false, err
	}
	if len(receipts) != 3 {
		return false, errors.New("authentication compatibility migration has not completed")
	}
	for _, receipt := range receipts {
		if receipt.CompletedAt <= 0 || receipt.Disposition != compatibilityDisposition(receipt.Layout) ||
			(receipt.Layout != compatibilityNexus && receipt.Layout != compatibilityAbsent) {
			return false, nil
		}
	}
	return true, nil
}
