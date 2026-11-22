package channel

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type pingDeadlineWriter struct {
	*httptest.ResponseRecorder
	deadline   time.Time
	writeCalls int
	flushCalls int
	failFlush  bool
}

func (w *pingDeadlineWriter) SetWriteDeadline(deadline time.Time) error {
	w.deadline = deadline
	return nil
}

func (w *pingDeadlineWriter) Write(data []byte) (int, error) {
	w.writeCalls++
	if w.deadline.IsZero() {
		return 0, errors.New("保活写入未设置截止时间")
	}
	if !w.failFlush {
		return 0, os.ErrDeadlineExceeded
	}
	return w.ResponseRecorder.Write(data)
}

func (w *pingDeadlineWriter) Flush() {
	w.flushCalls++
}

func (w *pingDeadlineWriter) FlushError() error {
	return os.ErrDeadlineExceeded
}

func TestPreResponsePingInstallsWriteDeadlineAndStopsOnFailure(t *testing.T) {
	for _, tc := range []struct {
		name      string
		failFlush bool
	}{
		{name: "写入超时"},
		{name: "刷新超时", failFlush: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			writer := &pingDeadlineWriter{
				ResponseRecorder: httptest.NewRecorder(),
				failFlush:        tc.failFlush,
			}
			c, _ := gin.CreateTestContext(writer)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			before := time.Now()
			stop, done := startPingKeepAlive(c, time.Millisecond)
			defer stop()
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Fatal("前置保活写入失败后未退出")
			}
			require.Equal(t, 1, writer.writeCalls)
			require.True(t, writer.deadline.After(before.Add(29*time.Second)))
			require.True(t, writer.deadline.Before(time.Now().Add(31*time.Second)))
			if tc.failFlush {
				require.Greater(t, writer.flushCalls, 0)
			}

			// 后续扫描阶段再次安装时，不能丢弃前置保活已经积累的预算。
			wrapped := c.Writer
			helper.EnsureStreamWriteBudget(c)
			require.Same(t, wrapped, c.Writer)
		})
	}
}

func TestPreResponsePingCancellationDoesNotWrite(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	stop, done := startPingKeepAlive(c, time.Hour)
	stop()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("取消后前置保活未退出")
	}
	require.Empty(t, recorder.Body.String())
}
