package common

import "strings"

func TurnstileConfigured() bool {
	return TurnstileCheckEnabled &&
		strings.TrimSpace(TurnstileSiteKey) != "" &&
		strings.TrimSpace(TurnstileSecretKey) != ""
}
