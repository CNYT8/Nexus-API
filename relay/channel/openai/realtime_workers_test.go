package openai

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestRealtimeWorkersCloseAndJoinBeforeReturning(t *testing.T) {
	started, closed := make(chan struct{}), make(chan struct{})
	finishing, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	releaseWorker := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseWorker()
	returned := make(chan error, 1)
	usage := &dto.RealtimeUsage{}
	go func() {
		returned <- runRealtimeWorkers(context.Background(), func() { close(closed) },
			func(context.Context) error {
				<-started
				return errors.New("client disconnected")
			}, func(ctx context.Context) error {
				close(started)
				<-ctx.Done()
				<-closed
				close(finishing)
				<-release
				usage.TotalTokens = 7
				return nil
			})
	}()
	select {
	case <-finishing:
	case <-time.After(3 * time.Second):
		t.Fatal("reader exit did not cancel and close the other direction")
	}
	select {
	case <-returned:
		t.Fatal("returned before the last usage writer exited")
	default:
	}
	releaseWorker()
	select {
	case err := <-returned:
		require.EqualError(t, err, "client disconnected")
	case <-time.After(3 * time.Second):
		t.Fatal("workers did not join")
	}
	require.Equal(t, 7, usage.TotalTokens)
}

func TestRealtimeWorkersPanicAndConcurrentErrorsDoNotBlock(t *testing.T) {
	for _, panicReader := range []bool{false, true} {
		name := "reader error"
		if panicReader {
			name = "reader panic"
		}
		t.Run(name, func(t *testing.T) {
			closed := make(chan struct{})
			returned := make(chan error, 1)
			go func() {
				returned <- runRealtimeWorkers(context.Background(), func() { close(closed) },
					func(context.Context) error {
						if panicReader {
							panic("broken event")
						}
						return errors.New("broken event")
					}, func(ctx context.Context) error {
						<-ctx.Done()
						return errors.New("peer closed")
					})
			}()
			select {
			case err := <-returned:
				require.ErrorContains(t, err, "broken event")
			case <-time.After(3 * time.Second):
				t.Fatal("reader error or panic blocked shutdown")
			}
			select {
			case <-closed:
			default:
				t.Fatal("returned before sockets were closed")
			}
		})
	}
}

func realtimeWorkerSocketPair(t *testing.T) (*websocket.Conn, *websocket.Conn) {
	t.Helper()
	accepted := make(chan *websocket.Conn, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{}
		if conn, err := upgrader.Upgrade(w, r, nil); err == nil {
			accepted <- conn
		}
	}))
	t.Cleanup(server.Close)
	peer, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = peer.Close() })
	require.NoError(t, peer.SetReadDeadline(time.Now().Add(5*time.Second)))
	require.NoError(t, peer.SetWriteDeadline(time.Now().Add(5*time.Second)))
	select {
	case conn := <-accepted:
		t.Cleanup(func() { _ = conn.Close() })
		return conn, peer
	case <-time.After(3 * time.Second):
		t.Fatal("websocket upgrade did not complete")
		return nil, nil
	}
}

func realtimeHandlerFixture(t *testing.T) (*gin.Context, *relaycommon.RelayInfo, *websocket.Conn, *websocket.Conn, context.CancelFunc) {
	t.Helper()
	client, clientPeer := realtimeWorkerSocketPair(t)
	target, targetPeer := realtimeWorkerSocketPair(t)
	c, engine := gin.CreateTestContext(httptest.NewRecorder())
	// Gin's c.Done() is nil by default; the handler must use Request.Context().
	require.False(t, engine.ContextWithFallback)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/realtime", nil).WithContext(ctx)
	info := &relaycommon.RelayInfo{
		ClientWs: client, TargetWs: target, UsePrice: true,
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "realtime-local-test"},
	}
	return c, info, clientPeer, targetPeer, cancel
}

type realtimeHandlerResult struct {
	err   *types.NewAPIError
	usage *dto.RealtimeUsage
}

func startRealtimeHandler(t *testing.T, c *gin.Context, info *relaycommon.RelayInfo) <-chan realtimeHandlerResult {
	t.Helper()
	result := make(chan realtimeHandlerResult, 1)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		err, usage := OpenaiRealtimeHandler(c, info)
		result <- realtimeHandlerResult{err: err, usage: usage}
	}()
	t.Cleanup(func() {
		_ = info.ClientWs.Close()
		_ = info.TargetWs.Close()
		select {
		case <-finished:
		case <-time.After(3 * time.Second):
			t.Error("realtime handler survived test cleanup")
		}
	})
	return result
}

func awaitRealtimeHandler(t *testing.T, result <-chan realtimeHandlerResult) *dto.RealtimeUsage {
	t.Helper()
	select {
	case got := <-result:
		require.Nil(t, got.err)
		require.NotNil(t, got.usage)
		return got.usage
	case <-time.After(3 * time.Second):
		t.Fatal("realtime handler did not finish")
		return nil
	}
}

func TestRealtimeHandlerCancellationUnblocksIdleSockets(t *testing.T) {
	c, info, client, target, cancel := realtimeHandlerFixture(t)
	result := startRealtimeHandler(t, c, info)
	cancel()
	usage := awaitRealtimeHandler(t, result)
	require.Zero(t, usage.TotalTokens)
	require.True(t, c.GetBool("responses_websocket"))
	for _, peer := range []*websocket.Conn{client, target} {
		_, _, err := peer.ReadMessage()
		require.Error(t, err)
		var netErr net.Error
		if errors.As(err, &netErr) {
			require.False(t, netErr.Timeout(), "socket remained open until its deadline")
		}
	}
}

func TestRealtimeHandlerJoinsOnMalformedEvent(t *testing.T) {
	for _, payload := range []string{`{`, `{"type":"response.done"}`} {
		t.Run(payload, func(t *testing.T) {
			c, info, _, target, _ := realtimeHandlerFixture(t)
			result := startRealtimeHandler(t, c, info)
			require.NoError(t, target.WriteMessage(websocket.TextMessage, []byte(payload)))
			require.Zero(t, awaitRealtimeHandler(t, result).TotalTokens)
		})
	}
}

func realtimeForwardEvent(t *testing.T, source, destination *websocket.Conn, event dto.RealtimeEvent) {
	t.Helper()
	require.NoError(t, source.WriteJSON(event))
	var forwarded dto.RealtimeEvent
	require.NoError(t, destination.ReadJSON(&forwarded))
	require.Equal(t, event.Type, forwarded.Type)
}

func TestRealtimeHandlerResponseUsageAndLocalTailCountOnce(t *testing.T) {
	c, info, client, target, cancel := realtimeHandlerFixture(t)
	result := startRealtimeHandler(t, c, info)
	local := dto.RealtimeEvent{Type: dto.RealtimeEventResponseAudioTranscriptionDelta, Delta: "local output after response"}
	// The estimate preceding response.done is superseded by upstream usage.
	realtimeForwardEvent(t, target, client, local)
	upstream := &dto.RealtimeUsage{
		TotalTokens: 11, InputTokens: 7, OutputTokens: 4,
		InputTokenDetails:  dto.InputTokenDetails{TextTokens: 5, AudioTokens: 2, CachedTokens: 3},
		OutputTokenDetails: dto.OutputTokenDetails{TextTokens: 1, AudioTokens: 3},
	}
	realtimeForwardEvent(t, target, client, dto.RealtimeEvent{
		Type: dto.RealtimeEventTypeResponseDone, Response: &dto.RealtimeResponse{Usage: upstream},
	})
	realtimeForwardEvent(t, target, client, local)
	cancel()
	usage := awaitRealtimeHandler(t, result)
	estimate := service.CountTextToken(local.Delta, info.UpstreamModelName)
	require.Positive(t, estimate)
	want := *upstream
	want.TotalTokens += estimate
	want.OutputTokens += estimate
	want.OutputTokenDetails.TextTokens += estimate
	require.Equal(t, &want, usage)
	require.True(t, c.GetBool("responses_websocket"))
}

func TestRealtimeHandlerFailedPreconsumeDoesNotRecountTail(t *testing.T) {
	// Missing users table forces an error before any quota mutation/cache work.
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	oldDB, oldRedis := model.DB, common.RedisEnabled
	model.DB, common.RedisEnabled = db, false
	t.Cleanup(func() { model.DB, common.RedisEnabled = oldDB, oldRedis })
	for _, failure := range []string{"query error", "query panic"} {
		t.Run(failure, func(t *testing.T) {
			if failure == "query panic" {
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("realtime_test_panic", func(*gorm.DB) {
					panic("quota query panicked")
				}))
				t.Cleanup(func() { _ = db.Callback().Query().Remove("realtime_test_panic") })
			}
			for _, reportedUsage := range []bool{true, false} {
				name := "upstream usage"
				if !reportedUsage {
					name = "local estimate"
				}
				t.Run(name, func(t *testing.T) {
					c, info, client, target, _ := realtimeHandlerFixture(t)
					info.UsePrice = false
					result := startRealtimeHandler(t, c, info)
					local := dto.RealtimeEvent{Type: dto.RealtimeEventResponseAudioTranscriptionDelta, Delta: "partial local output"}
					realtimeForwardEvent(t, target, client, local)
					estimate := service.CountTextToken(local.Delta, info.UpstreamModelName)
					require.Positive(t, estimate)
					want := &dto.RealtimeUsage{TotalTokens: estimate, OutputTokens: estimate,
						OutputTokenDetails: dto.OutputTokenDetails{TextTokens: estimate}}
					response := &dto.RealtimeResponse{}
					if reportedUsage {
						want = &dto.RealtimeUsage{TotalTokens: 9, InputTokens: 6, OutputTokens: 3,
							InputTokenDetails:  dto.InputTokenDetails{TextTokens: 4, AudioTokens: 2},
							OutputTokenDetails: dto.OutputTokenDetails{TextTokens: 3}}
						response.Usage = want
					}
					require.NoError(t, target.WriteJSON(dto.RealtimeEvent{Type: dto.RealtimeEventTypeResponseDone, Response: response}))
					require.Equal(t, want, awaitRealtimeHandler(t, result))
				})
			}
		})
	}
}

func TestRealtimeHandlerConcurrentDirectionsKeepUsageConsistent(t *testing.T) {
	c, info, client, target, cancel := realtimeHandlerFixture(t)
	result := startRealtimeHandler(t, c, info)
	const messages = 40
	input := dto.RealtimeEvent{Type: dto.RealtimeEventTypeSessionUpdate, Session: &dto.RealtimeSession{
		Instructions: "concurrent input", Tools: []dto.RealTimeTool{{Name: "lookup"}},
	}}
	output := dto.RealtimeEvent{Type: dto.RealtimeEventResponseAudioTranscriptionDelta, Delta: "concurrent output"}
	finished := make(chan error, 2)
	send := func(source, destination *websocket.Conn, event dto.RealtimeEvent) {
		for i := 0; i < messages; i++ {
			if err := source.WriteJSON(event); err != nil {
				finished <- err
				return
			}
			if _, _, err := destination.ReadMessage(); err != nil {
				finished <- err
				return
			}
		}
		finished <- nil
	}
	go send(client, target, input)
	go send(target, client, output)
	for i := 0; i < 2; i++ {
		select {
		case err := <-finished:
			require.NoError(t, err)
		case <-time.After(6 * time.Second):
			t.Fatal("concurrent forwarding stalled")
		}
	}
	cancel()
	usage := awaitRealtimeHandler(t, result)
	inputTokens := messages * service.CountTextToken(input.Session.Instructions, info.UpstreamModelName)
	outputTokens := messages * service.CountTextToken(output.Delta, info.UpstreamModelName)
	require.Equal(t, inputTokens, usage.InputTokens)
	require.Equal(t, outputTokens, usage.OutputTokens)
	require.Equal(t, inputTokens+outputTokens, usage.TotalTokens)
}
