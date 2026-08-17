package controller

import (
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
)

func channelSecurityContext(role int) *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set("role", role)
	return c
}

func TestDelegatedAdminCannotPersistCustomChannelTarget(t *testing.T) {
	for _, baseURL := range []string{
		"http://127.0.0.1:80",
		"https://93.184.216.34",
		"https://attacker.example",
	} {
		channel := &model.Channel{Type: constant.ChannelTypeOpenAI, BaseURL: &baseURL}
		if err := validateDelegatedAdminChannelNetwork(channelSecurityContext(common.RoleAdminUser), channel, nil); err == nil {
			t.Fatalf("delegated administrator unexpectedly allowed custom base URL %q", baseURL)
		}
		if err := validateDelegatedAdminChannelNetwork(channelSecurityContext(common.RoleRootUser), channel, nil); err != nil {
			t.Fatalf("root channel validation error for %q = %v", baseURL, err)
		}
	}
}

func TestDelegatedAdminCannotPersistChannelProxy(t *testing.T) {
	channel := &model.Channel{Type: constant.ChannelTypeOpenAI}
	settings := channel.GetSetting()
	settings.Proxy = "socks5://127.0.0.1:1080"
	channel.SetSetting(settings)

	if err := validateDelegatedAdminChannelNetwork(channelSecurityContext(common.RoleAdminUser), channel, nil); err == nil {
		t.Fatal("delegated administrator unexpectedly allowed a proxy")
	}
}

func TestDelegatedAdminCanUseTrustedBuiltInChannelTarget(t *testing.T) {
	baseURL := constant.ChannelBaseURLs[constant.ChannelTypeOpenAI] + "/"
	channel := &model.Channel{Type: constant.ChannelTypeOpenAI, BaseURL: &baseURL}

	if err := validateDelegatedAdminChannelNetwork(channelSecurityContext(common.RoleAdminUser), channel, nil); err != nil {
		t.Fatalf("built-in channel validation error = %v", err)
	}
}

func TestDelegatedAdminCannotUseBuiltInPrivateChannelTarget(t *testing.T) {
	channel := &model.Channel{Type: constant.ChannelTypeOllama}
	if err := validateDelegatedAdminChannelNetwork(channelSecurityContext(common.RoleAdminUser), channel, nil); err == nil {
		t.Fatal("delegated administrator unexpectedly allowed the localhost Ollama target")
	}
}

func TestDelegatedAdminCanEditWithoutChangingRootNetworkTarget(t *testing.T) {
	baseURL := "http://127.0.0.1:8080"
	origin := &model.Channel{Type: constant.ChannelTypeCustom, BaseURL: &baseURL}
	settings := origin.GetSetting()
	settings.Proxy = "http://127.0.0.1:3128"
	origin.SetSetting(settings)
	patch := &model.Channel{}

	if err := validateDelegatedAdminChannelNetwork(channelSecurityContext(common.RoleAdminUser), patch, origin); err != nil {
		t.Fatalf("metadata-only update validation error = %v", err)
	}
}

func TestDelegatedAdminCannotChangeExistingNetworkTarget(t *testing.T) {
	originBaseURL := "https://api.openai.com"
	newBaseURL := "https://attacker.example"
	origin := &model.Channel{Type: constant.ChannelTypeOpenAI, BaseURL: &originBaseURL}
	patch := &model.Channel{Type: constant.ChannelTypeOpenAI, BaseURL: &newBaseURL}

	if err := validateDelegatedAdminChannelNetwork(channelSecurityContext(common.RoleAdminUser), patch, origin); err == nil {
		t.Fatal("delegated administrator unexpectedly changed the network target")
	}
}
