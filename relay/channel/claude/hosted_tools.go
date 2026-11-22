package claude

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/dto"
)

// Only web search has a direct hosted equivalent. Other tool kinds must fail
// conversion instead of silently becoming a function with an empty name.
func chatHostedToolToClaude(tool dto.ToolCallRequest) (map[string]any, error) {
	if tool.Type != "web_search" && tool.Type != "web_search_preview" && !strings.HasPrefix(tool.Type, "web_search_20") {
		return nil, fmt.Errorf("hosted tool %q is not supported by Claude conversion", tool.Type)
	}
	converted := map[string]any{"type": "web_search_20250305", "name": "web_search"}
	if strings.HasPrefix(tool.Type, "web_search_20") {
		converted["type"] = tool.Type
	}
	for key, raw := range tool.Extra {
		var value any
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, err
		}
		switch key {
		case "name":
			if value != "web_search" {
				return nil, fmt.Errorf("Claude web search requires name web_search")
			}
		case "max_uses", "allowed_domains", "blocked_domains":
			converted[key] = value
		case "user_location":
			location, ok := value.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("web search user_location must be an object")
			}
			if approximate, ok := location["approximate"].(map[string]any); ok {
				location = approximate
				location["type"] = "approximate"
			}
			converted[key] = location
		case "filters":
			filters, ok := value.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("web search filters must be an object")
			}
			for name, filter := range filters {
				if name != "allowed_domains" {
					return nil, fmt.Errorf("Claude web search does not support filter %q", name)
				}
				converted[name] = filter
			}
		default:
			return nil, fmt.Errorf("Claude web search does not support field %q", key)
		}
	}
	return converted, nil
}
