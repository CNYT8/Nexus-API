package service

import (
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
)

const AuthSessionKey = "auth_session_secret"
const authBrowserKey = "auth_browser_binding"

func CurrentSessionUser(c *gin.Context) (*model.User, *model.UserSession, error) {
	session := sessions.Default(c)
	secret, _ := session.Get(AuthSessionKey).(string)
	id, _ := session.Get("id").(int)
	if secret != "" {
		return model.ValidateUserSession(secret, id)
	}
	// The session store has already checked the cookie signature and lifetime.
	// Preserve existing logins without trusting their cached role/group fields.
	username, _ := session.Get("username").(string)
	if id <= 0 || username == "" {
		return nil, nil, model.ErrAuthStateInvalid
	}
	user, err := model.GetUserById(id, false)
	if err != nil {
		return nil, nil, err
	}
	if user.Username != username || user.AuthVersion != 1 || user.Status != common.UserStatusEnabled {
		return nil, nil, model.ErrAuthStateInvalid
	}
	signedCookie, err := c.Cookie("session")
	if err != nil {
		return nil, nil, model.ErrAuthStateInvalid
	}
	secret, err = model.AdoptLegacyUserSession(user, signedCookie, c.ClientIP(), c.Request.UserAgent())
	if err != nil {
		return nil, nil, err
	}
	user, record, err := model.ValidateUserSession(secret, id)
	if err != nil {
		return nil, nil, err
	}
	session.Set(AuthSessionKey, secret)
	if err := session.Save(); err != nil {
		session.Delete(AuthSessionKey)
		return nil, nil, err
	}
	return user, record, nil
}

func AuthBrowserBinding(c *gin.Context) (string, error) {
	session := sessions.Default(c)
	if binding, ok := session.Get(authBrowserKey).(string); ok && binding != "" {
		return binding, nil
	}
	binding, err := model.NewAuthSecret()
	if err != nil {
		return "", err
	}
	session.Set(authBrowserKey, binding)
	if err := session.Save(); err != nil {
		return "", err
	}
	return binding, nil
}

func SessionBinding(c *gin.Context) string {
	secret, _ := sessions.Default(c).Get(AuthSessionKey).(string)
	return secret
}

func HasFreshSessionVerification(c *gin.Context, method string, age time.Duration) bool {
	_, record, err := CurrentSessionUser(c)
	if err != nil {
		return false
	}
	now := time.Now().Unix()
	return record.VerifiedAt > 0 && record.VerifiedAt <= now && now-record.VerifiedAt < int64(age/time.Second) &&
		(method == "" || method == record.VerifiedMethod)
}

func MarkSessionVerified(c *gin.Context, method string) (int64, error) {
	id, _ := sessions.Default(c).Get("id").(int)
	return model.MarkUserSessionVerified(SessionBinding(c), id, method)
}
