package middleware

import (
	"bytes"
	"encoding/binary"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/gin-gonic/gin"
	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/require"
)

func readBoundedZstd(t *testing.T, payload []byte) ([]byte, error) {
	t.Helper()
	var data []byte
	var readErr error
	router := gin.New()
	router.POST("/", DecompressRequestMiddleware(), func(c *gin.Context) {
		data, readErr = io.ReadAll(c.Request.Body)
	})
	request := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(payload))
	request.Header.Set("Content-Encoding", "zstd")
	router.ServeHTTP(httptest.NewRecorder(), request)
	return data, readErr
}

func TestDecompressZstdWindowAndOutputLimits(t *testing.T) {
	previous := constant.MaxRequestBodyMB
	constant.MaxRequestBodyMB = 16
	t.Cleanup(func() { constant.MaxRequestBodyMB = previous })
	magic := []byte{0x28, 0xb5, 0x2f, 0xfd}
	lastRLE := []byte{0x0b, 0x00, 0x00, 'a'}
	frame := func(window byte) []byte {
		return append(append(append([]byte{}, magic...), 0x00, window), lastRLE...)
	}
	singleSegment := binary.LittleEndian.AppendUint64(append(append([]byte{}, magic...), 0xe0), (8<<20)+1)
	singleSegment = append(singleSegment, lastRLE...)
	cases := []struct {
		name string
		body []byte
		want string
		err  error
	}{
		{"8MiB window", frame(0x68), "a", nil},
		{"9MiB window", frame(0x69), "", zstd.ErrWindowSizeExceeded},
		{"512MiB window", frame(0x98), "", zstd.ErrWindowSizeExceeded},
		{"oversized single segment", singleSegment, "", zstd.ErrDecoderSizeExceeded},
		{"later oversized frame", append(frame(0x50), frame(0x69)...), "a", zstd.ErrWindowSizeExceeded},
		{"later oversized single segment", append(frame(0x50), singleSegment...), "a", zstd.ErrDecoderSizeExceeded},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data, err := readBoundedZstd(t, tc.body)
			if tc.err == nil {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, tc.err)
			}
			require.Equal(t, tc.want, string(data))
		})
	}

	encoder, err := zstd.NewWriter(nil, zstd.WithWindowSize(1<<20), zstd.WithSingleSegment(false), zstd.WithEncoderConcurrency(1))
	require.NoError(t, err)
	payload := bytes.Repeat([]byte("a"), 9<<20)
	compressed := encoder.EncodeAll(payload, nil)
	require.NoError(t, encoder.Close())
	data, err := readBoundedZstd(t, compressed)
	require.NoError(t, err, "a bounded window must allow bodies larger than 8MiB")
	require.Equal(t, payload, data)

	constant.MaxRequestBodyMB = 1
	data, err = readBoundedZstd(t, compressed)
	var sizeErr *http.MaxBytesError
	require.ErrorAs(t, err, &sizeErr)
	require.Equal(t, int64(1<<20), sizeErr.Limit)
	require.Len(t, data, 1<<20)
	_, err = readBoundedZstd(t, frame(0x68))
	require.ErrorIs(t, err, zstd.ErrWindowSizeExceeded)
}

type countedEncodedBody struct {
	*bytes.Buffer
	reads, closes int
}

func (body *countedEncodedBody) Read(p []byte) (int, error) {
	body.reads++
	return body.Buffer.Read(p)
}

func (body *countedEncodedBody) Close() error {
	body.closes++
	return nil
}

func TestDecompressZstdDoesNotReadBeforeAuthAbort(t *testing.T) {
	// Expose Bytes/Len too: the decoder must not eagerly DecodeAll a small buffer.
	body := &countedEncodedBody{Buffer: bytes.NewBufferString("unread invalid zstd")}
	router := gin.New()
	router.POST("/", DecompressRequestMiddleware(), func(c *gin.Context) {
		c.AbortWithStatus(http.StatusUnauthorized)
	})
	request := httptest.NewRequest(http.MethodPost, "/", nil)
	request.Body = body
	request.Header.Set("Content-Encoding", "zstd")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	require.Equal(t, http.StatusUnauthorized, recorder.Code)
	require.Zero(t, body.reads)
	require.Equal(t, 1, body.closes)
}

func TestDecompressClosesOriginalWhenBodyIsReplacedOrPanics(t *testing.T) {
	for _, panicHandler := range []bool{false, true} {
		body := &countedEncodedBody{Buffer: bytes.NewBufferString("body")}
		router := gin.New()
		router.Use(gin.CustomRecoveryWithWriter(io.Discard, func(c *gin.Context, _ any) {
			c.AbortWithStatus(http.StatusInternalServerError)
		}))
		router.POST("/", DecompressRequestMiddleware(), func(c *gin.Context) {
			if panicHandler {
				panic("test panic")
			}
			require.NoError(t, c.Request.Body.Close())
			c.Request.Body = io.NopCloser(strings.NewReader("replay"))
		})
		request := httptest.NewRequest(http.MethodPost, "/", nil)
		request.Body = body
		router.ServeHTTP(httptest.NewRecorder(), request)
		require.Equal(t, 1, body.closes)
	}
}
