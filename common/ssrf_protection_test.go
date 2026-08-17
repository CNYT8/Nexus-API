package common

import (
	"strings"
	"testing"
)

func newTestSSRFProtection(t *testing.T, ports ...string) *SSRFProtection {
	t.Helper()
	protection, err := NewSSRFProtectionFromFetchSetting(
		false,
		false,
		false,
		nil,
		nil,
		ports,
		true,
	)
	if err != nil {
		t.Fatalf("NewSSRFProtectionFromFetchSetting() error = %v", err)
	}
	return protection
}

func TestSSRFProtectionRejectsPrivateAndSpecialTargets(t *testing.T) {
	protection := newTestSSRFProtection(t, "80", "443")
	targets := []string{
		"http://127.0.0.1/",
		"http://10.0.0.1/",
		"http://169.254.169.254/latest/meta-data/",
		"http://[::1]/",
		"http://[::ffff:127.0.0.1]/",
		"http://[64:ff9b::7f00:1]/",
		"http://[2002:7f00:1::]/",
		"http://[2002:a00:1::]/",
		"http://[fec0::1]/",
	}

	for _, target := range targets {
		t.Run(target, func(t *testing.T) {
			if err := protection.ValidateURL(target); err == nil {
				t.Fatalf("ValidateURL(%q) unexpectedly succeeded", target)
			}
		})
	}
}

func TestSSRFProtectionRejectsUnsupportedProtocolsAndPorts(t *testing.T) {
	protection := newTestSSRFProtection(t, "80", "443")

	if err := protection.ValidateURL("gopher://93.184.216.34:80/"); err == nil || !strings.Contains(err.Error(), "unsupported protocol") {
		t.Fatalf("unsupported protocol error = %v", err)
	}
	if err := protection.ValidateURL("http://93.184.216.34:22/"); err == nil || !strings.Contains(err.Error(), "port 22 is not allowed") {
		t.Fatalf("disallowed port error = %v", err)
	}
	if err := protection.ValidateURL("https://93.184.216.34/"); err != nil {
		t.Fatalf("allowed public target error = %v", err)
	}
}

func TestSSRFProtectionEmptyAllowedPortsFailsClosed(t *testing.T) {
	protection := newTestSSRFProtection(t)
	if err := protection.ValidateURL("https://93.184.216.34/"); err == nil || !strings.Contains(err.Error(), "port 443 is not allowed") {
		t.Fatalf("empty allowed ports error = %v", err)
	}
}

func TestParsePortRangesRejectsInvalidAndSupportsRanges(t *testing.T) {
	ports, err := parsePortRanges([]string{"80", "443", "8000-8002"})
	if err != nil {
		t.Fatalf("parsePortRanges() error = %v", err)
	}
	want := []int{80, 443, 8000, 8001, 8002}
	if len(ports) != len(want) {
		t.Fatalf("ports = %v, want %v", ports, want)
	}
	for i := range want {
		if ports[i] != want[i] {
			t.Fatalf("ports = %v, want %v", ports, want)
		}
	}

	for _, invalid := range [][]string{{"0"}, {"65536"}, {"9000-8000"}, {"1-65536"}} {
		if _, err := parsePortRanges(invalid); err == nil {
			t.Fatalf("parsePortRanges(%v) unexpectedly succeeded", invalid)
		}
	}
}
