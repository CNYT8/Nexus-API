package controller

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/dto"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestTaskRelayLocalBillingFailureNeverRetries(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusForbidden, http.StatusTemporaryRedirect, http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusBadGateway} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			require.False(t, shouldRetryTaskRelay(c, 1, &dto.TaskError{
				StatusCode: status,
				LocalError: true,
			}, 3))
		})
	}
}

func TestTaskRelayProviderFailureKeepsRetryPolicy(t *testing.T) {
	for _, status := range []int{http.StatusTemporaryRedirect, http.StatusTooManyRequests, http.StatusInternalServerError} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			require.True(t, shouldRetryTaskRelay(c, 1, &dto.TaskError{StatusCode: status}, 3))
			require.False(t, shouldRetryTaskRelay(c, 1, &dto.TaskError{StatusCode: status}, 0))
			c.Set("specific_channel_id", 1)
			require.False(t, shouldRetryTaskRelay(c, 1, &dto.TaskError{StatusCode: status}, 3))
		})
	}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	require.False(t, shouldRetryTaskRelay(c, 1, nil, 3))
}
