package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func setupTurnstileOptionTest(t *testing.T) {
	t.Helper()
	setupOAuthLegacyBindingControllerTestDB(t)
	require.NoError(t, model.DB.AutoMigrate(&model.Option{}))
	oldOptions := common.OptionMap
	oldEnabled, oldSite, oldSecret := common.TurnstileCheckEnabled, common.TurnstileSiteKey, common.TurnstileSecretKey
	common.OptionMap = map[string]string{}
	common.TurnstileCheckEnabled = false
	common.TurnstileSiteKey = ""
	common.TurnstileSecretKey = ""
	t.Cleanup(func() {
		common.OptionMap = oldOptions
		common.TurnstileCheckEnabled, common.TurnstileSiteKey, common.TurnstileSecretKey = oldEnabled, oldSite, oldSecret
	})
}

func updateTurnstileOption(t *testing.T, key string, value any) bool {
	t.Helper()
	body, err := json.Marshal(OptionUpdateRequest{Key: key, Value: value})
	require.NoError(t, err)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPut, "/api/option/", strings.NewReader(string(body)))
	c.Request.Header.Set("Content-Type", "application/json")
	UpdateOption(c)
	var result struct {
		Success bool `json:"success"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &result))
	return result.Success
}

func TestTurnstileEnableRequiresBothKeys(t *testing.T) {
	setupTurnstileOptionTest(t)
	for _, tc := range []struct {
		name, site, secret string
	}{
		{name: "两项缺失"},
		{name: "缺少服务端密钥", site: "site"},
		{name: "缺少站点密钥", secret: "secret"},
		{name: "空白服务端密钥", site: "site", secret: " \t "},
		{name: "空白站点密钥", site: " \t ", secret: "secret"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			common.TurnstileSiteKey, common.TurnstileSecretKey = tc.site, tc.secret
			require.False(t, updateTurnstileOption(t, "TurnstileCheckEnabled", true))
			require.False(t, common.TurnstileCheckEnabled)
		})
	}

	require.True(t, updateTurnstileOption(t, "TurnstileSiteKey", " site "))
	require.True(t, updateTurnstileOption(t, "TurnstileSecretKey", " secret "))
	require.True(t, updateTurnstileOption(t, "TurnstileCheckEnabled", true))
	require.True(t, common.TurnstileCheckEnabled)
	require.Equal(t, "site", common.TurnstileSiteKey)
	require.Equal(t, "secret", common.TurnstileSecretKey)

	for _, key := range []string{"TurnstileSiteKey", "TurnstileSecretKey"} {
		require.False(t, updateTurnstileOption(t, key, " \t "))
		var stored model.Option
		require.NoError(t, model.DB.First(&stored, "key = ?", key).Error)
		require.NotEmpty(t, stored.Value)
	}
	require.True(t, updateTurnstileOption(t, "TurnstileCheckEnabled", false))
	require.True(t, updateTurnstileOption(t, "TurnstileSecretKey", ""))
	require.False(t, updateTurnstileOption(t, "TurnstileCheckEnabled", true))
}

func TestTurnstileOptionsExposeOnlyPublicKeyAndConfiguredFlag(t *testing.T) {
	setupTurnstileOptionTest(t)
	common.OptionMap = map[string]string{
		"TurnstileSiteKey":             "public-site",
		"TurnstileSecretKey":           "private-secret",
		"TurnstileSecretKeyConfigured": "forged-value",
		"OtherSecret":                  "other-private-value",
	}
	for _, secret := range []string{"private-secret", ""} {
		common.TurnstileSecretKey = secret
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodGet, "/api/option/", nil)
		GetOptions(c)
		var result struct {
			Success bool           `json:"success"`
			Data    []model.Option `json:"data"`
		}
		require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &result))
		require.True(t, result.Success)
		options := map[string]string{}
		for _, option := range result.Data {
			_, duplicate := options[option.Key]
			require.False(t, duplicate, option.Key)
			options[option.Key] = option.Value
		}
		require.Equal(t, "public-site", options["TurnstileSiteKey"])
		require.NotContains(t, options, "TurnstileSecretKey")
		require.NotContains(t, options, "OtherSecret")
		require.Equal(t, secret != "", options["TurnstileSecretKeyConfigured"] == "true")
		require.NotContains(t, recorder.Body.String(), "private-secret")
	}
	require.False(t, updateTurnstileOption(t, "TurnstileSecretKeyConfigured", true))
}
