package jsplugin

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/QuantumNous/new-api/common"
)

type ProtocolClaim struct {
	Name     string   `json:"name"`
	Models   []string `json:"models,omitempty"`
	Supports []string `json:"supports,omitempty"`
}

type SubmitIntent struct {
	Kind          string   `json:"kind"`
	Model         string   `json:"model"`
	Action        string   `json:"action"`
	RequestBody   any      `json:"requestBody"`
	OriginTaskIDs []string `json:"originTaskIds,omitempty"`
	TaskIDs       []string `json:"taskIds,omitempty"`
}

type NativeReply struct {
	Status  int
	Headers map[string]string
	Body    any
}

var memberName = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]{0,127}$`)

func (m Meta) ProtocolClaims() ([]ProtocolClaim, error) {
	claims := make([]ProtocolClaim, 0, len(m.Protocols))
	for _, value := range m.Protocols {
		claim := ProtocolClaim{}
		if text, ok := value.(string); ok {
			claim.Name = text
		} else {
			encoded, err := common.Marshal(value)
			if err != nil || common.Unmarshal(encoded, &claim) != nil {
				return nil, errors.New("invalid protocol claim")
			}
		}
		claims = append(claims, claim)
	}
	return claims, nil
}

func (m Meta) ClaimsProtocol(name, model string) bool {
	claims, err := m.ProtocolClaims()
	if err != nil || !m.SupportsModel(model) {
		return false
	}
	for _, claim := range claims {
		if claim.Name != name {
			continue
		}
		if len(claim.Models) == 0 {
			return true
		}
		for _, declared := range claim.Models {
			if declared == model {
				return true
			}
		}
	}
	return false
}

func validatePresentation(ctx context.Context, e *Engine, meta Meta) error {
	require := func(export string, members ...string) error {
		callable, err := e.HasCallablePath(ctx, export, members...)
		if err != nil || !callable {
			return fmt.Errorf("required plugin presenter %s.%s is not callable", export, strings.Join(members, "."))
		}
		return nil
	}
	models := func(names []string) error {
		seen := map[string]bool{}
		for _, name := range names {
			if !meta.SupportsModel(name) || seen[name] {
				return errors.New("presenter models must be a unique subset of meta.models")
			}
			seen[name] = true
		}
		return nil
	}
	if len(meta.Routes) > 128 {
		return errors.New("too many native routes")
	}
	seen := map[string]bool{}
	for _, route := range meta.Routes {
		if err := ValidateNativeRoute(route); err != nil {
			return err
		}
		shape := route.Method + " "
		for _, segment := range strings.Split(route.Path, "/") {
			if strings.HasPrefix(segment, ":") {
				segment = ":"
			}
			shape += "/" + segment
		}
		if seen[shape] {
			return errors.New("duplicate native route shape")
		}
		seen[shape] = true
		if err := models(route.Models); err != nil {
			return err
		}
		if route.Decode != "" {
			if err := require("native", route.Decode); err != nil {
				return err
			}
		}
		if err := require("native", route.Render); err != nil {
			return err
		}
	}
	claims, err := meta.ProtocolClaims()
	if err != nil {
		return err
	}
	seen = map[string]bool{}
	for _, claim := range claims {
		if seen[claim.Name] {
			return errors.New("duplicate protocol claim")
		}
		seen[claim.Name] = true
		if err := models(claim.Models); err != nil {
			return err
		}
		if err := require("protocols", claim.Name, "decodeRequest"); err != nil {
			return err
		}
		switch claim.Name {
		case "openai_video", "openai_image":
			if len(claim.Supports) != 0 {
				return errors.New("this protocol has no request modes")
			}
			if err := require("protocols", claim.Name, "render"); err != nil {
				return err
			}
			if claim.Name == "openai_video" {
				for _, hook := range []string{"listArtifacts", "buildContentRequest"} {
					if err := require(hook); err != nil {
						return err
					}
				}
			}
		case "openai_responses":
			if len(claim.Supports) == 0 {
				return errors.New("openai_responses requires supports")
			}
			modes := map[string]bool{}
			for _, mode := range claim.Supports {
				if modes[mode] || mode != "stream" && mode != "sync" && mode != "background" {
					return errors.New("invalid response protocol mode")
				}
				modes[mode] = true
				hook := "renderFinal"
				if mode == "stream" {
					hook = "renderEvents"
				}
				if err := require("protocols", claim.Name, hook); err != nil {
					return err
				}
			}
			for hook, expected := range map[string]bool{"renderEvents": modes["stream"], "renderFinal": modes["sync"] || modes["background"]} {
				callable, err := e.HasCallablePath(ctx, "protocols", claim.Name, hook)
				if err != nil || callable != expected {
					return errors.New("response protocol render hooks must match declared modes")
				}
			}
		default:
			return errors.New("unknown host protocol")
		}
	}
	return nil
}

func ValidateNativeRoute(route Route) error {
	if route.Method != http.MethodGet && route.Method != http.MethodPost && route.Method != http.MethodPut && route.Method != http.MethodPatch && route.Method != http.MethodDelete {
		return errors.New("invalid native route method")
	}
	if len(route.Path) > 512 || !strings.HasPrefix(route.Path, "/") || strings.ContainsAny(route.Path, "?#%\\\r\n\x00*") || strings.Contains(route.Path, "//") {
		return errors.New("invalid native route path")
	}
	for _, prefix := range []string{"/api", "/oauth", "/login", "/register", "/assets", "/dashboard", "/console", "/v1/tasks", "/v1/task", "/health", "/metrics"} {
		if route.Path == prefix || strings.HasPrefix(route.Path, prefix+"/") {
			return errors.New("native route intersects a reserved host namespace")
		}
	}
	params := map[string]bool{}
	for _, segment := range strings.Split(route.Path, "/")[1:] {
		if segment == "." || segment == ".." {
			return errors.New("invalid native route segment")
		}
		if strings.HasPrefix(segment, ":") {
			name := strings.TrimPrefix(segment, ":")
			if !memberName.MatchString(name) || params[name] {
				return errors.New("invalid native route parameter")
			}
			params[name] = true
		}
	}
	if !memberName.MatchString(route.Render) || route.Decode != "" && !memberName.MatchString(route.Decode) {
		return errors.New("invalid native hook name")
	}
	switch route.Type {
	case "submit", "dynamic":
		if route.Decode == "" || route.TaskIDParam != "" {
			return errors.New("submit/dynamic route requires a decoder")
		}
	case "query":
		parameter := route.TaskIDParam
		if parameter == "" {
			parameter = "task_id"
		}
		if !memberName.MatchString(parameter) || route.Decode != "" || len(route.Models) != 0 || route.RetainResult != nil {
			return errors.New("query route forbids decode, models and retainResult and requires a valid taskIdParam")
		}
	default:
		return errors.New("invalid native route type")
	}
	return nil
}

// ParseNativeReply validates the small response escape hatch. Credentials,
// cookies and redirects to executable schemes cannot be emitted by presenters.
func ParseNativeReply(value any) (NativeReply, error) {
	reply := NativeReply{Status: http.StatusOK, Headers: map[string]string{}, Body: value}
	object, ok := value.(map[string]any)
	if !ok {
		return reply, nil
	}
	body := object["__body"]
	if _, hasStatus := object["__status"]; !hasStatus {
		return reply, nil
	}
	for key := range object {
		if key != "__body" && key != "__status" && key != "__headers" {
			return reply, errors.New("unknown native reply wrapper field")
		}
	}
	reply.Body = body
	if status, exists := object["__status"]; exists {
		switch n := status.(type) {
		case int:
			reply.Status = n
		case int64:
			reply.Status = int(n)
		case float64:
			reply.Status = int(n)
			if float64(reply.Status) != n {
				return reply, errors.New("invalid native reply status")
			}
		default:
			return reply, errors.New("invalid native reply status")
		}
		if reply.Status < 200 || reply.Status > 599 {
			return reply, errors.New("invalid native reply status")
		}
	}
	if raw, exists := object["__headers"]; exists {
		headers, ok := raw.(map[string]any)
		if !ok || len(headers) > 2 {
			return reply, errors.New("invalid native reply headers")
		}
		for name, rawValue := range headers {
			value, ok := rawValue.(string)
			name = http.CanonicalHeaderKey(name)
			if !ok || name != "Location" && name != "Retry-After" || len(value) > 2048 || strings.ContainsAny(value, "\r\n\x00") {
				return reply, errors.New("invalid native reply header")
			}
			if name == "Location" {
				u, err := url.Parse(value)
				if err != nil || u.User != nil || u.IsAbs() && u.Scheme != "http" && u.Scheme != "https" {
					return reply, errors.New("invalid native reply location")
				}
			}
			reply.Headers[name] = value
		}
	}
	return reply, nil
}
