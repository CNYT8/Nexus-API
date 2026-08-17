package service

import (
	"testing"

	"github.com/QuantumNous/new-api/setting/system_setting"
)

func withUntrustedOutboundTestSettings(t *testing.T, ports []string) {
	t.Helper()
	settings := system_setting.GetFetchSetting()
	original := *settings
	settings.EnableSSRFProtection = false
	settings.AllowPrivateIp = true
	settings.DomainFilterMode = false
	settings.IpFilterMode = false
	settings.DomainList = nil
	settings.IpList = nil
	settings.AllowedPorts = ports
	settings.ApplyIPFilterForDomain = false
	t.Cleanup(func() { *settings = original })
}

func TestUntrustedOutboundValidationCannotBeDisabled(t *testing.T) {
	withUntrustedOutboundTestSettings(t, []string{"80", "443"})

	if err := ValidateUntrustedOutboundURL("http://127.0.0.1/"); err == nil {
		t.Fatal("ValidateUntrustedOutboundURL() unexpectedly allowed loopback")
	}
	if err := ValidateUntrustedOutboundURL("http://169.254.169.254/latest/meta-data/"); err == nil {
		t.Fatal("ValidateUntrustedOutboundURL() unexpectedly allowed metadata service")
	}
	if err := ValidateUntrustedOutboundURL("https://93.184.216.34/"); err != nil {
		t.Fatalf("ValidateUntrustedOutboundURL() public target error = %v", err)
	}
}

func TestUntrustedOutboundValidationEmptyPortsFailsClosed(t *testing.T) {
	withUntrustedOutboundTestSettings(t, nil)
	if err := ValidateUntrustedOutboundURL("https://93.184.216.34/"); err == nil {
		t.Fatal("ValidateUntrustedOutboundURL() unexpectedly allowed an empty port policy")
	}
}
