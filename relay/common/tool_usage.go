package common

import (
	"crypto/sha256"
	"encoding/hex"
	"math"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// MaxBillableToolCallCount bounds observed calls and settlement multipliers.
const MaxBillableToolCallCount = 10_000

// Hosted billing keys must never price a client function with the same name.
var reservedBillableToolNames = map[string]struct{}{
	"web_search": {}, "web_search_preview": {}, "file_search": {},
	"google_search": {}, "google_search_grounded_prompt": {},
	"image_generation": {}, "code_interpreter": {}, "web_fetch": {},
	"code_execution": {}, "tool_search": {}, "bing_web_search": {},
	"search_std": {}, "search_pro": {}, "search_pro_sogou": {}, "search_pro_quark": {},
	"search_strategy_turbo": {}, "search_strategy_max": {},
	"search_strategy_agent": {}, "search_strategy_agent_max": {},
	"x_search_posts": {}, "x_search_profiles": {}, "deepseek_search": {}, "deepseek_fetch": {},
}

// ResponsesToolUsageCounter belongs to one response. Only completed output
// items are billable; request tools and added/argument-delta events are not.
// Raw items retain image result payloads omitted by the legacy response DTO.
type ResponsesToolUsageCounter struct {
	aliases      map[string]*responsesToolCall
	byIndex      map[string]*responsesToolCall
	calls        []*responsesToolCall
	imageQuality string
	imageSize    string
}

type responsesToolCall struct {
	parent *responsesToolCall
	kind   string
	id     string
	callID string
}

func (c *responsesToolCall) root() *responsesToolCall {
	if c.parent != nil {
		c.parent = c.parent.root()
		return c.parent
	}
	return c
}

func responsesBillableToolName(info *RelayInfo, item gjson.Result) string {
	switch item.Get("type").String() {
	case dto.BuildInCallWebSearchCall:
		if info != nil && info.ResponsesUsageInfo != nil {
			tools := info.ResponsesUsageInfo.BuiltInTools
			if _, preview := tools[dto.BuildInToolWebSearchPreview]; !preview {
				if _, search := tools["web_search"]; search {
					return "web_search"
				}
			}
		}
		return dto.BuildInToolWebSearchPreview
	case "file_search_call":
		return dto.BuildInToolFileSearch
	case dto.ResponsesOutputTypeImageGenerationCall:
		return "image_generation"
	case "code_interpreter_call":
		return "code_interpreter"
	case "function_call":
		name := item.Get("name").String()
		if info == nil || name == "" || strings.TrimSpace(name) != name {
			return ""
		}
		if _, reserved := reservedBillableToolNames[name]; reserved {
			return ""
		}
		// Match settlement's billing model, including explicit zero overrides.
		price := operation_setting.GetToolPriceForModel(name, info.OriginModelName)
		if !operation_setting.HasConfiguredToolPrice(name, info.OriginModelName) || price <= 0 || math.IsNaN(price) || math.IsInf(price, 0) {
			return ""
		}
		return name
	}
	return ""
}

// ObserveResponse accepts a full HTTP body or a terminal SSE/WS snapshot.
// A failed parent can still contain independently completed, billable items.
func (c *ResponsesToolUsageCounter) ObserveResponse(ctx *gin.Context, info *RelayInfo, response gjson.Result, completed bool) {
	status := strings.ToLower(strings.TrimSpace(response.Get("status").String()))
	if (status != "" && status != "completed") || response.Get("error").Type != gjson.Null {
		completed = false
	}
	for index, item := range response.Get("output").Array() {
		c.ObserveItem(ctx, info, item, &index, completed)
	}
}

// ObserveItem is called for output_item.done or terminal snapshots, never for
// output_item.added. Missing status is accepted only at a completed boundary.
func (c *ResponsesToolUsageCounter) ObserveItem(ctx *gin.Context, info *RelayInfo, item gjson.Result, index *int, completed bool) {
	if strings.HasSuffix(item.Get("type").String(), "_call") && ctx != nil {
		ctx.Set("responses_tool_call", true)
	}
	switch strings.ToLower(strings.TrimSpace(item.Get("status").String())) {
	case "completed":
	case "":
		if !completed {
			return
		}
	default:
		return
	}
	kind := responsesBillableToolName(info, item)
	if kind == "" {
		return
	}
	resultKey := ""
	if kind == "image_generation" {
		result := item.Get("result")
		if result.Type != gjson.String || strings.TrimSpace(result.String()) == "" {
			return
		}
		sum := sha256.Sum256([]byte(result.String()))
		resultKey = hex.EncodeToString(sum[:])
	}
	if !c.record(kind, item.Get("id").String(), item.Get("call_id").String(), resultKey, index) {
		return
	}
	if kind == "image_generation" {
		if c.imageQuality == "" {
			c.imageQuality = item.Get("quality").String()
		}
		if c.imageSize == "" {
			c.imageSize = item.Get("size").String()
		}
	}
}

func (c *ResponsesToolUsageCounter) record(kind, id, callID, result string, index *int) bool {
	prefix := kind + "\x00"
	var aliases []string
	for _, alias := range []struct{ tag, value string }{{"id:", id}, {"call:", callID}, {"result:", result}} {
		if alias.value != "" {
			aliases = append(aliases, prefix+alias.tag+alias.value)
		}
	}
	indexKey := ""
	if index != nil && *index >= 0 {
		indexKey = prefix + strconv.Itoa(*index)
	}
	if len(aliases) == 0 && indexKey == "" {
		return false
	}
	if c.aliases == nil {
		c.aliases = make(map[string]*responsesToolCall)
		c.byIndex = make(map[string]*responsesToolCall)
	}
	var call *responsesToolCall
	for _, alias := range aliases {
		if existing := c.aliases[alias]; existing != nil {
			existing = existing.root()
			if call == nil {
				call = existing
			} else if call != existing {
				existing.parent = call
			}
		}
	}
	if existing := c.byIndex[indexKey]; existing != nil {
		existing = existing.root()
		// Partial snapshots may reuse an array position for a different ID.
		// An index alone must not collapse two identified invocations.
		compatible := (id == "" || existing.id == "" || id == existing.id) &&
			(callID == "" || existing.callID == "" || callID == existing.callID)
		if compatible {
			if call == nil {
				call = existing
			} else if call != existing {
				existing.parent = call
			}
		}
	}
	if call == nil {
		if len(c.calls) >= MaxBillableToolCallCount {
			return false
		}
		call = &responsesToolCall{kind: kind}
		c.calls = append(c.calls, call)
	}
	if call.id == "" {
		call.id = id
	}
	if call.callID == "" {
		call.callID = callID
	}
	for _, alias := range aliases {
		c.aliases[alias] = call
	}
	if indexKey != "" {
		c.byIndex[indexKey] = call
	}
	return true
}

// Counts is always non-nil: observing no completed tools authoritatively
// suppresses legacy request/context fallbacks in Usage.ToolUsage.
func (c *ResponsesToolUsageCounter) Counts() map[string]int {
	counts := make(map[string]int)
	for _, call := range c.calls {
		if call.parent == nil {
			counts[call.kind]++
		}
	}
	if counts["image_generation"] > dto.MaxImageN {
		counts["image_generation"] = dto.MaxImageN
	}
	return counts
}

// Commit replaces compatibility counts; repeated commit cannot add charges.
// The returned map remains the authoritative Usage.ToolUsage contract.
func (c *ResponsesToolUsageCounter) Commit(ctx *gin.Context, info *RelayInfo) map[string]int {
	counts := c.Counts()
	if ctx != nil {
		ctx.Set("image_generation_call", counts["image_generation"] > 0)
		ctx.Set("image_generation_call_count", counts["image_generation"])
		ctx.Set("image_generation_call_quality", c.imageQuality)
		ctx.Set("image_generation_call_size", c.imageSize)
	}
	if info == nil {
		return counts
	}
	if info.ResponsesUsageInfo == nil {
		info.ResponsesUsageInfo = &ResponsesUsageInfo{}
	}
	if info.ResponsesUsageInfo.BuiltInTools == nil {
		info.ResponsesUsageInfo.BuiltInTools = make(map[string]*BuildInToolInfo)
	}
	for _, tool := range info.ResponsesUsageInfo.BuiltInTools {
		if tool != nil {
			tool.CallCount = 0
		}
	}
	for name, count := range counts {
		tool := info.ResponsesUsageInfo.BuiltInTools[name]
		if tool == nil {
			tool = &BuildInToolInfo{ToolName: name, SearchContextSize: "medium"}
			info.ResponsesUsageInfo.BuiltInTools[name] = tool
		}
		tool.CallCount = count
	}
	return counts
}
