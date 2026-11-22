package controller

import (
	"errors"
	"net/http"

	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

func getRelayRequestWithSearch(c *gin.Context, format types.RelayFormat) (dto.Request, error) {
	searchFormat := format == types.RelayFormatOpenAIAlphaSearch || format == types.RelayFormatDeepSeekTools
	if !searchFormat && !dto.IsSearchPath(c.Request.URL.Path) {
		return helper.GetAndValidateRequest(c, format)
	}
	if !searchFormat || format == types.RelayFormatOpenAIAlphaSearch && c.Request.URL.Path != dto.AlphaSearchPath ||
		format == types.RelayFormatDeepSeekTools && c.Request.URL.Path != dto.DeepSeekSearchPath && c.Request.URL.Path != dto.DeepSeekFetchPath {
		return nil, errors.New("search format does not match endpoint")
	}
	return helper.ReadSearchRequest(c)
}

func genRelayInfoWithSearch(c *gin.Context, format types.RelayFormat, request dto.Request, ws *websocket.Conn) (*relaycommon.RelayInfo, error) {
	if search, ok := request.(*dto.SearchRequest); ok {
		return relaycommon.GenRelayInfoSearch(c, search), nil
	}
	return relaycommon.GenRelayInfo(c, format, request, ws)
}

func relayPriceWithSearch(c *gin.Context, info *relaycommon.RelayInfo, tokens int, meta *types.TokenCountMeta) (types.PriceData, error) {
	if _, ok := info.Request.(*dto.SearchRequest); ok {
		return helper.SearchPriceHelper(c, info)
	}
	return helper.ModelPriceHelper(c, info, tokens, meta)
}

// Reserve the selected group's full call price before each attempt. The unit
// price stays frozen; retrying cannot double-charge or use an earlier discount.
func prepareSearchBillingForSelectedGroup(c *gin.Context, info *relaycommon.RelayInfo) *types.NewAPIError {
	if _, ok := info.Request.(*dto.SearchRequest); !ok {
		return nil
	}
	price, err := helper.RefreshSearchPriceForGroup(c, info)
	if err != nil {
		return types.NewErrorWithStatusCode(err, types.ErrorCodeModelPriceError, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}
	if info.Billing == nil {
		if price.FreeModel {
			return nil
		}
		return service.PreConsumeBilling(c, price.QuotaToPreConsume, info)
	}
	if err := info.Billing.Reserve(price.QuotaToPreConsume); err != nil {
		return types.NewErrorWithStatusCode(err, types.ErrorCodeUpdateDataError, http.StatusForbidden, types.ErrOptionWithSkipRetry())
	}
	info.FinalPreConsumedQuota = info.Billing.GetPreConsumedQuota()
	return nil
}
