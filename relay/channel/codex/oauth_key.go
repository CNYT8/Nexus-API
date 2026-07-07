package codex

import "github.com/QuantumNous/new-api/common/codexkey"

type OAuthKey = codexkey.OAuthKey

func ParseOAuthKey(raw string) (*OAuthKey, error) {
	return codexkey.Parse(raw)
}
