package controller

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupCheckinControllerTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	gin.SetMode(gin.TestMode)
	common.UsingSQLite = true
	common.UsingMySQL = false
	common.UsingPostgreSQL = false
	common.RedisEnabled = false

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	model.LOG_DB = db

	require.NoError(t, db.AutoMigrate(&model.Checkin{}, &model.Log{}))

	t.Cleanup(func() {
		sqlDB, err := db.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	})

	return db
}

func TestGetCheckinStatusDoesNotExposeStageRules(t *testing.T) {
	setupCheckinControllerTestDB(t)

	setting := operation_setting.GetCheckinSetting()
	oldSetting := *setting
	t.Cleanup(func() {
		*setting = oldSetting
	})

	*setting = operation_setting.CheckinSetting{
		Enabled:          true,
		MinQuota:         1000,
		MaxQuota:         10000,
		ConditionEnabled: true,
		StageRules: []operation_setting.CheckinStageRule{
			{
				RequestThreshold: 1,
				AllowCheckin:     true,
				MinQuota:         1000,
				MaxQuota:         2000,
			},
		},
	}

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/user/checkin?month=2026-06", nil)
	ctx.Set("id", 1)

	GetCheckinStatus(ctx)

	require.Equal(t, http.StatusOK, recorder.Code)
	var payload map[string]interface{}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &payload))
	require.Equal(t, true, payload["success"])

	data, ok := payload["data"].(map[string]interface{})
	require.True(t, ok)
	require.NotContains(t, data, "stage_rules")

	stats, ok := data["stats"].(map[string]interface{})
	require.True(t, ok)
	condition, ok := stats["condition"].(map[string]interface{})
	require.True(t, ok)
	require.NotContains(t, condition, "matched_stage")
	require.NotContains(t, condition, "stage_min_quota")
	require.NotContains(t, condition, "stage_max_quota")
}
