package middleware

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/gin-gonic/gin"
)

func TestSearchModelPermissions(t *testing.T) {
	for _, path := range []string{dto.DeepSeekSearchPath, dto.DeepSeekFetchPath} {
		t.Run(path, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", path, strings.NewReader(`{"model":"cheap-model","future":true}`))
			request, selectChannel, err := getModelRequest(c)
			if err != nil || !selectChannel {
				t.Fatalf("request=%+v, select=%v, error=%v", request, selectChannel, err)
			}
			want := dto.DeepSeekSearchModel
			if path == dto.DeepSeekFetchPath {
				want = dto.DeepSeekFetchModel
			}
			if request.Model != want {
				t.Fatalf("routing model=%s, want=%s", request.Model, want)
			}
			common.SetContextKey(c, constant.ContextKeyTokenModelLimitEnabled, true)
			common.SetContextKey(c, constant.ContextKeyTokenModelLimit, map[string]bool{"cheap-model": true})
			c.Set("specific_channel_id", "1")
			if searchModelAllowed(c, request.Model) {
				t.Fatal("client model or specific channel bypassed token permission")
			}
			common.SetContextKey(c, constant.ContextKeyTokenModelLimit, map[string]bool{want: true})
			if !searchModelAllowed(c, request.Model) {
				t.Fatal("explicit tool permission rejected")
			}
			common.SetContextKey(c, constant.ContextKeyTokenModelLimit, "invalid")
			if searchModelAllowed(c, request.Model) {
				t.Fatal("malformed permission context accepted")
			}
		})
	}
}

func TestSearchRequestIngress(t *testing.T) {
	for _, tc := range []struct {
		name, method, contentType string
		invalid                   bool
	}{
		{name: "json", method: "POST", contentType: "application/json"},
		{name: "json charset", method: "POST", contentType: "application/json; charset=utf-8"},
		{name: "missing content type", method: "POST"},
		{name: "get", method: "GET", invalid: true},
		{name: "form", method: "POST", contentType: "application/x-www-form-urlencoded", invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(tc.method, dto.DeepSeekSearchPath, strings.NewReader(`{}`))
			if tc.contentType != "" {
				c.Request.Header.Set("Content-Type", tc.contentType)
			}
			_, err := getSearchModelRequest(c)
			if (err != nil) != tc.invalid {
				t.Fatalf("error=%v, invalid=%v", err, tc.invalid)
			}
		})
	}
	for _, path := range []string{"/v2/tools/search", "/tools/search", "/v1/tools/search/extra", "/v1/alpha/searching"} {
		if dto.IsSearchPath(path) {
			t.Fatalf("search escaped exact /v1 boundary: %s", path)
		}
	}
}
