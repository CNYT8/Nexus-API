package middleware

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestBodyStorageCleanupOnPanicAndAbort(t *testing.T) {
	previous := common.GetDiskCacheConfig()
	common.SetDiskCacheConfig(common.DiskCacheConfig{})
	t.Cleanup(func() { common.SetDiskCacheConfig(previous) })
	for _, panicHandler := range []bool{false, true} {
		name := "abort"
		if panicHandler {
			name = "panic"
		}
		t.Run(name, func(t *testing.T) {
			storage, err := common.CreateBodyStorage([]byte("request"))
			require.NoError(t, err)
			cachedFile := types.NewMemoryCachedData("aGVsbG8=", "text/plain", 5)
			source := &types.Base64Source{}
			source.SetCache(cachedFile)
			router := gin.New()
			router.Use(gin.CustomRecoveryWithWriter(io.Discard, func(c *gin.Context, _ any) {
				c.AbortWithStatus(http.StatusInternalServerError)
			}))
			router.Use(BodyStorageCleanup())
			router.POST("/", func(c *gin.Context) {
				c.Set(common.KeyBodyStorage, storage)
				c.Set(string(constant.ContextKeyFileSourcesToCleanup), []types.FileSource{source})
				if panicHandler {
					panic("test panic")
				}
				c.AbortWithStatus(http.StatusForbidden)
			})
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(nil)))
			want := http.StatusForbidden
			if panicHandler {
				want = http.StatusInternalServerError
			}
			require.Equal(t, want, recorder.Code)
			_, err = storage.NewReader()
			require.ErrorIs(t, err, common.ErrStorageClosed)
			data, err := cachedFile.GetBase64Data()
			require.NoError(t, err)
			require.Empty(t, data, "file sources must also be released")
		})
	}
}
