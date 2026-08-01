package common

import (
	"strings"
	"sync"
)

var (
	webRiskFingerprintSecret   string
	webRiskFingerprintSecretMu sync.RWMutex
)

func SetWebRiskFingerprintSecret(value string) {
	webRiskFingerprintSecretMu.Lock()
	webRiskFingerprintSecret = strings.TrimSpace(value)
	webRiskFingerprintSecretMu.Unlock()
}

func GetWebRiskFingerprintSecret() string {
	webRiskFingerprintSecretMu.RLock()
	value := webRiskFingerprintSecret
	webRiskFingerprintSecretMu.RUnlock()
	return value
}
