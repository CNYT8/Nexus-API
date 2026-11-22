package dto

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const (
	AlphaSearchPath     = "/v1/alpha/search"
	DeepSeekSearchPath  = "/v1/tools/search"
	DeepSeekFetchPath   = "/v1/tools/fetch"
	DeepSeekSearchModel = "deepseek-search"
	DeepSeekFetchModel  = "deepseek-fetch"
)

// SearchRequest retains the wire body across retries. Model is the routing and
// billing identity, not necessarily the optional model supplied by a tool client.
type SearchRequest struct {
	Model   string
	Path    string
	RawBody json.RawMessage
}

var _ Request = (*SearchRequest)(nil)

func IsSearchPath(path string) bool {
	return path == AlphaSearchPath || path == DeepSeekSearchPath || path == DeepSeekFetchPath
}

// ParseSearchRequest rejects ambiguous top-level fields while leaving unknown
// fields (including large numbers) byte-for-byte intact. Tool payload schemas
// are upstream-owned; only the transport envelope is validated here.
func ParseSearchRequest(path string, data []byte) (*SearchRequest, error) {
	if !IsSearchPath(path) {
		return nil, errors.New("unsupported search path")
	}
	if !json.Valid(data) {
		return nil, errors.New("invalid JSON request body")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	start, err := dec.Token()
	if err != nil || start != json.Delim('{') {
		return nil, errors.New("search request must be a JSON object")
	}
	fields := make(map[string]json.RawMessage)
	for dec.More() {
		key, err := dec.Token()
		if err != nil {
			return nil, err
		}
		name := key.(string)
		if _, exists := fields[name]; exists {
			return nil, fmt.Errorf("duplicate search field %q", name)
		}
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return nil, err
		}
		fields[name] = value
	}
	model := ""
	if raw, exists := fields["model"]; exists {
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || json.Unmarshal(raw, &model) != nil {
			return nil, errors.New("model must be a string")
		}
	}
	if raw, exists := fields["stream"]; exists {
		var stream bool
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || json.Unmarshal(raw, &stream) != nil || stream {
			return nil, errors.New("search endpoints require stream=false")
		}
	}
	switch path {
	case AlphaSearchPath:
		if strings.TrimSpace(model) == "" {
			return nil, errors.New("model is required")
		}
	case DeepSeekSearchPath:
		model = DeepSeekSearchModel
	case DeepSeekFetchPath:
		model = DeepSeekFetchModel
	}
	return &SearchRequest{Model: model, Path: path, RawBody: append(json.RawMessage(nil), data...)}, nil
}

func (r *SearchRequest) GetTokenCountMeta() *types.TokenCountMeta {
	// Scan decoded string values in wire order. Raw JSON alone would let
	// unicode escapes hide sensitive prompt text; keys must not interrupt
	// adjacent content fragments. The original request remains untouched.
	var text strings.Builder
	var collect func(gjson.Result)
	collect = func(value gjson.Result) {
		if value.Type == gjson.String {
			text.WriteString(value.String())
		} else if value.IsObject() || value.IsArray() {
			value.ForEach(func(_, child gjson.Result) bool {
				collect(child)
				return true
			})
		}
	}
	collect(gjson.ParseBytes(r.RawBody))
	return &types.TokenCountMeta{CombineText: text.String(), TokenType: types.TokenTypeTokenizer}
}

func (r *SearchRequest) IsStream(_ *gin.Context) bool { return false }

func (r *SearchRequest) SetModelName(model string) { r.Model = model }

// UpstreamBody never mutates RawBody: retries must start from the client body,
// not from a previous channel's mapping or parameter overrides.
func (r *SearchRequest) UpstreamBody(mappedModel string, mapped bool) ([]byte, error) {
	body := append([]byte(nil), r.RawBody...)
	if !mapped {
		return body, nil
	}
	return sjson.SetBytes(body, "model", mappedModel)
}
