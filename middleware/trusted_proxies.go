package middleware

import (
	"fmt"
	"net"
	"os"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
)

// ResolveTrustedProxies follows upstream's deployment compatibility policy.
// Empty configuration trusts loopback/private networks; "none" disables forwarded
// client IPs. An explicit comma-separated IP/CIDR list replaces all defaults.
func ResolveTrustedProxies(raw string) (trustedProxies []string, usedDefaults bool, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return []string{"127.0.0.0/8", "::1", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "fc00::/7"}, true, nil
	}
	if strings.EqualFold(raw, "none") {
		return nil, false, nil
	}
	for _, entry := range strings.Split(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if strings.EqualFold(entry, "none") {
			return nil, false, fmt.Errorf("TRUSTED_PROXIES=none must be used alone")
		}
		if net.ParseIP(entry) == nil {
			if _, _, parseErr := net.ParseCIDR(entry); parseErr != nil {
				return nil, false, fmt.Errorf("invalid TRUSTED_PROXIES entry %q: expected IP address or CIDR", entry)
			}
		}
		trustedProxies = append(trustedProxies, entry)
	}
	if len(trustedProxies) == 0 {
		return nil, false, fmt.Errorf("TRUSTED_PROXIES does not contain an IP address or CIDR")
	}
	return trustedProxies, false, nil
}

// ConfigureTrustedProxies must be called after gin.New and before serving.
// Invalid explicit configuration is a startup error, never a fallback to trust-all.
func ConfigureTrustedProxies(engine *gin.Engine) error {
	proxies, defaults, err := ResolveTrustedProxies(os.Getenv("TRUSTED_PROXIES"))
	if err != nil {
		return err
	}
	if err := engine.SetTrustedProxies(proxies); err != nil {
		return fmt.Errorf("invalid TRUSTED_PROXIES: %w", err)
	}
	if defaults {
		common.SysLog("WARNING: TRUSTED_PROXIES is unset; trusting loopback/private networks for deployment compatibility. Set exact proxy IPs/CIDRs, or none when directly exposed.")
	}
	return nil
}
