package controller

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestDecodeTicketJSONStrictly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name    string
		body    string
		wantErr bool
	}{
		{name: "valid", body: `{"content":"hello"}`},
		{name: "unknown field", body: `{"content":"hello","role":"admin"}`, wantErr: true},
		{name: "second value", body: `{"content":"hello"} {"content":"again"}`, wantErr: true},
		{name: "trailing garbage", body: `{"content":"hello"} invalid`, wantErr: true},
		{name: "oversized", body: `{"content":"` + strings.Repeat("a", ticketRequestBodyLimit) + `"}`, wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			context, _ := gin.CreateTestContext(httptest.NewRecorder())
			context.Request = httptest.NewRequest("POST", "/api/tickets/1/replies", strings.NewReader(test.body))
			var request ticketReplyRequest
			err := decodeTicketJSON(context, &request)
			if test.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, "hello", request.Content)
		})
	}
}
