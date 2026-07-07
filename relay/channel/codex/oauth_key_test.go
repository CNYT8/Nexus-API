package codex

import "testing"

func TestParseOAuthKeySupportsSub2APIAccountCredentials(t *testing.T) {
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

	key, err := ParseOAuthKey(raw)
	if err != nil {
		t.Fatalf("ParseOAuthKey() error = %v", err)
	}
	if key.Type != "codex" {
		t.Fatalf("Type = %q, want codex", key.Type)
	}
	if key.AccessToken != "access-1" {
		t.Fatalf("AccessToken = %q", key.AccessToken)
	}
	if key.RefreshToken != "refresh-1" {
		t.Fatalf("RefreshToken = %q", key.RefreshToken)
	}
	if key.IDToken != "id-1" {
		t.Fatalf("IDToken = %q", key.IDToken)
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

func TestParseOAuthKeySupportsSub2APITokensAndBundle(t *testing.T) {
	raw := `{
		"accounts": [
			{
				"platform": "openai",
				"type": "oauth",
				"tokens": {
					"accessToken": "access-2",
					"refreshToken": "refresh-2",
					"idToken": "id-2",
					"expiresAt": "2026-08-02T00:00:00Z"
				},
				"account": {
					"chatgpt_account_id": "account-2"
				}
			}
		]
	}`

	key, err := ParseOAuthKey(raw)
	if err != nil {
		t.Fatalf("ParseOAuthKey() error = %v", err)
	}
	if key.AccessToken != "access-2" || key.RefreshToken != "refresh-2" || key.IDToken != "id-2" {
		t.Fatalf("unexpected token fields: %+v", key)
	}
	if key.AccountID != "account-2" {
		t.Fatalf("AccountID = %q", key.AccountID)
	}
	if key.Expired != "2026-08-02T00:00:00Z" {
		t.Fatalf("Expired = %q", key.Expired)
	}
}
