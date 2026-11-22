package model

import (
	"fmt"
	"sort"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// migrateAdditive 只创建新表、追加缺失列和索引。已有列的类型、默认值、
// 可空性和历史额外字段保持不变；需要转换的字段必须有独立的数据迁移。
func migrateAdditive(db *gorm.DB, values ...interface{}) error {
	if db == nil {
		return fmt.Errorf("additive migration requires a database")
	}
	for _, value := range values {
		stmt := &gorm.Statement{DB: db}
		if err := stmt.Parse(value); err != nil {
			return err
		}
		s := stmt.Schema
		m := db.Migrator()
		if !m.HasTable(value) {
			if err := m.CreateTable(value); err != nil {
				return fmt.Errorf("create table %s: %w", s.Table, err)
			}
			continue
		}
		types, err := m.ColumnTypes(value)
		if err != nil {
			return fmt.Errorf("inspect table %s: %w", s.Table, err)
		}
		columns := make(map[string]gorm.ColumnType, len(types))
		for _, column := range types {
			columns[column.Name()] = column
		}
		if err := ensureMigrationPrimaryKey(db, s, columns); err != nil {
			return err
		}
		indexes := s.ParseIndexes()
		actualIndexes, err := migrationIndexes(db, s.Table)
		if err != nil {
			return fmt.Errorf("inspect indexes %s: %w", s.Table, err)
		}
		names := make([]string, 0, len(indexes))
		for name, index := range indexes {
			names = append(names, name)
			want, err := expectedMigrationIndex(db, index)
			if err != nil {
				return err
			}
			if _, exists := actualIndexes[name]; exists {
				if err := requireMigrationIndex(s.Table, want, actualIndexes); err != nil {
					return err
				}
			}
		}
		sort.Strings(names)
		// 先检查所有新增必填字段，避免填入无依据的账户或账务数据。
		for _, field := range s.Fields {
			if field.DBName == "" || field.IgnoreMigration || columns[field.DBName] != nil {
				continue
			}
			if field.PrimaryKey {
				return fmt.Errorf("table %s requires an explicit primary key migration for %s", s.Table, field.DBName)
			}
			if field.NotNull && !migrationHasDefault(field) {
				var existing []int
				if err := db.Table(s.Table).Select("1").Limit(1).Scan(&existing).Error; err != nil {
					return err
				}
				if len(existing) != 0 {
					return fmt.Errorf("table %s requires an explicit backfill before adding required column %s", s.Table, field.DBName)
				}
			}
		}
		for _, field := range s.Fields {
			if field.DBName == "" || field.IgnoreMigration {
				continue
			}
			column := columns[field.DBName]
			if column == nil {
				// SQLite 不支持 ADD COLUMN UNIQUE；追加列后用独立唯一索引约束，
				// 三种关系库都使用相同路径，不触发表重建。
				definition := *field
				definition.Unique = false
				if err := db.Exec("ALTER TABLE ? ADD COLUMN ? ?", clause.Table{Name: s.Table}, clause.Column{Name: field.DBName}, m.FullDataTypeOf(&definition)).Error; err != nil {
					return fmt.Errorf("add column %s.%s: %w", s.Table, field.DBName, err)
				}
			}
			// ParseIndexes also sets Field.Unique for a one-column uniqueIndex.
			// Only an explicit unique tag requests an unconditional unique key.
			if field.Unique && field.TagSettings["UNIQUE"] != "" && !field.PrimaryKey {
				if err := ensureMigrationUnique(db, s.Table, field.DBName); err != nil {
					return err
				}
			}
		}
		for _, name := range names {
			if _, exists := actualIndexes[name]; !exists {
				if err := m.CreateIndex(value, name); err != nil {
					return fmt.Errorf("add index %s.%s: %w", s.Table, name, err)
				}
			}
		}
		actualIndexes, err = migrationIndexes(db, s.Table)
		if err != nil {
			return err
		}
		for _, name := range names {
			want, err := expectedMigrationIndex(db, indexes[name])
			if err != nil {
				return err
			}
			if err := requireMigrationIndex(s.Table, want, actualIndexes); err != nil {
				return err
			}
		}
	}
	return nil
}
