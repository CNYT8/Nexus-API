package model

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/clickhouse"
	"gorm.io/gorm"
)

func TestAuditLogFiltersLegacySemanticsAndRoleBoundary(t *testing.T) {
	db := newLogPolicyTestDB(t, &Log{})
	originalLogDB := LOG_DB
	LOG_DB = db
	t.Cleanup(func() { LOG_DB = originalLogDB })
	refA, refB := strings.Repeat("a", 64), strings.Repeat("b", 64)
	entries := []Log{
		{Type: LogTypeLogin, Content: "login", Other: `{"op":{"action":"login.password"},"audit_info":{"actor_role":1}}`},
		{Type: LogTypeManage, Content: "failed", Other: fmt.Sprintf(`{"op":{"action":"user.binding_bind","params":{"success":true}},"audit_info":{"category":"security","success":false,"token_ref":"%s","actor_role":1}}`, refA)},
		{Type: LogTypeManage, Content: "operation", Other: fmt.Sprintf(`{"op":{"action":"channel.update"},"audit_info":{"success":true},"admin_info":{"admin_role":10,"token_ref":"%s"},"root_info":{"secret":"hidden"}}`, refB)},
		{Type: LogTypeManage, Content: "unknown", Other: `{"op":{"action":"generic"}}`},
		{Type: LogTypeManage, Content: "root", Other: fmt.Sprintf(`{"audit_info":{"category":"operation","success":false,"actor_role":100,"token_ref":"%s"}}`, refA)},
		{Type: LogTypeManage, Content: "legacy-failed", Other: `{"op":{"action":"user.binding_bind","params":{"success":false}},"admin_info":{"admin_role":10}}`},
		{Type: LogTypeConsume, Content: "consume", Other: fmt.Sprintf(`{"audit_info":{"category":"security","success":false,"token_ref":"%s"}}`, refA)},
		{Type: LogTypeManage, Content: "token-operation", Other: `{"op":{"action":"access_token.delete"}}`},
		{Type: LogTypeManage, Content: "lookalike-operation", Other: `{"op":{"action":"accessXtoken.delete"}}`},
		{Type: LogTypeManage, Content: "malformed", Other: `{broken`},
	}
	for index := range entries {
		entries[index].UserId = 27
		entries[index].Username = "audit-owner"
		entries[index].CreatedAt = 2000000000
		entries[index].RequestId = "audit-request"
	}
	require.NoError(t, db.Create(&entries).Error)
	trueValue, falseValue := true, false
	for _, tc := range []struct {
		name   string
		filter AuditLogFilter
		role   int
		want   []string
	}{
		{"reference", AuditLogFilter{TokenRef: refA}, common.RoleAdminUser, []string{"failed"}},
		{"combined", AuditLogFilter{TokenRef: refA, Category: AuditCategorySecurity, Success: &falseValue, UserID: 27, Username: "audit-owner", RequestID: "audit-request", Start: 2000000000, End: 2000000000}, common.RoleAdminUser, []string{"failed"}},
		{"reference-excluded", AuditLogFilter{TokenRef: refA, ExcludeTokenRef: refA}, common.RoleRootUser, nil},
		{"exclude-retains-unmarked", AuditLogFilter{ExcludeTokenRef: refA, Success: &falseValue}, common.RoleRootUser, []string{"legacy-failed"}},
		{"false-is-not-unknown", AuditLogFilter{Success: &falseValue}, common.RoleAdminUser, []string{"legacy-failed", "failed"}},
		{"legacy-login", AuditLogFilter{Category: AuditCategoryLogin, Success: &trueValue}, common.RoleAdminUser, []string{"login"}},
		{"legacy-security", AuditLogFilter{Category: AuditCategorySecurity}, common.RoleAdminUser, []string{"legacy-failed", "failed"}},
		{"token-prefix-literal", AuditLogFilter{Category: AuditCategoryAccessToken}, common.RoleRootUser, []string{"token-operation"}},
		{"root-reference", AuditLogFilter{TokenRef: refA, Success: &falseValue}, common.RoleRootUser, []string{"root", "failed"}},
		{"legacy-reference", AuditLogFilter{TokenRef: refB}, common.RoleAdminUser, []string{"operation"}},
		{"admin-hides-unknown-role", AuditLogFilter{}, common.RoleAdminUser, []string{"legacy-failed", "operation", "failed", "login"}},
		{"root-retains-unknown-role", AuditLogFilter{Category: AuditCategoryOperation, Success: &trueValue}, common.RoleRootUser, []string{"lookalike-operation", "operation"}},
		{"bound-username", AuditLogFilter{Username: "' OR 1=1 --"}, common.RoleRootUser, nil},
		{"end-bound", AuditLogFilter{End: 1999999999}, common.RoleRootUser, nil},
		{"owner-bound", AuditLogFilter{UserID: 28}, common.RoleRootUser, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			logs, total, err := GetAuditLogs(context.Background(), tc.filter, 0, 100, tc.role)
			require.NoError(t, err)
			require.EqualValues(t, len(tc.want), total)
			var got []string
			for _, entry := range logs {
				got = append(got, entry.Content)
				if tc.role == common.RoleAdminUser {
					require.NotContains(t, entry.Other, "hidden")
				}
			}
			require.Equal(t, tc.want, got)
		})
	}
	logs, total, err := GetAuditLogs(context.Background(), AuditLogFilter{Success: &falseValue}, 1, 1, common.RoleAdminUser)
	require.NoError(t, err)
	require.EqualValues(t, 2, total)
	require.Len(t, logs, 1)
	require.Equal(t, "failed", logs[0].Content)
	_, _, err = GetAuditLogs(context.Background(), AuditLogFilter{}, 0, 100, common.RoleCommonUser)
	require.Error(t, err)
	_, _, err = GetAuditLogs(context.Background(), AuditLogFilter{}, -1, 100, common.RoleRootUser)
	require.Error(t, err)
	_, _, err = GetAuditLogs(context.Background(), AuditLogFilter{}, 0, 101, common.RoleRootUser)
	require.Error(t, err)
	var stored Log
	require.NoError(t, db.First(&stored, entries[2].Id).Error)
	require.Equal(t, entries[2].Other, stored.Other, "query projections must not rewrite audit evidence")
}

func TestAuditFilterValidation(t *testing.T) {
	for _, ref := range []string{"a", strings.Repeat("A", 64), strings.Repeat("g", 64), strings.Repeat("a", 63), strings.Repeat("a", 65), "' OR 1=1 --"} {
		require.False(t, ValidAuditTokenRef(ref))
		require.Error(t, ValidateAuditLogFilter(AuditLogFilter{TokenRef: ref}))
		require.Error(t, ValidateAuditLogFilter(AuditLogFilter{ExcludeTokenRef: ref}))
	}
	require.True(t, ValidAuditTokenRef(strings.Repeat("0123456789abcdef", 4)))
	for _, filter := range []AuditLogFilter{{Category: "unknown"}, {UserID: -1}, {Start: -1}, {End: -1}, {Start: 2, End: 1}} {
		require.Error(t, ValidateAuditLogFilter(filter))
	}
}

func TestAuditSQLUsesLogDialectAndBoundValues(t *testing.T) {
	ref := strings.Repeat("a", 64)
	falseValue := false
	for _, dialect := range []string{"sqlite", "mysql", "postgres", "clickhouse"} {
		t.Run(dialect, func(t *testing.T) {
			var db *gorm.DB
			switch dialect {
			case "sqlite":
				db = newLogPolicyTestDB(t, &Log{}).Session(&gorm.Session{DryRun: true})
			case "clickhouse":
				// Reuse an inert SQLite connection only to build SQL; no server is started.
				connection, err := newLogPolicyTestDB(t, &Log{}).DB()
				require.NoError(t, err)
				db, err = gorm.Open(clickhouse.New(clickhouse.Config{Conn: connection, SkipInitializeWithVersion: true}), &gorm.Config{DryRun: true, DisableAutomaticPing: true, SkipDefaultTransaction: true})
				require.NoError(t, err)
			default:
				db = newRebateSnapshotDryRunDB(t, dialect)
			}
			// A mismatched primary-database flag must not select JSON syntax.
			originalPostgres := common.UsingPostgreSQL
			common.UsingPostgreSQL = dialect != "postgres"
			t.Cleanup(func() { common.UsingPostgreSQL = originalPostgres })
			filter := AuditLogFilter{TokenRef: ref, ExcludeTokenRef: strings.Repeat("b", 64), Category: AuditCategorySecurity, Success: &falseValue, Username: "' OR 1=1 --", RequestID: "bound-request", Start: 1, End: 2}
			query, err := applyAuditLogFilters(db.Model(&Log{}), filter, common.RoleAdminUser)
			require.NoError(t, err)
			var logs []*Log
			statement := query.Order("logs.id DESC").Limit(10).Find(&logs).Statement
			require.NoError(t, statement.DB.Error)
			sql := statement.SQL.String()
			require.NotContains(t, sql, ref)
			require.NotContains(t, sql, filter.Username)
			require.Contains(t, statement.Vars, ref)
			require.Contains(t, statement.Vars, filter.Username)
			require.Contains(t, statement.Vars, "bound-request")
			require.NotContains(t, sql, "JOIN")
			switch dialect {
			case "sqlite":
				require.Contains(t, sql, "json_valid(")
			case "mysql":
				require.Contains(t, sql, "JSON_UNQUOTE(")
			case "postgres":
				require.Contains(t, sql, "::jsonb #>>")
			case "clickhouse":
				require.Contains(t, sql, "JSONExtractRaw(")
			}
		})
	}
}
