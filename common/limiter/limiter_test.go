package limiter

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// noScriptRedis is a minimal in-process RESP server. EVALSHA always answers
// NOSCRIPT, as Redis does after a restart or SCRIPT FLUSH; EVAL succeeds.
type noScriptRedis struct {
	listener     net.Listener
	evalShaCalls atomic.Int32
	evalCalls    atomic.Int32
	wg           sync.WaitGroup
}

func startNoScriptRedis(t *testing.T) *noScriptRedis {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	server := &noScriptRedis{listener: listener}
	server.wg.Add(1)
	go server.serve()
	t.Cleanup(func() {
		_ = listener.Close()
		server.wg.Wait()
	})
	return server
}

func (s *noScriptRedis) serve() {
	defer s.wg.Done()
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			defer conn.Close()
			s.handle(conn)
		}()
	}
}

func (s *noScriptRedis) handle(conn net.Conn) {
	reader := bufio.NewReader(conn)
	for {
		args, err := readRESPArray(reader)
		if err != nil {
			return
		}
		if len(args) == 0 {
			return
		}
		var reply string
		switch strings.ToLower(args[0]) {
		case "evalsha":
			s.evalShaCalls.Add(1)
			reply = "-NOSCRIPT No matching script. Please use EVAL.\r\n"
		case "eval":
			s.evalCalls.Add(1)
			reply = ":1\r\n"
		default:
			reply = "+OK\r\n"
		}
		if _, err := io.WriteString(conn, reply); err != nil {
			return
		}
	}
}

func readRESPArray(reader *bufio.Reader) ([]string, error) {
	header, err := reader.ReadString('\n')
	if err != nil {
		return nil, err
	}
	header = strings.TrimRight(header, "\r\n")
	if !strings.HasPrefix(header, "*") {
		return nil, fmt.Errorf("unexpected RESP header %q", header)
	}
	count, err := strconv.Atoi(header[1:])
	if err != nil {
		return nil, err
	}
	args := make([]string, 0, count)
	for i := 0; i < count; i++ {
		lengthLine, err := reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		length, err := strconv.Atoi(strings.TrimRight(lengthLine, "\r\n")[1:])
		if err != nil {
			return nil, err
		}
		buf := make([]byte, length+2)
		if _, err := io.ReadFull(reader, buf); err != nil {
			return nil, err
		}
		args = append(args, string(buf[:length]))
	}
	return args, nil
}

func TestAllowFallsBackToEvalAfterScriptFlush(t *testing.T) {
	server := startNoScriptRedis(t)
	client := redis.NewClient(&redis.Options{
		Addr:       server.listener.Addr().String(),
		MaxRetries: -1,
	})
	t.Cleanup(func() { _ = client.Close() })

	redisLimiter := &RedisLimiter{client: client, limitScriptSHA: "deadbeef"}

	allowed, err := redisLimiter.Allow(
		context.Background(),
		"rateLimit:test-after-flush",
		WithCapacity(60),
		WithRate(60),
		WithRequested(60),
	)
	require.NoError(t, err)
	assert.True(t, allowed)
	assert.EqualValues(t, 1, server.evalShaCalls.Load())
	assert.EqualValues(t, 1, server.evalCalls.Load())
}
