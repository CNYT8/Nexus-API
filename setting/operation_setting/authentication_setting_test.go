package operation_setting

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/stretchr/testify/require"
)

func TestAuthenticationSettingBoundaries(t *testing.T) {
	registered := config.GlobalConfig.Get("authentication_setting").(*AuthenticationSetting)
	common.OptionMapRWMutex.Lock()
	previous := *registered
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		*registered = previous
		common.OptionMapRWMutex.Unlock()
	})
	defaults := AuthenticationSetting{MaxAccessTokens: 20, MaxActiveSessions: 50, MaxSessionIssuance: 100, SessionWindowSeconds: 86400}
	for _, test := range []struct {
		name string
		in   AuthenticationSetting
		want AuthenticationSetting
	}{
		{"zero falls back", AuthenticationSetting{}, defaults},
		{"negative falls back", AuthenticationSetting{-1, -1, -1, -1}, defaults},
		{"configured", AuthenticationSetting{7, 8, 9, 60}, AuthenticationSetting{7, 8, 9, 60}},
		{"retained window boundary", AuthenticationSetting{7, 8, 9, 30 * 86400}, AuthenticationSetting{7, 8, 9, 30 * 86400}},
		{"window cannot outlive history", AuthenticationSetting{7, 8, 9, 30*86400 + 1}, AuthenticationSetting{7, 8, 9, 30 * 86400}},
		{"huge window cannot overflow subtraction", AuthenticationSetting{7, 8, 9, 1<<63 - 1}, AuthenticationSetting{7, 8, 9, 30 * 86400}},
	} {
		t.Run(test.name, func(t *testing.T) {
			common.OptionMapRWMutex.Lock()
			*registered = test.in
			common.OptionMapRWMutex.Unlock()
			actual := GetAuthenticationSetting()
			require.Equal(t, test.want, actual)
			actual.MaxAccessTokens = 999
			require.Equal(t, test.want.MaxAccessTokens, GetAuthenticationSetting().MaxAccessTokens, "callers must receive an independent snapshot")
		})
	}
}
