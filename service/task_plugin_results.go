package service

import (
	"encoding/json"
	"mime"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/model"
)

// CollectTaskResultMetadata accepts explicit result fields only; it does not
// return raw response objects, credentials, private metadata or channel details.
func CollectTaskResultMetadata(raw []byte, resultURL string) *model.TaskResultMetadata {
	result := &model.TaskResultMetadata{}
	seen := make(map[string]bool)
	add := func(kind, address, mime string) {
		if len(result.Artifacts) >= 64 || seen[address] || !IsTaskArtifactHTTPURL(address) {
			return
		}
		switch kind {
		case "image", "video", "audio", "file":
		default:
			kind = "file"
		}
		mime = taskArtifactMIME(mime)
		seen[address] = true
		result.Artifacts = append(result.Artifacts, model.TaskArtifact{ID: strconv.Itoa(len(result.Artifacts) + 1), Kind: kind, URL: address, MimeType: mime})
	}
	if strings.HasPrefix(resultURL, "/") && !strings.HasPrefix(resultURL, "//") && len(resultURL) <= 8192 {
		result.Artifacts = append(result.Artifacts, model.TaskArtifact{ID: "1", Kind: "file", URL: resultURL})
		seen[resultURL] = true
	} else {
		add("file", resultURL, "")
	}
	if len(raw) > 0 && len(raw) <= 4*1024*1024 {
		var body struct {
			Artifacts  []model.TaskArtifact `json:"artifacts"`
			ResultText string               `json:"result_text"`
		}
		if json.Unmarshal(raw, &body) == nil {
			for _, artifact := range body.Artifacts {
				add(artifact.Kind, artifact.URL, artifact.MimeType)
			}
			if len(body.ResultText) <= 64*1024 {
				result.Text = body.ResultText
			}
		}
	}
	if len(result.Artifacts) == 0 && result.Text == "" {
		return nil
	}
	return result
}

var taskArtifactIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._~-]{0,127}$`)

// IsTaskArtifactHTTPURL performs syntax validation only. Every actual fetch must
// still use GetMandatorySSRFProtectedHTTPClient to validate DNS and dial targets.
func IsTaskArtifactHTTPURL(address string) bool {
	if address == "" || len(address) > 8192 || strings.ContainsAny(address, "\\\r\n\x00") {
		return false
	}
	u, err := url.Parse(address)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Hostname() != "" && u.User == nil && u.Opaque == "" && u.Fragment == ""
}

func taskArtifactMIME(value string) string {
	if len(value) > 128 || strings.ContainsAny(value, "\r\n\x00") {
		return ""
	}
	if _, _, err := mime.ParseMediaType(value); err != nil {
		return ""
	}
	return value
}

// ValidatedTaskResultMetadata returns an isolated public projection. Provider
// content_url values are discarded; only the owner controller generates them.
func ValidatedTaskResultMetadata(stored *model.TaskResultMetadata) *model.TaskResultMetadata {
	result := &model.TaskResultMetadata{Artifacts: []model.TaskArtifact{}}
	if stored == nil {
		return result
	}
	if len(stored.Text) <= 64*1024 {
		result.Text = stored.Text
	}
	counts := make(map[string]int)
	for _, artifact := range stored.Artifacts {
		counts[artifact.ID]++
	}
	for _, artifact := range stored.Artifacts {
		if len(result.Artifacts) >= 64 {
			break
		}
		if !taskArtifactIDPattern.MatchString(artifact.ID) || counts[artifact.ID] != 1 {
			continue
		}
		u, err := url.Parse(artifact.URL)
		local := err == nil && strings.HasPrefix(artifact.URL, "/") && !strings.HasPrefix(artifact.URL, "//") && u.Host == "" && u.Fragment == "" && len(artifact.URL) <= 8192 && !strings.ContainsAny(artifact.URL, "\\\r\n\x00")
		if !local && !IsTaskArtifactHTTPURL(artifact.URL) {
			continue
		}
		switch artifact.Kind {
		case "image", "video", "audio", "file":
		default:
			artifact.Kind = "file"
		}
		result.Artifacts = append(result.Artifacts, model.TaskArtifact{
			ID: artifact.ID, Kind: artifact.Kind, URL: artifact.URL, MimeType: taskArtifactMIME(artifact.MimeType),
		})
	}
	return result
}
