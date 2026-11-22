package dto

import "encoding/json"

// UnmarshalJSON keeps hosted/custom tool fields intact during typed relay hops.
func (t *ToolCallRequest) UnmarshalJSON(data []byte) error {
	type plain ToolCallRequest
	var value plain
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	var extra map[string]json.RawMessage
	if err := json.Unmarshal(data, &extra); err != nil {
		return err
	}
	for _, key := range []string{"id", "type", "function", "custom"} {
		delete(extra, key)
	}
	*t = ToolCallRequest(value)
	t.Extra = extra
	return nil
}

func (t ToolCallRequest) MarshalJSON() ([]byte, error) {
	type plain ToolCallRequest
	data, err := json.Marshal(plain(t))
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, err
	}
	if t.Type != "function" && t.Function.Name == "" {
		delete(fields, "function")
	}
	for key, value := range t.Extra {
		if _, exists := fields[key]; !exists {
			fields[key] = value
		}
	}
	return json.Marshal(fields)
}
