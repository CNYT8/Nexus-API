package controller

import (
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
)

const taskArtifactDownloadTimeout = 2 * time.Minute

// Block our proxy route shapes on every host, including aliases of this server.
// Provider endpoints needing these routes or credentials keep using VideoProxy.
var taskArtifactProxyPath = regexp.MustCompile(`(^|/)(v1/videos/[^/]+/content|api/task/self/[^/]+/artifacts/[^/]+/content)/?$`)

var taskArtifactRequestHeaderNames = []string{
	"Range", "If-Range", "If-Match", "If-None-Match", "If-Modified-Since", "If-Unmodified-Since",
}

// taskArtifactRemoteURL is a syntax/recursion guard, not a replacement for the
// mandatory SSRF transport's DNS checks and protected dialer.
func taskArtifactRemoteURL(address, requestHost string) bool {
	if !service.IsTaskArtifactHTTPURL(address) {
		return false
	}
	u, _ := url.Parse(address)
	if taskArtifactProxyPath.MatchString(path.Clean(u.Path)) {
		return false
	}
	for _, local := range []string{system_setting.ServerAddress, "http://" + requestHost} {
		base, err := url.Parse(local)
		if err == nil && base.Hostname() != "" && strings.EqualFold(strings.TrimSuffix(base.Hostname(), "."), strings.TrimSuffix(u.Hostname(), ".")) {
			return false
		}
	}
	return true
}

func taskArtifactRequestHeaders(source http.Header) http.Header {
	headers := make(http.Header)
	for _, name := range taskArtifactRequestHeaderNames {
		if value := source.Get(name); value != "" && len(value) <= 8192 && !strings.ContainsAny(value, "\r\n\x00") {
			headers.Set(name, value)
		}
	}
	// Preserve byte-range and Content-Length semantics; do not forward cookies,
	// credentials, browser origin/referrer, or provider-specific request headers.
	headers.Set("Accept-Encoding", "identity")
	headers.Set("User-Agent", "Nexus-Task-Artifact")
	return headers
}

type taskArtifactTransport struct {
	base        http.RoundTripper
	requestHost string
}

func (t taskArtifactTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request == nil || request.URL == nil || !taskArtifactRemoteURL(request.URL.String(), t.requestHost) {
		return nil, errors.New("artifact destination is unavailable")
	}
	outbound := request.Clone(request.Context())
	outbound.Header = taskArtifactRequestHeaders(request.Header)
	outbound.Host = ""
	return t.base.RoundTrip(outbound)
}

func taskArtifactHTTPClient(base *http.Client, requestHost string) *http.Client {
	// Fail closed during startup rather than falling back to http.DefaultClient.
	if base == nil || base.Transport == nil || base.CheckRedirect == nil {
		return nil
	}
	client := *base
	client.Jar = nil
	if client.Timeout <= 0 || client.Timeout > taskArtifactDownloadTimeout {
		client.Timeout = taskArtifactDownloadTimeout
	}
	client.Transport = taskArtifactTransport{base: base.Transport, requestHost: requestHost}
	client.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if request == nil || request.URL == nil || len(via) >= 10 || !taskArtifactRemoteURL(request.URL.String(), requestHost) {
			return errors.New("artifact redirect is unavailable")
		}
		if len(via) > 0 && via[len(via)-1].URL.Scheme == "https" && request.URL.Scheme != "https" {
			return errors.New("artifact redirect downgrade is unavailable")
		}
		request.Header = taskArtifactRequestHeaders(request.Header)
		// Keep the service client's mandatory validation of every redirect.
		return base.CheckRedirect(request, via)
	}
	return &client
}

func taskArtifactSecurityHeaders(c *gin.Context) {
	c.Header("Cache-Control", "private, no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Content-Security-Policy", "sandbox; default-src 'none'; frame-ancestors 'none'")
	c.Header("Referrer-Policy", "no-referrer")
	c.Header("Cross-Origin-Resource-Policy", "same-origin")
}

func copyTaskArtifactResponseHeaders(c *gin.Context, response *http.Response, artifact model.TaskArtifact) {
	// Never forward Set-Cookie, Location, authentication, CORS, cache policy,
	// provider Content-Disposition, or any other unlisted response header.
	for _, name := range []string{"Content-Range", "Accept-Ranges", "ETag", "Last-Modified", "Content-Encoding"} {
		if value := response.Header.Get(name); value != "" && len(value) <= 8192 && !strings.ContainsAny(value, "\r\n\x00") {
			c.Header(name, value)
		}
	}
	if length, err := strconv.ParseInt(response.Header.Get("Content-Length"), 10, 64); err == nil && length >= 0 {
		c.Header("Content-Length", strconv.FormatInt(length, 10))
	}
	contentType := response.Header.Get("Content-Type")
	if _, _, err := mime.ParseMediaType(contentType); err != nil || len(contentType) > 512 || strings.ContainsAny(contentType, "\r\n\x00") {
		contentType = artifact.MimeType
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	c.Header("Content-Type", contentType)
	c.Header("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": "artifact-" + artifact.ID}))
}

// GetTaskArtifactContent serves only persisted, validated outputs of a successful
// retained task owned by the authenticated user. Administrators have no bypass.
func GetTaskArtifactContent(c *gin.Context) {
	getTaskArtifactContent(c, service.GetMandatorySSRFProtectedHTTPClient())
}

func getTaskArtifactContent(c *gin.Context, protectedClient *http.Client) {
	taskArtifactSecurityHeaders(c)
	if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead {
		c.Header("Allow", "GET, HEAD")
		c.AbortWithStatus(http.StatusMethodNotAllowed)
		return
	}
	userID := c.GetInt("id")
	if userID <= 0 {
		c.AbortWithStatus(http.StatusUnauthorized)
		return
	}
	task, exists, err := model.GetByTaskId(userID, c.Param("task_id"))
	if err != nil {
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	if !exists || task == nil || task.UserId != userID || !task.ResultRetrievable() || task.Status != model.TaskStatusSuccess {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	var selected *model.TaskArtifact
	metadata := service.ValidatedTaskResultMetadata(task.PrivateData.ResultMetadata)
	for _, artifact := range metadata.Artifacts {
		if artifact.ID == c.Param("artifact_id") && taskArtifactRemoteURL(artifact.URL, c.Request.Host) {
			copy := artifact
			selected = &copy
			break
		}
	}
	if selected == nil {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	client := taskArtifactHTTPClient(protectedClient, c.Request.Host)
	if client == nil {
		c.AbortWithStatus(http.StatusServiceUnavailable)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), taskArtifactDownloadTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, c.Request.Method, selected.URL, nil)
	if err != nil {
		c.AbortWithStatus(http.StatusBadGateway)
		return
	}
	request.Header = taskArtifactRequestHeaders(c.Request.Header)
	response, err := client.Do(request)
	if err != nil {
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		status := http.StatusBadGateway
		if errors.Is(err, context.DeadlineExceeded) {
			status = http.StatusGatewayTimeout
		}
		// Errors may contain signed URLs or private provider information.
		c.AbortWithStatus(status)
		return
	}
	defer response.Body.Close()
	switch response.StatusCode {
	case http.StatusOK, http.StatusPartialContent, http.StatusNotModified, http.StatusPreconditionFailed, http.StatusRequestedRangeNotSatisfiable:
		copyTaskArtifactResponseHeaders(c, response, *selected)
	case http.StatusNotFound, http.StatusGone:
		c.AbortWithStatus(http.StatusNotFound)
		return
	default:
		c.AbortWithStatus(http.StatusBadGateway)
		return
	}
	if response.StatusCode == http.StatusPreconditionFailed || response.StatusCode == http.StatusRequestedRangeNotSatisfiable {
		// Preserve condition/range status and headers, never a provider error body.
		c.Header("Content-Length", "0")
		c.Header("Content-Encoding", "")
	}
	c.Status(response.StatusCode)
	c.Writer.WriteHeaderNow()
	if c.Request.Method == http.MethodHead || response.StatusCode == http.StatusNotModified || response.StatusCode == http.StatusPreconditionFailed || response.StatusCode == http.StatusRequestedRangeNotSatisfiable {
		return
	}
	// 已发送响应头后只能中止传输，不能追加 JSON 或把残缺产物标为成功。
	written, copyErr := io.Copy(c.Writer, response.Body)
	if copyErr != nil || (response.ContentLength >= 0 && written != response.ContentLength) {
		panic(http.ErrAbortHandler)
	}
}
