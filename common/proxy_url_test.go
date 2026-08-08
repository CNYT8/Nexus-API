package common

import "testing"

func TestParseProxyURLStrict(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    string
		wantErr bool
	}{
		{name: "http", raw: "HTTP://proxy.example:8080", want: "http://proxy.example:8080"},
		{name: "socks default port", raw: "socks5://proxy.example", want: "socks5://proxy.example:1080"},
		{name: "root path", raw: "http://proxy.example:8080/", want: "http://proxy.example:8080"},
		{name: "path rejected", raw: "http://proxy.example:8080/proxy", wantErr: true},
		{name: "query rejected", raw: "http://proxy.example:8080?legacy=true", wantErr: true},
		{name: "fragment rejected", raw: "http://proxy.example:8080#legacy", wantErr: true},
		{name: "unsupported scheme", raw: "ftp://proxy.example:21", wantErr: true},
		{name: "missing host", raw: "http:///proxy", wantErr: true},
		{name: "invalid port", raw: "http://proxy.example:65536", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsedURL, err := ParseProxyURLStrict(tt.raw)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q", tt.raw)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseProxyURLStrict(%q): %v", tt.raw, err)
			}
			if got := parsedURL.String(); got != tt.want {
				t.Fatalf("canonical URL = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseProxyURLRuntimeStripsLegacySuffix(t *testing.T) {
	parsedURL, stripped, err := ParseProxyURLRuntime("socks5h://proxy.example/legacy?unused=true#fragment")
	if err != nil {
		t.Fatalf("ParseProxyURLRuntime: %v", err)
	}
	if !stripped {
		t.Fatal("expected legacy suffix to be reported as stripped")
	}
	if got, want := parsedURL.String(), "socks5h://proxy.example:1080"; got != want {
		t.Fatalf("canonical URL = %q, want %q", got, want)
	}
}
