package relay

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func responsesWSSelectionTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	originalDB, originalCache := model.DB, common.MemoryCacheEnabled
	dsn := fmt.Sprintf("file:ws-selection-%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	model.DB = db
	t.Cleanup(func() {
		model.DB, common.MemoryCacheEnabled = originalDB, originalCache
		_ = sqlDB.Close()
	})
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.Ability{}))
	return db
}

func responsesWSSelectionChannel(t *testing.T, db *gorm.DB, channelType int, group, modelName string, priority int64, enabled bool) *model.Channel {
	t.Helper()
	settingJSON := fmt.Sprintf(`{"responses_websocket_enabled":%t}`, enabled)
	channel := &model.Channel{Type: channelType, Status: common.ChannelStatusEnabled, Key: "upstream", Group: group, Models: modelName, Priority: &priority, Setting: &settingJSON}
	require.NoError(t, db.Create(channel).Error)
	require.NoError(t, db.Create(&model.Ability{Group: group, Model: modelName, ChannelId: channel.Id, Enabled: true, Priority: &priority}).Error)
	return channel
}

func responsesWSSelect(t *testing.T, group, modelName string, retry int, tokenGroups []string) (*model.Channel, string) {
	t.Helper()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set(ResponsesWebSocketContextKey, true)
	common.SetContextKey(c, constant.ContextKeyUserGroup, "ws-test-user")
	c.Set("token_auto_groups", tokenGroups)
	selected, selectedGroup, err := service.CacheGetRandomSatisfiedChannel(&service.RetryParam{Ctx: c, TokenGroup: group, ModelName: modelName, RequestPath: "/v1/responses", Retry: &retry})
	require.NoError(t, err)
	return selected, selectedGroup
}

func TestResponsesWSSelectionFiltersBeforePriority(t *testing.T) {
	db := responsesWSSelectionTestDB(t)
	responsesWSSelectionChannel(t, db, constant.ChannelTypeOpenAI, "ws", "test", 100, false)
	responsesWSSelectionChannel(t, db, constant.ChannelTypeAnthropic, "ws", "test", 90, true)
	unusable := responsesWSSelectionChannel(t, db, constant.ChannelTypeOpenAI, "ws", "test", 80, true)
	unusable.ChannelInfo = model.ChannelInfo{IsMultiKey: true, MultiKeySize: 1, MultiKeyStatusList: map[int]int{0: common.ChannelStatusAutoDisabled}}
	require.NoError(t, db.Model(unusable).Update("channel_info", unusable.ChannelInfo).Error)
	converted := responsesWSSelectionChannel(t, db, constant.ChannelTypeAdvancedCustom, "ws", "test", 70, true)
	require.NoError(t, db.Model(converted).Update("settings", `{"advanced_custom":{"advanced_routes":[{"incoming_path":"/v1/responses","upstream_path":"/responses","converter":"responses"}]}}`).Error)
	wanted := responsesWSSelectionChannel(t, db, constant.ChannelTypeOpenAI, "ws", "test", 5, true)
	fallback := responsesWSSelectionChannel(t, db, constant.ChannelTypeAzure, "ws", "test", 2, true)
	for _, cache := range []bool{false, true} {
		common.MemoryCacheEnabled = cache
		for _, test := range []struct {
			retry int
			id    int
		}{{0, wanted.Id}, {1, fallback.Id}, {8, fallback.Id}} {
			selected, group := responsesWSSelect(t, "ws", "test", test.retry, nil)
			require.NotNil(t, selected)
			require.Equal(t, test.id, selected.Id)
			require.Equal(t, "ws", group)
		}
	}
	// A setting change takes effect even when the ordinary channel cache is on.
	require.NoError(t, db.Model(wanted).Update("setting", `{"responses_websocket_enabled":false}`).Error)
	selected, _ := responsesWSSelect(t, "ws", "test", 0, nil)
	require.NotNil(t, selected)
	require.Equal(t, fallback.Id, selected.Id)
}

func TestResponsesWSSelectionModelNormalizationAndAbilityScope(t *testing.T) {
	db := responsesWSSelectionTestDB(t)
	common.MemoryCacheEnabled = true
	responsesWSSelectionChannel(t, db, constant.ChannelTypeOpenAI, "ws", "gpt-4o-gizmo-custom", 100, false)
	wanted := responsesWSSelectionChannel(t, db, constant.ChannelTypeOpenAI, "ws", "gpt-4o-gizmo-*", 1, true)
	responsesWSSelectionChannel(t, db, constant.ChannelTypeOpenAI, "other", "gpt-4o-gizmo-custom", 200, true)
	selected, _ := responsesWSSelect(t, "ws", "gpt-4o-gizmo-custom", 0, nil)
	require.NotNil(t, selected)
	require.Equal(t, wanted.Id, selected.Id)
	require.NoError(t, db.Model(&model.Ability{}).Where("channel_id = ?", wanted.Id).Update("enabled", false).Error)
	selected, _ = responsesWSSelect(t, "ws", "gpt-4o-gizmo-custom", 0, nil)
	require.Nil(t, selected)
}

func TestResponsesWSSelectionPreservesTokenAutoGroups(t *testing.T) {
	db := responsesWSSelectionTestDB(t)
	originalAuto := setting.AutoGroups2JsonString()
	originalUsable := setting.UserUsableGroups2JSONString()
	t.Cleanup(func() {
		_ = setting.UpdateAutoGroupsByJsonString(originalAuto)
		_ = setting.UpdateUserUsableGroupsByJSONString(originalUsable)
	})
	require.NoError(t, setting.UpdateAutoGroupsByJsonString(`["http-only","first","second"]`))
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"http-only":"HTTP","first":"First","second":"Second"}`))
	responsesWSSelectionChannel(t, db, constant.ChannelTypeOpenAI, "http-only", "test", 100, false)
	responsesWSSelectionChannel(t, db, constant.ChannelTypeOpenAI, "first", "test", 1, true)
	wanted := responsesWSSelectionChannel(t, db, constant.ChannelTypeOpenAI, "second", "test", 1, true)
	for _, cache := range []bool{false, true} {
		common.MemoryCacheEnabled = cache
		selected, group := responsesWSSelect(t, "auto", "test", 0, []string{"http-only", "second", "first"})
		require.NotNil(t, selected)
		require.Equal(t, wanted.Id, selected.Id)
		require.Equal(t, "second", group)
		selected, _ = responsesWSSelect(t, "auto", "test", 0, []string{"http-only"})
		require.Nil(t, selected)
	}
}
