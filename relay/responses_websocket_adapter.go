package relay

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

type responsesWSConnection struct {
	channelID   int
	key         string
	keyIndex    int
	fingerprint [32]byte
}

func responsesWSChannelSupported(ch *model.Channel) bool {
	return service.ChannelSupportsResponsesWebSocket(ch)
}

func responsesWSChannelFingerprint(ch *model.Channel) [32]byte {
	// All configuration that affects handshake/routing/policy is frozen to the
	// physical connection. Role, user and token fields are deliberately absent.
	data, _ := json.Marshal([]any{ch.Type, ch.GetBaseURL(), ch.OpenAIOrganization, ch.Other, ch.ModelMapping, ch.HeaderOverride, ch.GetParamOverride(), ch.GetSetting(), ch.GetOtherSettings()})
	return sha256.Sum256(data)
}

func responsesWSKeyEnabled(ch *model.Channel, key string, index int) bool {
	if key == "" {
		return false
	}
	if !ch.ChannelInfo.IsMultiKey {
		return ch.Key == key
	}
	keys := ch.GetKeys()
	if index < 0 || index >= len(keys) || keys[index] != key {
		return false
	}
	status, exists := ch.ChannelInfo.MultiKeyStatusList[index]
	return !exists || status == common.ChannelStatusEnabled
}

func responsesWSCheckModelPermission(c *gin.Context, modelName string) *types.NewAPIError {
	if strings.TrimSpace(modelName) == "" {
		return newResponsesWSInvalidRequestError(errors.New("model is required"))
	}
	if common.GetContextKeyBool(c, constant.ContextKeyTokenModelLimitEnabled) {
		value, _ := common.GetContextKey(c, constant.ContextKeyTokenModelLimit)
		allowed, _ := value.(map[string]bool)
		if _, ok := allowed[ratio_setting.FormatMatchingModelName(modelName)]; !ok {
			return types.NewErrorWithStatusCode(errors.New("token does not permit this model"), types.ErrorCodeAccessDenied, http.StatusForbidden, types.ErrOptionWithSkipRetry())
		}
	}
	return nil
}

func (s *responsesWSSession) selectChannel(c *gin.Context, modelName string) (*responsesWSConnection, *types.NewAPIError) {
	deny := func(message string) (*responsesWSConnection, *types.NewAPIError) {
		return nil, types.NewErrorWithStatusCode(errors.New(message), types.ErrorCodeGetChannelFailed, http.StatusForbidden, types.ErrOptionWithSkipRetry())
	}
	if apiErr := responsesWSCheckModelPermission(c, modelName); apiErr != nil {
		return nil, apiErr
	}
	connection := s.connection
	requestedGroup := common.GetContextKeyString(c, constant.ContextKeyUsingGroup)
	if connection == nil {
		middleware.Distribute()(c)
		if c.IsAborted() {
			status := c.Writer.Status()
			if status < 400 {
				status = http.StatusServiceUnavailable
			}
			return nil, types.NewErrorWithStatusCode(errors.New("no permitted Responses WebSocket channel is available"), types.ErrorCodeGetChannelFailed, status, types.ErrOptionWithSkipRetry())
		}
		connection = &responsesWSConnection{
			channelID: common.GetContextKeyInt(c, constant.ContextKeyChannelId),
			key:       common.GetContextKeyString(c, constant.ContextKeyChannelKey),
			keyIndex:  common.GetContextKeyInt(c, constant.ContextKeyChannelMultiKeyIndex),
		}
	}
	ch, err := model.GetChannelById(connection.channelID, true)
	if err != nil || !responsesWSChannelSupported(ch) {
		return deny("channel no longer permits native Responses WebSocket")
	}
	if !responsesWSKeyEnabled(ch, connection.key, connection.keyIndex) {
		return deny("channel credential has changed or is disabled")
	}
	fingerprint := responsesWSChannelFingerprint(ch)
	if s.connection != nil && s.connection.fingerprint != fingerprint {
		return deny("channel configuration changed; reconnect to continue")
	}
	if value := common.GetContextKeyString(c, constant.ContextKeyTokenSpecificChannelId); value != "" {
		id, err := strconv.Atoi(value)
		if err != nil || id != ch.Id {
			return deny("token no longer permits this channel")
		}
	}
	if !slices.Contains(ch.GetModels(), modelName) && !slices.Contains(ch.GetModels(), ratio_setting.FormatMatchingModelName(modelName)) {
		return deny("channel no longer permits this model")
	}
	group := common.GetContextKeyString(c, constant.ContextKeyUsingGroup)
	if requestedGroup == "auto" {
		allowedGroups := service.GetRequestAutoGroups(c, common.GetContextKeyString(c, constant.ContextKeyUserGroup))
		if group != "auto" && !slices.Contains(allowedGroups, group) {
			return deny("token no longer permits this automatic group")
		}
		if group == "auto" {
			group = ""
		}
		for _, candidate := range allowedGroups {
			if group == "" && slices.Contains(ch.GetGroups(), candidate) {
				group = candidate
				break
			}
		}
		if group != "" {
			common.SetContextKey(c, constant.ContextKeyAutoGroup, group)
		}
	}
	if group == "" || !slices.Contains(ch.GetGroups(), group) {
		return deny("channel no longer permits this group")
	}
	common.SetContextKey(c, constant.ContextKeyUsingGroup, group)
	// Refresh channel metadata while retaining exactly the credential used for
	// the handshake. Do not rotate a multi-key channel on a connected socket.
	selected := *ch
	selected.Key, selected.Keys = connection.key, nil
	selected.ChannelInfo.IsMultiKey = false
	if apiErr := middleware.SetupContextForSelectedChannel(c, &selected, modelName); apiErr != nil {
		return nil, apiErr
	}
	common.SetContextKey(c, constant.ContextKeyChannelIsMultiKey, ch.ChannelInfo.IsMultiKey)
	common.SetContextKey(c, constant.ContextKeyChannelMultiKeyIndex, connection.keyIndex)
	c.Set("use_channel", []string{strconv.Itoa(ch.Id)})
	return &responsesWSConnection{channelID: ch.Id, key: connection.key, keyIndex: connection.keyIndex, fingerprint: fingerprint}, nil
}

func (s *responsesWSSession) prepareCall(c *gin.Context, create responsesWSCreateRequest, info *relaycommon.RelayInfo) ([]byte, *responsesWSConnection, *types.NewAPIError) {
	if !responsesWSBillingReady() {
		return nil, nil, types.NewErrorWithStatusCode(errors.New("Responses WebSocket billing is not ready"), types.ErrorCodeUpdateDataError, http.StatusServiceUnavailable, types.ErrOptionWithSkipRetry())
	}
	connection, apiErr := s.selectChannel(c, create.Request.Model)
	if apiErr != nil {
		return nil, nil, apiErr
	}
	info.UsingGroup = common.GetContextKeyString(c, constant.ContextKeyUsingGroup)
	info.InitChannelMeta(c)
	meta := &types.TokenCountMeta{}
	if create.Request.MaxOutputTokens != nil {
		meta.MaxTokens = int(*create.Request.MaxOutputTokens)
	}
	if constant.CountToken || setting.ShouldCheckPromptSensitive() {
		meta = create.Request.GetTokenCountMeta()
	}
	if setting.ShouldCheckPromptSensitive() && meta != nil {
		if contains, _ := service.CheckSensitiveText(meta.CombineText); contains {
			common.SetContextKey(c, constant.ContextKeyAdminRejectReason, "input_sensitive")
			return nil, connection, types.NewErrorWithStatusCode(errors.New("prompt contains sensitive content"), types.ErrorCodeSensitiveWordsDetected, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
		}
	}
	tokens, err := service.EstimateRequestToken(c, meta, info)
	if err != nil {
		return nil, connection, types.NewError(err, types.ErrorCodeCountTokenFailed, types.ErrOptionWithSkipRetry())
	}
	info.SetEstimatePromptTokens(tokens)
	price, err := helper.ModelPriceHelper(c, info, tokens, meta)
	if err != nil {
		return nil, connection, types.NewErrorWithStatusCode(err, types.ErrorCodeModelPriceError, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}
	if !price.FreeModel {
		if apiErr = service.PreConsumeBilling(c, price.QuotaToPreConsume, info); apiErr != nil {
			return nil, connection, apiErr
		}
	}
	if apiErr = service.PrepareTieredBillingForSelectedGroup(c, info); apiErr != nil {
		return nil, connection, apiErr
	}
	payload, apiErr := prepareResponsesWSPayload(c, create, info)
	return payload, connection, apiErr
}

func prepareResponsesWSPayload(c *gin.Context, create responsesWSCreateRequest, info *relaycommon.RelayInfo) ([]byte, *types.NewAPIError) {
	request, err := common.DeepCopy(&create.Request)
	if err != nil {
		return nil, newResponsesWSInvalidRequestError(err)
	}
	if err = helper.ModelMappedHelper(c, info, request); err != nil {
		return nil, types.NewError(err, types.ErrorCodeChannelModelMappedError, types.ErrOptionWithSkipRetry())
	}
	adaptor := GetAdaptor(info.ApiType)
	if adaptor == nil {
		return nil, types.NewError(errors.New("invalid channel API type"), types.ErrorCodeInvalidApiType, types.ErrOptionWithSkipRetry())
	}
	adaptor.Init(info)
	data := create.Body
	if !model_setting.GetGlobalSettings().PassThroughRequestEnabled && !info.ChannelSetting.PassThroughBodyEnabled {
		converted, convertErr := adaptor.ConvertOpenAIResponsesRequest(c, info, *request)
		if convertErr != nil {
			return nil, types.NewError(convertErr, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
		}
		relaycommon.AppendRequestConversionFromRequest(info, converted)
		data, err = json.Marshal(converted)
		if err == nil {
			data, err = relaycommon.RemoveDisabledFields(data, info.ChannelOtherSettings, false)
		}
		if err == nil && len(info.ParamOverride) > 0 {
			data, err = relaycommon.ApplyParamOverrideWithRelayInfo(data, info)
			if err != nil {
				return nil, newAPIErrorFromParamOverride(err)
			}
		}
		if finalizer, ok := adaptor.(channel.ResponsesRequestFinalizer); err == nil && ok {
			data, err = finalizer.FinalizeResponsesRequest(info, data)
		}
	}
	if err == nil {
		data, err = buildResponsesWSCreateEvent(data, create.Generate, create.StreamID)
	}
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
	}
	return data, nil
}

func responsesWSHandshake(c *gin.Context, info *relaycommon.RelayInfo) (string, http.Header, error) {
	adaptor := GetAdaptor(info.ApiType)
	if adaptor == nil {
		return "", nil, errors.New("invalid channel API type")
	}
	adaptor.Init(info)
	target, err := adaptor.GetRequestURL(info)
	if err != nil {
		return "", nil, err
	}
	endpoint, err := url.Parse(target)
	if err != nil {
		return "", nil, err
	}
	switch endpoint.Scheme {
	case "https":
		endpoint.Scheme = "wss"
	case "http":
		endpoint.Scheme = "ws"
	case "ws", "wss":
	default:
		return "", nil, errors.New("unsupported upstream websocket scheme")
	}
	if endpoint.Host == "" || endpoint.User != nil || endpoint.Fragment != "" {
		return "", nil, errors.New("invalid upstream websocket URL")
	}
	headers := make(http.Header)
	if err := adaptor.SetupRequestHeader(c, &headers, info); err != nil {
		return "", nil, err
	}
	override, err := channel.ResolveHeaderOverride(info, c)
	if err != nil {
		return "", nil, err
	}
	for name, value := range override {
		headers.Set(name, value)
	}
	for _, name := range []string{"Connection", "Upgrade", "Sec-WebSocket-Key", "Sec-WebSocket-Version", "Sec-WebSocket-Extensions", "Sec-WebSocket-Protocol", "Content-Length", "Accept-Encoding"} {
		headers.Del(name)
	}
	if headers.Get("OpenAI-Beta") == "" || headers.Get("OpenAI-Beta") == "responses=experimental" {
		headers.Set("OpenAI-Beta", "responses_websockets=2026-02-06")
	}
	return endpoint.String(), headers, nil
}

func dialResponsesWS(c *gin.Context, info *relaycommon.RelayInfo) (*websocket.Conn, error) {
	target, headers, err := responsesWSHandshake(c, info)
	if err != nil {
		return nil, err
	}
	client, err := service.GetHttpClientWithProxy(info.ChannelSetting.Proxy)
	if err != nil {
		return nil, err
	}
	dialer := *websocket.DefaultDialer
	dialer.HandshakeTimeout = responsesWSWriteTimeout
	if transport, ok := client.Transport.(*http.Transport); ok {
		dialer.Proxy = transport.Proxy
		dialer.NetDialContext = transport.DialContext
		if transport.TLSClientConfig != nil {
			dialer.TLSClientConfig = transport.TLSClientConfig.Clone()
		}
	}
	ws, response, err := dialer.DialContext(c.Request.Context(), target, headers)
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
	return ws, err
}
