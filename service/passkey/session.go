package passkey

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
	webauthn "github.com/go-webauthn/webauthn/webauthn"
)

var errSessionNotFound = errors.New("Passkey 会话不存在或已过期")

func SaveSessionData(c *gin.Context, key string, data *webauthn.SessionData) error {
	session := sessions.Default(c)
	if data == nil {
		session.Delete(key)
		return session.Save()
	}
	payload, err := json.Marshal(data)
	if err != nil {
		return err
	}
	match, err := sessionFlowMatch(c, key)
	if err != nil {
		return err
	}
	secret, err := model.CreateAuthFlow(match, string(payload), 5*time.Minute)
	if err != nil {
		return err
	}
	session.Set(key, secret)
	return session.Save()
}

func PopSessionData(c *gin.Context, key string) (*webauthn.SessionData, error) {
	session := sessions.Default(c)
	secret, _ := session.Get(key).(string)
	if secret == "" {
		return nil, errSessionNotFound
	}
	match, err := sessionFlowMatch(c, key)
	if err != nil {
		return nil, err
	}
	flow, err := model.ConsumeAuthFlow(secret, match)
	if err != nil {
		return nil, errSessionNotFound
	}
	session.Delete(key)
	if err := session.Save(); err != nil {
		return nil, err
	}
	var data webauthn.SessionData
	if err := json.Unmarshal([]byte(flow.Payload), &data); err != nil {
		return nil, err
	}
	if data.Expires.IsZero() || !time.Now().Before(data.Expires) {
		return nil, errSessionNotFound
	}
	return &data, nil
}

func sessionFlowMatch(c *gin.Context, key string) (model.AuthFlowMatch, error) {
	match := model.AuthFlowMatch{Purpose: key, Provider: "passkey"}
	switch key {
	case LoginSessionKey:
		binding, err := service.AuthBrowserBinding(c)
		match.Binding = binding
		return match, err
	case RegistrationSessionKey, VerifySessionKey:
		user, _, err := service.CurrentSessionUser(c)
		if err != nil {
			return match, err
		}
		match.UserId, match.Binding = user.Id, service.SessionBinding(c)
		return match, nil
	default:
		return match, errSessionNotFound
	}
}
