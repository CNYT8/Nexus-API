package ali

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/dto"
)

// Compatible Chat supports search enablement and allowed domains. Other hosted
// controls have no equivalent here and must not silently change their meaning.
func adaptChatWebSearch(request *dto.GeneralOpenAIRequest) error {
	search := request.WebSearchOptions != nil
	options := make(map[string]any)
	if len(request.SearchOptions) > 0 {
		if err := json.Unmarshal(request.SearchOptions, &options); err != nil || options == nil {
			return fmt.Errorf("Ali search_options must be an object")
		}
	}
	if request.WebSearchOptions != nil {
		if request.WebSearchOptions.SearchContextSize != "" || len(request.WebSearchOptions.UserLocation) > 0 {
			return fmt.Errorf("Ali Chat search does not support search_context_size or user_location")
		}
	}
	tools := make([]dto.ToolCallRequest, 0, len(request.Tools))
	for _, tool := range request.Tools {
		if tool.Type != "web_search" && tool.Type != "web_search_preview" && !strings.HasPrefix(tool.Type, "web_search_20") {
			tools = append(tools, tool)
			continue
		}
		search = true
		for key, raw := range tool.Extra {
			switch key {
			case "name":
				var name string
				if json.Unmarshal(raw, &name) != nil || name != "web_search" {
					return fmt.Errorf("Ali hosted search requires name web_search")
				}
			case "filters", "allowed_domains":
				var domains []string
				if key == "filters" {
					var filters map[string]json.RawMessage
					if json.Unmarshal(raw, &filters) != nil || filters == nil {
						return fmt.Errorf("Ali search filters must be an object")
					}
					for name, value := range filters {
						if name != "allowed_domains" || json.Unmarshal(value, &domains) != nil {
							return fmt.Errorf("Ali search supports only allowed_domains filters")
						}
					}
				} else if json.Unmarshal(raw, &domains) != nil {
					return fmt.Errorf("Ali allowed_domains must be an array of strings")
				}
				if len(domains) > 25 {
					return fmt.Errorf("Ali search accepts at most 25 allowed domains")
				}
				for _, domain := range domains {
					if strings.TrimSpace(domain) == "" {
						return fmt.Errorf("Ali allowed_domains contains an empty domain")
					}
				}
				options["assigned_site_list"] = domains
			default:
				return fmt.Errorf("Ali Chat search does not support field %q", key)
			}
		}
	}
	if !search {
		return nil
	}
	choice, err := json.Marshal(request.ToolChoice)
	if err != nil {
		return err
	}
	var named map[string]any
	_ = json.Unmarshal(choice, &named)
	forced := string(choice) == `"required"` || string(choice) == `"any"`
	namedSearch := named["type"] == "web_search" || named["type"] == "web_search_preview" || named["name"] == "web_search"
	if forced && len(tools) > 0 {
		return fmt.Errorf("Ali cannot force a choice between hosted search and function tools")
	}
	if namedSearch || forced {
		options["forced_search"] = true
		request.ToolChoice = nil
	} else if string(choice) == `"none"` {
		request.EnableSearch = json.RawMessage("false")
		request.Tools = tools
		request.WebSearchOptions = nil
		return nil
	} else if len(tools) == 0 {
		request.ToolChoice = nil
	}
	request.EnableSearch = json.RawMessage("true")
	request.SearchOptions, err = json.Marshal(options)
	request.WebSearchOptions = nil
	request.Tools = tools
	return err
}

// DashScope's Anthropic endpoint requires the client marker in a text block.
func ensureAnthropicWebSearchEntrypoint(request *dto.ClaudeRequest) {
	encoded, err := json.Marshal(request.Tools)
	if err != nil {
		return
	}
	var tools []struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(encoded, &tools) != nil {
		return
	}
	hasSearch := false
	for _, tool := range tools {
		hasSearch = hasSearch || strings.HasPrefix(tool.Type, "web_search_")
	}
	if !hasSearch {
		return
	}
	var blocks []dto.ClaudeMediaMessage
	if request.System == nil || request.IsStringSystem() {
		text := request.GetStringSystem()
		if strings.Contains(text, "cc_entrypoint=cli") {
			return
		}
		if text != "" {
			blocks = append(blocks, dto.ClaudeMediaMessage{Type: "text", Text: &text})
		}
	} else {
		blocks = request.ParseSystem()
		for _, block := range blocks {
			if strings.Contains(block.GetText(), "cc_entrypoint=cli") {
				return
			}
		}
	}
	marker := "x-anthropic-billing-header: cc_entrypoint=cli;"
	request.System = append([]dto.ClaudeMediaMessage{{Type: "text", Text: &marker}}, blocks...)
}
