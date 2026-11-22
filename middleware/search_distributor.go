package middleware

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
)

func getSearchModelRequest(c *gin.Context) (*ModelRequest, error) {
	request, err := helper.ReadSearchRequest(c)
	if err != nil {
		return nil, err
	}
	return &ModelRequest{Model: request.Model}, nil
}

// Specific-channel tokens must not bypass model restrictions on these routes.
func searchModelAllowed(c *gin.Context, model string) bool {
	if !common.GetContextKeyBool(c, constant.ContextKeyTokenModelLimitEnabled) {
		return true
	}
	raw, exists := common.GetContextKey(c, constant.ContextKeyTokenModelLimit)
	if !exists {
		return false
	}
	limits, ok := raw.(map[string]bool)
	if !ok {
		return false
	}
	_, allowed := limits[ratio_setting.FormatMatchingModelName(model)]
	return allowed
}
