package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestLogEndpointsEnforceMetadataVisibility(t *testing.T) {
	setupOAuthLegacyBindingControllerTestDB(t)
	require.NoError(t, model.DB.AutoMigrate(&model.Channel{}))
	oldCache := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = false
	t.Cleanup(func() { common.MemoryCacheEnabled = oldCache })
	owner := &model.User{Username: "log-owner", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	admin := &model.User{Username: "log-admin", Role: common.RoleAdminUser, Status: common.UserStatusEnabled}
	restricted := &model.User{Username: "log-restricted", Role: common.RoleAdminUser, Status: common.UserStatusEnabled}
	for _, user := range []*model.User{owner, admin, restricted} {
		user.AffCode = user.Username
		require.NoError(t, model.DB.Create(user).Error)
	}
	config := model.DefaultAdminPermissionConfig()
	config[model.AdminPermissionChannel] = false
	require.NoError(t, model.SetAdminPermissionConfig(restricted, config))
	channel := &model.Channel{Name: "private-channel", Key: "secret", Status: common.ChannelStatusEnabled}
	require.NoError(t, model.DB.Create(channel).Error)
	const raw = `{"number":9007199254740993,"channel_id":99,"reject_reason":"private-policy","root_info":{"secret":"root"},"admin_info":{"use_channel":[99]},"audit_info":{"private":true}}`
	log := &model.Log{UserId: owner.Id, TokenId: 42, ChannelId: channel.Id, Type: model.LogTypeConsume, Other: raw}
	require.NoError(t, model.LOG_DB.Create(log).Error)
	for _, tc := range []struct {
		name       string
		id         int
		role       int
		handler    gin.HandlerFunc
		privileged bool
		root       bool
		channel    bool
	}{
		{"self", owner.Id, common.RoleCommonUser, GetUserLogs, false, false, false},
		{"token", owner.Id, common.RoleCommonUser, GetLogByKey, false, false, false},
		{"admin-self", admin.Id, common.RoleAdminUser, GetUserLogs, false, false, false},
		{"admin", admin.Id, common.RoleAdminUser, GetAllLogs, true, false, true},
		{"restricted", restricted.Id, common.RoleAdminUser, GetAllLogs, true, false, false},
		{"root", admin.Id, common.RoleRootUser, GetAllLogs, true, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := gin.New()
			r.GET("/logs", func(c *gin.Context) {
				c.Set("id", tc.id)
				if tc.name == "admin-self" {
					c.Set("id", owner.Id)
				}
				c.Set("role", tc.role)
				c.Set("token_id", 42)
				tc.handler(c)
			})
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/logs", nil))
			require.Equal(t, http.StatusOK, rec.Code)
			var response struct {
				Success bool            `json:"success"`
				Data    json.RawMessage `json:"data"`
			}
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
			require.True(t, response.Success, rec.Body.String())
			var logs []model.Log
			if tc.name == "token" {
				require.NoError(t, json.Unmarshal(response.Data, &logs))
			} else {
				var page struct {
					Items []model.Log `json:"items"`
				}
				require.NoError(t, json.Unmarshal(response.Data, &page))
				logs = page.Items
			}
			require.Len(t, logs, 1)
			var other map[string]json.RawMessage
			require.NoError(t, json.Unmarshal([]byte(logs[0].Other), &other))
			require.Equal(t, "9007199254740993", string(other["number"]))
			_, hasRoot := other["root_info"]
			_, hasAdmin := other["admin_info"]
			require.Equal(t, tc.root, hasRoot)
			require.Equal(t, tc.privileged, hasAdmin)
			require.NotContains(t, other, "reject_reason")
			if !tc.channel {
				require.Zero(t, logs[0].ChannelId)
				require.Empty(t, logs[0].ChannelName)
				require.NotContains(t, other, "channel_id")
			}
		})
	}
	var stored model.Log
	require.NoError(t, model.LOG_DB.First(&stored, log.Id).Error)
	require.Equal(t, raw, stored.Other, "API projection must not rewrite the database")
}
