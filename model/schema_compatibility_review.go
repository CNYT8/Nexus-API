package model

import (
	"encoding/json"
	"fmt"
	"strings"

	"gorm.io/gorm"
)

// PreservedTaskPluginRevision is a READ-ONLY upstream representation, not a
// runnable Nexus TaskPlugin/ScriptPlugin. Exporting it never enables execution.
// Source is available to an explicit review caller, never to integrity logs.
type PreservedTaskPluginRevision struct {
	ID         int64
	Key        string
	Version    string
	APIVersion int
	Source     string
	SourceHash string
	Enabled    bool
	Active     bool
	CreatedAt  int64
}

// ReadPreservedTaskPluginForReview provides an explicit offline/admin porting
// path. Keep the original revision, verify SourceHash/API version, port the
// source through the normal ScriptPlugin validation, then explicitly bind the
// reviewed new revision. Do not map upstream IDs to declarative billing IDs.
func ReadPreservedTaskPluginForReview(db *gorm.DB, id int64) (*PreservedTaskPluginRevision, error) {
	var receipt SchemaCompatibilityMigration
	if err := db.Where("source = ? AND layout = ? AND completed_at > 0", "task_plugins", compatibilityUpstream).First(&receipt).Error; err != nil {
		return nil, err
	}
	var revision PreservedTaskPluginRevision
	quotedKey := quoteIdentifierReadOnly(db.Dialector.Name(), "key")
	err := db.Table("task_plugins").Select("id, "+quotedKey+", version, api_version, source, source_hash, enabled, active, created_at").Where("id = ?", id).Take(&revision).Error
	return &revision, err
}

func inspectCompatibilityArchives(db *gorm.DB, label string, actual map[string]map[string]string, expected map[string]struct{}, report *DatabaseIntegrityReport, addInfo, addIssue, addWarning func(string)) {
	if !db.Migrator().HasTable(&SchemaCompatibilityMigration{}) {
		return // The registry's ordinary missing-table error is sufficient.
	}
	for _, spec := range compatibilitySpecs() {
		var receipt SchemaCompatibilityMigration
		if err := db.Where("source = ?", spec.Source).First(&receipt).Error; err != nil {
			addIssue(fmt.Sprintf("%s: 兼容迁移 %s 缺少完成凭据: %v", label, spec.Source, err))
			continue
		}
		layout, err := classifyCompatibilityTable(db, spec)
		if err == nil {
			err = validateCompatibilityReceipt(db, spec, receipt, layout)
		}
		if err != nil {
			addIssue(fmt.Sprintf("%s: 兼容归档 %s 与迁移凭据不符: %v", label, spec.Source, err))
			continue
		}
		if _, exists := actual[spec.Source]; !exists {
			continue
		}
		expected[spec.Source] = struct{}{}
		report.TotalTables++
		rows, err := db.Raw("SELECT 1 FROM " + quoteIdentifierReadOnly(db.Dialector.Name(), spec.Source) + " LIMIT 1").Rows()
		if err != nil {
			addIssue(fmt.Sprintf("%s: 兼容归档 %s 无法读取: %v", label, spec.Source, err))
			continue
		}
		_ = rows.Close()
		report.CheckedTables++
		addInfo(fmt.Sprintf("%s: 已登记兼容归档 `%s` → `%s`，布局=%s，处理=%s，首次继承=%d 行；不会重放归档覆盖新数据", label, spec.Source, receipt.Destination, receipt.Layout, receipt.Disposition, receipt.CopiedRows))
		if layout != compatibilityUpstream {
			continue
		}
		switch spec.Source {
		case "task_plugins":
			inspectPreservedTaskPlugins(db, label, addIssue, addWarning)
		case "system_tasks":
			addWarning(label + ": 上游 system_tasks 的 type/payload/state/result 与锁信息完整保留；不自动执行 pending/running 任务，请审阅旧任务后通过 Nexus 维护接口重新发起，避免重复清理")
		default:
			addWarning(label + ": 上游 " + spec.Source + " 已保留但凭据不等价，旧会话需重新登录、认证流程需重启、PAT 需重新签发；不会降级为旧 Cookie/明文令牌认证")
		}
	}
}

func inspectPreservedTaskPlugins(db *gorm.DB, label string, addIssue, addWarning func(string)) {
	var plugins []PreservedTaskPluginRevision
	quotedKey := quoteIdentifierReadOnly(db.Dialector.Name(), "key")
	if err := db.Table("task_plugins").Select("id, " + quotedKey + ", version, api_version, source_hash, enabled, active, created_at").Order("id").Limit(101).Find(&plugins).Error; err != nil {
		addIssue(fmt.Sprintf("%s: 上游插件归档目录读取失败: %v", label, err))
		return
	}
	addWarning(label + ": 上游可执行插件 task_plugins 已保留并登记，不会转换为声明式计费策略；原 channels.setting.task_plugin_key/task_extend_plugin_keys 及 tasks.private_data 快照不修改。审阅路径：ReadPreservedTaskPluginForReview 按原 ID 读取 source/source_hash/API 版本，移植到 ScriptPlugin 并通过现有校验后显式重新绑定渠道；导入前不自动启用。当前尚未自动转换执行语义。")
	for i, plugin := range plugins {
		if i == 100 {
			addWarning(label + ": 上游插件超过 100 个，完整目录可只读查询 task_plugins；源代码未写入检查日志")
			break
		}
		addWarning(fmt.Sprintf("%s: 待审阅上游插件 id=%d key=%s version=%s api_version=%d source_hash=%s enabled=%t active=%t", label,
			plugin.ID, plugin.Key, plugin.Version, plugin.APIVersion, plugin.SourceHash, plugin.Enabled, plugin.Active))
	}
	if !db.Migrator().HasTable(&Channel{}) || !db.Migrator().HasColumn(&Channel{}, "setting") {
		return
	}
	// Scan in bounded batches without relying on dialect-specific JSON syntax.
	var after int
	for {
		var channels []struct {
			ID      int
			Setting string
		}
		if err := db.Table("channels").Select("id, setting").Where("id > ? AND setting IS NOT NULL", after).Order("id").Limit(256).Find(&channels).Error; err != nil {
			addIssue(fmt.Sprintf("%s: 读取上游插件渠道绑定失败: %v", label, err))
			return
		}
		if len(channels) == 0 {
			return
		}
		for _, channel := range channels {
			after = channel.ID
			var setting struct {
				Key  string   `json:"task_plugin_key"`
				Keys []string `json:"task_extend_plugin_keys"`
			}
			if json.Unmarshal([]byte(channel.Setting), &setting) != nil {
				continue // Do not expose unrelated channel settings or credentials.
			}
			if setting.Key != "" {
				setting.Keys = append(setting.Keys, setting.Key)
			}
			if len(setting.Keys) != 0 {
				addWarning(fmt.Sprintf("%s: 保留上游插件绑定 channel_id=%d plugin_keys=[%s]；尚未映射为 Nexus 可执行插件绑定", label, channel.ID, strings.Join(setting.Keys, ", ")))
			}
		}
	}
}
