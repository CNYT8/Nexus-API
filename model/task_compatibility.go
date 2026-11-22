package model

import (
	"bytes"
	"encoding/json"
	"errors"
)

// ErrTaskExecutionProtected is a compatibility skip, not a failed poll or a
// billing failure. Upstream plugin execution and pricing are not Nexus drivers.
var ErrTaskExecutionProtected = errors.New("task execution belongs to an unsupported upstream runtime")

type taskPrivateDataJSON struct {
	raw   string
	known string
}

type taskPrivateDataFields TaskPrivateData

func (p *TaskPrivateData) UnmarshalJSON(data []byte) error {
	var fields taskPrivateDataFields
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	known, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	*p = TaskPrivateData(fields)
	if taskJSONHasUnknownFields(data, known) {
		p.preservedJSON = &taskPrivateDataJSON{raw: string(data), known: string(known)}
	}
	return nil
}

func taskJSONHasUnknownFields(raw, known []byte) bool {
	var rawItems, knownItems []json.RawMessage
	if json.Unmarshal(raw, &rawItems) == nil && json.Unmarshal(known, &knownItems) == nil && len(rawItems) == len(knownItems) {
		for i := range rawItems {
			if taskJSONHasUnknownFields(rawItems[i], knownItems[i]) {
				return true
			}
		}
	}
	var rawFields, knownFields map[string]json.RawMessage
	if json.Unmarshal(raw, &rawFields) != nil || json.Unmarshal(known, &knownFields) != nil {
		return false
	}
	for key, value := range rawFields {
		knownValue, exists := knownFields[key]
		if !exists || taskJSONHasUnknownFields(value, knownValue) {
			return true
		}
	}
	return false
}

func (p TaskPrivateData) MarshalJSON() ([]byte, error) {
	known, err := json.Marshal(taskPrivateDataFields(p))
	if err != nil || p.preservedJSON == nil {
		return known, err
	}
	if (&Task{PrivateData: p}).HasForeignExecutionEvidence() {
		return []byte(p.preservedJSON.raw), nil
	}
	return mergeTaskPrivateJSON([]byte(p.preservedJSON.raw), []byte(p.preservedJSON.known), known)
}

// Apply only changes to understood fields, recursively. An unchanged value
// returns the original bytes; unknown nested objects, arrays and numbers never
// pass through interface{} / float64 or an incomplete Nexus struct.
func mergeTaskPrivateJSON(original, before, after []byte) ([]byte, error) {
	if bytes.Equal(before, after) {
		return original, nil
	}
	var oldItems, newItems, rawItems []json.RawMessage
	if json.Unmarshal(before, &oldItems) == nil && json.Unmarshal(after, &newItems) == nil &&
		json.Unmarshal(original, &rawItems) == nil && len(oldItems) == len(newItems) && len(oldItems) == len(rawItems) {
		for i := range oldItems {
			merged, err := mergeTaskPrivateJSON(rawItems[i], oldItems[i], newItems[i])
			if err != nil {
				return nil, err
			}
			rawItems[i] = merged
		}
		return json.Marshal(rawItems)
	}
	var oldFields, newFields, rawFields map[string]json.RawMessage
	if json.Unmarshal(before, &oldFields) != nil || oldFields == nil ||
		json.Unmarshal(after, &newFields) != nil || newFields == nil ||
		json.Unmarshal(original, &rawFields) != nil || rawFields == nil {
		return after, nil
	}
	for key, oldValue := range oldFields {
		newValue, exists := newFields[key]
		if !exists {
			delete(rawFields, key)
			continue
		}
		if bytes.Equal(oldValue, newValue) {
			continue
		}
		merged, err := mergeTaskPrivateJSON(rawFields[key], oldValue, newValue)
		if err != nil {
			return nil, err
		}
		rawFields[key] = merged
	}
	for key, value := range newFields {
		if _, existed := oldFields[key]; !existed {
			rawFields[key] = value
		}
	}
	return json.Marshal(rawFields)
}

func taskJSONHasValue(fields map[string]json.RawMessage, key string) bool {
	value, exists := fields[key]
	return exists && !bytes.Equal(bytes.TrimSpace(value), []byte("null"))
}

// HasForeignExecutionEvidence does not infer ownership from numeric platforms,
// task IDs, request metadata, or the absence of a Nexus billing reservation.
func (t *Task) HasForeignExecutionEvidence() bool {
	if t == nil || t.PrivateData.preservedJSON == nil {
		return false
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal([]byte(t.PrivateData.preservedJSON.raw), &fields) != nil {
		return false
	}
	// RawMessage plugin state may be any JSON value, including null.
	if _, exists := fields["plugin_state"]; exists {
		return true
	}
	var execution, billing map[string]json.RawMessage
	_ = json.Unmarshal(fields["execution"], &execution)
	_ = json.Unmarshal(fields["billing_context"], &billing)
	return taskJSONHasValue(execution, "task_plugin") || taskJSONHasValue(billing, "tiered_snapshot")
}
