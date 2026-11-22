package model

import (
	"crypto/sha256"
	"database/sql"
	"fmt"
	"strings"
	"unicode"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/schema"
)

// HasIndex only checks a name. Keep the definition as well: an existing ordinary,
// partial, invalid or differently keyed index cannot stand in for a unique key.
type migrationIndex struct {
	Name        string
	Columns     []string
	Unique      bool
	Valid       bool
	Unsupported bool
	Predicate   string
}

func migrationIndexes(db *gorm.DB, table string) (map[string]migrationIndex, error) {
	indexes := make(map[string]migrationIndex)
	switch db.Dialector.Name() {
	case "sqlite":
		var list []struct {
			Name     string
			IsUnique bool
			Partial  bool
		}
		if err := db.Raw(`SELECT name, "unique" AS is_unique, partial FROM pragma_index_list(?)`, table).Scan(&list).Error; err != nil {
			return nil, err
		}
		for _, item := range list {
			index := migrationIndex{Name: item.Name, Unique: item.IsUnique, Valid: true}
			var fields []struct {
				CID  int `gorm:"column:cid"`
				Name sql.NullString
			}
			if err := db.Raw(`SELECT cid, name FROM pragma_index_info(?) ORDER BY seqno`, item.Name).Scan(&fields).Error; err != nil {
				return nil, err
			}
			for _, field := range fields {
				index.Columns = append(index.Columns, field.Name.String)
				index.Unsupported = index.Unsupported || field.CID < 0 || !field.Name.Valid
			}
			if item.Partial {
				var ddl string
				if err := db.Raw(`SELECT sql FROM sqlite_master WHERE type = 'index' AND name = ? AND tbl_name = ?`, item.Name, table).Scan(&ddl).Error; err != nil {
					return nil, err
				}
				tokens := migrationSQLTokens(ddl)
				predicateFound := false
				for i, token := range tokens {
					if token == "where" {
						index.Predicate = strings.Join(tokens[i+1:], " ")
						predicateFound = true
						break
					}
				}
				index.Unsupported = index.Unsupported || !predicateFound
			}
			indexes[index.Name] = index
		}
	case "mysql":
		// This driver exposes uniqueness and ordered columns, but not prefixes.
		list, err := db.Migrator().GetIndexes(table)
		if err != nil {
			return nil, err
		}
		for _, item := range list {
			unique, known := item.Unique()
			indexes[item.Name()] = migrationIndex{Name: item.Name(), Columns: item.Columns(), Unique: unique, Valid: known}
		}
		var shortened []struct{ Name string }
		if err := db.Raw(`SELECT DISTINCT INDEX_NAME AS name FROM information_schema.STATISTICS
WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ? AND (SUB_PART IS NOT NULL OR COLUMN_NAME IS NULL)`, table).Scan(&shortened).Error; err != nil {
			return nil, err
		}
		for _, item := range shortened {
			index := indexes[item.Name]
			index.Unsupported = true
			indexes[item.Name] = index
		}
	case "postgres":
		// GetIndexes omits constraint-backed keys and predicate/validity metadata.
		var rows []struct {
			Name        string
			ColumnName  sql.NullString
			IsUnique    bool
			Valid       bool
			Unsupported bool
			Predicate   string
		}
		if err := db.Raw(`SELECT ic.relname AS name, a.attname AS column_name, i.indisunique AS is_unique,
       (i.indisvalid AND i.indisready AND i.indislive AND COALESCE(NOT c.condeferrable, true)) AS valid,
       (i.indexprs IS NOT NULL OR i.indnatts <> i.indnkeyatts) AS unsupported,
       COALESCE(pg_get_expr(i.indpred, i.indrelid), '') AS predicate
FROM pg_catalog.pg_index i
JOIN pg_catalog.pg_class ic ON ic.oid = i.indexrelid
CROSS JOIN LATERAL generate_series(0, i.indnkeyatts - 1) AS n(position)
LEFT JOIN pg_catalog.pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = i.indkey[n.position]
LEFT JOIN pg_catalog.pg_constraint c ON c.conindid = i.indexrelid AND c.contype IN ('p', 'u')
WHERE i.indrelid = to_regclass(?) ORDER BY ic.relname, n.position`, quoteIdentifierReadOnly("postgres", table)).Scan(&rows).Error; err != nil {
			return nil, err
		}
		for _, row := range rows {
			index := indexes[row.Name]
			index.Name, index.Unique, index.Valid = row.Name, row.IsUnique, row.Valid
			index.Predicate = row.Predicate
			index.Unsupported = index.Unsupported || row.Unsupported || !row.ColumnName.Valid
			index.Columns = append(index.Columns, row.ColumnName.String)
			indexes[row.Name] = index
		}
	default:
		return nil, fmt.Errorf("index definition inspection is unsupported for %s", db.Dialector.Name())
	}
	return indexes, nil
}

// Tokenize without changing string literals. PostgreSQL adds outer parentheses
// and quotes identifiers; neither changes a predicate's meaning.
func migrationSQLTokens(text string) []string {
	var tokens []string
	for i := 0; i < len(text); {
		if unicode.IsSpace(rune(text[i])) {
			i++
			continue
		}
		start := i
		switch text[i] {
		case '\'', '"', '`', '[':
			quote, end := text[i], text[i]
			if end == '[' {
				end = ']'
			}
			i++
			for i < len(text) {
				if text[i] == end {
					i++
					if i < len(text) && text[i] == end {
						i++
						continue
					}
					break
				}
				i++
			}
			token := text[start:i]
			if quote != '\'' && len(token) >= 2 {
				token = strings.ToLower(token[1 : len(token)-1])
			}
			tokens = append(tokens, token)
		case '(', ')', ',', ';', '=', '<', '>', '!':
			tokens = append(tokens, text[i:i+1])
			i++
		default:
			for i < len(text) && !strings.ContainsRune(" \t\r\n() ,;=<>!'\"`[", rune(text[i])) {
				i++
			}
			tokens = append(tokens, strings.ToLower(text[start:i]))
		}
	}
	return tokens
}

func migrationPredicate(text string) string {
	tokens := migrationSQLTokens(text)
	if len(tokens) > 0 && tokens[len(tokens)-1] == ";" {
		tokens = tokens[:len(tokens)-1]
	}
	for len(tokens) >= 2 && tokens[0] == "(" && tokens[len(tokens)-1] == ")" {
		depth, wraps := 0, true
		for i, token := range tokens {
			if token == "(" {
				depth++
			} else if token == ")" {
				depth--
			}
			if depth == 0 && i < len(tokens)-1 {
				wraps = false
				break
			}
		}
		if !wraps || depth != 0 {
			break
		}
		tokens = tokens[1 : len(tokens)-1]
	}
	return strings.Join(tokens, " ")
}

func migrationIndexMatches(actual, expected migrationIndex) bool {
	if !actual.Valid || actual.Unsupported || (expected.Unique && !actual.Unique) || len(actual.Columns) != len(expected.Columns) || migrationPredicate(actual.Predicate) != migrationPredicate(expected.Predicate) {
		return false
	}
	for i, column := range expected.Columns {
		if actual.Columns[i] != column {
			return false
		}
	}
	return true
}

func expectedMigrationIndex(db *gorm.DB, index schema.Index) (migrationIndex, error) {
	want := migrationIndex{Name: index.Name, Unique: strings.EqualFold(index.Class, "UNIQUE"), Predicate: index.Where}
	// The MySQL driver does not emit WHERE: that dialect has no partial indexes.
	if db.Dialector.Name() == "mysql" {
		want.Predicate = ""
	}
	for _, field := range index.Fields {
		if field.Expression != "" || field.Length != 0 || field.Collate != "" || field.Sort != "" {
			return want, fmt.Errorf("index %s requires an explicit migration for its expression, prefix, collation or sort", index.Name)
		}
		want.Columns = append(want.Columns, field.DBName)
	}
	return want, nil
}

func migrationHasDefault(field *schema.Field) bool {
	_, explicit := field.TagSettings["DEFAULT"]
	return explicit && field.HasDefaultValue && !strings.EqualFold(strings.TrimSpace(field.DefaultValue), "null") && field.DefaultValue != "(-)"
}

// Earlier upstream versions created this ordinary index as (id, created_at).
// AutoMigrate left existing indexes untouched when the priorities were corrected.
// Its order affects query performance, not data integrity. Preserve this exact
// historical definition: rebuilding a large log index during startup is unsafe.
func migrationLegacyLogIndexMatches(table string, actual, expected migrationIndex) bool {
	if table != "logs" || expected.Name != "idx_created_at_id" || actual.Name != expected.Name || expected.Unique || actual.Unique || migrationPredicate(expected.Predicate) != "" {
		return false
	}
	if len(expected.Columns) != 2 || expected.Columns[0] != "created_at" || expected.Columns[1] != "id" {
		return false
	}
	return migrationIndexMatches(actual, migrationIndex{Columns: []string{"id", "created_at"}})
}

func requireMigrationIndex(table string, expected migrationIndex, indexes map[string]migrationIndex) error {
	actual, exists := indexes[expected.Name]
	if !exists || (!migrationIndexMatches(actual, expected) && !migrationLegacyLogIndexMatches(table, actual, expected)) {
		return fmt.Errorf("index %s.%s has an unexpected definition (want unique=%t columns=%v predicate=%q; actual exists=%t unique=%t valid=%t unsupported=%t columns=%v predicate=%q); preserve it and reconcile explicitly", table, expected.Name, expected.Unique, expected.Columns, expected.Predicate, exists, actual.Unique, actual.Valid, actual.Unsupported, actual.Columns, actual.Predicate)
	}
	return nil
}

func ensureMigrationUnique(db *gorm.DB, table, column string) error {
	indexes, err := migrationIndexes(db, table)
	if err != nil {
		return err
	}
	digest := sha256.Sum256([]byte(table + "." + column))
	want := migrationIndex{Name: fmt.Sprintf("nexus_uq_%x", digest[:12]), Unique: true, Columns: []string{column}}
	for _, index := range indexes {
		if migrationIndexMatches(index, want) {
			return nil
		}
	}
	if _, exists := indexes[want.Name]; !exists {
		if err := db.Exec("CREATE UNIQUE INDEX ? ON ? (?)", clause.Column{Name: want.Name}, clause.Table{Name: table}, clause.Column{Name: column}).Error; err != nil {
			return fmt.Errorf("add unique index %s.%s: %w", table, column, err)
		}
		indexes, err = migrationIndexes(db, table)
		if err != nil {
			return err
		}
	}
	return requireMigrationIndex(table, want, indexes)
}

func migrationPrimaryColumns(db *gorm.DB, table string, columns map[string]gorm.ColumnType) (map[string]bool, error) {
	actual := make(map[string]bool)
	if db.Dialector.Name() == "sqlite" {
		// The driver's DDL parser misses named PRIMARY KEY constraints.
		var fields []struct{ Name string }
		if err := db.Raw(`SELECT name FROM pragma_table_info(?) WHERE pk > 0 ORDER BY pk`, table).Scan(&fields).Error; err != nil {
			return nil, err
		}
		for _, field := range fields {
			actual[field.Name] = true
		}
		return actual, nil
	}
	for name, column := range columns {
		primary, known := column.PrimaryKey()
		if !known {
			return nil, fmt.Errorf("cannot inspect primary key %s.%s", table, name)
		}
		if primary {
			actual[name] = true
		}
	}
	return actual, nil
}

func ensureMigrationPrimaryKey(db *gorm.DB, s *schema.Schema, columns map[string]gorm.ColumnType) error {
	actual, err := migrationPrimaryColumns(db, s.Table, columns)
	if err != nil {
		return err
	}
	matches := len(actual) == len(s.PrimaryFields)
	for _, field := range s.PrimaryFields {
		matches = matches && actual[field.DBName]
	}
	if matches {
		return nil
	}
	// Historical options tables can support the existing upsert contract with
	// an additive unique index. Never deduplicate or rebuild their rows.
	if s.Table != "options" || len(s.PrimaryFields) != 1 || s.PrimaryFields[0].DBName != "key" || len(actual) != 0 || columns["key"] == nil {
		return fmt.Errorf("table %s requires an explicit primary key migration", s.Table)
	}
	var nulls, duplicates int64
	key, table := clause.Column{Name: "key"}, clause.Table{Name: s.Table}
	if err := db.Raw("SELECT COUNT(*) FROM ? WHERE ? IS NULL", table, key).Scan(&nulls).Error; err != nil {
		return err
	}
	if err := db.Raw("SELECT COUNT(*) FROM (SELECT ? FROM ? GROUP BY ? HAVING COUNT(*) > 1) AS nexus_duplicate_keys", key, table, key).Scan(&duplicates).Error; err != nil {
		return err
	}
	if nulls != 0 || duplicates != 0 {
		return fmt.Errorf("options.key has NULL or duplicate historical values; preserve all rows and reconcile explicitly")
	}
	return ensureMigrationUnique(db, s.Table, "key")
}
