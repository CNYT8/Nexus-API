package middleware

import (
	"bytes"
	"compress/gzip"
	"io"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-contrib/static"
	"github.com/gin-gonic/gin"
)

const frontendThemeKey = "frontend_theme"

// Frontend replaces the web router's gzip, GlobalWebRateLimit, Cache and
// static.Serve middlewares. Each request selects its theme once; each theme has
// a separate compression cache, while both share the same per-IP rate buckets.
func Frontend(defaultFS, classicFS static.ServeFileSystem) gin.HandlerFunc {
	defaultFiles := ServeFrontendFiles(defaultFS)
	classicFiles := ServeFrontendFiles(classicFS)
	limiter := frontendRateLimitFor(func(c *gin.Context) static.ServeFileSystem {
		if FrontendTheme(c) == "classic" {
			return classicFS
		}
		return defaultFS
	})
	return func(c *gin.Context) {
		c.Set(frontendThemeKey, common.GetTheme())
		limiter(c)
		if c.IsAborted() {
			return
		}
		if FrontendTheme(c) == "classic" {
			classicFiles(c)
		} else {
			defaultFiles(c)
		}
	}
}

// FrontendTheme also lets NoRoute choose the same theme as the static handler.
func FrontendTheme(c *gin.Context) string {
	if theme, ok := c.Get(frontendThemeKey); ok {
		return theme.(string)
	}
	return common.GetTheme()
}

type frontendCompressedFile struct {
	data []byte
}

type frontendCompressionCache struct {
	mu      sync.Mutex
	entries map[string]frontendCompressedFile
	bytes   int
}

// Bound retained gzip data and compression work even if served from a mutable FS.
// The public handler is intended for an immutable embedded FS, one per theme.
const frontendMaxCachedFile = 8 << 20
const frontendMaxCacheBytes = 64 << 20
const frontendMaxCacheEntries = 4096

func (cache *frontendCompressionCache) compressed(name string, file http.File, size int64) []byte {
	if size <= 0 || size > frontendMaxCachedFile {
		return nil
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cached, ok := cache.entries[name]; ok {
		return cached.data
	}
	if len(cache.entries) >= frontendMaxCacheEntries || cache.bytes >= frontendMaxCacheBytes {
		return nil
	}
	var buffer bytes.Buffer
	writer, _ := gzip.NewWriterLevel(&buffer, gzip.DefaultCompression)
	n, err := io.Copy(writer, io.LimitReader(file, frontendMaxCachedFile+1))
	closeErr := writer.Close()
	_, seekErr := file.Seek(0, io.SeekStart)
	if err != nil || closeErr != nil || seekErr != nil || n != size || buffer.Len() >= int(size) {
		cache.entries[name] = frontendCompressedFile{}
		return nil
	}
	if cache.bytes+buffer.Len() > frontendMaxCacheBytes {
		cache.entries[name] = frontendCompressedFile{}
		return nil
	}
	// Retain only the bytes counted against the budget, not buffer growth slack.
	data := bytes.Clone(buffer.Bytes())
	cache.entries[name] = frontendCompressedFile{data: data}
	cache.bytes += len(data)
	return data
}

// ServeFrontendFiles caches compressed bytes once per canonical asset path.
// Do not wrap it in gin-contrib/gzip: this handler owns the selected encoding.
// ServeContent retains conditional, range and HEAD semantics for each variant.
func ServeFrontendFiles(frontendFS static.ServeFileSystem) gin.HandlerFunc {
	cache := &frontendCompressionCache{entries: make(map[string]frontendCompressedFile)}
	return func(c *gin.Context) {
		if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead {
			return
		}
		name := path.Clean("/" + c.Request.URL.Path)
		if name == "/" {
			return
		}
		file, err := frontendFS.Open(name)
		if err != nil {
			return
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil || info.IsDir() {
			return
		}
		if strings.HasSuffix(name, "/index.html") {
			c.Redirect(http.StatusMovedPermanently, "./")
			c.Abort()
			return
		}
		c.Header("Cache-Control", "public, max-age=604800")
		contentType := mime.TypeByExtension(path.Ext(name))
		if contentType != "" {
			c.Header("Content-Type", contentType)
		}
		if frontendCompressible(contentType) {
			addFrontendVary(c.Writer.Header(), "Accept-Encoding")
			// Serve range requests as identity bytes, including multipart ranges.
			if c.GetHeader("Range") == "" && acceptsFrontendGzip(c.GetHeader("Accept-Encoding")) && c.Writer.Header().Get("Content-Encoding") == "" {
				if data := cache.compressed(name, file, info.Size()); data != nil {
					c.Header("Content-Encoding", "gzip")
					c.Header("Content-Length", strconv.Itoa(len(data)))
					http.ServeContent(c.Writer, c.Request, info.Name(), info.ModTime(), bytes.NewReader(data))
					c.Abort()
					return
				}
			}
		}
		http.ServeContent(c.Writer, c.Request, info.Name(), info.ModTime(), file)
		c.Abort()
	}
}

func frontendCompressible(contentType string) bool {
	contentType = strings.Split(contentType, ";")[0]
	return strings.HasPrefix(contentType, "text/") || contentType == "application/javascript" ||
		contentType == "application/json" || contentType == "application/wasm" || contentType == "image/svg+xml"
}

func acceptsFrontendGzip(header string) bool {
	var wildcard bool
	for _, part := range strings.Split(header, ",") {
		fields := strings.Split(part, ";")
		encoding := strings.TrimSpace(fields[0])
		if !strings.EqualFold(encoding, "gzip") && encoding != "*" {
			continue
		}
		quality := 1.0
		for _, parameter := range fields[1:] {
			key, value, ok := strings.Cut(strings.TrimSpace(parameter), "=")
			if ok && strings.EqualFold(key, "q") {
				parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
				if err != nil || !(parsed >= 0 && parsed <= 1) {
					quality = 0
				} else {
					quality = parsed
				}
			}
		}
		if strings.EqualFold(encoding, "gzip") {
			return quality > 0
		}
		wildcard = quality > 0
	}
	return wildcard
}

func addFrontendVary(header http.Header, value string) {
	for _, existing := range header.Values("Vary") {
		for _, token := range strings.Split(existing, ",") {
			if strings.EqualFold(strings.TrimSpace(token), value) || strings.TrimSpace(token) == "*" {
				return
			}
		}
	}
	header.Add("Vary", value)
}

// FrontendIndex precompresses the already-injected index pages once. Construct
// this handler when registering NoRoute, then call it after the API/asset 404 guard.
func FrontendIndex(defaultIndex, classicIndex []byte) gin.HandlerFunc {
	compress := func(data []byte) []byte {
		var buffer bytes.Buffer
		writer := gzip.NewWriter(&buffer)
		_, _ = writer.Write(data)
		_ = writer.Close()
		return buffer.Bytes()
	}
	defaultGzip, classicGzip := compress(defaultIndex), compress(classicIndex)
	return func(c *gin.Context) {
		data, compressed := defaultIndex, defaultGzip
		if FrontendTheme(c) == "classic" {
			data, compressed = classicIndex, classicGzip
		}
		c.Header("Cache-Control", "no-cache")
		c.Header("Content-Type", "text/html; charset=utf-8")
		addFrontendVary(c.Writer.Header(), "Accept-Encoding")
		if c.GetHeader("Range") == "" && acceptsFrontendGzip(c.GetHeader("Accept-Encoding")) && c.Writer.Header().Get("Content-Encoding") == "" {
			data = compressed
			c.Header("Content-Encoding", "gzip")
			c.Header("Content-Length", strconv.Itoa(len(data)))
		}
		// Gin enters NoRoute with status 404, so explicitly select the SPA status.
		c.Status(http.StatusOK)
		http.ServeContent(c.Writer, c.Request, "index.html", time.Time{}, bytes.NewReader(data))
	}
}
