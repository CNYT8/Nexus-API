package controller

import (
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
)

func oauthFlowMatch(c *gin.Context, provider string) (model.AuthFlowMatch, error) {
	binding, err := service.AuthBrowserBinding(c)
	if err != nil {
		return model.AuthFlowMatch{}, err
	}
	match := model.AuthFlowMatch{Purpose: "oauth_login", Provider: provider, Binding: binding}
	if sessions.Default(c).Get("id") != nil {
		user, _, err := service.CurrentSessionUser(c)
		if err != nil || !service.HasFreshSessionVerification(c, "", 5*time.Minute) {
			return model.AuthFlowMatch{}, model.ErrAuthStateInvalid
		}
		match.Purpose, match.UserId, match.Binding = "oauth_bind", user.Id, service.SessionBinding(c)
	}
	return match, nil
}
