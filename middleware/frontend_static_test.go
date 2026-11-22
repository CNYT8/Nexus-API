package middleware

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type frontendTestFS struct {
	http.FileSystem
}

func (fs frontendTestFS) Exists(_ string, name string) bool {
	file, err := fs.Open(name)
	if err != nil {
		return false
	}
	defer file.Close()
	return true
}

func newFrontendTestFS(body string) frontendTestFS {
	return frontendTestFS{http.FS(fstest.MapFS{
		"assets/app.js": &fstest.MapFile{Data: []byte(body), ModTime: time.Unix(1700000000, 0)},
	})}
}

func frontendTestRequest(router *gin.Engine, method, target, encoding string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, target, nil)
	request.Header.Set("Accept-Encoding", encoding)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

func frontendTestDecode(t *testing.T, response *httptest.ResponseRecorder) string {
	t.Helper()
	if response.Header().Get("Content-Encoding") != "gzip" {
		return response.Body.String()
	}
	reader, err := gzip.NewReader(response.Body)
	require.NoError(t, err)
	defer reader.Close()
	data, err := io.ReadAll(reader)
	require.NoError(t, err)
	return string(data)
}

func TestFrontendStaticEncodingAndHTTPResponses(t *testing.T) {
	payload := strings.Repeat("const value = 'test';\n", 256)
	router := gin.New()
	router.Use(ServeFrontendFiles(newFrontendTestFS(payload)))
	response := frontendTestRequest(router, http.MethodGet, "/assets/app.js", "br, gzip")
	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, "gzip", response.Header().Get("Content-Encoding"))
	require.Equal(t, strconv.Itoa(response.Body.Len()), response.Header().Get("Content-Length"))
	compressedLength := response.Header().Get("Content-Length")
	require.Equal(t, payload, frontendTestDecode(t, response), "one gzip decode must yield the original bytes")
	require.Contains(t, response.Header().Values("Vary"), "Accept-Encoding")

	response = frontendTestRequest(router, http.MethodGet, "/assets/app.js", "gzip;q=0, *;q=1")
	require.Equal(t, http.StatusOK, response.Code)
	require.Empty(t, response.Header().Get("Content-Encoding"))
	require.Equal(t, payload, response.Body.String())

	response = frontendTestRequest(router, http.MethodHead, "/assets/app.js", "gzip")
	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, compressedLength, response.Header().Get("Content-Length"))
	require.Empty(t, response.Body.String())

	request := httptest.NewRequest(http.MethodGet, "/assets/app.js", nil)
	request.Header.Set("Accept-Encoding", "gzip")
	request.Header.Set("Range", "bytes=0-4")
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	require.Equal(t, http.StatusPartialContent, response.Code)
	require.Empty(t, response.Header().Get("Content-Encoding"))
	require.Equal(t, payload[:5], response.Body.String())

	request = httptest.NewRequest(http.MethodGet, "/assets/app.js", nil)
	request.Header.Set("Accept-Encoding", "gzip")
	request.Header.Set("If-Modified-Since", time.Unix(1700000000, 0).UTC().Format(http.TimeFormat))
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	require.Equal(t, http.StatusNotModified, response.Code)
	require.Empty(t, response.Body.String())

	response = frontendTestRequest(router, http.MethodGet, "/assets/missing.js", "gzip")
	require.Equal(t, http.StatusNotFound, response.Code)
}

func TestFrontendStaticGzipNegotiation(t *testing.T) {
	for _, header := range []string{"", "br", "gzip;q=0", "gzip;q=NaN", "gzip;q=2", "gzip;q=invalid", "*;q=1,gzip;q=0"} {
		require.False(t, acceptsFrontendGzip(header), header)
	}
	for _, header := range []string{"gzip", "GZIP;Q=0.5", "br, *;q=1", "gzip;q=1, *;q=0"} {
		require.True(t, acceptsFrontendGzip(header), header)
	}
}

func TestFrontendThemeCacheAndIndex(t *testing.T) {
	previousTheme, previousEnable := common.GetTheme(), common.GlobalWebRateLimitEnable
	common.GlobalWebRateLimitEnable = false
	t.Cleanup(func() {
		common.SetTheme(previousTheme)
		common.GlobalWebRateLimitEnable = previousEnable
	})
	t.Setenv("GLOBAL_STATIC_RATE_LIMIT_ENABLE", "false")
	defaultBody := strings.Repeat("default();\n", 128)
	classicBody := strings.Repeat("classic();\n", 128)
	router := gin.New()
	router.Use(Frontend(newFrontendTestFS(defaultBody), newFrontendTestFS(classicBody)))
	index := FrontendIndex([]byte("<html>default injected</html>"), []byte("<html>classic injected</html>"))
	router.NoRoute(func(c *gin.Context) {
		if strings.HasPrefix(c.Request.URL.Path, "/assets") {
			c.Status(http.StatusNotFound)
			return
		}
		index(c)
	})
	for _, theme := range []string{"default", "classic", "default"} {
		common.SetTheme(theme)
		response := frontendTestRequest(router, http.MethodGet, "/assets/app.js", "gzip")
		require.Equal(t, http.StatusOK, response.Code)
		want := defaultBody
		if theme == "classic" {
			want = classicBody
		}
		require.Equal(t, want, frontendTestDecode(t, response))
		response = frontendTestRequest(router, http.MethodGet, "/console", "gzip")
		require.Equal(t, http.StatusOK, response.Code)
		require.Equal(t, "no-cache", response.Header().Get("Cache-Control"))
		require.Equal(t, "<html>"+theme+" injected</html>", frontendTestDecode(t, response))
	}
	common.SetTheme("default")
	router.NoRoute(func(c *gin.Context) {
		common.SetTheme("classic")
		index(c)
	})
	response := frontendTestRequest(router, http.MethodGet, "/console", "gzip")
	require.Equal(t, "<html>default injected</html>", frontendTestDecode(t, response), "select the theme once per request")
}

func TestFrontendRateBucketsSeparatePagesAndAssets(t *testing.T) {
	previousEnabled, previousNum := common.GlobalWebRateLimitEnable, common.GlobalWebRateLimitNum
	previousDuration, previousRedis := common.GlobalWebRateLimitDuration, common.RedisEnabled
	common.GlobalWebRateLimitEnable, common.GlobalWebRateLimitNum = true, 1
	common.GlobalWebRateLimitDuration, common.RedisEnabled = 60, false
	t.Cleanup(func() {
		common.GlobalWebRateLimitEnable, common.GlobalWebRateLimitNum = previousEnabled, previousNum
		common.GlobalWebRateLimitDuration, common.RedisEnabled = previousDuration, previousRedis
	})
	t.Setenv("GLOBAL_STATIC_RATE_LIMIT_ENABLE", "true")
	t.Setenv("GLOBAL_STATIC_RATE_LIMIT", "2")
	t.Setenv("GLOBAL_STATIC_RATE_LIMIT_DURATION", "60")
	fs := newFrontendTestFS(strings.Repeat("static();\n", 32))
	router := gin.New()
	router.Use(Frontend(fs, fs))
	router.NoRoute(FrontendIndex([]byte("index"), []byte("index")))
	for _, target := range []string{"/assets/app.js", "/assets/app.js", "/console"} {
		response := frontendTestRequest(router, http.MethodGet, target, "gzip")
		require.Equal(t, http.StatusOK, response.Code, target)
	}
	for _, target := range []string{"/assets/app.js", "/console", "/assets/missing.js"} {
		response := frontendTestRequest(router, http.MethodGet, target, "gzip")
		require.Equal(t, http.StatusTooManyRequests, response.Code, target)
	}
}
