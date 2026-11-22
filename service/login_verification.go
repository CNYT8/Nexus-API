package service

import (
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
)

const pendingLoginFlowKey = "pending_login_flow"
const pendingLoginProviderKey = "pending_login_provider"

func BeginLoginVerification(c *gin.Context, user *model.User, provider string) error {
	binding, err := AuthBrowserBinding(c)
	if err != nil {
		return err
	}
	secret, err := model.CreateAuthFlow(model.AuthFlowMatch{Purpose: "2fa_login", Provider: provider, Binding: binding, UserId: user.Id}, "", 5*time.Minute)
	if err != nil {
		return err
	}
	session := sessions.Default(c)
	session.Set(pendingLoginFlowKey, secret)
	session.Set(pendingLoginProviderKey, provider)
	session.Set("pending_user_id", user.Id)
	return session.Save()
}

func PendingLoginUser(c *gin.Context, consume bool) (*model.User, error) {
	session := sessions.Default(c)
	secret, _ := session.Get(pendingLoginFlowKey).(string)
	provider, _ := session.Get(pendingLoginProviderKey).(string)
	id, _ := session.Get("pending_user_id").(int)
	binding, _ := session.Get(authBrowserKey).(string)
	match := model.AuthFlowMatch{Purpose: "2fa_login", Provider: provider, Binding: binding, UserId: id}
	if id <= 0 || provider == "" {
		return nil, model.ErrAuthStateInvalid
	}
	var flow *model.AuthFlow
	var err error
	if consume {
		flow, err = model.ConsumeAuthFlow(secret, match)
	} else {
		flow, err = model.GetAuthFlow(secret, match)
	}
	if err != nil {
		return nil, err
	}
	user, err := model.GetUserById(id, false)
	if err != nil {
		return nil, err
	}
	if user.AuthVersion != flow.AuthVersion {
		return nil, model.ErrAuthStateInvalid
	}
	if consume {
		session.Delete(pendingLoginFlowKey)
		session.Delete(pendingLoginProviderKey)
		session.Delete("pending_user_id")
		session.Delete("pending_username")
		if err := session.Save(); err != nil {
			return nil, err
		}
	}
	return user, nil
}
