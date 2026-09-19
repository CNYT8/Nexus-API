package service

import (
	"errors"
	"fmt"
	"io"
	"math"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// PrepareImageBillingForRequest reserves the effective outbound image quantity
// before each attempt, including channel retries and parameter overrides. The
// client request body stays frozen; only the independent quantity is refreshed.
//
// The per-attempt ratios are overwritten (never merged) so a failed Ali
// attempt cannot leak its quantity or prompt-extension surcharge into another
// channel. Existing expression pricing remains independent of legacy quantity
// multipliers; this change does not extend the expression language.
func PrepareImageBillingForRequest(c *gin.Context, info *relaycommon.RelayInfo, count int, promptExtend bool) *types.NewAPIError {
	if count < 1 || count > dto.MaxImageN {
		return types.NewErrorWithStatusCode(fmt.Errorf("image quantity must be an integer between 1 and %d", dto.MaxImageN), types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}
	if info == nil {
		return types.NewErrorWithStatusCode(fmt.Errorf("relay info is nil"), types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}
	info.ImageRequestCount = count
	delete(info.PriceData.OtherRatios, "n")
	delete(info.PriceData.OtherRatios, "prompt_extend")
	if info.TieredBillingSnapshot != nil {
		// Token-only tiered images were already reserved from the expression
		// estimate; only the effective quantity is recorded for this attempt.
		return nil
	}

	channelType := 0
	if info.ChannelMeta != nil {
		channelType = info.ChannelType
	}
	quantity := 1
	if info.PriceData.UsePrice || channelType == constant.ChannelTypeAli {
		quantity = count
	}
	info.PriceData.AddOtherRatio("n", float64(quantity))
	extensionRatio := 1.0
	if channelType == constant.ChannelTypeAli && promptExtend && strings.Contains(info.UpstreamModelName, "z-image") {
		extensionRatio = common.ZImagePromptExtendMultiplier
	}
	info.PriceData.AddOtherRatio("prompt_extend", extensionRatio)

	base := info.ImageQuotaBeforeGroup
	if info.PriceData.UsePrice {
		base = info.PriceData.ModelPrice * common.QuotaPerUnit
	}
	groupRatio := info.PriceData.GroupRatioInfo.GroupRatio
	if base < 0 || math.IsNaN(base) || math.IsInf(base, 0) || groupRatio < 0 || math.IsNaN(groupRatio) || math.IsInf(groupRatio, 0) {
		return types.NewErrorWithStatusCode(fmt.Errorf("invalid image billing price or group ratio"), types.ErrorCodeModelPriceError, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}
	for _, ratio := range info.PriceData.OtherRatios {
		if ratio <= 0 || math.IsNaN(ratio) || math.IsInf(ratio, 0) {
			return types.NewErrorWithStatusCode(fmt.Errorf("invalid image billing multiplier"), types.ErrorCodeModelPriceError, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
		}
	}
	quota, err := common.QuotaFromFloatStrict(info.PriceData.ApplyOtherRatiosToFloat(base * groupRatio))
	if err != nil {
		return types.NewErrorWithStatusCode(err, types.ErrorCodeModelPriceError, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}
	info.PriceData.QuotaToPreConsume = quota
	if quota == 0 && info.Billing == nil {
		// Free groups keep their existing no-reservation behavior.
		return nil
	}
	info.PriceData.FreeModel = false
	if info.Billing == nil {
		return PreConsumeBilling(c, quota, info)
	}
	if err := info.Billing.Reserve(quota); err != nil {
		var apiErr *types.NewAPIError
		if errors.As(err, &apiErr) {
			return apiErr
		}
		return types.NewErrorWithStatusCode(err, types.ErrorCodeInsufficientUserQuota, http.StatusForbidden, types.ErrOptionWithSkipRetry())
	}
	info.FinalPreConsumedQuota = info.Billing.GetPreConsumedQuota()
	if info.FinalPreConsumedQuota < quota {
		return types.NewErrorWithStatusCode(fmt.Errorf("image reservation is below required quota"), types.ErrorCodeInsufficientUserQuota, http.StatusForbidden, types.ErrOptionWithSkipRetry())
	}
	return nil
}

// ImageBillingRequestFromJSON also resolves the quantity fields emitted by
// existing image protocol converters. Missing top-level n must not reduce a
// converted Imagen/Replicate multi-image request to a single-image reserve.
func ImageBillingRequestFromJSON(data []byte, channelType int) (*dto.ImageRequest, error) {
	request, err := dto.ImageBillingRequestFromJSON(data)
	if err != nil {
		return nil, err
	}
	if _, err := request.ImageCount(channelType == constant.ChannelTypeAli); err != nil {
		return nil, err
	}
	path := ""
	switch channelType {
	case constant.ChannelTypeGemini, constant.ChannelTypeVertexAi:
		path = "parameters.sampleCount"
	case constant.ChannelTypeReplicate:
		path = "input.num_outputs"
	}
	if path != "" {
		value := gjson.GetBytes(data, path)
		if value.Exists() && value.Raw != "null" {
			var n uint
			if err := common.Unmarshal([]byte(value.Raw), &n); err != nil || n == 0 || n > dto.MaxImageN {
				return nil, fmt.Errorf("%s must be an integer between 1 and %d", path, dto.MaxImageN)
			}
			request.N = &n
		}
	}
	return request, nil
}

// ImageBillingRequestFromMultipart inspects the final outgoing body using an
// independent reader. It never buffers image files or consumes the send reader.
func ImageBillingRequestFromMultipart(body io.Reader, contentType string) (*dto.ImageRequest, error) {
	mediaType, parameters, err := mime.ParseMediaType(contentType)
	if err != nil || mediaType != "multipart/form-data" || parameters["boundary"] == "" {
		return nil, fmt.Errorf("invalid image multipart content type")
	}
	reader := multipart.NewReader(body, parameters["boundary"])
	values := make(map[string][]string)
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("invalid image multipart body: %w", err)
		}
		name := part.FormName()
		if name == "n" || name == "parameters" {
			if part.FileName() != "" {
				return nil, fmt.Errorf("image %s must be a scalar field", name)
			}
			const maxScalarSize = 64 << 10
			value, err := io.ReadAll(io.LimitReader(part, maxScalarSize+1))
			if err != nil || len(value) > maxScalarSize {
				return nil, fmt.Errorf("invalid or oversized image %s field", name)
			}
			values[name] = append(values[name], string(value))
		}
		if _, err := io.Copy(io.Discard, part); err != nil {
			return nil, fmt.Errorf("invalid image multipart part: %w", err)
		}
		if err := part.Close(); err != nil {
			return nil, err
		}
	}
	return dto.ImageBillingRequestFromForm(values)
}
