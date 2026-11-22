package relay

import (
	"errors"
	"net/http"

	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
)

func DeepSeekToolsHelper(c *gin.Context, info *relaycommon.RelayInfo) *types.NewAPIError {
	path := c.Request.URL.Path
	if path != dto.DeepSeekSearchPath && path != dto.DeepSeekFetchPath {
		return types.NewErrorWithStatusCode(errors.New("invalid tools endpoint"), types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}
	return searchRelay(c, info, path)
}
