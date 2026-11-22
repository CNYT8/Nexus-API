package relay

import (
	"errors"
	"io"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
)

// Full responses must be buffered for lossless output-policy checks. Bound
// this independently of client input limits so a tool cannot exhaust memory.
const maxSearchResponseBytes int64 = 32 << 20

func AlphaSearchHelper(c *gin.Context, info *relaycommon.RelayInfo) *types.NewAPIError {
	return searchRelay(c, info, dto.AlphaSearchPath)
}

func searchRelay(c *gin.Context, info *relaycommon.RelayInfo, path string) *types.NewAPIError {
	request, ok := info.Request.(*dto.SearchRequest)
	if !ok || request.Path != path || c.Request.URL.Path != path {
		return types.NewErrorWithStatusCode(errors.New("invalid search request"), types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}
	info.InitChannelMeta(c)
	if !searchChannelSupported(info.ChannelType, path) {
		// Retry on another configured channel; do not expand the generic adaptor
		// interface or force unsupported providers to pretend to support search.
		return types.NewError(errors.New("channel does not support search endpoint"), types.ErrorCodeInvalidApiType)
	}
	if !info.PriceData.UsePrice || info.PriceData.Quota < 0 || (info.PriceData.Quota > 0 || info.FinalPreConsumedQuota > 0) && info.Billing == nil {
		return types.NewErrorWithStatusCode(errors.New("search billing must be prepared before dispatch"), types.ErrorCodeModelPriceError, http.StatusInternalServerError, types.ErrOptionWithSkipRetry())
	}
	if err := helper.ModelMappedHelper(c, info, request); err != nil {
		return types.NewError(err, types.ErrorCodeChannelModelMappedError, types.ErrOptionWithSkipRetry())
	}
	body, err := request.UpstreamBody(info.UpstreamModelName, info.IsModelMapped)
	if err != nil {
		return types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
	}
	if len(info.ParamOverride) > 0 {
		body, err = relaycommon.ApplyParamOverrideWithRelayInfo(body, info)
		if err != nil {
			return newAPIErrorFromParamOverride(err)
		}
	}
	// An administrator override may change fields but cannot turn this bounded
	// non-streaming endpoint into SSE or a malformed request.
	if _, err := dto.ParseSearchRequest(path, body); err != nil {
		return types.NewError(err, types.ErrorCodeChannelParamOverrideInvalid, types.ErrOptionWithSkipRetry())
	}
	adaptor := GetAdaptor(info.ApiType)
	if adaptor == nil {
		return types.NewError(errors.New("search adaptor is unavailable"), types.ErrorCodeInvalidApiType)
	}
	adaptor.Init(info)
	response, err := doSearchRequest(c, adaptor, info, path, body)
	if err != nil {
		return types.NewErrorWithStatusCode(err, types.ErrorCodeDoRequestFailed, http.StatusBadGateway, types.ErrOptionWithHideErrMsg("search upstream request failed"))
	}
	if response == nil || response.Body == nil {
		return types.NewErrorWithStatusCode(errors.New("empty search upstream response"), types.ErrorCodeBadResponse, http.StatusBadGateway)
	}
	defer service.CloseResponseBodyGracefully(response)
	info.SetFirstResponseTime()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		// Error bodies are untrusted too. Retain the original closer while
		// bounding the existing error parser, logging and status mapping path.
		response.Body = struct {
			io.Reader
			io.Closer
		}{io.LimitReader(response.Body, maxSearchResponseBytes), response.Body}
		apiErr := service.RelayErrorHandler(c.Request.Context(), response, false)
		service.ResetStatusCode(apiErr, c.GetString("status_code_mapping"))
		return apiErr
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxSearchResponseBytes+1))
	if err != nil || int64(len(data)) > maxSearchResponseBytes {
		return types.NewErrorWithStatusCode(errors.New("search response is unreadable or exceeds the 32 MiB limit"), types.ErrorCodeReadResponseBodyFailed, http.StatusBadGateway)
	}
	billable, err := searchResultBillable(path, data)
	if err != nil {
		return types.NewErrorWithStatusCode(err, types.ErrorCodeBadResponseBody, http.StatusBadGateway)
	}
	cleaned, matched, err := sanitizeSearchResponse(data, info)
	if err != nil {
		return types.NewErrorWithStatusCode(err, types.ErrorCodeBadResponseBody, http.StatusBadGateway)
	}
	if matched {
		common.SetContextKey(c, constant.ContextKeyAdminRejectReason, "output_sensitive")
		if info.OutputSensitiveConfig.Action == "error" {
			return types.NewError(relaycommon.OutputSensitiveError(), types.ErrorCodeSensitiveWordsDetected, types.ErrOptionWithSkipRetry())
		}
	}
	// Successful upstream work is finalized before writing. A disconnected client
	// or settlement retry must not cause another upstream call or a refund.
	service.PostSearchConsumeQuota(c, info, billable)
	if response.Header == nil {
		response.Header = make(http.Header)
	}
	response.Header.Set("Content-Type", "application/json")
	if matched || info.ThinkingProcessStrip {
		response.Header.Del("ETag")
		response.Header.Del("Content-MD5")
		response.Header.Del("Digest")
	}
	service.IOCopyBytesGracefully(c, response, cleaned)
	return nil
}
