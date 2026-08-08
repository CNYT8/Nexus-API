package controller

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupUserGroupRatioControllerTestDB(t *testing.T) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	oldDB := model.DB
	oldLogDB := model.LOG_DB
	oldUsingSQLite := common.UsingSQLite
	oldUsingMySQL := common.UsingMySQL
	oldUsingPostgreSQL := common.UsingPostgreSQL
	oldRedisEnabled := common.RedisEnabled
	oldGroupRatios := ratio_setting.GroupRatio2JSONString()

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	model.LOG_DB = db
	common.UsingSQLite = true
	common.UsingMySQL = false
	common.UsingPostgreSQL = false
	common.RedisEnabled = false
	require.NoError(t, db.AutoMigrate(
		&model.User{},
		&model.UserGroupRatioOverride{},
		&model.Log{},
	))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":0.8,"vip":1.2}`))

	t.Cleanup(func() {
		_ = ratio_setting.UpdateGroupRatioByJSONString(oldGroupRatios)
		model.DB = oldDB
		model.LOG_DB = oldLogDB
		common.UsingSQLite = oldUsingSQLite
		common.UsingMySQL = oldUsingMySQL
		common.UsingPostgreSQL = oldUsingPostgreSQL
		common.RedisEnabled = oldRedisEnabled
		sqlDB, err := db.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	})
}

func runUserGroupRatioControllerRequest(method string, target string, body string, role int, handler gin.HandlerFunc) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Params = gin.Params{{Key: "id", Value: "2"}}
	ctx.Request = httptest.NewRequest(method, target, bytes.NewBufferString(body))
	ctx.Request.Header.Set("Content-Type", "application/json")
	ctx.Set("id", 1)
	ctx.Set("role", role)
	ctx.Set("username", "operator")
	handler(ctx)
	return recorder
}

func decodeUserGroupRatioResponse(t *testing.T, recorder *httptest.ResponseRecorder) struct {
	Success bool                           `json:"success"`
	Data    []service.UserGroupRatioDetail `json:"data"`
} {
	t.Helper()
	response := struct {
		Success bool                           `json:"success"`
		Data    []service.UserGroupRatioDetail `json:"data"`
	}{}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	return response
}

func findUserGroupRatioDetail(t *testing.T, details []service.UserGroupRatioDetail, group string) service.UserGroupRatioDetail {
	t.Helper()
	for _, detail := range details {
		if detail.Group == group {
			return detail
		}
	}
	t.Fatalf("group %s not found", group)
	return service.UserGroupRatioDetail{}
}

func TestAdminCanSetAndRestoreUserGroupRatio(t *testing.T) {
	setupUserGroupRatioControllerTestDB(t)
	require.NoError(t, model.DB.Create(&model.User{Id: 1, Username: "admin", Role: common.RoleAdminUser, AffCode: "admin_ratio"}).Error)
	require.NoError(t, model.DB.Create(&model.User{Id: 2, Username: "target", Role: common.RoleCommonUser, Group: "default", AffCode: "target_ratio"}).Error)

	recorder := runUserGroupRatioControllerRequest(
		http.MethodPut,
		"/api/user/2/group-ratios",
		`{"group":"default","ratio":0.5}`,
		common.RoleAdminUser,
		UpdateUserGroupRatio,
	)
	response := decodeUserGroupRatioResponse(t, recorder)
	require.True(t, response.Success)
	detail := findUserGroupRatioDetail(t, response.Data, "default")
	assert.True(t, detail.HasCustomRatio)
	assert.Equal(t, 0.5, detail.EffectiveRatio)

	recorder = runUserGroupRatioControllerRequest(
		http.MethodDelete,
		"/api/user/2/group-ratios?group=default",
		"",
		common.RoleAdminUser,
		DeleteUserGroupRatio,
	)
	response = decodeUserGroupRatioResponse(t, recorder)
	require.True(t, response.Success)
	detail = findUserGroupRatioDetail(t, response.Data, "default")
	assert.False(t, detail.HasCustomRatio)
	assert.Equal(t, 0.8, detail.EffectiveRatio)
}

func TestSameLevelUserCannotChangeGroupRatio(t *testing.T) {
	setupUserGroupRatioControllerTestDB(t)
	require.NoError(t, model.DB.Create(&model.User{Id: 1, Username: "operator", Role: common.RoleCommonUser, AffCode: "operator_ratio"}).Error)
	require.NoError(t, model.DB.Create(&model.User{Id: 2, Username: "target", Role: common.RoleCommonUser, Group: "default", AffCode: "target_ratio"}).Error)

	recorder := runUserGroupRatioControllerRequest(
		http.MethodPut,
		"/api/user/2/group-ratios",
		`{"group":"default","ratio":0.5}`,
		common.RoleCommonUser,
		UpdateUserGroupRatio,
	)
	response := decodeUserGroupRatioResponse(t, recorder)
	assert.False(t, response.Success)

	var count int64
	require.NoError(t, model.DB.Model(&model.UserGroupRatioOverride{}).Count(&count).Error)
	assert.Zero(t, count)
}
