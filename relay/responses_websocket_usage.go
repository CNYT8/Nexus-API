package relay

import (
	"encoding/json"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/tidwall/gjson"
)

// Each create owns an accumulator. Usage snapshots replace nonzero fields;
// deltas, item snapshots and the terminal snapshot are alternative estimates,
// never additive copies of the same generated output.
type responsesWSUsage struct {
	info           *relaycommon.RelayInfo
	value          dto.Usage
	deltas         strings.Builder
	items          strings.Builder
	terminalTokens int
	doneTokens     int
	haveInput      bool
	haveOutput     bool
	terminal       bool
	toolCalls      relaycommon.ResponsesToolUsageCounter
	seenItems      map[string]bool
}

func newResponsesWSUsage(info *relaycommon.RelayInfo) *responsesWSUsage {
	return &responsesWSUsage{info: info, seenItems: make(map[string]bool)}
}

func (u *responsesWSUsage) observe(c *gin.Context, event *dto.ResponsesStreamResponse, data []byte) {
	raw := gjson.ParseBytes(data)
	usageJSON := raw.Get("usage")
	if event.Response != nil {
		u.merge(event.Response.Usage)
		usageJSON = raw.Get("response.usage")
	}
	if usageJSON.IsObject() {
		var usage dto.Usage
		if json.Unmarshal([]byte(usageJSON.Raw), &usage) == nil {
			u.merge(&usage)
		}
		for _, name := range []string{"input_tokens", "prompt_tokens"} {
			field := usageJSON.Get(name)
			u.haveInput = u.haveInput || field.Type == gjson.Number && field.Int() >= 0
		}
		for _, name := range []string{"output_tokens", "completion_tokens"} {
			field := usageJSON.Get(name)
			u.haveOutput = u.haveOutput || field.Type == gjson.Number && field.Int() >= 0
		}
	}
	if responsesWSTerminal(event.Type) {
		u.terminal = true
		outputs := raw.Get("response.output").Array()
		u.terminalTokens = max(u.terminalTokens, service.CountTextToken(responsesWSOutputTokenText(outputs), u.info.UpstreamModelName))
		u.toolCalls.ObserveResponse(c, u.info, raw.Get("response"),
			event.Type == "response.completed" || event.Type == "response.done")
	}
	switch event.Type {
	case "response.output_text.delta", "response.reasoning_text.delta", "response.reasoning_summary_text.delta", "response.refusal.delta", "response.function_call_arguments.delta":
		u.deltas.WriteString(event.Delta)
		if event.Type == "response.function_call_arguments.delta" {
			c.Set("responses_tool_call", true)
		}
	case "response.output_text.done", "response.reasoning_text.done", "response.reasoning_summary_text.done", "response.refusal.done", "response.function_call_arguments.done":
		text := raw.Get("text").String() + raw.Get("refusal").String() + raw.Get("arguments").String()
		u.doneTokens = max(u.doneTokens, service.CountTextToken(text, u.info.UpstreamModelName))
	case "response.output_item.added":
		if strings.HasSuffix(raw.Get("item.type").String(), "_call") {
			c.Set("responses_tool_call", true)
		}
	case dto.ResponsesOutputTypeItemDone:
		item := raw.Get("item")
		u.toolCalls.ObserveItem(c, u.info, item, event.OutputIndex, true)
		id := item.Get("id").String()
		if id != "" && u.seenItems[id] {
			return
		}
		if id != "" {
			u.seenItems[id] = true
		}
		u.items.WriteString(responsesWSOutputTokenText([]gjson.Result{item}))
	}
}

func (u *responsesWSUsage) merge(src *dto.Usage) {
	if src == nil {
		return
	}
	if src.InputTokens > 0 {
		u.value.PromptTokens = src.InputTokens
	} else if src.PromptTokens > 0 {
		u.value.PromptTokens = src.PromptTokens
	}
	if src.OutputTokens > 0 {
		u.value.CompletionTokens = src.OutputTokens
	} else if src.CompletionTokens > 0 {
		u.value.CompletionTokens = src.CompletionTokens
	}
	if src.TotalTokens > 0 {
		u.value.TotalTokens = src.TotalTokens
	}
	u.haveInput = u.haveInput || u.value.PromptTokens > 0
	u.haveOutput = u.haveOutput || u.value.CompletionTokens > 0
	u.value.PromptTokensDetails.MergeNonZero(src.PromptTokensDetails)
	if src.InputTokensDetails != nil {
		u.value.PromptTokensDetails.MergeNonZero(*src.InputTokensDetails)
	}
	mergeResponsesWSOutputDetails(&u.value.CompletionTokenDetails, src.CompletionTokenDetails)
	if src.OutputTokensDetails != nil {
		mergeResponsesWSOutputDetails(&u.value.CompletionTokenDetails, *src.OutputTokensDetails)
	}
}

func mergeResponsesWSOutputDetails(dst *dto.OutputTokenDetails, src dto.OutputTokenDetails) {
	if src.TextTokens > 0 {
		dst.TextTokens = src.TextTokens
	}
	if src.AudioTokens > 0 {
		dst.AudioTokens = src.AudioTokens
	}
	if src.ImageTokens > 0 {
		dst.ImageTokens = src.ImageTokens
	}
	if src.ReasoningTokens > 0 {
		dst.ReasoningTokens = src.ReasoningTokens
	}
}

func (u *responsesWSUsage) finish(c *gin.Context) *dto.Usage {
	usage := u.value
	usage.ToolUsage = u.toolCalls.Commit(c, u.info)
	if usage.TotalTokens > 0 {
		if usage.PromptTokens > 0 && usage.CompletionTokens == 0 && usage.TotalTokens > usage.PromptTokens {
			usage.CompletionTokens = usage.TotalTokens - usage.PromptTokens
		} else if usage.PromptTokens == 0 && usage.TotalTokens > usage.CompletionTokens {
			usage.PromptTokens = usage.TotalTokens - usage.CompletionTokens
		}
	}
	if !u.haveOutput && usage.CompletionTokens == 0 {
		usage.CompletionTokens = max(u.terminalTokens, u.doneTokens,
			service.CountTextToken(u.deltas.String(), u.info.UpstreamModelName),
			service.CountTextToken(u.items.String(), u.info.UpstreamModelName))
	}
	if !u.haveInput && usage.PromptTokens == 0 {
		usage.PromptTokens = u.info.GetEstimatePromptTokens()
	}
	if !u.haveInput || !u.haveOutput || !u.terminal {
		// Existing text/audio settlement keeps the durable reservation floor for
		// incomplete usage, including an ambiguous write or transport failure.
		usage.UsageSource = "responses_estimated"
	}
	usage.InputTokens, usage.OutputTokens = usage.PromptTokens, usage.CompletionTokens
	usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	inputDetails, outputDetails := usage.PromptTokensDetails.Clone(), usage.CompletionTokenDetails
	usage.InputTokensDetails, usage.OutputTokensDetails = &inputDetails, &outputDetails
	return &usage
}

func responsesWSOutputTokenText(outputs []gjson.Result) string {
	var text strings.Builder
	for _, output := range outputs {
		if output.Get("type").String() == "function_call" {
			text.WriteString(output.Get("arguments").String())
		}
		for _, field := range []string{"content", "summary"} {
			for _, part := range output.Get(field).Array() {
				switch part.Get("type").String() {
				case "output_text", "text", "reasoning_text", "summary_text":
					text.WriteString(part.Get("text").String())
				case "refusal":
					text.WriteString(part.Get("refusal").String())
				}
			}
		}
	}
	return text.String()
}

type responsesWSOutput struct {
	context     *gin.Context
	info        *relaycommon.RelayInfo
	thinking    relaycommon.ThinkingResponseFilter
	matchers    map[string]*relaycommon.OutputSensitiveMatcher
	initialized bool
}

func (o *responsesWSOutput) filter(data []byte) ([]responsesWSMessage, error) {
	if !o.initialized {
		o.initialized = true
		if relaycommon.NewOutputSensitiveMatcherForConfig(o.info.OutputSensitiveConfig) != nil {
			o.matchers = make(map[string]*relaycommon.OutputSensitiveMatcher)
		}
	}
	events := []*relaycommon.ThinkingResponseEvent{{Data: data}}
	if o.info.ThinkingProcessStrip {
		var err error
		events, err = o.thinking.FilterEvent(data, nil)
		if err != nil {
			return nil, err
		}
	}
	frames := make([]responsesWSMessage, 0, len(events))
	for _, event := range events {
		if o.matchers != nil {
			fragments := relaycommon.OutputSensitiveTextFragments(string(event.Data))
			if o.info.ThinkingProcessStrip {
				fragments = relaycommon.VisibleOutputSensitiveTextFragments(string(event.Data))
			}
			for _, fragment := range fragments {
				matcher := o.matchers[fragment.Path]
				if matcher == nil {
					matcher = relaycommon.NewOutputSensitiveMatcherForConfig(o.info.OutputSensitiveConfig)
					o.matchers[fragment.Path] = matcher
				}
				if hit, _ := matcher.Scan(fragment.Text); hit {
					common.SetContextKey(o.context, constant.ContextKeyAdminRejectReason, "output_sensitive")
					o.info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonOutputSensitive, relaycommon.OutputSensitiveError())
					if o.info.OutputSensitiveConfig.Action == "error" {
						o.info.StreamStatus.RecordError("output sensitive pattern matched")
					}
					return nil, relaycommon.OutputSensitiveError()
				}
			}
		}
		frames = append(frames, responsesWSMessage{kind: websocket.TextMessage, body: event.Data})
	}
	return frames, nil
}
