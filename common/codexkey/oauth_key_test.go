package codexkey

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

func TestParseSupportsSub2APICodexSessionJSON(t *testing.T) {
	raw := `{
		"accessToken": "access-1",
		"refreshToken": "refresh-1",
		"idToken": "id-1",
		"user": {
			"email": "user@example.com"
		},
		"account": {
			"id": "account-1"
		}
	}`

	key, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
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
}

func TestParseDerivesCodexAccountFromJWT(t *testing.T) {
	expiresAt := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	accessToken := testJWT(t, map[string]any{
		"email": "claim@example.com",
		"exp":   expiresAt.Unix(),
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": "account-from-claim",
		},
	})
	raw := fmt.Sprintf(`{
		"tokens": {
			"accessToken": %q,
			"refreshToken": "refresh-from-tokens"
		}
	}`, accessToken)

	key, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if key.AccountID != "account-from-claim" {
		t.Fatalf("AccountID = %q", key.AccountID)
	}
	if key.Email != "claim@example.com" {
		t.Fatalf("Email = %q", key.Email)
	}
	if key.Expired != expiresAt.Format(time.RFC3339) {
		t.Fatalf("Expired = %q", key.Expired)
	}
}

func TestParseManySupportsMixedCodexConfigs(t *testing.T) {
	raw := `{
		"accounts": [
			{
				"type": "codex",
				"access_token": "nexus-access",
				"account_id": "nexus-account",
				"email": "nexus@example.com"
			},
			{
				"platform": "openai",
				"type": "oauth",
				"tokens": {
					"accessToken": "sub2api-access",
					"refreshToken": "sub2api-refresh"
				},
				"account": {
					"chatgpt_account_id": "sub2api-account",
					"planType": "plus"
				},
				"user": {
					"email": "sub2api@example.com"
				}
			}
		]
	}`

	keys, err := ParseMany(raw)
	if err != nil {
		t.Fatalf("ParseMany() error = %v", err)
	}
	if len(keys) != 2 {
		t.Fatalf("len(keys) = %d, want 2", len(keys))
	}
	if keys[0].AccessToken != "nexus-access" || keys[0].AccountID != "nexus-account" {
		t.Fatalf("unexpected first key: %+v", keys[0])
	}
	if keys[1].AccessToken != "sub2api-access" || keys[1].RefreshToken != "sub2api-refresh" {
		t.Fatalf("unexpected second token fields: %+v", keys[1])
	}
	if keys[1].AccountID != "sub2api-account" || keys[1].PlanType != "plus" || keys[1].Email != "sub2api@example.com" {
		t.Fatalf("unexpected second account fields: %+v", keys[1])
	}
}

func TestParseManySupportsNewlineSeparatedStoredConfigs(t *testing.T) {
	raw := `{"access_token":"access-1","account_id":"account-1"}
{"access_token":"access-2","account_id":"account-2"}`

	keys, err := ParseMany(raw)
	if err != nil {
		t.Fatalf("ParseMany() error = %v", err)
	}
	if len(keys) != 2 {
		t.Fatalf("len(keys) = %d, want 2", len(keys))
	}
	if keys[0].AccessToken != "access-1" || keys[1].AccessToken != "access-2" {
		t.Fatalf("unexpected keys: %+v", keys)
	}
}

func TestNormalizeAndEncodeManyDeduplicatesByCodexIdentity(t *testing.T) {
	raw := `{
		"accounts": [
			{
				"access_token": "old-access",
				"account_id": "same-account",
				"imported_at": "2026-01-01T00:00:00Z"
			},
			{
				"access_token": "new-access",
				"account_id": "same-account",
				"imported_at": "2026-01-02T00:00:00Z"
			}
		]
	}`

	lines, err := NormalizeAndEncodeMany(raw, "2026-01-03T00:00:00Z")
	if err != nil {
		t.Fatalf("NormalizeAndEncodeMany() error = %v", err)
	}
	if len(lines) != 1 {
		t.Fatalf("len(lines) = %d, want 1", len(lines))
	}
}

func testJWT(t *testing.T, claims map[string]any) string {
	t.Helper()
	headerRaw, err := json.Marshal(map[string]string{"alg": "none", "typ": "JWT"})
	if err != nil {
		t.Fatal(err)
	}
	claimsRaw, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	return base64.RawURLEncoding.EncodeToString(headerRaw) + "." +
		base64.RawURLEncoding.EncodeToString(claimsRaw) + ".signature"
}
