package jsplugin

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"strings"
	"time"

	pluginruntime "github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/gin-gonic/gin"
)

// AcceptedSubmitStreamKey prevents a second provider submission after accepting
// SSE. The relay retry loop must consult this even if no client bytes were sent.
const AcceptedSubmitStreamKey = "script_plugin_accepted_submit_stream"

func flatHeaders(headers http.Header) map[string]string {
	result := make(map[string]string, len(headers))
	for name := range headers {
		result[name] = headers.Get(name)
	}
	return result
}

func (a *TaskAdaptor) submitResponseBody(c *gin.Context, response *http.Response) (any, error) {
	if response == nil || response.Body == nil {
		return nil, errors.New("empty script submit response")
	}
	kind, _, _ := mime.ParseMediaType(response.Header.Get("Content-Type"))
	streaming := a.submit != nil && a.submit.ResponseType == "sse"
	if kind != "text/event-stream" {
		data, err := boundedBody(response)
		return orderedJSONValue(data), err
	}
	defer response.Body.Close()
	if !streaming {
		return nil, errors.New("unexpected SSE response for JSON submission")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, errors.New("script upstream rejected submission")
	}
	c.Set(AcceptedSubmitStreamKey, true)
	var client *gin.Context
	if prepared, ok := PreparedIntent(c); ok && prepared.Protocol == "" {
		client = c
	}
	body, stopped, err := a.readSubmitEvents(c.Request.Context(), response, client)
	a.keepReservation = stopped || err != nil
	if err != nil {
		// Parsing the last complete state can still recover the provider id.
		// Keep the frozen reservation regardless of the recovered usage facts.
		a.usageError = "submit_stream_interrupted"
		return body, nil
	}
	return body, nil
}

// Each event and control snapshot is bounded independently. A delta failure
// poisons the accumulator: a partially applied result can never be published.
func (a *TaskAdaptor) readSubmitEvents(parent context.Context, response *http.Response, client *gin.Context) (any, bool, error) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), 10*time.Minute)
	defer cancel()
	stopClose := context.AfterFunc(ctx, func() { _ = response.Body.Close() })
	defer stopClose()
	timer := time.AfterFunc(30*time.Second, func() { cancel(); _ = response.Body.Close() })
	defer timer.Stop()
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 4096), maxJSONBytes+1)
	hook := "parseSubmitEvent"
	var accumulated *pluginruntime.JSONState
	if a.plugin.UsesSubmitEventDelta() {
		hook = "parseSubmitEventDelta"
		accumulated = pluginruntime.NewJSONState(maxJSONBytes)
	}
	var state any
	var lines []string
	var frame strings.Builder
	var eventName, lastID string
	frameBytes := 0
	first, completed, relayed := true, false, false
	var streamErr error
	for scanner.Scan() {
		if streamErr = ctx.Err(); streamErr != nil {
			break
		}
		timer.Reset(30 * time.Second)
		line := scanner.Text()
		if first {
			line = strings.TrimPrefix(line, "\ufeff")
			first = false
		}
		frameBytes += len(line) + 1
		if frameBytes > maxJSONBytes {
			streamErr = errors.New("script SSE event exceeds 1 MiB")
			break
		}
		if client != nil {
			frame.WriteString(line)
			frame.WriteByte('\n')
		}
		if line != "" {
			field, value, _ := strings.Cut(line, ":")
			value = strings.TrimPrefix(value, " ")
			switch field {
			case "data":
				lines = append(lines, value)
			case "event":
				eventName = value
			case "id":
				if !strings.ContainsRune(value, '\x00') {
					lastID = value
				}
			}
			continue
		}
		frameBytes = 0
		if client != nil {
			if client.Request.Context().Err() != nil {
				client = nil
			} else {
				client.Header("Content-Type", "text/event-stream")
				client.Header("Cache-Control", "private, no-store")
				client.Header("X-Accel-Buffering", "no")
				_ = http.NewResponseController(client.Writer).SetWriteDeadline(time.Now().Add(30 * time.Second))
				_, writeErr := client.Writer.WriteString(frame.String())
				relayed = true
				if writeErr != nil {
					client = nil
				} else {
					client.Writer.Flush()
				}
			}
			frame.Reset()
		}
		if len(lines) == 0 {
			eventName = ""
			continue
		}
		if eventName == "" {
			eventName = "message"
		}
		event := map[string]any{"event": eventName, "id": lastID, "data": strings.Join(lines, "\n")}
		encoded, err := a.plugin.Engine.CallJSON(a.hookContext(ctx), hook, nil, a.submitContext, event, state)
		if err != nil {
			streamErr = err
			break
		}
		var result map[string]json.RawMessage
		if json.Unmarshal(encoded, &result) != nil || result == nil {
			streamErr = errors.New("submit event hook must return an object")
			break
		}
		next, hasState := result["state"]
		var done bool
		if accumulated == nil && len(result) != 2 || !hasState || string(result["done"]) != "true" && string(result["done"]) != "false" || json.Unmarshal(result["done"], &done) != nil || len(next) > maxJSONBytes {
			streamErr = errors.New("invalid submit stream state or done")
			break
		}
		if accumulated != nil {
			if len(result) != 3 || len(next) > 64<<10 {
				streamErr = errors.New("invalid delta result or oversized control state")
				break
			}
			if err = accumulated.ApplyJSON(ctx, pluginruntime.RawJSON(result["changes"])); err != nil {
				streamErr = err
				break
			}
		}
		state = pluginruntime.RawJSON(next)
		if done {
			completed = true
			break
		}
		lines, eventName = nil, ""
	}
	if streamErr == nil && !completed {
		streamErr = ctx.Err()
		if streamErr == nil {
			streamErr = scanner.Err()
		}
	}
	if streamErr != nil {
		if relayed && client != nil {
			_ = http.NewResponseController(client.Writer).SetWriteDeadline(time.Now())
		}
		if accumulated != nil {
			return nil, true, streamErr
		}
		return state, true, streamErr
	}
	if accumulated != nil {
		value, err := accumulated.RawValue()
		return value, err != nil && relayed, err
	}
	return state, false, nil
}
