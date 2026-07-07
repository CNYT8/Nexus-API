package codexkey

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
)

const openAIAuthClaimPath = "https://api.openai.com/auth"

type OAuthKey struct {
	IDToken      string `json:"id_token,omitempty"`
	AccessToken  string `json:"access_token,omitempty"`
	RefreshToken string `json:"refresh_token,omitempty"`

	AccountID   string `json:"account_id,omitempty"`
	LastRefresh string `json:"last_refresh,omitempty"`
	Email       string `json:"email,omitempty"`
	UserID      string `json:"user_id,omitempty"`
	PlanType    string `json:"plan_type,omitempty"`
	ImportedAt  string `json:"imported_at,omitempty"`
	Type        string `json:"type,omitempty"`
	Expired     string `json:"expired,omitempty"`
}

func Parse(raw string) (*OAuthKey, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.New("codex channel: empty oauth key")
	}
	var payload map[string]any
	if err := common.Unmarshal([]byte(raw), &payload); err != nil {
		return nil, errors.New("codex channel: invalid oauth key json")
	}
	key := NormalizePayload(payload)
	return &key, nil
}

func ParseMany(raw string) ([]OAuthKey, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.New("codex channel: empty oauth key")
	}
	payloads, err := decodeManyPayloads(raw)
	if err != nil {
		return nil, errors.New("codex channel: invalid oauth key json")
	}
	keys := make([]OAuthKey, 0, len(payloads))
	for _, payload := range payloads {
		keys = append(keys, normalizeManyPayload(payload)...)
	}
	if len(keys) == 0 {
		return nil, errors.New("codex channel: no oauth key found")
	}
	return keys, nil
}

func decodeManyPayloads(raw string) ([]any, error) {
	decoder := json.NewDecoder(strings.NewReader(raw))
	payloads := make([]any, 0, 1)
	for {
		var payload any
		err := decoder.Decode(&payload)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		payloads = append(payloads, payload)
	}
	if len(payloads) == 0 {
		return nil, errors.New("empty json content")
	}
	return payloads, nil
}

func NormalizePayload(payload map[string]any) OAuthKey {
	source := selectPayloadSource(payload)
	key := OAuthKey{
		IDToken: firstString(source,
			[]string{"id_token"},
			[]string{"idToken"},
			[]string{"tokens", "id_token"},
			[]string{"tokens", "idToken"},
			[]string{"credentials", "id_token"},
			[]string{"credentials", "idToken"},
		),
		AccessToken: firstString(source,
			[]string{"access_token"},
			[]string{"accessToken"},
			[]string{"token"},
			[]string{"tokens", "access_token"},
			[]string{"tokens", "accessToken"},
			[]string{"credentials", "access_token"},
			[]string{"credentials", "accessToken"},
			[]string{"credentials", "token"},
		),
		RefreshToken: firstString(source,
			[]string{"refresh_token"},
			[]string{"refreshToken"},
			[]string{"tokens", "refresh_token"},
			[]string{"tokens", "refreshToken"},
			[]string{"credentials", "refresh_token"},
			[]string{"credentials", "refreshToken"},
		),
		AccountID: firstString(source,
			[]string{"account_id"},
			[]string{"accountId"},
			[]string{"chatgpt_account_id"},
			[]string{"chatgptAccountId"},
			[]string{"tokens", "account_id"},
			[]string{"tokens", "accountId"},
			[]string{"tokens", "chatgpt_account_id"},
			[]string{"tokens", "chatgptAccountId"},
			[]string{"account", "id"},
			[]string{"account", "account_id"},
			[]string{"account", "accountId"},
			[]string{"account", "chatgpt_account_id"},
			[]string{"account", "chatgptAccountId"},
			[]string{"credentials", "account_id"},
			[]string{"credentials", "accountId"},
			[]string{"credentials", "chatgpt_account_id"},
			[]string{"credentials", "chatgptAccountId"},
		),
		LastRefresh: firstString(source,
			[]string{"last_refresh"},
			[]string{"lastRefresh"},
			[]string{"credentials", "last_refresh"},
			[]string{"credentials", "lastRefresh"},
		),
		Email: firstString(source,
			[]string{"email"},
			[]string{"user", "email"},
			[]string{"credentials", "email"},
		),
		UserID: firstString(source,
			[]string{"user_id"},
			[]string{"userId"},
			[]string{"chatgpt_user_id"},
			[]string{"chatgptUserId"},
			[]string{"user", "id"},
			[]string{"credentials", "user_id"},
			[]string{"credentials", "userId"},
			[]string{"credentials", "chatgpt_user_id"},
			[]string{"credentials", "chatgptUserId"},
		),
		PlanType: firstString(source,
			[]string{"plan_type"},
			[]string{"planType"},
			[]string{"chatgpt_plan_type"},
			[]string{"chatgptPlanType"},
			[]string{"account", "plan_type"},
			[]string{"account", "planType"},
			[]string{"credentials", "plan_type"},
			[]string{"credentials", "planType"},
			[]string{"credentials", "chatgpt_plan_type"},
			[]string{"credentials", "chatgptPlanType"},
		),
		ImportedAt: firstString(source,
			[]string{"imported_at"},
			[]string{"importedAt"},
			[]string{"extra", "imported_at"},
			[]string{"extra", "importedAt"},
			[]string{"credentials", "imported_at"},
			[]string{"credentials", "importedAt"},
		),
		Expired: firstString(source,
			[]string{"expired"},
			[]string{"expires_at"},
			[]string{"expiresAt"},
			[]string{"tokens", "expires_at"},
			[]string{"tokens", "expiresAt"},
			[]string{"credentials", "expired"},
			[]string{"credentials", "expires_at"},
			[]string{"credentials", "expiresAt"},
		),
		Type: "codex",
	}
	key.enrichFromJWT()
	return key
}

func NormalizeAndEncodeMany(raw string, importedAt string) ([]string, error) {
	keys, err := ParseMany(raw)
	if err != nil {
		return nil, err
	}
	lines := make([]string, 0, len(keys))
	seen := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		if strings.TrimSpace(key.Type) == "" {
			key.Type = "codex"
		}
		if strings.TrimSpace(key.ImportedAt) == "" {
			key.ImportedAt = importedAt
		}
		identity := Identity(key)
		if _, ok := seen[identity]; ok {
			continue
		}
		seen[identity] = struct{}{}
		encoded, err := common.Marshal(key)
		if err != nil {
			return nil, err
		}
		line := string(encoded)
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return nil, errors.New("codex channel: no oauth key found")
	}
	return lines, nil
}

func Identity(key OAuthKey) string {
	accountID := strings.TrimSpace(key.AccountID)
	if accountID != "" {
		return "account:" + accountID
	}
	userID := strings.TrimSpace(key.UserID)
	if userID != "" {
		return "user:" + userID
	}
	email := strings.ToLower(strings.TrimSpace(key.Email))
	if email != "" {
		return "email:" + email
	}
	accessToken := strings.TrimSpace(key.AccessToken)
	if accessToken != "" {
		return "access:" + accessToken
	}
	return strings.TrimSpace(key.RefreshToken) + ":" + strings.TrimSpace(key.IDToken)
}

func normalizeManyPayload(payload any) []OAuthKey {
	switch value := payload.(type) {
	case []any:
		return normalizeManyValues(value)
	case map[string]any:
		if hasTokenSignal(value) {
			key := NormalizePayload(value)
			if strings.TrimSpace(key.AccessToken) == "" {
				return nil
			}
			return []OAuthKey{key}
		}
		if values := collectAccountValues(value); len(values) > 0 {
			return normalizeManyValues(values)
		}
		key := NormalizePayload(value)
		if strings.TrimSpace(key.AccessToken) == "" {
			return nil
		}
		return []OAuthKey{key}
	default:
		return nil
	}
}

func collectAccountValues(payload map[string]any) []any {
	var values []any
	appendArray := func(raw any) {
		if arr, ok := raw.([]any); ok {
			values = append(values, arr...)
		}
	}
	for _, key := range []string{"accounts", "Accounts", "configs", "Configs", "items", "Items", "keys", "Keys"} {
		appendArray(payload[key])
	}
	if data, ok := payload["data"].(map[string]any); ok {
		for _, key := range []string{"accounts", "Accounts", "configs", "Configs", "items", "Items", "keys", "Keys"} {
			appendArray(data[key])
		}
	}
	appendArray(payload["data"])
	return values
}

func normalizeManyValues(values []any) []OAuthKey {
	keys := make([]OAuthKey, 0, len(values))
	for _, value := range values {
		switch item := value.(type) {
		case map[string]any:
			if values := collectAccountValues(item); !hasTokenSignal(item) && len(values) > 0 {
				keys = append(keys, normalizeManyValues(values)...)
				continue
			}
			key := NormalizePayload(item)
			if strings.TrimSpace(key.AccessToken) == "" {
				continue
			}
			keys = append(keys, key)
		}
	}
	return keys
}

func (key *OAuthKey) enrichFromJWT() {
	if key == nil {
		return
	}
	token := strings.TrimSpace(key.AccessToken)
	if token == "" {
		token = strings.TrimSpace(key.IDToken)
	}
	if token == "" {
		return
	}
	if strings.TrimSpace(key.AccountID) == "" {
		if accountID, ok := ExtractAccountIDFromJWT(token); ok {
			key.AccountID = accountID
		}
	}
	if strings.TrimSpace(key.Email) == "" {
		if email, ok := ExtractEmailFromJWT(token); ok {
			key.Email = email
		}
	}
	if strings.TrimSpace(key.UserID) == "" {
		if userID, ok := ExtractUserIDFromJWT(token); ok {
			key.UserID = userID
		}
	}
	if strings.TrimSpace(key.PlanType) == "" {
		if planType, ok := ExtractPlanTypeFromJWT(token); ok {
			key.PlanType = planType
		}
	}
	if strings.TrimSpace(key.Expired) == "" {
		if expiresAt, ok := ExtractExpiresAtFromJWT(token); ok {
			key.Expired = expiresAt.Format(time.RFC3339)
		}
	}
}

func selectPayloadSource(payload map[string]any) map[string]any {
	if hasTokenSignal(payload) {
		return payload
	}
	for _, key := range []string{"accounts", "Accounts"} {
		if accounts, ok := payload[key].([]any); ok {
			if account := firstAccountPayload(accounts); account != nil {
				return account
			}
		}
	}
	if data, ok := payload["data"].(map[string]any); ok {
		for _, key := range []string{"accounts", "Accounts"} {
			if accounts, ok := data[key].([]any); ok {
				if account := firstAccountPayload(accounts); account != nil {
					return account
				}
			}
		}
	}
	return payload
}

func firstAccountPayload(accounts []any) map[string]any {
	for _, raw := range accounts {
		account, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		platform := strings.ToLower(strings.TrimSpace(firstString(account, []string{"platform"})))
		accountType := strings.ToLower(strings.TrimSpace(firstString(account, []string{"type"})))
		if platform != "" && platform != "openai" {
			continue
		}
		if accountType != "" && accountType != "oauth" && accountType != "codex" {
			continue
		}
		if hasTokenSignal(account) {
			return account
		}
	}
	return nil
}

func hasTokenSignal(payload map[string]any) bool {
	return firstString(payload,
		[]string{"access_token"},
		[]string{"accessToken"},
		[]string{"token"},
		[]string{"tokens", "access_token"},
		[]string{"tokens", "accessToken"},
		[]string{"credentials", "access_token"},
		[]string{"credentials", "accessToken"},
		[]string{"credentials", "token"},
	) != ""
}

func firstString(payload map[string]any, paths ...[]string) string {
	for _, path := range paths {
		if value := stringAtPath(payload, path); value != "" {
			return value
		}
	}
	return ""
}

func stringAtPath(payload map[string]any, path []string) string {
	var current any = payload
	for _, key := range path {
		currentMap, ok := current.(map[string]any)
		if !ok {
			return ""
		}
		current, ok = currentMap[key]
		if !ok {
			return ""
		}
	}
	if value, ok := current.(string); ok {
		return strings.TrimSpace(value)
	}
	return ""
}

func ExtractAccountIDFromJWT(token string) (string, bool) {
	claims, ok := decodeJWTClaims(token)
	if !ok {
		return "", false
	}
	raw, ok := claims[openAIAuthClaimPath]
	if !ok {
		return "", false
	}
	obj, ok := raw.(map[string]any)
	if !ok {
		return "", false
	}
	return trimmedString(obj["chatgpt_account_id"])
}

func ExtractEmailFromJWT(token string) (string, bool) {
	claims, ok := decodeJWTClaims(token)
	if !ok {
		return "", false
	}
	return trimmedString(claims["email"])
}

func ExtractUserIDFromJWT(token string) (string, bool) {
	claims, ok := decodeJWTClaims(token)
	if !ok {
		return "", false
	}
	raw, ok := claims[openAIAuthClaimPath]
	if ok {
		if obj, ok := raw.(map[string]any); ok {
			if userID, ok := trimmedString(obj["chatgpt_user_id"]); ok {
				return userID, true
			}
			if userID, ok := trimmedString(obj["user_id"]); ok {
				return userID, true
			}
		}
	}
	return trimmedString(claims["sub"])
}

func ExtractPlanTypeFromJWT(token string) (string, bool) {
	claims, ok := decodeJWTClaims(token)
	if !ok {
		return "", false
	}
	raw, ok := claims[openAIAuthClaimPath]
	if !ok {
		return "", false
	}
	obj, ok := raw.(map[string]any)
	if !ok {
		return "", false
	}
	return trimmedString(obj["chatgpt_plan_type"])
}

func ExtractExpiresAtFromJWT(token string) (time.Time, bool) {
	claims, ok := decodeJWTClaims(token)
	if !ok {
		return time.Time{}, false
	}
	exp, ok := claims["exp"].(float64)
	if !ok || exp <= 0 {
		return time.Time{}, false
	}
	return time.Unix(int64(exp), 0).UTC(), true
}

func decodeJWTClaims(token string) (map[string]any, bool) {
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) != 3 {
		return nil, false
	}
	payloadRaw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		payloadRaw, err = base64.URLEncoding.DecodeString(parts[1])
		if err != nil {
			return nil, false
		}
	}
	var claims map[string]any
	if err := json.Unmarshal(payloadRaw, &claims); err != nil {
		return nil, false
	}
	return claims, true
}

func trimmedString(value any) (string, bool) {
	s, ok := value.(string)
	if !ok {
		return "", false
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return "", false
	}
	return s, true
}
