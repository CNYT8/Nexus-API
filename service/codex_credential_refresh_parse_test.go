package service

import "testing"

func TestParseCodexOAuthKeySupportsSub2APIAccountCredentials(t *testing.T) {
	raw := `{
		"platform": "openai",
		"type": "oauth",
		"credentials": {
			"access_token": "access-1",
			"refresh_token": "refresh-1",
			"id_token": "id-1",
			"chatgpt_account_id": "account-1",
			"email": "user@example.com",
			"expires_at": "2026-08-01T00:00:00Z"
		}
	}`

	key, err := parseCodexOAuthKey(raw)
	if err != nil {
		t.Fatalf("parseCodexOAuthKey() error = %v", err)
	}
	if key.Type != "codex" {
		t.Fatalf("Type = %q, want codex", key.Type)
	}
	if key.AccessToken != "access-1" || key.RefreshToken != "refresh-1" || key.IDToken != "id-1" {
		t.Fatalf("unexpected token fields: %+v", key)
	}
	if key.AccountID != "account-1" {
		t.Fatalf("AccountID = %q", key.AccountID)
	}
	if key.Email != "user@example.com" {
		t.Fatalf("Email = %q", key.Email)
	}
	if key.Expired != "2026-08-01T00:00:00Z" {
		t.Fatalf("Expired = %q", key.Expired)
	}
}
