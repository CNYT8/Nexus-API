package common

import (
	"github.com/QuantumNous/new-api/dto"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
)

func GenRelayInfoSearch(c *gin.Context, request *dto.SearchRequest) *RelayInfo {
	info := GenRelayInfoOpenAI(c, request)
	info.OriginModelName = request.Model
	info.RelayMode = relayconstant.Path2RelayMode(request.Path)
	info.IsStream = false
	info.RelayFormat = types.RelayFormatDeepSeekTools
	if request.Path == dto.AlphaSearchPath {
		info.RelayFormat = types.RelayFormatOpenAIAlphaSearch
	}
	info.InitRequestConversionChain()
	return info
}
