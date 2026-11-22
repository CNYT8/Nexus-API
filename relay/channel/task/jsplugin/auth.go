package jsplugin

import (
	"crypto/sha256"
	"errors"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	vertexcore "github.com/QuantumNous/new-api/relay/channel/vertex"
)

type cachedAuth struct {
	header    string
	projectID string
	expiresAt time.Time
}

var authCache = struct {
	sync.Mutex
	entries map[[32]byte]cachedAuth
}{entries: map[[32]byte]cachedAuth{}}

func (a *TaskAdaptor) credentials(target map[string]any, key, proxy string) error {
	delete(target, "apiKey")
	delete(target, "authHeader")
	delete(target, "auth")
	target["upstream"] = map[string]any{"kind": "vendor"}
	if a.info != nil && a.info.ChannelType == 60 {
		header := "Bearer " + key
		target["upstream"] = map[string]any{"kind": "new_api"}
		target["auth"], target["authHeader"], target["apiKey"] = map[string]any{"authHeader": header}, header, key
		return nil
	}
	if a.plugin.Meta.Auth.Type == "none" {
		target["auth"] = map[string]any{}
		return nil
	}
	if a.plugin.Meta.Auth.Type != "oauth2_jwt" {
		header := "Bearer " + key
		target["auth"], target["authHeader"], target["apiKey"] = map[string]any{"authHeader": header}, header, key
		return nil
	}
	fingerprint := sha256.Sum256([]byte(key + "\x00" + proxy))
	authCache.Lock()
	entry, found := authCache.entries[fingerprint]
	authCache.Unlock()
	if !found || !time.Now().Before(entry.expiresAt) {
		var credentials vertexcore.Credentials
		if common.Unmarshal([]byte(key), &credentials) != nil {
			return errors.New("invalid script OAuth credentials")
		}
		token, err := vertexcore.AcquireAccessToken(credentials, proxy)
		if err != nil {
			return errors.New("script OAuth token acquisition failed")
		}
		entry = cachedAuth{header: "Bearer " + token, projectID: credentials.ProjectID, expiresAt: time.Now().Add(25 * time.Minute)}
		authCache.Lock()
		if len(authCache.entries) >= 64 {
			clear(authCache.entries)
		}
		authCache.entries[fingerprint] = entry
		authCache.Unlock()
	}
	target["auth"] = map[string]any{"authHeader": entry.header, "projectId": entry.projectID}
	target["authHeader"] = entry.header
	return nil
}
