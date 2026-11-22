package helper

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
)

// 在实际 SSE 出口过滤，覆盖直接 Render 和各协议 helper。原始上游事件仍由
// handler 计量、转换；此 writer 位于预算 writer 外层，隐藏内容不消耗写出预算。
// 调用方继续使用 scanner 的 writeMutex 串行化数据与心跳。
type thinkingStreamWriter struct {
	gin.ResponseWriter
	context  *gin.Context
	info     *relaycommon.RelayInfo
	filter   relaycommon.ThinkingResponseFilter
	pending  []byte
	matchers map[string]*relaycommon.OutputSensitiveMatcher
	err      error
}

func (w *thinkingStreamWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *thinkingStreamWriter) WriteString(text string) (int, error) {
	return w.Write([]byte(text))
}

func (w *thinkingStreamWriter) Write(data []byte) (int, error) {
	if w.err != nil {
		return 0, w.err
	}
	if outputSensitiveStreamBlocked(w.context) {
		return len(data), nil
	}
	w.pending = append(w.pending, data...)
	for {
		end := bytes.Index(w.pending, []byte("\n\n"))
		if end < 0 {
			break
		}
		frame := string(w.pending[:end])
		w.pending = w.pending[end+2:]
		if err := w.frame(frame); err != nil {
			w.err = err
			return 0, err
		}
	}
	return len(data), nil
}

func (w *thinkingStreamWriter) frame(frame string) error {
	var payload []string
	var envelope []string
	for _, line := range strings.Split(frame, "\n") {
		if value, ok := strings.CutPrefix(line, "data:"); ok {
			payload = append(payload, strings.TrimPrefix(value, " "))
		} else if line != "" {
			envelope = append(envelope, line)
		}
	}
	if len(payload) == 0 {
		_, err := w.ResponseWriter.Write([]byte(frame + "\n\n"))
		return err
	}
	data := strings.TrimSpace(strings.Join(payload, "\n"))
	if data == "[DONE]" {
		if err := w.events(w.filter.FlushEvents()); err != nil {
			return err
		}
		_, err := w.ResponseWriter.Write([]byte(frame + "\n\n"))
		return err
	}
	if data == "" {
		return nil
	}
	events, err := w.filter.FilterEvent([]byte(data), envelope)
	if err != nil {
		// 开启剥离时，不把无法识别的半截 JSON 当成正文泄漏到客户端。
		return err
	}
	return w.events(events)
}

func (w *thinkingStreamWriter) events(events []*relaycommon.ThinkingResponseEvent) error {
	for _, output := range events {
		data, envelope := output.Data, output.Envelope
		if err := w.scan(data); err != nil {
			return err
		}
		var frame strings.Builder
		if envelope != nil {
			for _, line := range envelope {
				frame.WriteString(line)
				frame.WriteByte('\n')
			}
		} else {
			var event struct {
				Type string `json:"type"`
			}
			_ = json.Unmarshal(data, &event)
			if event.Type != "" && (w.info.RelayFormat == types.RelayFormatClaude || w.info.RelayFormat == types.RelayFormatOpenAIResponses) {
				frame.WriteString("event: " + event.Type + "\n")
			}
		}
		frame.WriteString("data: ")
		frame.Write(data)
		frame.WriteString("\n\n")
		if _, err := w.ResponseWriter.Write([]byte(frame.String())); err != nil {
			return err
		}
	}
	return nil
}

func (w *thinkingStreamWriter) scan(data []byte) error {
	if w.matchers == nil {
		return nil
	}
	for _, fragment := range relaycommon.VisibleOutputSensitiveTextFragments(string(data)) {
		matcher := w.matchers[fragment.Path]
		if matcher == nil {
			matcher = relaycommon.NewOutputSensitiveMatcherForConfig(w.info.OutputSensitiveConfig)
			w.matchers[fragment.Path] = matcher
		}
		if hit, _ := matcher.Scan(fragment.Text); hit {
			common.SetContextKey(w.context, constant.ContextKeyAdminRejectReason, "output_sensitive")
			w.info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonOutputSensitive, relaycommon.OutputSensitiveError())
			if w.info.OutputSensitiveConfig.Action == "error" {
				w.info.StreamStatus.RecordError("output sensitive pattern matched")
			}
			EmitOutputSensitiveStreamTerminal(w.context, w.info)
			return relaycommon.OutputSensitiveError()
		}
	}
	return nil
}

// 策略终止帧走同一个预算 writer，但绕过思考/敏感词过滤，且只发送一次。
func (w *thinkingStreamWriter) WritePolicyTerminal(data []byte) error {
	w.pending = nil
	_, err := w.ResponseWriter.Write(data)
	w.ResponseWriter.Flush()
	return err
}

func (w *thinkingStreamWriter) FlushError() error {
	if w.err != nil {
		return w.err
	}
	var writer http.ResponseWriter = w.ResponseWriter
	for {
		if flusher, ok := writer.(interface{ FlushError() error }); ok {
			return flusher.FlushError()
		}
		unwrapper, ok := writer.(interface{ Unwrap() http.ResponseWriter })
		if !ok {
			return nil
		}
		writer = unwrapper.Unwrap()
	}
}

func (w *thinkingStreamWriter) finish() error {
	if w.err != nil || outputSensitiveStreamBlocked(w.context) || requestContextDone(w.context) {
		return w.err
	}
	if len(bytes.TrimSpace(w.pending)) != 0 {
		return fmt.Errorf("incomplete output SSE frame")
	}
	if err := w.events(w.filter.FlushEvents()); err != nil {
		w.err = err
		return err
	}
	w.ResponseWriter.Flush()
	return w.FlushError()
}

// FinalizeThinkingStream 必须在适配器发送完延迟正文、usage 和 finish 后调用。
func FinalizeThinkingStream(c *gin.Context) {
	writer, ok := c.Writer.(*thinkingStreamWriter)
	if !ok {
		return
	}
	if err := writer.finish(); err != nil && !outputSensitiveStreamBlocked(c) {
		writer.info.StreamStatus.RecordError(err.Error())
	}
	writer.finishStatus()
}

// 仅在 scanner 的 goroutine 全部退出后调用；延迟的最终帧仍可能命中策略。
func (w *thinkingStreamWriter) finishStatus() {
	status := w.info.StreamStatus
	if outputSensitiveStreamBlocked(w.context) && status != nil && status.IsNormalEnd() {
		status.EndReason = relaycommon.StreamEndReasonOutputSensitive
		status.EndError = relaycommon.OutputSensitiveError()
	}
}

func installThinkingStreamWriter(c *gin.Context, info *relaycommon.RelayInfo) *thinkingStreamWriter {
	if info == nil || !info.ThinkingProcessStrip {
		return nil
	}
	writer := &thinkingStreamWriter{ResponseWriter: c.Writer, context: c, info: info}
	if relaycommon.NewOutputSensitiveMatcherForConfig(info.OutputSensitiveConfig) != nil {
		writer.matchers = make(map[string]*relaycommon.OutputSensitiveMatcher)
	}
	c.Writer = writer
	return writer
}
