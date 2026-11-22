package ali

import (
	"bytes"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const (
	aliSearchTurboKey    = "search_strategy_turbo"
	aliSearchAgentKey    = "search_strategy_agent"
	aliUsageCaptureLimit = 16 << 20
)

// Observe the original bytes before protocol conversion or filtering. The body
// is forwarded unchanged, and repeated usage frames are snapshots, not deltas.
type aliSearchUsageBody struct {
	io.ReadCloser
	stream      bool
	pending     []byte
	dropping    bool
	event       []byte
	skipEvent   bool
	explicit    bool
	count       int
	serverCount int
	responses   bool
	calls       dto.ToolUsageCounter
}

func (b *aliSearchUsageBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.observe(p[:n])
	if err == io.EOF {
		b.finish()
	}
	return n, err
}

func (b *aliSearchUsageBody) observe(data []byte) {
	for len(data) > 0 {
		size := len(data)
		if b.stream {
			if i := bytes.IndexByte(data, '\n'); i >= 0 {
				size = i + 1
			}
		}
		if !b.dropping {
			if len(b.pending)+size > aliUsageCaptureLimit {
				b.pending = nil
				b.dropping = true
			} else {
				b.pending = append(b.pending, data[:size]...)
			}
		}
		if b.stream && data[size-1] == '\n' {
			b.consumeLine()
		}
		data = data[size:]
	}
}

func (b *aliSearchUsageBody) consumeLine() {
	line := bytes.TrimSpace(b.pending)
	switch {
	case b.dropping:
		b.event, b.skipEvent = nil, true
	case len(line) == 0:
		if !b.skipEvent {
			b.consume(b.event)
		}
		b.event, b.skipEvent = nil, false
	case !b.skipEvent && bytes.HasPrefix(line, []byte("data:")):
		// Also accept providers that omit the blank separator between complete
		// JSON frames, while retaining standard multiline SSE data fields.
		if gjson.ValidBytes(b.event) {
			b.consume(b.event)
			b.event = nil
		}
		payload := bytes.TrimSpace(line[5:])
		if len(b.event)+len(payload)+1 > aliUsageCaptureLimit {
			b.event, b.skipEvent = nil, true
		} else {
			if len(b.event) > 0 {
				b.event = append(b.event, '\n')
			}
			b.event = append(b.event, payload...)
		}
	}
	b.pending = nil
	b.dropping = false
}

func (b *aliSearchUsageBody) finish() {
	if b.stream {
		if len(b.pending) > 0 || b.dropping {
			b.consumeLine()
		}
		if !b.skipEvent {
			b.consume(b.event)
		}
		b.event = nil
	} else if !b.dropping && len(b.pending) > 0 {
		b.consume(b.pending)
		b.pending = nil
	}
}

func (b *aliSearchUsageBody) consume(raw []byte) {
	if !gjson.ValidBytes(raw) {
		return
	}
	root := gjson.ParseBytes(raw)
	if strings.HasPrefix(root.Get("type").String(), "response.") || root.Get("object").String() == "response" {
		b.responses = true
	}
	for _, path := range []string{"usage", "response.usage", "message.usage"} {
		u := root.Get(path)
		count := u.Get("x_tools.web_search.count")
		if count.Type == gjson.Number && count.Num >= 0 && count.Num <= 1000000 && count.Num == float64(count.Int()) {
			b.explicit = true
			b.count = int(count.Int())
		}
		server := u.Get("server_tool_use.web_search_requests")
		if server.Type == gjson.Number && server.Num >= 0 && server.Num <= 1000000 && server.Num == float64(server.Int()) {
			b.serverCount = max(b.serverCount, int(server.Int()))
		}
	}
	record := func(item gjson.Result, index *int) {
		if item.Get("type").String() == "web_search_call" {
			b.responses = true
			b.calls.Record(dto.BuildInToolWebSearchPreview, item.Get("id").String(), index)
		}
	}
	var index *int
	if value := root.Get("output_index"); value.Exists() {
		i := int(value.Int())
		index = &i
	}
	record(root.Get("item"), index)
	for _, path := range []string{"output", "response.output"} {
		for i, item := range root.Get(path).Array() {
			record(item, &i)
		}
	}
}

func (b *aliSearchUsageBody) apply(c *gin.Context, info *relaycommon.RelayInfo, usage *dto.Usage) *dto.Usage {
	b.finish()
	count := max(b.calls.Counts()[dto.BuildInToolWebSearchPreview], b.serverCount, c.GetInt("claude_web_search_requests"))
	if usage != nil {
		for _, key := range []string{"web_search", dto.BuildInToolWebSearchPreview, aliSearchTurboKey, aliSearchAgentKey} {
			count = max(count, usage.ToolUsage[key])
		}
	}
	if b.explicit {
		// x_tools is the authoritative total, including an explicit zero.
		count = b.count
	}
	if usage == nil {
		if count == 0 {
			return nil
		}
		usage = &dto.Usage{}
	}
	counts := make(map[string]int, len(usage.ToolUsage)+1)
	for key, value := range usage.ToolUsage {
		if key != "web_search" && key != dto.BuildInToolWebSearchPreview && key != aliSearchTurboKey && key != aliSearchAgentKey {
			counts[key] = value
		}
	}
	key := aliSearchTurboKey
	if b.responses || info.RelayMode == constant.RelayModeResponses || info.RelayFormat == types.RelayFormatClaude || c.GetString("ali_search_strategy") == "agent" {
		key = aliSearchAgentKey
	}
	if count > 0 {
		counts[key] = count
	}
	usage.ToolUsage = counts
	return usage
}

func observeAliSearchUsage(resp *http.Response, info *relaycommon.RelayInfo) *aliSearchUsageBody {
	if resp == nil || resp.Body == nil {
		return nil
	}
	body := &aliSearchUsageBody{
		ReadCloser: resp.Body,
		stream:     info.IsStream || strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream"),
	}
	resp.Body = body
	return body
}
