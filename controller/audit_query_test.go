package controller

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAuditQueryGuardValidationAndCombinedFilters(t *testing.T) {
	setupOAuthLegacyBindingControllerTestDB(t)
	setControllerLogPolicy(t, 365, true)
	for _, identity := range []struct{ id, role int }{{0, common.RoleRootUser}, {27, common.RoleCommonUser}} {
		require.Equal(t, http.StatusForbidden, invokeLogPolicyHandler(GetAuditLogs, identity.id, identity.role, "").Code)
	}
	for _, query := range []string{
		"token_ref=short", "exclude_token_ref=" + strings.Repeat("A", 64), "category=unknown",
		"success=", "success=1", "success=False", "success=false&success=true",
		"category=login&category=operation", "user_id=", "user_id=-1", "user_id=1.5",
		"start_timestamp=", "start_timestamp=2&end_timestamp=1", "p=-1", "page_size=-1",
		"p=9223372036854775807&page_size=100", "start_timestamp=1&end_timestamp=31536002",
	} {
		response := invokeLogPolicyHandler(GetAuditLogs, 27, common.RoleRootUser, query)
		require.Equal(t, http.StatusBadRequest, response.Code, query)
	}
	ref := strings.Repeat("a", 64)
	rows := []model.Log{
		{UserId: 27, Username: "audit-user", Type: model.LogTypeManage, CreatedAt: time.Now().Unix() - 1, RequestId: "audit-query", Other: fmt.Sprintf(`{"audit_info":{"category":"security","success":false,"token_ref":"%s","actor_role":1},"root_info":{"secret":"root-only"}}`, ref)},
		{UserId: 27, Type: model.LogTypeManage, CreatedAt: time.Now().Unix() - 1, Other: fmt.Sprintf(`{"audit_info":{"category":"security","success":true,"token_ref":"%s","actor_role":1}}`, ref)},
	}
	require.NoError(t, model.LOG_DB.Create(&rows).Error)
	query := "user_id=27&username=audit-user&request_id=audit-query&category=security&success=false&token_ref=" + ref
	response := invokeLogPolicyHandler(GetAuditLogs, 99, common.RoleRootUser, query)
	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, "no-store", response.Header().Get("Cache-Control"))
	var result struct {
		Success bool `json:"success"`
		Data    struct {
			Total int         `json:"total"`
			Items []model.Log `json:"items"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &result))
	require.True(t, result.Success)
	require.Equal(t, 1, result.Data.Total)
	require.Len(t, result.Data.Items, 1)
	require.Equal(t, rows[0].Id, result.Data.Items[0].Id)
	require.Contains(t, result.Data.Items[0].Other, "root-only")
	response = invokeLogPolicyHandler(GetAuditLogs, 99, common.RoleRootUser, query+"&exclude_token_ref="+ref)
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &result))
	require.True(t, result.Success)
	require.Zero(t, result.Data.Total)
	require.Empty(t, result.Data.Items)
}

func TestAuditResultMetadataUsesExplicitOutcomeAndFingerprintOnly(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set("role", common.RoleAdminUser)
	c.Set("token_ref", "raw-bearer-is-not-a-fingerprint")
	info := auditResultInfo(c, model.AuditCategorySecurity, map[string]interface{}{"success": false})
	require.Equal(t, false, info["success"])
	require.Equal(t, common.RoleAdminUser, info["actor_role"])
	require.Equal(t, model.AuditCategorySecurity, info["category"])
	require.NotContains(t, info, "token_ref")
	ref := strings.Repeat("b", 64)
	c.Set("token_ref", ref)
	c.Writer.WriteHeader(http.StatusBadRequest)
	info = auditResultInfo(c, "", nil)
	require.Equal(t, false, info["success"])
	require.Equal(t, ref, info["token_ref"])
	require.NotContains(t, info, "category")
	info = auditResultInfo(c, "", map[string]interface{}{"success": true})
	require.Equal(t, true, info["success"])
}
