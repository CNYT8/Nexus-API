package channel

import (
	"net/http"
	"testing"
)

func TestKeepUpstreamRedirectResponse(t *testing.T) {
	if err := keepUpstreamRedirectResponse(nil, nil); err != http.ErrUseLastResponse {
		t.Fatalf("redirect callback error = %v, want %v", err, http.ErrUseLastResponse)
	}
}
