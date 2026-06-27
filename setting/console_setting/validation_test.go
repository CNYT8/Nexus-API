package console_setting

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateApiInfoAllowsNexusPresetColor(t *testing.T) {
	payload := fmt.Sprintf(`[{
		"url": "https://api.example.com",
		"route": "香港线路",
		"description": "Nexus-API preset endpoint",
		"color": %q
	}]`, nexusHongKongCloudflarePresetColor)

	require.NoError(t, ValidateConsoleSettings(payload, "ApiInfo"))
}

func TestValidateApiInfoRejectsUnknownColor(t *testing.T) {
	payload := `[{
		"url": "https://api.example.com",
		"route": "香港线路",
		"description": "Nexus-API preset endpoint",
		"color": "unknown-preset"
	}]`

	require.ErrorContains(t, ValidateConsoleSettings(payload, "ApiInfo"), "颜色值不合法")
}
