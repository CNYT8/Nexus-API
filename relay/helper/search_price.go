package helper

import (
	"errors"
	"fmt"
	"math"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
)

func SearchPriceHelper(c *gin.Context, info *relaycommon.RelayInfo) (types.PriceData, error) {
	request, ok := info.Request.(*dto.SearchRequest)
	if !ok {
		return types.PriceData{}, errors.New("invalid search request")
	}
	// Search has no token usage. Never silently evaluate a token expression or
	// add a conservative missing-usage floor to the known per-call charge.
	if billing_setting.GetBillingMode(info.OriginModelName) == billing_setting.BillingModeTieredExpr {
		return types.PriceData{}, errors.New("search endpoints require per-call pricing, not tiered token pricing")
	}
	var price float64
	if request.Path == dto.AlphaSearchPath {
		price = operation_setting.GetToolPriceForModel("web_search_preview", info.OriginModelName) / 1000
	} else {
		var configured bool
		price, configured = ratio_setting.GetModelPrice(info.OriginModelName, false)
		if !configured {
			return types.PriceData{}, fmt.Errorf("configure an explicit per-call price for %s before enabling this endpoint", info.OriginModelName)
		}
	}
	data, err := searchPriceData(price, HandleGroupRatio(c, info))
	if err == nil {
		info.PriceData = data
	}
	return data, err
}

func RefreshSearchPriceForGroup(c *gin.Context, info *relaycommon.RelayInfo) (types.PriceData, error) {
	data, err := searchPriceData(info.PriceData.ModelPrice, HandleGroupRatio(c, info))
	if err == nil {
		info.PriceData = data
	}
	return data, err
}

func searchPriceData(price float64, group types.GroupRatioInfo) (types.PriceData, error) {
	if price < 0 || math.IsNaN(price) || math.IsInf(price, 0) || group.GroupRatio < 0 || math.IsNaN(group.GroupRatio) || math.IsInf(group.GroupRatio, 0) {
		return types.PriceData{}, errors.New("invalid search price or group ratio")
	}
	quota, err := common.QuotaRoundStrict(price * group.GroupRatio * common.QuotaPerUnit)
	if err != nil {
		return types.PriceData{}, err
	}
	free := price == 0 || group.GroupRatio == 0
	if !free && quota == 0 {
		quota = 1
	}
	return types.PriceData{
		FreeModel: free, ModelPrice: price, UsePrice: true,
		Quota: quota, QuotaToPreConsume: quota, GroupRatioInfo: group,
	}, nil
}
