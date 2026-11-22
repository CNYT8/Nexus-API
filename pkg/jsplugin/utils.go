package jsplugin

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/Calcium-Ion/moejs"
	"github.com/MoeForge/moego/base64"
	"github.com/QuantumNous/new-api/common"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// Capabilities are advertised only when their complete host contract exists.
func HasCapability(name string) bool {
	switch name {
	case "fetch@1", CapabilityJSONClone, CapabilityJSONOrder, CapabilitySubmitSSEDelta, CapabilityDurationAuto:
		return true
	}
	return false
}

func injectGlobals(runtime *moejs.Runtime, now func() time.Time, logOutput func(string), currentCall func() *runtimeCall) error {
	stringFunction := func(name string, fn func(string) (string, error)) moejs.Value {
		return runtime.Function(name, 1, func(r *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			text, err := stringValue(r, moejs.Arg(args, 0))
			if err != nil {
				return moejs.Undefined(), err
			}
			value, err := fn(text)
			return moejs.String(value), err
		})
	}
	utils := map[string]any{
		"fetch": runtime.Function("fetch", 1, func(r *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			call := currentCall()
			if call == nil {
				return moejs.Undefined(), fmt.Errorf("utils.fetch unavailable outside hooks")
			}
			return call.fetch(r, moejs.Arg(args, 0))
		}),
		"hasCapability": runtime.Function("hasCapability", 1, func(r *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			name, err := stringValue(r, moejs.Arg(args, 0))
			return moejs.Bool(HasCapability(name)), err
		}),
		"json": map[string]any{"clone": runtime.Function("clone", 1, func(r *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			ctx := context.Background()
			if call := currentCall(); call != nil {
				ctx = call.ctx
			}
			node, err := makeJSONNode(moejs.Arg(args, 0), 0, &jsonBudget{ctx: ctx, realm: r, bytes: MaxJSONToolBytes, nodes: maxJSONToolNodes})
			if err != nil {
				return moejs.Undefined(), r.TypeError("%s", err.Error())
			}
			return node.jsValue(r)
		})},
		"unixNow": runtime.Function("unixNow", 0, func(*moejs.Realm, moejs.Value, []moejs.Value) (moejs.Value, error) {
			return moejs.Int(now().Unix()), nil
		}),
		"uuid": runtime.Function("uuid", 0, func(*moejs.Realm, moejs.Value, []moejs.Value) (moejs.Value, error) {
			return moejs.String(uuid.NewString()), nil
		}),
		"base64":    stringFunction("base64", func(text string) (string, error) { return base64.StdEncoding.EncodeToString([]byte(text)), nil }),
		"base64URL": stringFunction("base64URL", func(text string) (string, error) { return base64.RawURLEncoding.EncodeToString([]byte(text)), nil }),
		"base64URLDecode": stringFunction("base64URLDecode", func(text string) (string, error) {
			value, err := base64.RawURLEncoding.DecodeString(text)
			return string(value), err
		}),
		"hmacSHA256": runtime.Function("hmacSHA256", 2, func(r *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			message, err := stringValue(r, moejs.Arg(args, 0))
			if err != nil {
				return moejs.Undefined(), err
			}
			secret, err := stringValue(r, moejs.Arg(args, 1))
			if err != nil {
				return moejs.Undefined(), err
			}
			mac := hmac.New(sha256.New, []byte(secret))
			_, _ = mac.Write([]byte(message))
			return moejs.String(hex.EncodeToString(mac.Sum(nil))), nil
		}),
		"jwtSignHS256": runtime.Function("jwtSignHS256", 2, func(r *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			claims := map[string]any{}
			if arg := moejs.Arg(args, 0); !arg.IsNullish() {
				value, err := r.ToGoStrict(arg)
				if err != nil {
					return moejs.Undefined(), err
				}
				var ok bool
				claims, ok = value.(map[string]any)
				if !ok {
					return moejs.Undefined(), r.TypeError("jwtSignHS256 claims must be an object")
				}
			}
			secret, err := stringValue(r, moejs.Arg(args, 1))
			if err != nil {
				return moejs.Undefined(), err
			}
			token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims(claims)).SignedString([]byte(secret))
			return moejs.String(token), err
		}),
		"volcSignV4": runtime.Function("volcSignV4", 1, func(r *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			value, err := r.ToGoStrict(moejs.Arg(args, 0))
			if err != nil {
				return moejs.Undefined(), err
			}
			data, err := common.Marshal(value)
			if err != nil {
				return moejs.Undefined(), err
			}
			var request volcSignRequest
			if err = common.Unmarshal(data, &request); err != nil {
				return moejs.Undefined(), err
			}
			signed, err := signVolcV4(request, now)
			if err != nil {
				return moejs.Undefined(), err
			}
			return r.FromGo(signed)
		}),
	}
	if err := runtime.SetGlobal("utils", utils); err != nil {
		return err
	}
	return runtime.SetGlobal("console", map[string]any{"log": runtime.Function("log", 0, func(r *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
		if logOutput == nil {
			return moejs.Undefined(), nil
		}
		parts := make([]string, 0, len(args))
		for _, argument := range args {
			text, err := stringValue(r, argument)
			if err != nil {
				return moejs.Undefined(), err
			}
			parts = append(parts, text)
		}
		text := strings.Join(parts, " ")
		if len(text) > 2048 {
			text = text[:2048]
		}
		logOutput(text)
		return moejs.Undefined(), nil
	})})
}
func stringValue(r *moejs.Realm, value moejs.Value) (string, error) {
	if value.IsNullish() {
		return "", nil
	}
	if value.IsString() {
		return value.AsString().GoString(), nil
	}
	text, err := r.ToString(value)
	if err != nil {
		return "", err
	}
	return text.GoString(), nil
}

type volcSignRequest struct {
	Method    string
	URL       string
	Headers   map[string]string
	Body      string
	AccessKey string
	SecretKey string
	Region    string
	Service   string
	Timestamp int64
}

func signVolcV4(request volcSignRequest, now func() time.Time) (map[string]string, error) {
	parsedURL, err := url.Parse(request.URL)
	if err != nil || parsedURL.Host == "" {
		return nil, fmt.Errorf("invalid Volcengine signing URL")
	}
	region, service := request.Region, request.Service
	if region == "" {
		region = "cn-north-1"
	}
	if service == "" {
		service = "cv"
	}
	timestamp := now().UTC()
	if request.Timestamp != 0 {
		timestamp = time.Unix(request.Timestamp, 0).UTC()
	}
	xDate, shortDate := timestamp.Format("20060102T150405Z"), timestamp.Format("20060102")
	bodyHash := sha256.Sum256([]byte(request.Body))
	requestPath := parsedURL.EscapedPath()
	if requestPath == "" {
		requestPath = "/"
	}
	headers := make(map[string]string, len(request.Headers)+3)
	for name, value := range request.Headers {
		headers[strings.ToLower(name)] = strings.TrimSpace(value)
	}
	headers["host"], headers["x-date"], headers["x-content-sha256"] = parsedURL.Host, xDate, hex.EncodeToString(bodyHash[:])
	keys := make([]string, 0, len(headers))
	for name := range headers {
		keys = append(keys, name)
	}
	sort.Strings(keys)
	var canonicalHeaders strings.Builder
	for _, name := range keys {
		canonicalHeaders.WriteString(name + ":" + headers[name] + "\n")
	}
	signedHeaders := strings.Join(keys, ";")
	canonicalRequest := strings.Join([]string{strings.ToUpper(request.Method), requestPath, parsedURL.Query().Encode(), canonicalHeaders.String(), signedHeaders, hex.EncodeToString(bodyHash[:])}, "\n")
	canonicalHash := sha256.Sum256([]byte(canonicalRequest))
	scope := fmt.Sprintf("%s/%s/%s/request", shortDate, region, service)
	stringToSign := fmt.Sprintf("HMAC-SHA256\n%s\n%s\n%s", xDate, scope, hex.EncodeToString(canonicalHash[:]))
	sign := func(key []byte, value string) []byte {
		mac := hmac.New(sha256.New, key)
		_, _ = mac.Write([]byte(value))
		return mac.Sum(nil)
	}
	signingKey := sign(sign(sign([]byte(request.SecretKey), shortDate), region), service)
	signingKey = sign(signingKey, "request")
	return map[string]string{
		"Authorization": fmt.Sprintf("HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s", request.AccessKey, scope, signedHeaders, hex.EncodeToString(sign(signingKey, stringToSign))),
		"X-Date":        xDate, "X-Content-Sha256": hex.EncodeToString(bodyHash[:]),
	}, nil
}
