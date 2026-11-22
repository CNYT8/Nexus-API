package jsplugin

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Calcium-Ion/moejs"
	"golang.org/x/net/http/httpguts"
)

type Fetcher func(context.Context, FetchRequest) (FetchResponse, error)
type FetchRequest struct {
	Hook    string
	URL     string
	Method  string
	Headers map[string]string
}
type FetchResponse struct {
	Status  int
	Headers map[string]string
	Body    []byte
}

const MaxFetchBodyBytes = 1 << 20
const fetchTimeout = 15 * time.Second
const maxFetchTimePerCall = 30 * time.Second

type fetcherContextKey struct{}

func WithFetcher(ctx context.Context, fetcher Fetcher) context.Context {
	return context.WithValue(ctx, fetcherContextKey{}, fetcher)
}

type runtimeCall struct {
	engine    *Engine
	runtime   *moejs.Runtime
	ctx       context.Context
	hook      string
	fetcher   Fetcher
	watchdog  *runtimeWatchdog
	holdsSlot bool
	parked    bool
	fetches   int
	fetchTime time.Duration
}

func (c *runtimeCall) release() {
	if c.holdsSlot {
		<-c.engine.semaphore
		c.holdsSlot = false
	}
	if c.parked {
		<-c.engine.parked
		c.parked = false
	}
}
func (c *runtimeCall) fetch(r *moejs.Realm, argument moejs.Value) (moejs.Value, error) {
	if c.fetcher == nil {
		return moejs.Undefined(), errors.New("utils.fetch unavailable in this hook")
	}
	request, host, wantBytes, err := fetchRequest(r, argument)
	if err != nil {
		return moejs.Undefined(), err
	}
	request.Hook = c.hook
	if c.fetches >= 4 {
		return moejs.Undefined(), errors.New("utils.fetch allows at most 4 requests per hook call")
	}
	remaining := maxFetchTimePerCall - c.fetchTime
	if remaining <= 0 {
		return moejs.Undefined(), errors.New("utils.fetch exhausted the 30s call budget")
	}
	c.fetches++
	timeout := min(fetchTimeout, remaining)
	if !c.watchdog.pause() {
		return moejs.Undefined(), r.CheckInterrupt()
	}
	c.yieldSlot()
	ctx, cancel := context.WithTimeout(c.ctx, timeout)
	started := time.Now()
	response, err := c.fetcher(ctx, request)
	c.fetchTime += time.Since(started)
	cancel()
	if !c.reclaimSlot() {
		cause := context.Cause(c.ctx)
		if cause == nil {
			cause = ErrCallAdmissionTimeout
		}
		c.runtime.Interrupt(cause)
		return moejs.Undefined(), r.CheckInterrupt()
	}
	c.watchdog.resume()
	if interrupted := r.CheckInterrupt(); interrupted != nil {
		return moejs.Undefined(), interrupted
	}
	if err != nil {
		// Do not return URL paths/queries or transport errors containing credentials.
		return moejs.Undefined(), fmt.Errorf("utils.fetch to %s failed", host)
	}
	if len(response.Body) > MaxFetchBodyBytes {
		return moejs.Undefined(), errors.New("utils.fetch response exceeds 1 MiB")
	}
	body := moejs.String("")
	if wantBytes {
		if response.Body == nil {
			response.Body = []byte{}
		}
		body, err = r.FromGo(response.Body)
	} else if request.Method != http.MethodHead {
		body, err = c.runtime.ParseJSONString(string(response.Body))
		if err != nil {
			body, err = moejs.String(string(response.Body)), nil
		}
	}
	if err != nil {
		return moejs.Undefined(), err
	}
	headers := r.NewObject()
	for name, value := range response.Headers {
		if err = headers.CreateDataPropertyOrThrow(r, r.KeyFromGoString(name), moejs.String(value)); err != nil {
			return moejs.Undefined(), err
		}
	}
	headerValue, err := r.FromGo(headers)
	if err != nil {
		return moejs.Undefined(), err
	}
	result := r.NewObject()
	for _, member := range []struct {
		name  string
		value moejs.Value
	}{
		{"status", moejs.Int(int64(response.Status))}, {"headers", headerValue}, {"body", body},
	} {
		if err = result.CreateDataPropertyOrThrow(r, r.KeyFromGoString(member.name), member.value); err != nil {
			return moejs.Undefined(), err
		}
	}
	return r.FromGo(result)
}
func fetchRequest(r *moejs.Realm, argument moejs.Value) (FetchRequest, string, bool, error) {
	value, err := r.ToGoStrict(argument)
	if err != nil {
		return FetchRequest{}, "", false, err
	}
	members, ok := value.(map[string]any)
	if !ok {
		return FetchRequest{}, "", false, r.TypeError("utils.fetch request must be an object")
	}
	request := FetchRequest{Method: http.MethodGet}
	wantBytes := false
	for name, value := range members {
		switch name {
		case "url":
			request.URL, ok = value.(string)
		case "method":
			if value == nil {
				continue
			}
			request.Method, ok = value.(string)
			request.Method = strings.ToUpper(strings.TrimSpace(request.Method))
			if request.Method == "" {
				request.Method = http.MethodGet
			}
		case "headers":
			if value == nil {
				continue
			}
			var headers map[string]any
			headers, ok = value.(map[string]any)
			request.Headers = make(map[string]string, len(headers))
			for key, v := range headers {
				text, valid := v.(string)
				if !valid {
					ok = false
					break
				}
				request.Headers[key] = text
			}
		case "responseType":
			if value == nil {
				continue
			}
			text, valid := value.(string)
			ok = valid && (text == "bytes" || text == "json")
			wantBytes = text == "bytes"
		default:
			ok = false
		}
		if !ok {
			return FetchRequest{}, "", false, r.TypeError("invalid utils.fetch member %q", name)
		}
	}
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		return FetchRequest{}, "", false, r.TypeError("utils.fetch supports only GET and HEAD")
	}
	parsed, err := url.Parse(request.URL)
	if err != nil || len(request.URL) > 64<<10 || parsed.Host == "" || parsed.User != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return FetchRequest{}, "", false, r.TypeError("utils.fetch requires an absolute http(s) URL")
	}
	if err = ValidateRequestHeaders(request.Headers); err != nil {
		return FetchRequest{}, "", false, err
	}
	return request, parsed.Host, wantBytes, nil
}
func (c *runtimeCall) yieldSlot() {
	select {
	case c.engine.parked <- struct{}{}:
	default:
		return
	}
	c.parked = true
	<-c.engine.semaphore
	c.holdsSlot = false
}
func (c *runtimeCall) reclaimSlot() bool {
	if c.holdsSlot {
		return true
	}
	timer := time.NewTimer(fetchTimeout)
	defer timer.Stop()
	select {
	case c.engine.semaphore <- struct{}{}:
		c.holdsSlot = true
	case <-c.ctx.Done():
	case <-timer.C:
	}
	<-c.engine.parked
	c.parked = false
	return c.holdsSlot
}

// A channel URL and explicitly approved hosts are the only credential targets.
func ValidateRequestURL(requestURL, baseURL string, allowedHosts []string) error {
	request, err := url.Parse(requestURL)
	if err != nil || request.Host == "" || request.User != nil || request.Fragment != "" || (request.Scheme != "http" && request.Scheme != "https") {
		return errors.New("plugin request URL must be absolute http(s) without credentials or fragment")
	}
	base, err := url.Parse(baseURL)
	if err != nil || base.Host == "" || (base.Scheme != "http" && base.Scheme != "https") {
		return errors.New("invalid channel base URL")
	}
	if request.Scheme == "http" && base.Scheme == "https" {
		return errors.New("plugin request cannot downgrade HTTPS")
	}
	if canonicalHost(request) == canonicalHost(base) {
		return nil
	}
	for _, allowed := range allowedHosts {
		u, err := url.Parse(request.Scheme + "://" + strings.TrimSpace(allowed))
		if err == nil && u.User == nil && u.Path == "" && canonicalHost(request) == canonicalHost(u) {
			return nil
		}
	}
	return errors.New("plugin request host is not allowed")
}
func ValidateRequestHeaders(headers map[string]string) error {
	if len(headers) > 64 {
		return errors.New("at most 64 request headers are allowed")
	}
	for name, value := range headers {
		if !httpguts.ValidHeaderFieldName(name) || !httpguts.ValidHeaderFieldValue(value) || len(value) > 8192 {
			return errors.New("invalid plugin request header")
		}
		switch strings.ToLower(name) {
		case "host", "content-length", "accept-encoding", "connection", "proxy-connection", "keep-alive", "proxy-authorization", "te", "trailer", "transfer-encoding", "upgrade":
			return fmt.Errorf("request header %q is not allowed", name)
		}
	}
	return nil
}
func canonicalHost(value *url.URL) string {
	host, port := strings.ToLower(value.Hostname()), value.Port()
	if port == "" || port == "80" && value.Scheme == "http" || port == "443" && value.Scheme == "https" {
		return host
	}
	return net.JoinHostPort(host, port)
}
