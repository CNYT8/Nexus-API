package helper

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/bytedance/gopkg/util/gopool"

	"github.com/gin-gonic/gin"
)

const (
	InitialScannerBufferSize    = 8 << 10   // 8KB; the scanner grows for unusually large SSE frames
	DefaultMaxScannerBufferSize = 128 << 20 // 128MB default SSE buffer size
	DefaultPingInterval         = 10 * time.Second
	// streamWriteTimeout bounds a write blocked on a client that stopped
	// reading so the unconditional wg.Wait() in cleanup can always finish.
	// Without it, a connected client with a full TCP buffer (no server
	// WriteTimeout) could hang the handler forever. StreamWriteBudget adds the
	// time a client reading at streamMinClientRate needs for the data.
	streamWriteTimeout  = 30 * time.Second
	streamMinClientRate = 8 << 10 // bytes per second
)

func getScannerBufferSize() int {
	if constant.StreamScannerMaxBufferMB > 0 {
		return constant.StreamScannerMaxBufferMB << 20
	}
	return DefaultMaxScannerBufferSize
}

func NewStreamScanner(reader io.Reader) *bufio.Scanner {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, InitialScannerBufferSize), getScannerBufferSize())
	return scanner
}

// StreamWriteBudget gives the writes of one stream a deadline of
// streamWriteTimeout past the time a client reading at streamMinClientRate
// needs for everything written so far. A fixed deadline per write cut slow
// clients off in the middle of a multi-megabyte image event, and the timeout
// looked like a client disconnect. The budget carries over between writes
// because the kernel can still hold megabytes of earlier writes and wakes a
// blocked writer only after a large part of them drains. A client that stops
// reading is still cut off once the budget runs out. Callers serialize writes.
type StreamWriteBudget struct {
	drainBy time.Time
}

// Deadline returns the write deadline for n more bytes written at now.
func (b *StreamWriteBudget) Deadline(now time.Time, n int) time.Time {
	if b.drainBy.Before(now) {
		b.drainBy = now
	}
	b.drainBy = b.drainBy.Add(time.Duration(n) * time.Second / streamMinClientRate)
	return b.drainBy.Add(streamWriteTimeout)
}

// streamResponseWriter sets the connection write deadline from its budget
// before every write. Writers that don't support deadlines (e.g. httptest
// recorders) are silently ignored.
type streamResponseWriter struct {
	gin.ResponseWriter
	budget StreamWriteBudget
}

func (w *streamResponseWriter) Write(p []byte) (int, error) {
	_ = http.NewResponseController(w.ResponseWriter).SetWriteDeadline(w.budget.Deadline(time.Now(), len(p)))
	return w.ResponseWriter.Write(p)
}

func (w *streamResponseWriter) WriteString(s string) (int, error) {
	_ = http.NewResponseController(w.ResponseWriter).SetWriteDeadline(w.budget.Deadline(time.Now(), len(s)))
	return w.ResponseWriter.WriteString(s)
}

// Unwrap lets http.ResponseController reach the connection.
func (w *streamResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func StreamScannerHandler(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo, dataHandler func(data string, sr *StreamResult)) {

	if resp == nil || dataHandler == nil {
		return
	}
	ensureOutputSensitiveStreamBlockState(c)

	// Wrap the writer so every stream write gets a deadline from a budget that
	// grows with the data size. A fixed per-write deadline cut slow clients
	// off in the middle of large events; the budget lets a client reading at
	// streamMinClientRate keep up while still cutting off a stalled client.
	c.Writer = &streamResponseWriter{ResponseWriter: c.Writer}
	// 无条件新建 StreamStatus
	info.StreamStatus = relaycommon.NewStreamStatus()

	ctx, cancel := context.WithCancel(context.Background())

	streamingTimeout := time.Duration(constant.StreamingTimeout) * time.Second

	var (
		stopChan     = make(chan bool, 3) // 增加缓冲区避免阻塞
		scanner      = NewStreamScanner(resp.Body)
		ticker       = time.NewTimer(streamingTimeout)
		pingTicker   *time.Ticker
		writeMutex   sync.Mutex // Mutex to protect concurrent writes
		timeoutMutex sync.Mutex
		wg           sync.WaitGroup // 用于等待所有 goroutine 退出
		resetTimeout = func() {
			timeoutMutex.Lock()
			defer timeoutMutex.Unlock()
			if !ticker.Stop() {
				select {
				case <-ticker.C:
				default:
				}
			}
			ticker.Reset(streamingTimeout)
		}
		cleanupOnce sync.Once
		stopOnce    sync.Once
	)

	var outputSensitiveMatchers map[string]*relaycommon.OutputSensitiveMatcher
	outputSensitiveConfig := info.OutputSensitiveConfig
	if outputSensitiveConfig.Enabled && len(outputSensitiveConfig.Patterns) > 0 {
		outputSensitiveMatchers = make(map[string]*relaycommon.OutputSensitiveMatcher)
	}

	stop := func() {
		stopOnce.Do(func() {
			close(stopChan)
		})
	}

	generalSettings := operation_setting.GetGeneralSetting()
	// A hidden reasoning phase can last longer than the normal client idle
	// window. Always allow comment heartbeats for this policy, even when the
	// global optional ping switch is disabled.
	thinkingStripPingEnabled := info != nil && info.ThinkingProcessStrip
	pingEnabled := (generalSettings.PingIntervalEnabled || thinkingStripPingEnabled) && !info.DisablePing
	pingInterval := time.Duration(generalSettings.PingIntervalSeconds) * time.Second
	if pingInterval <= 0 {
		pingInterval = DefaultPingInterval
	}

	if pingEnabled {
		pingTicker = time.NewTicker(pingInterval)
	}

	logger.LogDebug(c, "relay timeout seconds: %d", common.RelayTimeout)
	logger.LogDebug(c, "relay max idle conns: %d", common.RelayMaxIdleConns)
	logger.LogDebug(c, "relay max idle conns per host: %d", common.RelayMaxIdleConnsPerHost)
	logger.LogDebug(c, "streaming timeout seconds: %d", int64(streamingTimeout.Seconds()))
	logger.LogDebug(c, "ping interval seconds: %d", int64(pingInterval.Seconds()))

	cleanup := func() {
		cleanupOnce.Do(func() {
			cancel()
			stop()
			if resp.Body != nil {
				_ = resp.Body.Close()
			}

			ticker.Stop()
			if pingTicker != nil {
				pingTicker.Stop()
			}

			wg.Wait()
		})
	}
	// Ensure gin.Context is not returned to Gin's pool while any stream goroutine can still use it.
	defer cleanup()

	scanner.Split(bufio.ScanLines)
	SetEventStreamHeaders(c)

	ctx = context.WithValue(ctx, "stop_chan", stopChan)

	// Handle ping data sending with improved error handling
	if pingEnabled && pingTicker != nil {
		wg.Add(1)
		gopool.Go(func() {
			defer func() {
				if r := recover(); r != nil {
					logger.LogError(c, fmt.Sprintf("ping goroutine panic: %v", r))
					info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonPanic, fmt.Errorf("ping panic: %v", r))
					stop()
				}
				logger.LogDebug(c, "ping goroutine exited")
				wg.Done()
			}()

			// 添加超时保护，防止 goroutine 无限运行
			maxPingDuration := 30 * time.Minute // 最大 ping 持续时间
			pingTimeout := time.NewTimer(maxPingDuration)
			defer pingTimeout.Stop()

			for {
				select {
				case <-pingTicker.C:
					var err error
					func() {
						writeMutex.Lock()
						defer writeMutex.Unlock()
						// The ping writer uses the budget via streamResponseWriter; no extra deadline needed here.
						err = PingData(c)
						if err == nil && thinkingStripPingEnabled {
							resetTimeout()
						}
					}()
					if err != nil {
						logger.LogError(c, "ping data error: "+err.Error())
						info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonPingFail, err)
						return
					}
					logger.LogDebug(c, "ping data sent")
				case <-ctx.Done():
					return
				case <-stopChan:
					return
				case <-c.Request.Context().Done():
					// 监听客户端断开连接
					return
				case <-pingTimeout.C:
					logger.LogError(c, "ping goroutine max duration reached")
					return
				}
			}
		})
	}

	dataChan := make(chan string, 10)
	sr := newStreamResult(info.StreamStatus)

	wg.Add(1)
	gopool.Go(func() {
		defer func() {
			if r := recover(); r != nil {
				logger.LogError(c, fmt.Sprintf("data handler goroutine panic: %v", r))
				info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonPanic, fmt.Errorf("handler panic: %v", r))
			}
			stop()
			wg.Done()
		}()
		for data := range dataChan {
			sr.reset()
			func() {
				writeMutex.Lock()
				defer writeMutex.Unlock()
				// The streamResponseWriter budget sets the deadline on every write.
				dataHandler(data, sr)
			}()
			if sr.IsStopped() {
				return
			}
		}
	})

	// Scanner goroutine with improved error handling
	wg.Add(1)
	common.RelayCtxGo(ctx, func() {
		defer func() {
			close(dataChan)
			if r := recover(); r != nil {
				logger.LogError(c, fmt.Sprintf("scanner goroutine panic: %v", r))
				info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonPanic, fmt.Errorf("scanner panic: %v", r))
			}
			stop()
			logger.LogDebug(c, "scanner goroutine exited")
			wg.Done()
		}()

		for scanner.Scan() {
			select {
			case <-stopChan:
				return
			case <-ctx.Done():
				return
			default:
			}

			resetTimeout()
			rawData := scanner.Text()
			logger.LogDebug(c, "stream scanner frame received: bytes=%d", len(rawData))
			data := strings.TrimSpace(rawData)
			data, hasDataPrefix := strings.CutPrefix(data, "data:")
			data = strings.TrimSpace(data)
			if !hasDataPrefix && data != "[DONE]" {
				continue
			}
			if data == "" {
				continue
			}
			if data == "[DONE]" {
				info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonDone, nil)
				logger.LogDebug(c, "received [DONE], stopping scanner")
				return
			}
			if outputSensitiveMatchers != nil {
				matched := false
				for _, fragment := range relaycommon.OutputSensitiveTextFragments(data) {
					if info.ThinkingProcessStrip && fragment.HiddenThinking {
						continue
					}
					matcher := outputSensitiveMatchers[fragment.Path]
					if matcher == nil {
						matcher = relaycommon.NewOutputSensitiveMatcherForConfig(outputSensitiveConfig)
						outputSensitiveMatchers[fragment.Path] = matcher
					}
					if hit, _ := matcher.Scan(fragment.Text); hit {
						matched = true
						break
					}
				}
				if matched {
					common.SetContextKey(c, constant.ContextKeyAdminRejectReason, "output_sensitive")
					info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonOutputSensitive, relaycommon.OutputSensitiveError())
					if outputSensitiveConfig.Action == "error" {
						info.StreamStatus.RecordError("output sensitive pattern matched")
					}
					logger.LogWarn(c, fmt.Sprintf("output sensitive pattern matched, action=%s", outputSensitiveConfig.Action))
					writeMutex.Lock()
					EmitOutputSensitiveStreamTerminal(c, info)
					writeMutex.Unlock()
					return
				}
			}
			info.SetFirstResponseTime()
			info.ReceivedResponseCount++

			select {
			case dataChan <- data:
			case <-ctx.Done():
				return
			case <-stopChan:
				return
			}
		}

		// Closing the upstream body is part of stopping the scanner. Its EOF or
		// read error must not replace the actual stop reason or add a false error.
		if ctx.Err() != nil {
			return
		}
		if err := scanner.Err(); err != nil {
			if err != io.EOF {
				logger.LogError(c, "scanner error: "+err.Error())
				info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonScannerErr, err)
			}
		}
		info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonEOF, nil)
	})

	// 主循环等待完成或超时
	select {
	case <-ticker.C:
		info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonTimeout, nil)
	case <-stopChan:
		// EndReason already set by the goroutine that triggered stopChan
	case <-c.Request.Context().Done():
		// 客户端断开：立即 cleanup 关闭上游 resp.Body，解除 scanner 阻塞并让上游停止生成，
		// 避免为已放弃的请求继续消费上游 token。
		info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonClientGone, c.Request.Context().Err())
	}

	cleanup()
	// All stream goroutines have joined. Done from a successfully delivered
	// protocol completion can now correct a cancellation that won while its
	// callback was still running. Bare EOF and failed writes never qualify.
	if sr.done &&
		info.StreamStatus.EndReason == relaycommon.StreamEndReasonClientGone &&
		errors.Is(info.StreamStatus.EndError, context.Canceled) && !info.StreamStatus.HasErrors() {
		info.StreamStatus.EndReason = relaycommon.StreamEndReasonDone
		info.StreamStatus.EndError = nil
	}
	if info.StreamStatus.IsNormalEnd() && !info.StreamStatus.HasErrors() {
		logger.LogInfo(c, fmt.Sprintf("stream ended: %s", info.StreamStatus.Summary()))
	} else {
		logger.LogError(c, fmt.Sprintf("stream ended: %s, received=%d", info.StreamStatus.Summary(), info.ReceivedResponseCount))
	}
}
