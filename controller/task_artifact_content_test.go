package controller

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type artifactTestTransport func(*http.Request) (*http.Response, error)

func (f artifactTestTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

type artifactTestBody struct {
	reader io.Reader
	reads  int
	closed bool
}

func (b *artifactTestBody) Read(p []byte) (int, error) {
	b.reads++
	return b.reader.Read(p)
}

func (b *artifactTestBody) Close() error {
	b.closed = true
	return nil
}

type artifactBrokenReader struct{}

func (artifactBrokenReader) Read(p []byte) (int, error) {
	return copy(p, "partial"), io.ErrUnexpectedEOF
}

func artifactContentFixture(t *testing.T) *model.Task {
	t.Helper()
	setupOAuthLegacyBindingControllerTestDB(t)
	require.NoError(t, model.DB.AutoMigrate(&model.Task{}))
	task := &model.Task{
		TaskID: "artifact-task", UserId: 42, Status: model.TaskStatusSuccess,
		PrivateData: model.TaskPrivateData{ResultMetadata: &model.TaskResultMetadata{
			Artifacts: []model.TaskArtifact{{ID: "output-1", Kind: "file", URL: "https://files.example.test/result.bin"}},
		}},
	}
	require.NoError(t, model.DB.Create(task).Error)
	return task
}

func artifactContentContext(method string, userID int, artifactID string) (*gin.Context, *httptest.ResponseRecorder) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(method, "https://nexus.example.test/api/task/self/artifact-task/artifacts/"+artifactID+"/content", nil)
	c.Set("id", userID)
	c.Params = gin.Params{{Key: "task_id", Value: "artifact-task"}, {Key: "artifact_id", Value: artifactID}}
	return c, w
}

func artifactContentClient(transport artifactTestTransport) *http.Client {
	return &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return nil }}
}

func TestTaskArtifactContentOwnerAndRetention(t *testing.T) {
	task := artifactContentFixture(t)
	client := artifactContentClient(func(*http.Request) (*http.Response, error) {
		t.Fatal("被拒绝的请求不应访问上游")
		return nil, errors.New("unexpected request")
	})
	for _, tc := range []struct {
		name      string
		method    string
		userID    int
		artifact  string
		status    model.TaskStatus
		discarded bool
		want      int
	}{
		{name: "anonymous", method: http.MethodGet, artifact: "output-1", status: model.TaskStatusSuccess, want: http.StatusUnauthorized},
		{name: "other_owner", method: http.MethodGet, userID: 43, artifact: "output-1", status: model.TaskStatusSuccess, want: http.StatusNotFound},
		{name: "unknown_artifact", method: http.MethodGet, userID: 42, artifact: "missing", status: model.TaskStatusSuccess, want: http.StatusNotFound},
		{name: "not_successful", method: http.MethodGet, userID: 42, artifact: "output-1", status: model.TaskStatus("IN_PROGRESS"), want: http.StatusNotFound},
		{name: "discarded", method: http.MethodGet, userID: 42, artifact: "output-1", status: model.TaskStatusSuccess, discarded: true, want: http.StatusNotFound},
		{name: "unsupported_method", method: http.MethodPost, userID: 42, artifact: "output-1", status: model.TaskStatusSuccess, want: http.StatusMethodNotAllowed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			task.Status = tc.status
			task.PrivateData.ResultDiscarded = tc.discarded
			require.NoError(t, model.DB.Save(task).Error)
			c, w := artifactContentContext(tc.method, tc.userID, tc.artifact)
			getTaskArtifactContent(c, client)
			require.Equal(t, tc.want, w.Code)
			require.Empty(t, w.Body.String())
			require.Equal(t, "private, no-store", w.Header().Get("Cache-Control"))
		})
	}
}

func TestTaskArtifactContentRangeAndHead(t *testing.T) {
	artifactContentFixture(t)
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		t.Run(method, func(t *testing.T) {
			body := &artifactTestBody{reader: strings.NewReader("abc")}
			client := artifactContentClient(func(r *http.Request) (*http.Response, error) {
				require.Equal(t, method, r.Method)
				require.Equal(t, "https://files.example.test/result.bin", r.URL.String())
				require.Equal(t, "bytes=2-4", r.Header.Get("Range"))
				require.Equal(t, "\"etag\"", r.Header.Get("If-Range"))
				require.Equal(t, "identity", r.Header.Get("Accept-Encoding"))
				require.Empty(t, r.Header.Get("Authorization"))
				require.Empty(t, r.Header.Get("Cookie"))
				require.Empty(t, r.Header.Get("X-Api-Key"))
				return &http.Response{StatusCode: http.StatusPartialContent, ContentLength: 3, Body: body, Header: http.Header{
					"Content-Length": {"3"}, "Content-Range": {"bytes 2-4/9"}, "Accept-Ranges": {"bytes"},
					"Content-Type": {"text/html"}, "Etag": {"\"etag\""}, "Set-Cookie": {"secret=upstream"},
					"Content-Disposition": {"inline"}, "Location": {"https://private.example.test/secret"},
				}}, nil
			})
			c, w := artifactContentContext(method, 42, "output-1")
			c.Request.Header.Set("Range", "bytes=2-4")
			c.Request.Header.Set("If-Range", "\"etag\"")
			c.Request.Header.Set("Authorization", "Bearer browser-secret")
			c.Request.Header.Set("Cookie", "session=browser-secret")
			c.Request.Header.Set("X-Api-Key", "browser-secret")
			getTaskArtifactContent(c, client)
			require.Equal(t, http.StatusPartialContent, w.Code)
			require.Equal(t, "bytes 2-4/9", w.Header().Get("Content-Range"))
			require.Equal(t, "3", w.Header().Get("Content-Length"))
			require.Contains(t, w.Header().Get("Content-Disposition"), "attachment")
			require.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))
			require.Contains(t, w.Header().Get("Content-Security-Policy"), "sandbox")
			require.Empty(t, w.Header().Get("Set-Cookie"))
			require.Empty(t, w.Header().Get("Location"))
			require.True(t, body.closed)
			if method == http.MethodHead {
				require.Zero(t, body.reads)
				require.Empty(t, w.Body.String())
			} else {
				require.Equal(t, "abc", w.Body.String())
			}
		})
	}
}

func TestTaskArtifactContentStatusAndCancellation(t *testing.T) {
	artifactContentFixture(t)
	for _, status := range []int{http.StatusNotModified, http.StatusPreconditionFailed, http.StatusRequestedRangeNotSatisfiable, http.StatusGone, http.StatusInternalServerError} {
		body := &artifactTestBody{reader: strings.NewReader("private supplier error")}
		client := artifactContentClient(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: status, Body: body, ContentLength: -1, Header: http.Header{"Content-Range": {"bytes */9"}}}, nil
		})
		c, w := artifactContentContext(http.MethodGet, 42, "output-1")
		getTaskArtifactContent(c, client)
		want := status
		if status == http.StatusGone {
			want = http.StatusNotFound
		} else if status == http.StatusInternalServerError {
			want = http.StatusBadGateway
		}
		require.Equal(t, want, w.Code)
		require.Empty(t, w.Body.String())
		require.Zero(t, body.reads)
		require.True(t, body.closed)
	}
	c, w := artifactContentContext(http.MethodGet, 42, "output-1")
	ctx, cancel := context.WithCancel(c.Request.Context())
	cancel()
	c.Request = c.Request.WithContext(ctx)
	getTaskArtifactContent(c, artifactContentClient(func(r *http.Request) (*http.Response, error) {
		require.ErrorIs(t, r.Context().Err(), context.Canceled)
		return nil, r.Context().Err()
	}))
	require.Equal(t, http.StatusBadGateway, w.Code)
	require.Empty(t, w.Body.String())
	c, w = artifactContentContext(http.MethodGet, 42, "output-1")
	getTaskArtifactContent(c, nil)
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestTaskArtifactContentRejectsRedirects(t *testing.T) {
	artifactContentFixture(t)
	for _, target := range []string{"http://files.example.test/downgrade", "https://nexus.example.test/loop", "https://files.example.test/api/task/self/a/artifacts/b/content", "https://blocked.example.test/result"} {
		t.Run(target, func(t *testing.T) {
			calls := 0
			checks := 0
			client := artifactContentClient(func(r *http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: http.StatusFound, Header: http.Header{"Location": {target}}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
			})
			client.CheckRedirect = func(*http.Request, []*http.Request) error {
				checks++
				return errors.New("mandatory destination policy")
			}
			c, w := artifactContentContext(http.MethodGet, 42, "output-1")
			getTaskArtifactContent(c, client)
			require.Equal(t, http.StatusBadGateway, w.Code)
			require.Equal(t, 1, calls)
			require.Empty(t, w.Body.String())
			if strings.Contains(target, "blocked.example.test") {
				require.Equal(t, 1, checks)
			}
		})
	}
	client := taskArtifactHTTPClient(artifactContentClient(func(*http.Request) (*http.Response, error) { return nil, nil }), "nexus.example.test")
	require.Equal(t, 2*time.Minute, client.Timeout)
	require.Nil(t, client.Jar)
	u, err := url.Parse("https://files.example.test/result")
	require.NoError(t, err)
	require.Error(t, client.CheckRedirect(&http.Request{URL: u}, make([]*http.Request, 10)))
	require.Nil(t, taskArtifactHTTPClient(&http.Client{Transport: client.Transport}, "nexus.example.test"))
}

func TestTaskArtifactContentAbortsIncompleteStream(t *testing.T) {
	artifactContentFixture(t)
	for _, tc := range []struct {
		name   string
		reader io.Reader
		length int64
	}{
		{name: "read_error", reader: artifactBrokenReader{}, length: -1},
		{name: "short_body", reader: strings.NewReader("partial"), length: 20},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := &artifactTestBody{reader: tc.reader}
			c, w := artifactContentContext(http.MethodGet, 42, "output-1")
			client := artifactContentClient(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), ContentLength: tc.length, Body: body}, nil
			})
			require.PanicsWithValue(t, http.ErrAbortHandler, func() { getTaskArtifactContent(c, client) })
			require.Equal(t, "partial", w.Body.String())
			require.True(t, body.closed)
		})
	}
}
