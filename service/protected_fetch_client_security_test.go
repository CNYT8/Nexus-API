package service

import (
	"context"
	"net"
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/common"
)

type staticSSRFResolver struct {
	addresses map[string][]net.IPAddr
}

func (r staticSSRFResolver) LookupIPAddr(_ context.Context, host string) ([]net.IPAddr, error) {
	return r.addresses[host], nil
}

func mandatoryTestProtection(t *testing.T) *common.SSRFProtection {
	t.Helper()
	protection, err := common.NewSSRFProtectionFromFetchSetting(
		false,
		false,
		false,
		nil,
		nil,
		[]string{"80", "443"},
		true,
	)
	if err != nil {
		t.Fatalf("NewSSRFProtectionFromFetchSetting() error = %v", err)
	}
	return protection
}

func TestProtectedFetchDialerRejectsReboundPrivateIPBeforeDial(t *testing.T) {
	protection := mandatoryTestProtection(t)
	dialCalls := 0
	dialer := protectedFetchDialer{
		resolver: staticSSRFResolver{addresses: map[string][]net.IPAddr{
			"attacker.example": {{IP: net.ParseIP("127.0.0.1")}},
		}},
		dialContext: func(context.Context, string, string) (net.Conn, error) {
			dialCalls++
			return nil, nil
		},
		getProtection: func() (*common.SSRFProtection, bool, error) {
			return protection, true, nil
		},
	}

	if _, err := dialer.DialContext(context.Background(), "tcp", "attacker.example:80"); err == nil {
		t.Fatal("DialContext() unexpectedly allowed a private resolved IP")
	}
	if dialCalls != 0 {
		t.Fatalf("underlying dial calls = %d, want 0", dialCalls)
	}
}

func TestProtectedFetchDialerPinsValidatedPublicIP(t *testing.T) {
	protection := mandatoryTestProtection(t)
	var dialAddress string
	clientConn, serverConn := net.Pipe()
	defer serverConn.Close()

	dialer := protectedFetchDialer{
		resolver: staticSSRFResolver{addresses: map[string][]net.IPAddr{
			"public.example": {{IP: net.ParseIP("93.184.216.34")}},
		}},
		dialContext: func(_ context.Context, _ string, address string) (net.Conn, error) {
			dialAddress = address
			return clientConn, nil
		},
		getProtection: func() (*common.SSRFProtection, bool, error) {
			return protection, true, nil
		},
	}

	conn, err := dialer.DialContext(context.Background(), "tcp", "public.example:443")
	if err != nil {
		t.Fatalf("DialContext() error = %v", err)
	}
	defer conn.Close()
	if dialAddress != "93.184.216.34:443" {
		t.Fatalf("dial address = %q, want pinned public IP", dialAddress)
	}
}

func TestMandatoryProtectedFetchRedirectIsRevalidated(t *testing.T) {
	protection := mandatoryTestProtection(t)
	client := newProtectedFetchHTTPClientWithProxy(
		staticSSRFResolver{},
		func(context.Context, string, string) (net.Conn, error) { return nil, nil },
		func() (*common.SSRFProtection, bool, error) { return protection, true, nil },
		directProtectedFetchProxy,
	)
	req, err := http.NewRequest(http.MethodGet, "http://127.0.0.1/", nil)
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}
	if err := client.CheckRedirect(req, nil); err == nil {
		t.Fatal("CheckRedirect() unexpectedly allowed a private redirect")
	}
}

func TestProtectedFetchIgnoresEnvironmentProxyByDefault(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "https://example.com/", nil)
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}
	if proxyURL, err := directProtectedFetchProxy(req); err != nil || proxyURL != nil {
		t.Fatalf("directProtectedFetchProxy() = (%v, %v), want (nil, nil)", proxyURL, err)
	}
}

func TestProtectedFetchHasBoundedNetworkTimeouts(t *testing.T) {
	client := newProtectedFetchHTTPClientWithDialer(nil, nil, nil)
	if client.Timeout <= 0 {
		t.Fatalf("client timeout = %v, want a positive timeout", client.Timeout)
	}
	roundTripper, ok := client.Transport.(*ssrfProtectedRoundTripper)
	if !ok {
		t.Fatalf("transport type = %T", client.Transport)
	}
	transport := roundTripper.transportFor(nil)
	if transport.TLSHandshakeTimeout <= 0 {
		t.Fatal("TLS handshake timeout is not bounded")
	}
	if transport.ResponseHeaderTimeout <= 0 {
		t.Fatal("response header timeout is not bounded")
	}
	if transport.ExpectContinueTimeout <= 0 {
		t.Fatal("expect-continue timeout is not bounded")
	}
}
