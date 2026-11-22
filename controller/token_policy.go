package controller

import (
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
)

type tokenPolicyRequest struct {
	model.Token
	AutoGroups          *[]string `json:"auto_groups"`
	AllowUserQuotaQuery *bool     `json:"allow_user_quota_query"`
}

type tokenPolicyResponse struct {
	*model.Token
	AutoGroups []string `json:"auto_groups"`
}

func applyTokenPolicy(token *model.Token, request tokenPolicyRequest, userID int, creating bool) error {
	if creating {
		token.AllowUserQuotaQuery = true
	}
	if request.AllowUserQuotaQuery != nil {
		token.AllowUserQuotaQuery = *request.AllowUserQuotaQuery
	}
	if token.Group != "auto" {
		if request.AutoGroups != nil && len(*request.AutoGroups) != 0 {
			return fmt.Errorf("auto_groups requires the auto group")
		}
		return token.SetAutoGroups(nil)
	}
	if request.AutoGroups == nil {
		return nil
	}
	if len(*request.AutoGroups) > 32 {
		return fmt.Errorf("auto_groups supports at most 32 groups")
	}
	user, err := model.GetUserById(userID, false)
	if err != nil {
		return err
	}
	allowed := map[string]bool{}
	for _, group := range service.GetUserAutoGroup(user.Group) {
		allowed[group] = true
	}
	seen := map[string]bool{}
	groups := make([]string, 0, len(*request.AutoGroups))
	for _, group := range *request.AutoGroups {
		if group == "" || strings.TrimSpace(group) != group || !allowed[group] || seen[group] {
			return fmt.Errorf("invalid or unavailable auto group: %s", group)
		}
		seen[group] = true
		groups = append(groups, group)
	}
	return token.SetAutoGroups(groups)
}
