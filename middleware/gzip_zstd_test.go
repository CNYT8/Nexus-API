package middleware

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/require"
)

func TestDecompressRequestMiddlewareSupportsZstd(t *testing.T) {
	gin.SetMode(gin.TestMode)

	const payload = `{"model":"qwen-plus","messages":[{"role":"user","content":"hello"}]}`
	var compressed bytes.Buffer
	encoder, err := zstd.NewWriter(&compressed)
	require.NoError(t, err)
	_, err = encoder.Write([]byte(payload))
	require.NoError(t, err)
	require.NoError(t, encoder.Close())

	router := gin.New()
	router.Use(DecompressRequestMiddleware())
	router.POST("/test", func(c *gin.Context) {
		body, readErr := io.ReadAll(c.Request.Body)
		if readErr != nil {
			c.Status(http.StatusBadRequest)
			return
		}
		c.Data(http.StatusOK, "text/plain", body)
	})

	request := httptest.NewRequest(http.MethodPost, "/test", bytes.NewReader(compressed.Bytes()))
	request.Header.Set("Content-Encoding", "zstd")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, payload, recorder.Body.String())
	require.Empty(t, request.Header.Get("Content-Encoding"))
}
