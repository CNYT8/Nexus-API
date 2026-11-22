package common

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type failingRequestBody struct {
	reads  int
	closes int
}

func (body *failingRequestBody) Read(p []byte) (int, error) {
	body.reads++
	return copy(p, "partial"), io.ErrUnexpectedEOF
}

func (body *failingRequestBody) Close() error {
	body.closes++
	return nil
}

func TestGetRequestBodyCachesFirstReadError(t *testing.T) {
	previous := GetDiskCacheConfig()
	t.Cleanup(func() { SetDiskCacheConfig(previous) })
	for _, disk := range []bool{false, true} {
		name := "memory"
		if disk {
			name = "disk"
		}
		t.Run(name, func(t *testing.T) {
			SetDiskCacheConfig(DiskCacheConfig{Enabled: disk, ThresholdMB: 0, MaxSizeMB: 1024, Path: t.TempDir()})
			body := &failingRequestBody{}
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
			c.Request.Body, c.Request.ContentLength = body, 7
			_, first := GetRequestBody(c)
			require.ErrorIs(t, first, io.ErrUnexpectedEOF)
			_, second := GetRequestBody(c)
			require.Same(t, first, second)
			require.Equal(t, 1, body.reads)
			require.Equal(t, 1, body.closes)
			if disk {
				files, err := os.ReadDir(GetDiskCacheDir())
				require.NoError(t, err)
				require.Empty(t, files, "failed disk reads must remove partial files")
			}
		})
	}
}

func TestGetRequestBodyCachesSizeLimitError(t *testing.T) {
	previous := constant.MaxRequestBodyMB
	constant.MaxRequestBodyMB = 1
	t.Cleanup(func() { constant.MaxRequestBodyMB = previous })
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(make([]byte, (1<<20)+1)))
	_, first := GetRequestBody(c)
	require.ErrorIs(t, first, ErrRequestBodyTooLarge)
	_, second := GetRequestBody(c)
	require.Same(t, first, second)
}

func TestGetRequestBodyPreservesDiskReplay(t *testing.T) {
	previous := GetDiskCacheConfig()
	SetDiskCacheConfig(DiskCacheConfig{Enabled: true, ThresholdMB: 0, MaxSizeMB: 1024, Path: t.TempDir()})
	t.Cleanup(func() { SetDiskCacheConfig(previous) })
	payload := []byte(`{"model":"test","input":"hello"}`)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(payload))
	storage, err := GetBodyStorage(c)
	require.NoError(t, err)
	defer CleanupBodyStorage(c)
	require.True(t, storage.IsDisk())
	for i := 0; i < 2; i++ {
		cached, err := GetBodyStorage(c)
		require.NoError(t, err)
		require.Same(t, storage, cached)
		reader, err := NewReplayableBodyReader(cached).NewReader()
		require.NoError(t, err)
		data, err := io.ReadAll(reader)
		require.NoError(t, err)
		require.NoError(t, reader.Close())
		require.Equal(t, payload, data)
	}
	CleanupBodyStorage(c)
	_, err = storage.NewReader()
	require.ErrorIs(t, err, ErrStorageClosed)
}
