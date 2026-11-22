package model

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

const (
	AuditCategoryLogin       = "login"
	AuditCategorySecurity    = "security"
	AuditCategoryOperation   = "operation"
	AuditCategoryAccessToken = "access_token"
)

// AuditLogFilter targets Nexus's existing logs and metadata. No second audit
// database/table or backfill of historical token fingerprints is required.
type AuditLogFilter struct {
	UserID          int
	Username        string
	TokenRef        string
	ExcludeTokenRef string
	Category        string
	Success         *bool
	RequestID       string
	Start           int64
	End             int64
}

func ValidAuditTokenRef(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, char := range value {
		if !(char >= '0' && char <= '9') && !(char >= 'a' && char <= 'f') {
			return false
		}
	}
	return true
}

func ValidateAuditLogFilter(filter AuditLogFilter) error {
	for _, value := range []string{filter.TokenRef, filter.ExcludeTokenRef} {
		if value != "" && !ValidAuditTokenRef(value) {
			return errors.New("token_ref 必须为64位小写十六进制指纹")
		}
	}
	switch filter.Category {
	case "", AuditCategoryLogin, AuditCategorySecurity, AuditCategoryOperation, AuditCategoryAccessToken:
	default:
		return errors.New("无效的审计分类")
	}
	if filter.UserID < 0 || filter.Start < 0 || filter.End < 0 || (filter.End > 0 && filter.Start > filter.End) {
		return errors.New("无效的审计用户或时间范围")
	}
	return nil
}

// All paths below are internal constants. User input is only passed as query
// parameters. Consult the log connection, never the primary database flags.
func auditJSONText(dialect string, path ...string) (string, error) {
	jsonPath := "$." + strings.Join(path, ".")
	var expression string
	switch dialect {
	case "sqlite":
		expression = "CASE WHEN json_valid(logs.other) THEN CAST(json_extract(logs.other, '" + jsonPath + "') AS TEXT) END"
	case "mysql":
		expression = "JSON_UNQUOTE(JSON_EXTRACT(IF(JSON_VALID(logs.other), logs.other, '{}'), '" + jsonPath + "'))"
	case "postgres":
		// Nexus writers encode Other as JSON text. Blank legacy rows are
		// supported; corrupt nonblank JSON fails the query closed on PG.
		expression = "(COALESCE(NULLIF(BTRIM(logs.other), ''), '{}')::jsonb #>> '{" + strings.Join(path, ",") + "}')"
	case "clickhouse":
		args := "'" + strings.Join(path, "', '") + "'"
		expression = "trim(BOTH '\"' FROM JSONExtractRaw(if(isValidJSON(logs.other), logs.other, '{}'), " + args + "))"
	default:
		return "", errors.New("不支持的审计日志数据库方言")
	}
	return "NULLIF(NULLIF(" + expression + ", ''), 'null')", nil
}

func applyAuditLogFilters(tx *gorm.DB, filter AuditLogFilter, viewerRole int) (*gorm.DB, error) {
	if viewerRole < common.RoleAdminUser {
		return nil, errors.New("仅管理员可查询审计日志")
	}
	if err := ValidateAuditLogFilter(filter); err != nil {
		return nil, err
	}
	dialect := tx.Dialector.Name()
	action, err := auditJSONText(dialect, "op", "action")
	if err != nil {
		return nil, err
	}
	category, _ := auditJSONText(dialect, "audit_info", "category")
	tokenRef, _ := auditJSONText(dialect, "audit_info", "token_ref")
	legacyTokenRef, _ := auditJSONText(dialect, "admin_info", "token_ref")
	success, _ := auditJSONText(dialect, "audit_info", "success")
	legacySuccess, _ := auditJSONText(dialect, "op", "params", "success")
	actorRole, _ := auditJSONText(dialect, "admin_info", "admin_role")
	metadataRole, _ := auditJSONText(dialect, "audit_info", "actor_role")
	tx = tx.Where("logs.type IN ?", []int{LogTypeLogin, LogTypeManage})
	if viewerRole < common.RoleRootUser {
		// Historical records without an actor role cannot establish that the
		// event was below root privilege. Keep them visible to root only.
		role := "COALESCE(" + metadataRole + ", " + actorRole + ", '0')"
		tx = tx.Where(role+" IN ?", []string{strconv.Itoa(common.RoleCommonUser), strconv.Itoa(common.RoleAdminUser)})
	}
	ref := "COALESCE(" + tokenRef + ", " + legacyTokenRef + ", '')"
	if filter.TokenRef != "" {
		tx = tx.Where(ref+" = ?", filter.TokenRef)
	}
	if filter.ExcludeTokenRef != "" {
		tx = tx.Where(ref+" <> ?", filter.ExcludeTokenRef)
	}
	if filter.Category != "" {
		// Preserve known legacy security operations and successful login rows.
		categorySQL := "COALESCE(" + category + ", CASE WHEN logs.type = ? THEN ? WHEN " + action + " IN ? THEN ? WHEN SUBSTR(" + action + ", 1, 13) = ? THEN ? ELSE ? END) = ?"
		tx = tx.Where(categorySQL, LogTypeLogin, AuditCategoryLogin,
			[]string{"user.binding_clear", "user.binding_bind", "user.2fa_disable", "user.passkey_register", "user.passkey_delete", "user.reset_passkey", "user.oauth_unbind"}, AuditCategorySecurity,
			"access_token.", AuditCategoryAccessToken, AuditCategoryOperation, filter.Category)
	}
	if filter.Success != nil {
		// Missing results are unknown, never implicitly failed. Legacy login
		// records are success-only; manually recorded operations have actions.
		result := "COALESCE(" + success + ", " + legacySuccess + ", CASE WHEN logs.type = ? THEN ? WHEN " + action + " IS NOT NULL AND " + action + " <> ? THEN ? END)"
		values := []string{"false", "0"}
		if *filter.Success {
			values = []string{"true", "1"}
		}
		tx = tx.Where(result+" IN ?", LogTypeLogin, "true", "generic", "true", values)
	}
	if filter.UserID > 0 {
		tx = tx.Where("logs.user_id = ?", filter.UserID)
	}
	for column, value := range map[string]string{"logs.username": filter.Username, "logs.request_id": filter.RequestID} {
		if value != "" {
			tx = tx.Where(column+" = ?", value)
		}
	}
	if filter.Start > 0 {
		tx = tx.Where("logs.created_at >= ?", filter.Start)
	}
	if filter.End > 0 {
		tx = tx.Where("logs.created_at <= ?", filter.End)
	}
	return tx, nil
}

func GetAuditLogs(ctx context.Context, filter AuditLogFilter, start, limit, viewerRole int) ([]*Log, int64, error) {
	if LOG_DB == nil {
		return nil, 0, errors.New("日志数据库未初始化")
	}
	if start < 0 || limit < 1 || limit > 100 {
		return nil, 0, errors.New("无效的审计分页参数")
	}
	tx, err := applyAuditLogFilters(LOG_DB.WithContext(ctx).Model(&Log{}), filter, viewerRole)
	if err != nil {
		return nil, 0, err
	}
	var total int64
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	logs := make([]*Log, 0)
	if err := tx.Order("logs.id DESC").Offset(start).Limit(limit).Find(&logs).Error; err != nil {
		return nil, 0, err
	}
	stripNonConsumeLogClients(logs)
	if viewerRole == common.RoleRootUser {
		FormatRootLogs(logs)
	} else {
		FormatAdminLogs(logs)
	}
	return logs, total, nil
}
