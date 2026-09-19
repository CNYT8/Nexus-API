package oauth

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type githubEmailTransport func(*http.Request) (*http.Response, error)

func (f githubEmailTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestGitHubVerifiedEmailsRequireProviderVerification(t *testing.T) {
	original := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = original })
	for _, tc := range []struct {
		name      string
		body      string
		status    int
		want      []string
		wantError bool
	}{
		{"verified only", `[{"email":"verified@example.com","verified":true},{"email":"unverified@example.com","verified":false},{"email":"","verified":true}]`, 200, []string{"verified@example.com"}, false},
		{"no addresses", `[]`, 200, []string{}, false},
		{"denied", `{"error":"private-provider-message"}`, 403, nil, true},
		{"malformed", `{"email":"private"}`, 200, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			http.DefaultTransport = githubEmailTransport(func(r *http.Request) (*http.Response, error) {
				require.Equal(t, "https://api.github.com/user/emails", r.URL.String())
				require.Equal(t, http.MethodGet, r.Method)
				require.Equal(t, "Bearer test-token", r.Header.Get("Authorization"))
				return &http.Response{StatusCode: tc.status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(tc.body))}, nil
			})
			emails, err := (&GitHubProvider{}).GetVerifiedEmails(context.Background(), &OAuthToken{AccessToken: "test-token"})
			if tc.wantError {
				require.Error(t, err)
				require.NotContains(t, err.Error(), "private")
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.want, emails)
			}
		})
	}
}
