package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

var scriptMarketID = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)
var scriptDigest = regexp.MustCompile(`^[a-f0-9]{64}$`)

type scriptMarketVersion struct {
	Version       string            `json:"version"`
	Path          string            `json:"path"`
	SHA256        string            `json:"sha256,omitempty"`
	MinAPIVersion string            `json:"minApiVersion,omitempty"`
	Kind          string            `json:"kind,omitempty"`
	AllowedHosts  []string          `json:"allowedHosts,omitempty"`
	BaseURL       string            `json:"baseUrl,omitempty"`
	Auth          jsplugin.AuthMeta `json:"auth,omitempty"`
	SourceURL     string            `json:"source_url"`
}
type scriptMarketEntry struct {
	Key          string                   `json:"key"`
	Name         string                   `json:"name"`
	Latest       string                   `json:"latest"`
	Description  any                      `json:"description,omitempty"`
	Icon         string                   `json:"icon,omitempty"`
	IconFile     string                   `json:"iconFile,omitempty"`
	SortPriority int                      `json:"sortPriority,omitempty"`
	Website      string                   `json:"website,omitempty"`
	ChannelTypes []int                    `json:"channelTypes,omitempty"`
	Models       []string                 `json:"models,omitempty"`
	Protocols    []jsplugin.ProtocolClaim `json:"protocols,omitempty"`
	Versions     []scriptMarketVersion    `json:"versions"`
}
type scriptMarketCatalog struct {
	IndexVersion int                 `json:"indexVersion"`
	Name         string              `json:"name"`
	Plugins      []scriptMarketEntry `json:"plugins"`
}

func scriptMarketURL(address string) (*url.URL, error) {
	u, err := url.Parse(address)
	if err != nil || len(address) > 4096 || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
		return nil, errors.New("market URL must be an absolute HTTPS URL without credentials or fragments")
	}
	return u, nil
}
func readScriptMarket(ctx context.Context, address string, limit int64) ([]byte, error) {
	if _, err := scriptMarketURL(address); err != nil {
		return nil, err
	}
	client := service.GetMandatorySSRFProtectedHTTPClient()
	if client == nil {
		return nil, errors.New("market download transport unavailable")
	}
	isolated := *client
	isolated.Timeout = 20 * time.Second
	isolated.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	response, err := isolated.Do(request)
	if err != nil {
		return nil, errors.New("market download failed")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, errors.New("market download returned an unexpected status")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil || int64(len(body)) > limit {
		return nil, errors.New("market response exceeds size limit")
	}
	return body, nil
}
func loadScriptMarket(ctx context.Context, sourceID string) (*model.ScriptPluginMarketSource, *scriptMarketCatalog, error) {
	source, err := model.GetScriptPluginMarketSource(sourceID)
	if err != nil {
		return nil, nil, err
	}
	body, err := readScriptMarket(ctx, source.URL, 1<<20)
	if err != nil {
		return nil, nil, err
	}
	var catalog scriptMarketCatalog
	if err = common.Unmarshal(body, &catalog); err != nil || catalog.IndexVersion != 1 || catalog.Plugins == nil || len(catalog.Plugins) > 512 {
		return nil, nil, errors.New("invalid market catalog")
	}
	origin, err := scriptMarketURL(source.URL)
	if err != nil {
		return nil, nil, err
	}
	seen := map[string]bool{}
	for i := range catalog.Plugins {
		entry := &catalog.Plugins[i]
		if !jsplugin.ValidPluginKey(entry.Key) || seen[entry.Key] || len(entry.Versions) == 0 || len(entry.Versions) > 128 {
			return nil, nil, errors.New("invalid or duplicate market plugin")
		}
		seen[entry.Key] = true
		versions := map[string]bool{}
		for j := range entry.Versions {
			revision := &entry.Versions[j]
			relative, parseErr := url.Parse(revision.Path)
			if parseErr != nil || revision.Path == "" {
				return nil, nil, errors.New("invalid market revision path")
			}
			u, parseErr := scriptMarketURL(origin.ResolveReference(relative).String())
			if parseErr != nil || !strings.EqualFold(u.Host, origin.Host) || revision.SHA256 != "" && !scriptDigest.MatchString(revision.SHA256) || revision.Version == "" || len(revision.Version) > 64 || versions[revision.Version] || revision.Kind != "" && revision.Kind != "task" {
				return nil, nil, errors.New("invalid or duplicate market revision")
			}
			revision.SourceURL = u.String()
			versions[revision.Version] = true
		}
		if !versions[entry.Latest] {
			entry.Latest = entry.Versions[0].Version
		}
	}
	return source, &catalog, nil
}
func ListScriptPluginMarketSources(c *gin.Context) {
	if !requireScriptPluginRoot(c) {
		return
	}
	data, err := model.ListScriptPluginMarketSources()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, data)
}
func PutScriptPluginMarketSources(c *gin.Context) {
	if !requireScriptPluginRoot(c) {
		return
	}
	var request struct {
		Sources []model.ScriptPluginMarketSource `json:"sources"`
	}
	if err := decodeTaskPluginRequest(c, 128<<10, &request); err != nil {
		common.ApiError(c, err)
		return
	}
	if request.Sources == nil || len(request.Sources) > 32 {
		common.ApiErrorMsg(c, "sources must be an array of at most 32 sources")
		return
	}
	seen := map[string]bool{}
	for _, source := range request.Sources {
		_, err := scriptMarketURL(source.URL)
		if err != nil || !scriptMarketID.MatchString(source.ID) || source.Name == "" || len(source.Name) > 128 || seen[source.ID] {
			common.ApiErrorMsg(c, "invalid or duplicate market source")
			return
		}
		seen[source.ID] = true
	}
	if err := model.ReplaceScriptPluginMarketSources(request.Sources); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, request.Sources)
}
func GetScriptPluginMarketCatalog(c *gin.Context) {
	if !requireScriptPluginRoot(c) {
		return
	}
	source, catalog, err := loadScriptMarket(c.Request.Context(), c.Query("source_id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"source": source, "indexVersion": catalog.IndexVersion, "name": catalog.Name, "plugins": catalog.Plugins})
}
func InstallScriptPluginMarketRevision(c *gin.Context) {
	if !requireScriptPluginRoot(c) {
		return
	}
	var request struct {
		SourceID string `json:"source_id"`
		Key      string `json:"key"`
		Version  string `json:"version"`
		SHA256   string `json:"sha256"`
		Enabled  bool   `json:"enabled"`
	}
	if err := decodeTaskPluginRequest(c, 8192, &request); err != nil {
		common.ApiError(c, err)
		return
	}
	_, catalog, err := loadScriptMarket(c.Request.Context(), request.SourceID)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	for _, entry := range catalog.Plugins {
		if entry.Key != request.Key {
			continue
		}
		for _, version := range entry.Versions {
			if version.Version != request.Version || version.SHA256 != request.SHA256 {
				continue
			}
			body, err := readScriptMarket(c.Request.Context(), version.SourceURL, 1<<20)
			if err != nil {
				common.ApiError(c, err)
				return
			}
			digest := sha256.Sum256(body)
			if version.SHA256 != "" && hex.EncodeToString(digest[:]) != version.SHA256 {
				common.ApiErrorMsg(c, "market source digest mismatch")
				return
			}
			compiled, err := jsplugin.CompilePlugin(c.Request.Context(), string(body), jsplugin.Options{Concurrency: 1})
			if err != nil {
				common.ApiError(c, err)
				return
			}
			if compiled.Meta.Key != entry.Key || compiled.Meta.Version != version.Version {
				common.ApiErrorMsg(c, "market source identity mismatch")
				return
			}
			revision, err := service.InstallScriptPlugin(c.Request.Context(), string(body), request.Enabled)
			if err != nil {
				common.ApiError(c, err)
				return
			}
			revision.Source = ""
			common.ApiSuccess(c, revision)
			return
		}
	}
	common.ApiErrorMsg(c, "market revision changed or was not found")
}
func ListScriptPluginActivations(c *gin.Context) {
	if !requireScriptPluginRoot(c) {
		return
	}
	data, err := model.ListScriptPluginActivations()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, data)
}
func ActivateScriptPlugin(c *gin.Context) {
	if !requireScriptPluginRoot(c) {
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		common.ApiErrorMsg(c, "invalid script plugin id")
		return
	}
	var request struct {
		Expected *int64 `json:"expected_plugin_id"`
	}
	if err := decodeTaskPluginRequest(c, 1024, &request); err != nil {
		common.ApiError(c, err)
		return
	}
	if request.Expected == nil {
		common.ApiErrorMsg(c, "expected_plugin_id is required")
		return
	}
	data, err := model.ActivateScriptPluginRevision(id, *request.Expected)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, data)
}
func DeleteScriptPluginRevision(c *gin.Context) {
	if !requireScriptPluginRoot(c) {
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		common.ApiErrorMsg(c, "invalid script plugin id")
		return
	}
	if err := model.DeleteScriptPluginRevision(id); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, nil)
}
func DryRunScriptPlugin(c *gin.Context) {
	if !requireScriptPluginRoot(c) {
		return
	}
	var request struct {
		Source         string   `json:"source"`
		PluginID       int64    `json:"plugin_id"`
		Hook           string   `json:"hook"`
		Members        []string `json:"members"`
		Args           []any    `json:"args"`
		FetchResponses []struct {
			URL     string            `json:"url"`
			Method  string            `json:"method"`
			Status  int               `json:"status"`
			Headers map[string]string `json:"headers"`
			Body    string            `json:"body"`
		} `json:"fetch_responses"`
	}
	if err := decodeTaskPluginRequest(c, 3<<20, &request); err != nil {
		common.ApiError(c, err)
		return
	}
	if parameter := c.Param("id"); parameter != "" {
		id, err := strconv.ParseInt(parameter, 10, 64)
		if err != nil || id <= 0 || request.Source != "" || request.PluginID != 0 && request.PluginID != id {
			common.ApiErrorMsg(c, "invalid or conflicting dryrun revision")
			return
		}
		request.PluginID = id
	}
	if (request.Source == "") == (request.PluginID == 0) || request.PluginID < 0 || request.Hook == "" || len(request.Args) > 8 || len(request.Members) > 8 || len(request.FetchResponses) > 16 {
		common.ApiErrorMsg(c, "provide exactly one source or plugin_id and a bounded hook call")
		return
	}
	if request.PluginID != 0 {
		revision, err := model.GetScriptPlugin(request.PluginID)
		if err != nil {
			common.ApiError(c, err)
			return
		}
		request.Source = revision.Source
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	plugin, err := jsplugin.CompilePlugin(ctx, request.Source, jsplugin.Options{Concurrency: 1})
	if err != nil {
		common.ApiError(c, err)
		return
	}
	next := 0
	ctx = jsplugin.WithFetcher(ctx, func(_ context.Context, fetch jsplugin.FetchRequest) (jsplugin.FetchResponse, error) {
		if next >= len(request.FetchResponses) {
			return jsplugin.FetchResponse{}, errors.New("dryrun fetch has no fixture")
		}
		fixture := request.FetchResponses[next]
		next++
		if fixture.URL != fetch.URL || strings.ToUpper(fixture.Method) != fetch.Method || fixture.Status < 100 || fixture.Status > 599 || len(fixture.Body) > 1<<20 {
			return jsplugin.FetchResponse{}, errors.New("dryrun fetch fixture mismatch")
		}
		return jsplugin.FetchResponse{Status: fixture.Status, Headers: fixture.Headers, Body: []byte(fixture.Body)}, nil
	})
	result, err := plugin.Engine.CallPath(ctx, request.Hook, request.Members, request.Args...)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if next != len(request.FetchResponses) {
		common.ApiErrorMsg(c, "dryrun contains unused fetch fixtures")
		return
	}
	common.ApiSuccess(c, gin.H{"meta": plugin.Meta, "digest": plugin.Digest, "result": result})
}
