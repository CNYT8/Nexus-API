package vertex

import (
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/sync/singleflight"
)

type Credentials struct {
	ProjectID    string `json:"project_id"`
	PrivateKeyID string `json:"private_key_id"`
	PrivateKey   string `json:"private_key"`
	ClientEmail  string `json:"client_email"`
	ClientID     string `json:"client_id"`
}

const (
	accessTokenExpiryMargin   = 5 * time.Minute
	defaultAccessTokenTTL     = 50 * time.Minute
	accessTokenAssertionLease = 35 * time.Minute
)

type cachedAccessToken struct {
	token       string
	expiresAt   time.Time
	fingerprint string
}

type accessTokenResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int64  `json:"expires_in"`
}

var (
	accessTokenCacheMu sync.RWMutex
	accessTokenCache   = make(map[string]cachedAccessToken)
	accessTokenFlight  singleflight.Group
)

func getAccessToken(a *Adaptor, info *relaycommon.RelayInfo) (string, error) {
	return getAccessTokenAt(a, info, time.Now)
}

func getAccessTokenAt(a *Adaptor, info *relaycommon.RelayInfo, now func() time.Time) (string, error) {
	slotKey := accessTokenSlotKey(info)
	fingerprint := credentialsFingerprint(a.AccountCredentials)
	if token, ok := loadCachedAccessToken(slotKey, fingerprint, now()); ok {
		return token, nil
	}

	flightKey := slotKey + ":" + fingerprint
	val, err, _ := accessTokenFlight.Do(flightKey, func() (interface{}, error) {
		currentTime := now()
		if token, ok := loadCachedAccessToken(slotKey, fingerprint, currentTime); ok {
			return token, nil
		}

		signedJWT, err := createSignedJWT(a.AccountCredentials.ClientEmail, a.AccountCredentials.PrivateKey)
		if err != nil {
			return "", fmt.Errorf("failed to create signed JWT: %w", err)
		}
		tokenResp, err := exchangeJwtForAccessToken(signedJWT, info)
		if err != nil {
			return "", fmt.Errorf("failed to exchange JWT for access token: %w", err)
		}
		storeCachedAccessToken(slotKey, fingerprint, tokenResp, now())
		return tokenResp.AccessToken, nil
	})
	if err != nil {
		return "", err
	}
	return val.(string), nil
}

func accessTokenSlotKey(info *relaycommon.RelayInfo) string {
	if info.ChannelIsMultiKey {
		return fmt.Sprintf("access-token-%d-%d", info.ChannelId, info.ChannelMultiKeyIndex)
	}
	return fmt.Sprintf("access-token-%d", info.ChannelId)
}

func credentialsFingerprint(creds Credentials) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{
		creds.ProjectID,
		creds.ClientEmail,
		creds.PrivateKeyID,
		creds.PrivateKey,
	}, "\x00")))
	return hex.EncodeToString(sum[:])
}

func loadCachedAccessToken(slotKey, fingerprint string, now time.Time) (string, bool) {
	accessTokenCacheMu.RLock()
	entry, ok := accessTokenCache[slotKey]
	accessTokenCacheMu.RUnlock()
	if !ok || entry.fingerprint != fingerprint || entry.token == "" || !now.Before(entry.expiresAt) {
		return "", false
	}
	return entry.token, true
}

func storeCachedAccessToken(slotKey, fingerprint string, resp accessTokenResponse, now time.Time) {
	if resp.AccessToken == "" {
		return
	}
	ttl := defaultAccessTokenTTL
	if resp.ExpiresIn > 0 {
		tokenLifetime := time.Duration(resp.ExpiresIn) * time.Second
		if tokenLifetime <= accessTokenExpiryMargin {
			return
		}
		ttl = tokenLifetime - accessTokenExpiryMargin
	}
	accessTokenCacheMu.Lock()
	accessTokenCache[slotKey] = cachedAccessToken{
		token:       resp.AccessToken,
		expiresAt:   now.Add(ttl),
		fingerprint: fingerprint,
	}
	accessTokenCacheMu.Unlock()
}
func createSignedJWT(email, privateKeyPEM string) (string, error) {

	privateKeyPEM = strings.ReplaceAll(privateKeyPEM, "-----BEGIN PRIVATE KEY-----", "")
	privateKeyPEM = strings.ReplaceAll(privateKeyPEM, "-----END PRIVATE KEY-----", "")
	privateKeyPEM = strings.ReplaceAll(privateKeyPEM, "\r", "")
	privateKeyPEM = strings.ReplaceAll(privateKeyPEM, "\n", "")
	privateKeyPEM = strings.ReplaceAll(privateKeyPEM, "\\n", "")

	block, _ := pem.Decode([]byte("-----BEGIN PRIVATE KEY-----\n" + privateKeyPEM + "\n-----END PRIVATE KEY-----"))
	if block == nil {
		return "", fmt.Errorf("failed to parse PEM block containing the private key")
	}

	privateKey, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return "", err
	}

	rsaPrivateKey, ok := privateKey.(*rsa.PrivateKey)
	if !ok {
		return "", fmt.Errorf("not an RSA private key")
	}

	now := time.Now()
	claims := jwt.MapClaims{
		"iss":   email,
		"scope": "https://www.googleapis.com/auth/cloud-platform",
		"aud":   "https://www.googleapis.com/oauth2/v4/token",
		"exp":   now.Add(accessTokenAssertionLease).Unix(),
		"iat":   now.Unix(),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	signedToken, err := token.SignedString(rsaPrivateKey)
	if err != nil {
		return "", err
	}

	return signedToken, nil
}

func exchangeJwtForAccessToken(signedJWT string, info *relaycommon.RelayInfo) (accessTokenResponse, error) {
	var proxy string
	if info != nil {
		proxy = info.ChannelSetting.Proxy
	}
	return exchangeJwtForAccessTokenWithProxy(signedJWT, proxy)
}

func AcquireAccessToken(creds Credentials, proxy string) (string, error) {
	signedJWT, err := createSignedJWT(creds.ClientEmail, creds.PrivateKey)
	if err != nil {
		return "", fmt.Errorf("failed to create signed JWT: %w", err)
	}
	resp, err := exchangeJwtForAccessTokenWithProxy(signedJWT, proxy)
	if err != nil {
		return "", err
	}
	return resp.AccessToken, nil
}

func exchangeJwtForAccessTokenWithProxy(signedJWT string, proxy string) (accessTokenResponse, error) {
	authURL := "https://www.googleapis.com/oauth2/v4/token"
	data := url.Values{}
	data.Set("grant_type", "urn:ietf:params:oauth:grant-type:jwt-bearer")
	data.Set("assertion", signedJWT)

	var client *http.Client
	var err error
	if proxy != "" {
		client, err = service.NewProxyHttpClient(proxy)
		if err != nil {
			return accessTokenResponse{}, fmt.Errorf("new proxy http client failed: %w", err)
		}
	} else {
		client = service.GetHttpClient()
	}

	resp, err := client.PostForm(authURL, data)
	if err != nil {
		return accessTokenResponse{}, err
	}
	defer resp.Body.Close()

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return accessTokenResponse{}, err
	}

	if accessToken, ok := result["access_token"].(string); ok && accessToken != "" {
		return accessTokenResponse{
			AccessToken: accessToken,
			ExpiresIn:   parseAccessTokenExpiresIn(result["expires_in"]),
		}, nil
	}
	return accessTokenResponse{}, fmt.Errorf("failed to get access token: %v", result)
}

func parseAccessTokenExpiresIn(raw interface{}) int64 {
	switch v := raw.(type) {
	case float64:
		return int64(v)
	case int64:
		return v
	case int:
		return int64(v)
	case json.Number:
		n, err := v.Int64()
		if err == nil {
			return n
		}
	}
	return 0
}
