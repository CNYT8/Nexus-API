package helper

import (
	"errors"
	"io"
	"mime"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/gin-gonic/gin"
)

// ReadSearchRequest uses the same bounded, replayable body store as other relays.
func ReadSearchRequest(c *gin.Context) (*dto.SearchRequest, error) {
	if c.Request.Method != http.MethodPost {
		return nil, errors.New("search endpoints require POST")
	}
	if contentType := c.Request.Header.Get("Content-Type"); contentType != "" {
		mediaType, _, err := mime.ParseMediaType(contentType)
		if err != nil || mediaType != "application/json" {
			return nil, errors.New("search endpoints require application/json")
		}
	}
	storage, err := common.GetBodyStorage(c)
	if err != nil {
		return nil, err
	}
	body, err := storage.Bytes()
	if err != nil {
		return nil, err
	}
	request, err := dto.ParseSearchRequest(c.Request.URL.Path, body)
	if err != nil {
		return nil, err
	}
	if _, err := storage.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	c.Request.Body = io.NopCloser(storage)
	return request, nil
}
