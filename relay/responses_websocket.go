package relay

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

const (
	// The distributor must apply the native WS capability filter to every
	// selection source (including affinity and token pins) when this is true.
	ResponsesWebSocketContextKey       = "responses_websocket"
	ResponsesWebSocketInfoContextKey   = "responses_websocket_info"
	ResponsesWebSocketErrorContextKey  = "responses_websocket_error"
	responsesWSEventTypeResponseCreate = "response.create"
	responsesWSWriteTimeout            = 30 * time.Second
)

// ResponsesWSRequestRunner wraps one complete create in fresh auth/rate-limit
// middleware. Its request body is normalized HTTP Responses JSON.
type ResponsesWSRequestRunner func(*http.Request, string, func(*gin.Context) *types.NewAPIError) *types.NewAPIError

type responsesWSCreateEvent struct {
	Type     string          `json:"type"`
	EventID  string          `json:"event_id,omitempty"`
	StreamID json.RawMessage `json:"stream_id,omitempty"`
	Generate json.RawMessage `json:"generate,omitempty"`
	Request  json.RawMessage `json:"response,omitempty"`
}

type responsesWSCreateRequest struct {
	Request  dto.OpenAIResponsesRequest
	Body     []byte
	Generate json.RawMessage
	StreamID string
}

type responsesWSErrorEvent struct {
	Type       string             `json:"type"`
	Status     int                `json:"status"`
	EventID    string             `json:"event_id,omitempty"`
	StreamID   string             `json:"stream_id,omitempty"`
	ResponseID string             `json:"response_id,omitempty"`
	Error      *types.OpenAIError `json:"error"`
}

type responsesWSMessage struct {
	kind int
	body []byte
	err  error
}

type responsesWSControl struct {
	body              []byte
	eventID, streamID string
}

type responsesWSCallState struct {
	inbox       chan responsesWSMessage
	controls    chan responsesWSControl
	done        chan struct{}
	drainDone   func()
	terminal    []responsesWSMessage
	closeCode   int
	closeReason string
}

// Adapted from the upstream Responses WS session: a single worker owns each
// generation and its billing. Readers only deliver bounded messages/controls.
type responsesWSSession struct {
	ctx            context.Context
	cancel         context.CancelFunc
	client         *websocket.Conn
	runner         ResponsesWSRequestRunner
	drain          *common.RequestDrain
	request        *http.Request
	requestID      string
	nextEventIndex int
	workers        sync.WaitGroup
	closeOnce      sync.Once
	clientWriteMu  sync.Mutex
	targetWriteMu  sync.Mutex
	connectionMu   sync.Mutex
	target         *websocket.Conn
	stateMu        sync.Mutex
	current        *responsesWSCallState
	clientBudget   helper.StreamWriteBudget

	// Worker-owned connection metadata only; never restore user/token context.
	lastResponseID string
	connection     *responsesWSConnection
}

func ResponsesWebSocketHelper(c *gin.Context, client *websocket.Conn, runner ResponsesWSRequestRunner, drain *common.RequestDrain, stopping <-chan struct{}) *types.NewAPIError {
	if client == nil || runner == nil || drain == nil || stopping == nil || c.GetInt("id") <= 0 || c.GetInt("token_id") <= 0 {
		return types.NewErrorWithStatusCode(errors.New("authenticated request runner is required"), types.ErrorCodeAccessDenied, http.StatusUnauthorized, types.ErrOptionWithSkipRetry())
	}
	ctx, cancel := context.WithCancel(c.Request.Context())
	s := &responsesWSSession{ctx: ctx, cancel: cancel, client: client, runner: runner, drain: drain, request: c.Request.Clone(ctx), requestID: common.GetUUID()}
	maxMB := constant.MaxRequestBodyMB
	if maxMB <= 0 {
		maxMB = 128
	}
	client.SetReadLimit(int64(maxMB) << 20)
	defer func() {
		s.shutdown(websocket.CloseNormalClosure, "")
		s.workers.Wait()
	}()
	// Drain may have begun between Register and Upgrade. Check before admitting
	// any frames, then keep a watcher to interrupt blocked reads on either peer.
	select {
	case <-stopping:
		s.shutdown(websocket.CloseGoingAway, "server is shutting down")
		return nil
	default:
	}
	s.workers.Add(1)
	go func() {
		defer s.workers.Done()
		select {
		case <-stopping:
			s.shutdown(websocket.CloseGoingAway, "server is shutting down")
		case <-s.ctx.Done():
			s.shutdown(websocket.CloseNormalClosure, "")
		}
	}()
	for {
		kind, message, err := client.ReadMessage()
		if err != nil {
			return nil
		}
		envelope, streamID, err := parseResponsesWSEnvelope(message)
		if kind != websocket.TextMessage && err == nil {
			err = errors.New("websocket events must be text JSON")
		}
		if err != nil {
			s.sendError(envelope.EventID, streamID, newResponsesWSInvalidRequestError(err))
			continue
		}
		if envelope.Type != responsesWSEventTypeResponseCreate {
			state := s.getCurrent()
			if envelope.Type != "response.cancel" || state == nil {
				s.sendError(envelope.EventID, streamID, newResponsesWSInvalidRequestError(errors.New("unsupported websocket control event")))
				continue
			}
			select {
			case state.controls <- responsesWSControl{body: message, eventID: envelope.EventID, streamID: streamID}:
			case <-state.done:
			case <-s.ctx.Done():
				return nil
			default:
				s.sendError(envelope.EventID, streamID, newResponsesWSInvalidRequestError(errors.New("a response control event is already pending")))
			}
			continue
		}
		state := &responsesWSCallState{inbox: make(chan responsesWSMessage), controls: make(chan responsesWSControl, 1), done: make(chan struct{})}
		if !s.tryReserveCurrent(state) {
			select {
			case <-stopping:
				return nil
			case <-s.ctx.Done():
				return nil
			default:
			}
			s.sendError(envelope.EventID, streamID, types.NewErrorWithStatusCode(errors.New("another response.create is already in progress"), types.ErrorCodeInvalidRequest, http.StatusConflict, types.ErrOptionWithSkipRetry()))
			continue
		}
		requestID := fmt.Sprintf("%s-ws-%d", s.requestID, s.nextEventIndex)
		s.nextEventIndex++
		s.workers.Add(1)
		go func() {
			defer s.workers.Done()
			s.runRequest(state, message, envelope, streamID, requestID)
		}()
	}
}

func (s *responsesWSSession) runRequest(state *responsesWSCallState, message []byte, envelope responsesWSCreateEvent, streamID, requestID string) {
	if state.drainDone != nil {
		defer state.drainDone()
	}
	var apiErr *types.NewAPIError
	defer func() {
		if recover() != nil {
			apiErr = types.NewError(errors.New("responses websocket request failed"), types.ErrorCodeBadResponse, types.ErrOptionWithSkipRetry())
			state.closeCode = websocket.CloseInternalServerErr
		}
		outgoing := state.terminal
		if apiErr != nil {
			if body, err := buildResponsesWSErrorPayload(envelope.EventID, streamID, apiErr); err == nil {
				outgoing = []responsesWSMessage{{kind: websocket.TextMessage, body: body}}
			}
		}
		// Hold admission until both settlement and middleware have finished. A
		// client reacting immediately to the terminal cannot race slot release.
		s.clientWriteMu.Lock()
		s.stateMu.Lock()
		for _, event := range outgoing {
			if err := s.client.SetWriteDeadline(s.clientBudget.Deadline(time.Now(), len(event.body))); err != nil {
				state.closeCode = websocket.CloseInternalServerErr
				break
			}
			if err := s.client.WriteMessage(event.kind, event.body); err != nil {
				state.closeCode = websocket.CloseInternalServerErr
				break
			}
		}
		if state.closeCode != 0 {
			s.shutdown(state.closeCode, state.closeReason)
		}
		s.current = nil
		close(state.done)
		s.stateMu.Unlock()
		s.clientWriteMu.Unlock()
	}()
	create, parseErr := normalizeResponsesWSCreateEvent(message, envelope, streamID)
	request := s.request.Clone(s.ctx)
	request.Body = io.NopCloser(bytes.NewReader(create.Body))
	request.ContentLength = int64(len(create.Body))
	apiErr = s.runner(request, requestID, func(c *gin.Context) *types.NewAPIError {
		if parseErr != nil {
			return newResponsesWSInvalidRequestError(parseErr)
		}
		return s.runCall(c, state, create)
	})
	if apiErr != nil && (apiErr.StatusCode == http.StatusUnauthorized || apiErr.StatusCode == http.StatusForbidden) {
		state.closeCode = websocket.ClosePolicyViolation
	}
}

// finish is deliberately worker-only. Set the barrier before invoking callbacks:
// a persistence failure or panic must never turn a settlement into a refund.
type responsesWSFinalizer struct {
	dispatched bool
	rejected   bool
	finished   bool
	settle     func()
	refund     func()
}

func (f *responsesWSFinalizer) finish() {
	if f.finished {
		return
	}
	f.finished = true
	if !f.dispatched || f.rejected {
		f.refund()
	} else {
		f.settle()
	}
}

// Installed only during settlement, after the durable dispatch barrier, so the
// optional MarkDispatched interface on the original session remains intact.
type responsesWSBillingObserver struct {
	relaycommon.BillingSettler
	called bool
	err    error
}

func (b *responsesWSBillingObserver) Settle(quota int) error {
	b.called = true
	b.err = b.BillingSettler.Settle(quota)
	return b.err
}

// RefundSync must wait for durable funding and token refunds, including an
// already-running refund. The ordinary asynchronous Refund cannot satisfy the
// request drain barrier, even when NeedsRefund has already become false.
type responsesWSSynchronousRefunder interface {
	RefundSync(c *gin.Context) error
}

func responsesWSRefundBilling(c *gin.Context, billing relaycommon.BillingSettler) error {
	if billing == nil {
		return nil
	}
	refunder, ok := billing.(responsesWSSynchronousRefunder)
	if !ok {
		return errors.New("Responses WebSocket requires synchronous billing refunds")
	}
	return refunder.RefundSync(c)
}

func responsesWSBillingReady() bool {
	// Test the concrete session contract before any precharge. This temporary
	// integration guard disappears naturally when the funding owner adds the
	// method; do not fall back to an asynchronous refund for WS requests.
	_, ready := any((*service.BillingSession)(nil)).(responsesWSSynchronousRefunder)
	return ready
}

func (s *responsesWSSession) runCall(c *gin.Context, state *responsesWSCallState, create responsesWSCreateRequest) (apiErr *types.NewAPIError) {
	// RecordConsumeLog must use this request flag to finish LogQuotaData inline
	// before Post*ConsumeQuota returns and the request drain registration ends.
	c.Set(ResponsesWebSocketContextKey, true)
	common.SetContextKey(c, constant.ContextKeyOriginalModel, create.Request.Model)
	info := relaycommon.GenRelayInfoResponses(c, &create.Request)
	info.InitRequestConversionChain()
	info.IsStream = true
	common.SetContextKey(c, constant.ContextKeyIsStream, true)
	info.StreamStatus = relaycommon.NewStreamStatus()
	c.Set(ResponsesWebSocketInfoContextKey, info)
	usage := newResponsesWSUsage(info)
	finalizer := &responsesWSFinalizer{
		refund: func() {
			if err := responsesWSRefundBilling(c, info.Billing); err != nil {
				apiErr = types.NewError(errors.New("response billing refund failed"), types.ErrorCodeUpdateDataError, types.ErrOptionWithSkipRetry())
				state.closeCode = websocket.CloseInternalServerErr
			}
		},
		settle: func() {
			result := usage.finish(c)
			original := info.Billing
			observer := &responsesWSBillingObserver{BillingSettler: original}
			if original != nil {
				info.Billing = observer
				defer func() { info.Billing = original }()
			}
			if strings.HasPrefix(info.OriginModelName, "gpt-4o-audio") {
				service.PostAudioConsumeQuota(c, info, result, "")
			} else {
				service.PostTextConsumeQuota(c, info, result, nil)
			}
			if observer.err != nil || original != nil && !observer.called {
				apiErr = types.NewError(errors.New("response billing settlement failed"), types.ErrorCodeUpdateDataError, types.ErrOptionWithSkipRetry())
				state.closeCode = websocket.CloseInternalServerErr
			}
		},
	}
	defer func() {
		if recover() != nil {
			apiErr = types.NewError(errors.New("responses websocket call failed"), types.ErrorCodeBadResponse, types.ErrOptionWithSkipRetry())
			state.closeCode = websocket.CloseInternalServerErr
		}
		finalizer.finish()
	}()
	payload, connection, apiErr := s.prepareCall(c, create, info)
	if apiErr != nil {
		return apiErr
	}
	if s.getTarget() == nil {
		target, err := dialResponsesWS(c, info)
		if err != nil {
			return types.NewErrorWithStatusCode(errors.New("upstream websocket handshake failed"), types.ErrorCodeDoRequestFailed, http.StatusBadGateway, types.ErrOptionWithSkipRetry())
		}
		if !s.setTarget(target) {
			return types.NewError(context.Canceled, types.ErrorCodeBadResponse, types.ErrOptionWithSkipRetry())
		}
		s.connection = connection
		s.startTargetReader(target)
	}
	if s.ctx.Err() != nil {
		return types.NewError(s.ctx.Err(), types.ErrorCodeBadResponse, types.ErrOptionWithSkipRetry())
	}
	if err := service.MarkBillingDispatched(info); err != nil {
		return types.NewError(err, types.ErrorCodeUpdateDataError, types.ErrOptionWithSkipRetry())
	}
	// A failed data write is ambiguous: it may have delivered the create.
	finalizer.dispatched = true
	if err := s.writeTarget(websocket.TextMessage, payload); err != nil {
		state.closeCode = websocket.CloseInternalServerErr
		return types.NewError(errors.New("upstream websocket write failed"), types.ErrorCodeDoRequestFailed, types.ErrOptionWithSkipRetry())
	}
	return s.consume(c, state, create, info, usage, finalizer)
}

func (s *responsesWSSession) consume(c *gin.Context, state *responsesWSCallState, create responsesWSCreateRequest, info *relaycommon.RelayInfo, usage *responsesWSUsage, finalizer *responsesWSFinalizer) *types.NewAPIError {
	output := responsesWSOutput{info: info, context: c}
	timeout := time.Duration(constant.StreamingTimeout) * time.Second
	if timeout <= 0 {
		timeout = 300 * time.Second
	}
	idle := time.NewTimer(timeout)
	defer idle.Stop()
	accepted := false
	var pendingControl, sentControl []byte
	var responseID string
	for {
		select {
		case incoming := <-state.inbox:
			idle.Reset(timeout)
			if incoming.err != nil {
				info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonScannerErr, incoming.err)
				state.closeCode, state.closeReason = websocket.CloseInternalServerErr, "upstream connection closed"
				return nil
			}
			info.SetFirstResponseTime()
			var event struct {
				dto.ResponsesStreamResponse
				StreamID   string `json:"stream_id"`
				ResponseID string `json:"response_id"`
			}
			if incoming.kind != websocket.TextMessage || json.Unmarshal(incoming.body, &event) != nil || event.Type == "" {
				state.closeCode = websocket.CloseInternalServerErr
				return types.NewError(errors.New("invalid upstream websocket event"), types.ErrorCodeBadResponse, types.ErrOptionWithSkipRetry())
			}
			if event.StreamID != "" && event.StreamID != create.StreamID && event.Type != "error" {
				continue
			}
			if event.Response != nil && event.Response.ID != "" && event.Response.ID == s.lastResponseID || event.ResponseID != "" && event.ResponseID == s.lastResponseID {
				continue
			}
			if event.Type != "error" && responseID != "" && event.ResponseID != "" && event.ResponseID != responseID {
				continue
			}
			terminal := responsesWSTerminal(event.Type)
			if event.Type == "error" {
				var rejection responsesWSErrorEvent
				_ = json.Unmarshal(incoming.body, &rejection)
				ends, ambiguous, controlError := responsesWSErrorEndsRequest(rejection, create.StreamID, responseID, sentControl)
				if !ends {
					if controlError {
						sentControl = nil
						if err := s.forwardError(c, incoming, rejection); err != nil {
							s.shutdown(websocket.CloseInternalServerErr, "")
						}
					}
					continue
				}
				upstreamErr := responsesWSRejection(rejection)
				c.Set(ResponsesWebSocketErrorContextKey, upstreamErr)
				if !accepted {
					finalizer.rejected = true
					return upstreamErr
				}
				service.ApplyErrorMask(c, upstreamErr)
				incoming.body, _ = buildResponsesWSErrorPayload(rejection.EventID, create.StreamID, upstreamErr)
				terminal = true
				if ambiguous {
					state.closeCode = websocket.CloseInternalServerErr
				}
			}
			if strings.HasPrefix(event.Type, "response.") {
				if event.Response != nil && event.Response.ID != "" {
					if responseID != "" && responseID != event.Response.ID {
						continue
					}
					responseID = event.Response.ID
				}
				if !accepted {
					service.RecordChannelAffinity(c, info.ChannelId)
				}
				accepted = true
			}
			// Meter the original upstream bytes before stripping or blocking text.
			usage.observe(c, &event.ResponsesStreamResponse, incoming.body)
			frames, err := output.filter(incoming.body)
			if err != nil {
				state.closeCode, state.closeReason = websocket.ClosePolicyViolation, "output policy terminated response"
				if common.GetContextKeyString(c, constant.ContextKeyAdminRejectReason) == "output_sensitive" && info.OutputSensitiveConfig.Action != "error" {
					return nil
				}
				return types.NewError(errors.New("response blocked by output policy"), types.ErrorCodeBadResponse, types.ErrOptionWithSkipRetry())
			}
			if terminal {
				s.lastResponseID = responseID
				info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonDone, nil)
				state.terminal = frames
				return nil
			}
			for _, frame := range frames {
				if err := s.writeClient(frame.kind, frame.body); err != nil {
					s.shutdown(websocket.CloseInternalServerErr, "")
				}
			}
			if accepted && pendingControl != nil {
				if err := s.writeTarget(websocket.TextMessage, pendingControl); err != nil {
					s.shutdown(websocket.CloseInternalServerErr, "")
				}
				sentControl, pendingControl = pendingControl, nil
			}
		case control := <-state.controls:
			if pendingControl != nil || sentControl != nil {
				s.sendError(control.eventID, control.streamID, newResponsesWSInvalidRequestError(errors.New("a response control event is already pending")))
				continue
			}
			if !accepted {
				pendingControl = control.body
				continue
			}
			if err := s.writeTarget(websocket.TextMessage, control.body); err != nil {
				s.shutdown(websocket.CloseInternalServerErr, "")
			}
			sentControl = control.body
		case <-idle.C:
			info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonTimeout, context.DeadlineExceeded)
			state.closeCode, state.closeReason = websocket.CloseInternalServerErr, "upstream response timed out"
			return nil
		case <-s.ctx.Done():
			info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonClientGone, s.ctx.Err())
			return nil
		}
	}
}

func responsesWSTerminal(kind string) bool {
	switch kind {
	case "response.completed", "response.done", "response.incomplete", "response.failed", "response.cancelled", "response.canceled":
		return true
	}
	return false
}

func responsesWSErrorEndsRequest(event responsesWSErrorEvent, streamID, responseID string, control []byte) (terminal, ambiguous, controlError bool) {
	if len(control) > 0 {
		var pending struct {
			EventID    string `json:"event_id"`
			StreamID   string `json:"stream_id"`
			ResponseID string `json:"response_id"`
		}
		_ = json.Unmarshal(control, &pending)
		if event.EventID != "" && event.EventID == pending.EventID {
			return false, false, true
		}
		if event.ResponseID != "" && event.ResponseID == pending.ResponseID && event.ResponseID != responseID {
			return false, false, true
		}
		if (event.StreamID == "" || event.StreamID == pending.StreamID) && event.Error != nil && event.Error.Type == "invalid_request_error" {
			switch event.Error.Code {
			case "response_not_found", "response_not_active", "response_already_completed":
				return false, false, true
			}
		}
	}
	if event.StreamID != "" && event.StreamID != streamID || responseID != "" && event.ResponseID != "" && event.ResponseID != responseID {
		return false, false, false
	}
	return true, len(control) > 0 && event.ResponseID == "", false
}

func responsesWSRejection(event responsesWSErrorEvent) *types.NewAPIError {
	if event.Error == nil {
		event.Error = &types.OpenAIError{Type: "invalid_request_error", Message: "upstream rejected the response"}
	}
	if event.Status < 400 || event.Status > 599 {
		event.Status = http.StatusBadRequest
	}
	return types.WithOpenAIError(*event.Error, event.Status, types.ErrOptionWithSkipRetry())
}

func (s *responsesWSSession) forwardError(c *gin.Context, message responsesWSMessage, event responsesWSErrorEvent) error {
	apiErr := responsesWSRejection(event)
	service.ApplyErrorMask(c, apiErr)
	body, err := buildResponsesWSErrorPayload(event.EventID, event.StreamID, apiErr)
	if err != nil {
		return err
	}
	return s.writeClient(message.kind, body)
}

func (s *responsesWSSession) startTargetReader(target *websocket.Conn) {
	limit := int64(helper.DefaultMaxScannerBufferSize)
	if constant.StreamScannerMaxBufferMB > 0 {
		limit = int64(constant.StreamScannerMaxBufferMB) << 20
	}
	target.SetReadLimit(limit)
	s.workers.Add(1)
	go func() {
		defer s.workers.Done()
		for {
			kind, body, err := target.ReadMessage()
			if state := s.getCurrent(); state != nil {
				select {
				case state.inbox <- responsesWSMessage{kind: kind, body: body, err: err}:
					if err != nil {
						return
					}
					continue
				case <-state.done:
				case <-s.ctx.Done():
					return
				}
			}
			// Unowned frames cannot bypass per-request output policy or become
			// usage for the next generation. Idle close still ends both peers.
			if err != nil {
				s.shutdown(websocket.CloseInternalServerErr, "upstream connection closed")
				return
			}
		}
	}()
}

func (s *responsesWSSession) getCurrent() *responsesWSCallState {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	return s.current
}
func (s *responsesWSSession) tryReserveCurrent(state *responsesWSCallState) bool {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if s.current != nil || s.ctx.Err() != nil {
		return false
	}
	if s.drain != nil {
		_, done, ok := s.drain.Register()
		if !ok {
			return false
		}
		state.drainDone = done
	}
	s.current = state
	return true
}
func (s *responsesWSSession) getTarget() *websocket.Conn {
	s.connectionMu.Lock()
	defer s.connectionMu.Unlock()
	return s.target
}
func (s *responsesWSSession) setTarget(target *websocket.Conn) bool {
	s.connectionMu.Lock()
	defer s.connectionMu.Unlock()
	if s.ctx.Err() != nil {
		_ = target.Close()
		return false
	}
	s.target = target
	return true
}
func (s *responsesWSSession) writeTarget(kind int, body []byte) error {
	s.targetWriteMu.Lock()
	defer s.targetWriteMu.Unlock()
	target := s.getTarget()
	if target == nil {
		return errors.New("upstream websocket is not connected")
	}
	if err := target.SetWriteDeadline(time.Now().Add(responsesWSWriteTimeout)); err != nil {
		return err
	}
	return target.WriteMessage(kind, body)
}
func (s *responsesWSSession) writeClient(kind int, body []byte) error {
	s.clientWriteMu.Lock()
	defer s.clientWriteMu.Unlock()
	if err := s.client.SetWriteDeadline(s.clientBudget.Deadline(time.Now(), len(body))); err != nil {
		return err
	}
	return s.client.WriteMessage(kind, body)
}
func (s *responsesWSSession) sendError(eventID, streamID string, apiErr *types.NewAPIError) {
	if body, err := buildResponsesWSErrorPayload(eventID, streamID, apiErr); err == nil {
		if err := s.writeClient(websocket.TextMessage, body); err != nil {
			s.shutdown(websocket.CloseInternalServerErr, "")
		}
	}
}
func (s *responsesWSSession) shutdown(code int, reason string) {
	s.closeOnce.Do(func() {
		s.cancel()
		s.connectionMu.Lock()
		target := s.target
		s.target = nil
		s.connectionMu.Unlock()
		body := websocket.FormatCloseMessage(code, reason)
		_ = s.client.WriteControl(websocket.CloseMessage, body, time.Now().Add(time.Second))
		if target != nil {
			_ = target.WriteControl(websocket.CloseMessage, body, time.Now().Add(time.Second))
			_ = target.Close()
		}
		_ = s.client.Close()
	})
}

func parseResponsesWSEnvelope(message []byte) (responsesWSCreateEvent, string, error) {
	var event responsesWSCreateEvent
	if err := json.Unmarshal(message, &event); err != nil {
		return event, "", fmt.Errorf("invalid websocket event json: %w", err)
	}
	streamRaw := event.StreamID
	if len(streamRaw) == 0 && len(event.Request) > 0 {
		var wrapped struct {
			StreamID json.RawMessage `json:"stream_id"`
		}
		if json.Unmarshal(event.Request, &wrapped) == nil {
			streamRaw = wrapped.StreamID
		}
	}
	var streamID string
	if len(streamRaw) > 0 {
		if json.Unmarshal(streamRaw, &streamID) != nil || len(streamID) < 1 || len(streamID) > 256 {
			return event, "", errors.New("stream_id must contain 1-256 ASCII letters, digits, underscores, hyphens, or periods")
		}
		for _, char := range streamID {
			if !(char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '_' || char == '-' || char == '.') {
				return event, "", errors.New("stream_id contains an invalid character")
			}
		}
	}
	if strings.TrimSpace(event.Type) == "" {
		return event, streamID, errors.New("websocket event type is required")
	}
	return event, streamID, nil
}

func normalizeResponsesWSCreateEvent(message []byte, event responsesWSCreateEvent, streamID string) (responsesWSCreateRequest, error) {
	create := responsesWSCreateRequest{StreamID: streamID, Generate: event.Generate}
	if len(event.Request) > 0 {
		message = event.Request
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(message, &body); err != nil || body == nil {
		return create, errors.New("response.create request must be a JSON object")
	}
	if len(create.Generate) == 0 {
		create.Generate = body["generate"]
	}
	if len(create.Generate) > 0 && string(create.Generate) != "true" && string(create.Generate) != "false" {
		return create, errors.New("generate must be a boolean")
	}
	for _, key := range []string{"type", "event_id", "stream_id", "generate", "stream", "stream_options", "background"} {
		delete(body, key)
	}
	var err error
	create.Body, err = json.Marshal(body)
	if err == nil {
		err = json.Unmarshal(create.Body, &create.Request)
	}
	if err == nil && create.Request.MaxOutputTokens != nil && *create.Request.MaxOutputTokens > 2147483647/2 {
		err = errors.New("max_output_tokens is invalid")
	}
	return create, err
}

func buildResponsesWSCreateEvent(data []byte, generate json.RawMessage, streamID string) ([]byte, error) {
	var event map[string]json.RawMessage
	if json.Unmarshal(data, &event) != nil || event == nil {
		return nil, errors.New("response.create body must be a JSON object")
	}
	for _, key := range []string{"type", "event_id", "stream_id", "generate", "background", "stream", "stream_options"} {
		delete(event, key)
	}
	event["type"] = json.RawMessage(`"response.create"`)
	if streamID != "" {
		event["stream_id"], _ = json.Marshal(streamID)
	}
	if len(generate) > 0 {
		event["generate"] = generate
	}
	return json.Marshal(event)
}
func newResponsesWSInvalidRequestError(err error) *types.NewAPIError {
	return types.NewErrorWithStatusCode(err, types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
}
func buildResponsesWSErrorPayload(eventID, streamID string, apiErr *types.NewAPIError) ([]byte, error) {
	if apiErr == nil {
		return nil, errors.New("api error is nil")
	}
	status := apiErr.StatusCode
	if status == 0 {
		status = http.StatusInternalServerError
	}
	openaiErr := apiErr.ToOpenAIError()
	return json.Marshal(&responsesWSErrorEvent{Type: "error", Status: status, EventID: eventID, StreamID: streamID, Error: &openaiErr})
}
