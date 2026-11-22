package operation_setting

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/config"
)

// AuthenticationSetting is independent of relay API-key limits. Non-positive
// values fall back to defaults; they never silently disable credential limits.
type AuthenticationSetting struct {
	MaxAccessTokens      int   `json:"max_access_tokens"`
	MaxActiveSessions    int   `json:"max_active_sessions"`
	MaxSessionIssuance   int   `json:"max_session_issuance"`
	SessionWindowSeconds int64 `json:"session_window_seconds"`
}

var authenticationSetting = AuthenticationSetting{
	MaxAccessTokens: 20, MaxActiveSessions: 50, MaxSessionIssuance: 100, SessionWindowSeconds: 86400,
}

func init() {
	config.GlobalConfig.Register("authentication_setting", &authenticationSetting)
}

// GetAuthenticationSetting returns a snapshot under the option-update lock.
// The window cannot exceed 30 days: historical session cleanup retains expired
// records for that long, so every record in the counting window remains present.
func GetAuthenticationSetting() AuthenticationSetting {
	common.OptionMapRWMutex.RLock()
	value := authenticationSetting
	common.OptionMapRWMutex.RUnlock()
	if value.MaxAccessTokens <= 0 {
		value.MaxAccessTokens = 20
	}
	if value.MaxActiveSessions <= 0 {
		value.MaxActiveSessions = 50
	}
	if value.MaxSessionIssuance <= 0 {
		value.MaxSessionIssuance = 100
	}
	if value.SessionWindowSeconds <= 0 {
		value.SessionWindowSeconds = 86400
	}
	if value.SessionWindowSeconds > 30*86400 {
		value.SessionWindowSeconds = 30 * 86400
	}
	return value
}
