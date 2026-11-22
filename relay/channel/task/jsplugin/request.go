package jsplugin

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	pluginruntime "github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/gin-gonic/gin"
)

const maxFileRequestBytes = 32 << 20

type requestFile struct {
	Ref         string `json:"ref"`
	Field       string `json:"field"`
	Filename    string `json:"filename"`
	ContentType string `json:"contentType"`
	Size        int    `json:"size"`
	data        []byte
}

type Input struct {
	Body   any
	Object map[string]any
	Files  []requestFile
}

// ReadInput uses the replayable host body and never creates multipart temp files.
func ReadInput(c *gin.Context) (*Input, error) {
	storage, err := common.GetBodyStorage(c)
	if err != nil {
		return nil, err
	}
	if storage.Size() > maxFileRequestBytes {
		return nil, errors.New("script request exceeds 32 MiB")
	}
	data, err := storage.Bytes()
	if err != nil {
		return nil, err
	}
	kind, parameters, err := mime.ParseMediaType(c.GetHeader("Content-Type"))
	if err != nil && len(data) != 0 {
		return nil, errors.New("invalid request content type")
	}
	result := &Input{Object: map[string]any{}, Files: []requestFile{}}
	put := func(name, value string) {
		if old, exists := result.Object[name]; exists {
			if list, ok := old.([]any); ok {
				result.Object[name] = append(list, value)
			} else {
				result.Object[name] = []any{old, value}
			}
		} else {
			result.Object[name] = value
		}
	}
	switch {
	case len(data) == 0:
		result.Body = result.Object
	case kind == "application/json" || strings.HasSuffix(kind, "+json"):
		if len(data) > maxJSONBytes || common.Unmarshal(data, &result.Object) != nil || result.Object == nil {
			return nil, errors.New("script body must be a JSON object of at most 1 MiB")
		}
		result.Body = pluginruntime.RawJSON(data)
	case kind == "application/x-www-form-urlencoded":
		if len(data) > maxJSONBytes {
			return nil, errors.New("script form too large")
		}
		fields, err := url.ParseQuery(string(data))
		if err != nil || len(fields) > 256 {
			return nil, errors.New("invalid script form")
		}
		for name, values := range fields {
			for _, value := range values {
				put(name, value)
			}
		}
		result.Body = result.Object
	case kind == "multipart/form-data":
		reader := multipart.NewReader(bytes.NewReader(data), parameters["boundary"])
		indexes := map[string]int{}
		fieldBytes := 0
		for count := 0; ; count++ {
			part, err := reader.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil || count >= 256 {
				return nil, errors.New("invalid multipart request")
			}
			name := part.FormName()
			if name == "" || len(name) > 128 || strings.ContainsAny(name, "#\r\n\x00") {
				return nil, errors.New("invalid multipart field name")
			}
			content, err := io.ReadAll(io.LimitReader(part, maxFileRequestBytes+1))
			part.Close()
			if err != nil || len(content) > maxFileRequestBytes {
				return nil, errors.New("multipart part exceeds limit")
			}
			if part.FileName() == "" {
				fieldBytes += len(content)
				if fieldBytes > maxJSONBytes {
					return nil, errors.New("multipart fields exceed limit")
				}
				put(name, string(content))
				continue
			}
			ref := "request_file:" + name
			if indexes[name] != 0 {
				ref += "#" + strconv.Itoa(indexes[name])
			}
			indexes[name]++
			contentType := part.Header.Get("Content-Type")
			if contentType == "" {
				contentType = http.DetectContentType(content)
			}
			result.Files = append(result.Files, requestFile{Ref: ref, Field: name, Filename: part.FileName(), ContentType: contentType, Size: len(content), data: content})
		}
		result.Body = result.Object
	default:
		return nil, errors.New("script body must be JSON, a form or multipart")
	}
	return result, nil
}

func (a *TaskAdaptor) callDescriptor(ctx context.Context, hook string, args ...any) (requestDescriptor, error) {
	var descriptor requestDescriptor
	encoded, err := a.plugin.Engine.CallJSON(ctx, hook, nil, args...)
	if err != nil {
		return descriptor, err
	}
	if err = common.Unmarshal(encoded, &descriptor); err != nil {
		return descriptor, err
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(encoded, &fields); err != nil {
		return descriptor, err
	}
	descriptor.bodyJSON = fields["body"]
	return descriptor, nil
}

func (a *TaskAdaptor) encodeDescriptor(descriptor *requestDescriptor) (io.Reader, error) {
	if descriptor.BodyType == "multipart" {
		if descriptor.Body != nil || len(descriptor.Parts) > 256 {
			return nil, errors.New("multipart requires only bounded parts")
		}
		var data bytes.Buffer
		writer := multipart.NewWriter(&data)
		for _, raw := range descriptor.Parts {
			part, ok := raw.(map[string]any)
			if !ok {
				return nil, errors.New("invalid multipart part")
			}
			for key := range part {
				if key != "name" && key != "value" && key != "fileRef" && key != "filename" {
					return nil, errors.New("unknown multipart part field")
				}
			}
			name, _ := part["name"].(string)
			ref, hasFile := part["fileRef"].(string)
			value, hasValue := part["value"]
			if name == "" || len(name) > 128 || strings.ContainsAny(name, "\r\n\x00") || hasFile == hasValue {
				return nil, errors.New("invalid multipart part name or content")
			}
			if hasFile {
				file, ok := a.files[ref]
				if !ok {
					return nil, errors.New("unknown multipart file reference")
				}
				filename := file.Filename
				if override, exists := part["filename"]; exists {
					var ok bool
					filename, ok = override.(string)
					if !ok {
						return nil, errors.New("invalid multipart filename")
					}
				}
				if filename == "" || len(filename) > 512 || strings.ContainsAny(filename, "\r\n\x00") {
					return nil, errors.New("invalid multipart filename")
				}
				if _, _, err := mime.ParseMediaType(file.ContentType); err != nil || strings.ContainsAny(file.ContentType, "\r\n\x00") {
					return nil, errors.New("invalid multipart MIME type")
				}
				header := textproto.MIMEHeader{}
				header.Set("Content-Disposition", mime.FormatMediaType("form-data", map[string]string{"name": name, "filename": filename}))
				header.Set("Content-Type", file.ContentType)
				out, err := writer.CreatePart(header)
				if err != nil {
					return nil, err
				}
				if _, err = out.Write(file.data); err != nil {
					return nil, err
				}
			} else {
				text, ok := value.(string)
				if !ok {
					return nil, errors.New("multipart field value must be a string")
				}
				if err := writer.WriteField(name, text); err != nil {
					return nil, err
				}
			}
			if data.Len() > maxFileRequestBytes {
				return nil, errors.New("encoded multipart exceeds 32 MiB")
			}
		}
		if err := writer.Close(); err != nil {
			return nil, err
		}
		if data.Len() > maxFileRequestBytes {
			return nil, errors.New("encoded multipart exceeds 32 MiB")
		}
		if descriptor.Headers == nil {
			descriptor.Headers = map[string]string{}
		}
		for key := range descriptor.Headers {
			if strings.EqualFold(key, "Content-Type") {
				delete(descriptor.Headers, key)
			}
		}
		descriptor.Headers["Content-Type"] = writer.FormDataContentType()
		return bytes.NewReader(data.Bytes()), nil
	}
	if len(descriptor.Parts) != 0 {
		return nil, errors.New("parts require multipart bodyType")
	}
	if descriptor.Body == nil {
		return nil, nil
	}
	if text, ok := descriptor.Body.(string); ok {
		return descriptorBody(text)
	}
	data := descriptor.bodyJSON
	if len(data) == 0 {
		var err error
		data, err = common.Marshal(descriptor.Body)
		if err != nil {
			return nil, err
		}
	}
	budget := maxFileRequestBytes
	encoded, err := a.resolveJSONFiles(data, 0, &budget)
	if err != nil {
		return nil, err
	}
	if len(encoded) > maxFileRequestBytes {
		return nil, errors.New("encoded JSON body exceeds 32 MiB")
	}
	return bytes.NewReader(encoded), nil
}

// Traverse raw members in encounter order so file expansion retains json-order@1.
func (a *TaskAdaptor) resolveJSONFiles(raw []byte, depth int, budget *int) ([]byte, error) {
	if depth > 32 {
		return nil, errors.New("request body exceeds depth limit")
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil, errors.New("invalid JSON body")
	}
	if raw[0] != '{' && raw[0] != '[' {
		return raw, nil
	}
	var placeholder map[string]any
	if raw[0] == '{' {
		if err := json.Unmarshal(raw, &placeholder); err != nil {
			return nil, err
		}
		if _, exists := placeholder["__fileRef"]; exists {
			for key := range placeholder {
				if key != "__fileRef" && key != "encoding" && key != "mimeType" && key != "maxBytes" {
					return nil, errors.New("invalid file placeholder")
				}
			}
			ref, _ := placeholder["__fileRef"].(string)
			file, ok := a.files[ref]
			if !ok {
				return nil, fmt.Errorf("unknown file reference %q", ref)
			}
			limit := maxFileRequestBytes
			if number, exists := placeholder["maxBytes"]; exists {
				n, ok := number.(float64)
				if !ok || n <= 0 || n > maxFileRequestBytes || n != float64(int(n)) {
					return nil, errors.New("invalid file maxBytes")
				}
				limit = int(n)
			}
			if file.Size > limit || file.Size > *budget {
				return nil, errors.New("file expansion exceeds byte limit")
			}
			*budget -= file.Size
			encoded := base64.StdEncoding.EncodeToString(file.data)
			switch placeholder["encoding"] {
			case "base64":
			case "dataUrl":
				kind := file.ContentType
				if value, exists := placeholder["mimeType"]; exists {
					var ok bool
					kind, ok = value.(string)
					if !ok {
						return nil, errors.New("invalid file MIME type")
					}
				}
				if _, _, err := mime.ParseMediaType(kind); err != nil || strings.ContainsAny(kind, "\r\n\x00") {
					return nil, errors.New("invalid file MIME type")
				}
				encoded = "data:" + kind + ";base64," + encoded
			default:
				return nil, errors.New("file encoding must be base64 or dataUrl")
			}
			return json.Marshal(encoded)
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if _, err := decoder.Token(); err != nil {
		return nil, err
	}
	var out bytes.Buffer
	out.WriteByte(raw[0])
	first := true
	for decoder.More() {
		if !first {
			out.WriteByte(',')
		}
		first = false
		if raw[0] == '{' {
			key, err := decoder.Token()
			if err != nil {
				return nil, err
			}
			text, _ := json.Marshal(key)
			out.Write(text)
			out.WriteByte(':')
		}
		var child json.RawMessage
		if err := decoder.Decode(&child); err != nil {
			return nil, err
		}
		encoded, err := a.resolveJSONFiles(child, depth+1, budget)
		if err != nil {
			return nil, err
		}
		out.Write(encoded)
		if out.Len() > maxFileRequestBytes {
			return nil, errors.New("file expansion exceeds byte limit")
		}
	}
	if raw[0] == '{' {
		out.WriteByte('}')
	} else {
		out.WriteByte(']')
	}
	return out.Bytes(), nil
}
