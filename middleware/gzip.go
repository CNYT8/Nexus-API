package middleware

import (
	"compress/gzip"
	"io"
	"net/http"
	"sync"

	"github.com/QuantumNous/new-api/constant"
	"github.com/andybalholm/brotli"
	"github.com/gin-gonic/gin"
	"github.com/klauspost/compress/zstd"
)

type readCloser struct {
	io.Reader
	closeFn   func() error
	closeOnce sync.Once
	closeErr  error
}

func (rc *readCloser) Close() error {
	rc.closeOnce.Do(func() {
		if rc.closeFn != nil {
			rc.closeErr = rc.closeFn()
		}
	})
	return rc.closeErr
}

func DecompressRequestMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Body == nil || c.Request.Method == http.MethodGet {
			c.Next()
			return
		}
		maxMB := constant.MaxRequestBodyMB
		if maxMB <= 0 {
			maxMB = 32
		}
		maxBytes := int64(maxMB) << 20

		origBody := c.Request.Body
		wrapMaxBytes := func(body io.ReadCloser) io.ReadCloser {
			return http.MaxBytesReader(c.Writer, body, maxBytes)
		}

		decompressed := false
		switch c.GetHeader("Content-Encoding") {
		case "gzip":
			gzipReader, err := gzip.NewReader(origBody)
			if err != nil {
				_ = origBody.Close()
				c.AbortWithStatus(http.StatusBadRequest)
				return
			}
			// Replace the request body with the decompressed data, and enforce a max size (post-decompression).
			c.Request.Body = wrapMaxBytes(&readCloser{
				Reader: gzipReader,
				closeFn: func() error {
					_ = gzipReader.Close()
					return origBody.Close()
				},
			})
			decompressed = true
		case "br":
			reader := brotli.NewReader(origBody)
			c.Request.Body = wrapMaxBytes(&readCloser{
				Reader: reader,
				closeFn: func() error {
					return origBody.Close()
				},
			})
			decompressed = true
		case "zstd":
			// Bound both ordinary and single-segment frame windows. The output
			// limit remains independent, so larger bodies can stream in small windows.
			reader, err := zstd.NewReader(origBody,
				zstd.WithDecoderMaxWindow(8<<20),
				zstd.WithDecoderMaxMemory(uint64(min(maxBytes, 8<<20))),
				zstd.WithDecoderConcurrency(1),
				// Never take the eager DecodeAll shortcut before auth consumes a body.
				zstd.WithDecodeBuffersBelow(0),
			)
			if err != nil {
				_ = origBody.Close()
				c.AbortWithStatus(http.StatusBadRequest)
				return
			}
			c.Request.Body = wrapMaxBytes(&readCloser{
				Reader: reader,
				closeFn: func() error {
					reader.Close()
					return origBody.Close()
				},
			})
			decompressed = true
		default:
			// Even for uncompressed bodies, enforce a max size to avoid huge request allocations.
			c.Request.Body = wrapMaxBytes(&readCloser{Reader: origBody, closeFn: origBody.Close})
		}
		if decompressed {
			c.Request.Header.Del("Content-Encoding")
			c.Request.Header.Del("Content-Length")
			c.Request.ContentLength = -1
		}

		// Capture this body: handlers may replace Request.Body with a replay reader.
		// Decoder resources must also be freed when auth aborts or a handler panics.
		defer c.Request.Body.Close()
		c.Next()
	}
}
