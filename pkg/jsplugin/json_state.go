package jsplugin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/Calcium-Ion/moejs/engine"
	"github.com/QuantumNous/new-api/common"
)

const (
	CapabilityJSONClone      = "json-clone@1"
	CapabilityJSONOrder      = "json-order@1"
	CapabilitySubmitSSEDelta = "submit-sse-delta@1"
	CapabilityDurationAuto   = "duration-auto@1"
	MaxJSONToolBytes         = 1 << 20
	maxJSONToolDepth         = 32
	maxJSONToolNodes         = 32768
)

type jsonNode struct {
	keys   []string
	object map[string]*jsonNode
	array  []*jsonNode
	scalar any
	text   *strings.Builder
	bytes  int
	nodes  int
}

type jsonBudget struct {
	ctx   context.Context
	realm *engine.Realm
	bytes int
	nodes int
}

func (b *jsonBudget) spend(bytes, nodes int) error {
	if err := b.ctx.Err(); err != nil {
		return err
	}
	if bytes > b.bytes || nodes > b.nodes {
		return errors.New("JSON state exceeds size or node limit")
	}
	b.bytes -= bytes
	b.nodes -= nodes
	return nil
}

func makeJSONNode(value any, depth int, b *jsonBudget) (*jsonNode, error) {
	var orderedKeys []string
	if depth > maxJSONToolDepth {
		return nil, errors.New("JSON state exceeds depth limit")
	}
	if raw, ok := value.(RawJSON); ok {
		decoder := json.NewDecoder(bytes.NewReader(raw))
		trimmed := bytes.TrimSpace(raw)
		if len(trimmed) == 0 || !json.Valid(trimmed) {
			return nil, errors.New("invalid ordered JSON value")
		}
		switch trimmed[0] {
		case '{':
			_, _ = decoder.Token()
			fields := map[string]any{}
			orderedKeys = []string{}
			for decoder.More() {
				key, err := decoder.Token()
				if err != nil {
					return nil, err
				}
				name := key.(string)
				var child json.RawMessage
				if err = decoder.Decode(&child); err != nil {
					return nil, err
				}
				if _, exists := fields[name]; !exists {
					orderedKeys = append(orderedKeys, name)
				}
				fields[name] = RawJSON(child)
			}
			value = fields
		case '[':
			var items []json.RawMessage
			if err := json.Unmarshal(raw, &items); err != nil {
				return nil, err
			}
			values := make([]any, len(items))
			for i := range items {
				values[i] = RawJSON(items[i])
			}
			value = values
		default:
			if err := json.Unmarshal(raw, &value); err != nil {
				return nil, err
			}
		}
	}
	if js, ok := value.(engine.Value); ok {
		if js.IsUndefined() {
			return nil, errors.New("undefined is not a JSON value")
		}
		if !js.IsObject() {
			value = b.realm.ToGo(js)
		} else {
			object := js.AsObject()
			switch {
			case object.ClassName() == "Object" && !object.IsCallable() && (object.Proto() == nil || object.Proto() == b.realm.ObjectPrototype):
				keys, err := b.realm.EnumerableOwnKeys(object)
				if err != nil {
					return nil, err
				}
				if len(keys) > b.nodes {
					return nil, errors.New("JSON object exceeds node limit")
				}
				fields := make(map[string]any, len(keys))
				orderedKeys = make([]string, 0, len(keys))
				for _, key := range keys {
					orderedKeys = append(orderedKeys, key.GoString())
					if fields[key.GoString()], err = object.GetProp(b.realm, key); err != nil {
						return nil, err
					}
				}
				value = fields
			case object.ClassName() == "Array":
				length, err := b.realm.LengthOfArrayLike(object)
				if err != nil {
					return nil, err
				}
				if length > int64(b.nodes) {
					return nil, errors.New("JSON array exceeds node limit")
				}
				items := make([]any, int(length))
				for i := range items {
					key := engine.IndexKey(uint32(i))
					present, err := b.realm.HasOwn(object, key)
					if err != nil {
						return nil, err
					}
					if !present {
						return nil, errors.New("sparse arrays are not JSON values")
					}
					if items[i], err = object.GetProp(b.realm, key); err != nil {
						return nil, err
					}
				}
				value = items
			default:
				return nil, errors.New("json.clone accepts only plain objects, arrays and JSON scalars")
			}
		}
	}
	switch v := value.(type) {
	case map[string]any:
		if v == nil {
			value = nil
		}
	case []any:
		if v == nil {
			value = nil
		}
	}
	beforeBytes, beforeNodes := b.bytes, b.nodes
	if err := b.spend(0, 1); err != nil {
		return nil, err
	}
	n := &jsonNode{}
	switch v := value.(type) {
	case map[string]any:
		if err := b.spend(2, 0); err != nil {
			return nil, err
		}
		n.object = make(map[string]*jsonNode, len(v))
		if orderedKeys == nil {
			orderedKeys = make([]string, 0, len(v))
			for key := range v {
				orderedKeys = append(orderedKeys, key)
			}
		}
		n.keys = orderedKeys
		for _, key := range orderedKeys {
			child := v[key]
			if !utf8.ValidString(key) || len(key) > b.bytes {
				return nil, errors.New("invalid or oversized JSON key")
			}
			encoded, _ := common.Marshal(key)
			cost := len(encoded) + 1
			if len(n.object) != 0 {
				cost++
			}
			if err := b.spend(cost, 0); err != nil {
				return nil, err
			}
			next, err := makeJSONNode(child, depth+1, b)
			if err != nil {
				return nil, err
			}
			n.object[key] = next
		}
	case []any:
		if err := b.spend(2+max(0, len(v)-1), 0); err != nil {
			return nil, err
		}
		n.array = make([]*jsonNode, 0, len(v))
		for _, child := range v {
			next, err := makeJSONNode(child, depth+1, b)
			if err != nil {
				return nil, err
			}
			n.array = append(n.array, next)
		}
	default:
		switch v := value.(type) {
		case int:
			value = float64(v)
		case int64:
			value = float64(v)
		}
		switch v := value.(type) {
		case nil, bool:
		case float64:
			if math.IsNaN(v) || math.IsInf(v, 0) {
				return nil, errors.New("JSON numbers must be finite")
			}
		case string:
			if len(v) > b.bytes {
				return nil, errors.New("JSON string exceeds size limit")
			}
			if !utf8.ValidString(v) {
				value = string([]rune(v))
			}
		default:
			return nil, errors.New("value must contain only JSON objects, arrays and scalars")
		}
		encoded, err := common.Marshal(value)
		if err != nil {
			return nil, err
		}
		if err = b.spend(len(encoded), 0); err != nil {
			return nil, err
		}
		n.scalar = value
	}
	n.bytes, n.nodes = beforeBytes-b.bytes, beforeNodes-b.nodes
	return n, nil
}

func (n *jsonNode) value() any {
	if n.object != nil {
		result := make(map[string]any, len(n.object))
		for key, child := range n.object {
			result[key] = child.value()
		}
		return result
	}
	if n.array != nil {
		result := make([]any, len(n.array))
		for i, child := range n.array {
			result[i] = child.value()
		}
		return result
	}
	if n.text != nil {
		return n.text.String()
	}
	return n.scalar
}

func (n *jsonNode) jsValue(r *engine.Realm) (engine.Value, error) {
	if n.object != nil {
		result := r.NewObject()
		for _, key := range n.keys {
			value, err := n.object[key].jsValue(r)
			if err != nil {
				return engine.Undefined(), err
			}
			if err = result.CreateDataPropertyOrThrow(r, r.KeyFromGoString(key), value); err != nil {
				return engine.Undefined(), err
			}
		}
		return engine.ObjectValue(result), nil
	}
	if n.array != nil {
		items := make([]engine.Value, len(n.array))
		for i, child := range n.array {
			value, err := child.jsValue(r)
			if err != nil {
				return engine.Undefined(), err
			}
			items[i] = value
		}
		return engine.ObjectValue(r.NewArrayFromSlice(items)), nil
	}
	return r.FromGo(n.value())
}

// JSONState has one owner per submission. A failed batch poisons it; callers
// cannot publish or continue from a partially applied event.
type JSONState struct {
	root   *jsonNode
	limit  int
	failed error
}

func NewJSONState(limit int) *JSONState {
	return &JSONState{root: &jsonNode{bytes: 4, nodes: 1}, limit: max(0, min(limit, MaxJSONToolBytes))}
}

func ValidateJSONPath(path []any) error {
	if len(path) > maxJSONToolDepth {
		return errors.New("JSON path exceeds depth limit")
	}
	for _, part := range path {
		switch v := part.(type) {
		case string:
			if !utf8.ValidString(v) {
				return errors.New("invalid JSON path key")
			}
		case int:
			if v < 0 {
				return errors.New("invalid JSON path index")
			}
		case int64:
			if v < 0 || v > math.MaxInt32 {
				return errors.New("invalid JSON path index")
			}
		case float64:
			if math.IsNaN(v) || v < 0 || v > math.MaxInt32 || math.Trunc(v) != v {
				return errors.New("invalid JSON path index")
			}
		default:
			return errors.New("invalid JSON path segment")
		}
	}
	return nil
}

func (s *JSONState) Apply(ctx context.Context, changes any) (err error) {
	if s.failed != nil {
		return s.failed
	}
	defer func() {
		if err != nil {
			s.failed = err
		}
	}()
	items, ok := changes.([]any)
	if !ok || len(items) > 256 {
		return errors.New("changes must be an array of at most 256 operations")
	}
	for _, item := range items {
		if err = ctx.Err(); err != nil {
			return err
		}
		change, ok := item.(map[string]any)
		if !ok || len(change) != 3 {
			return errors.New("each change requires only op, path and value")
		}
		op, _ := change["op"].(string)
		path, ok := change["path"].([]any)
		value, hasValue := change["value"]
		if !ok || !hasValue || ValidateJSONPath(path) != nil {
			return errors.New("invalid change path or value")
		}
		ancestors := make([]*jsonNode, 0, len(path))
		target := s.root
		var key string
		var index int
		for i, segment := range path {
			if target == nil {
				return errors.New("JSON change parent does not exist")
			}
			ancestors = append(ancestors, target)
			switch {
			case target.object != nil:
				key, ok = segment.(string)
				if !ok || len(key) > s.limit {
					return errors.New("JSON object path requires a bounded string")
				}
				target = target.object[key]
			case target.array != nil:
				switch v := segment.(type) {
				case int:
					index = v
				case int64:
					index = int(v)
				case float64:
					index = int(v)
				default:
					return errors.New("JSON array path requires an index")
				}
				if index < 0 || index >= len(target.array) {
					return errors.New("JSON array index out of range")
				}
				target = target.array[index]
			default:
				return errors.New("JSON change traverses a scalar")
			}
			if target == nil && (i != len(path)-1 || op != "set") {
				return errors.New("JSON change target does not exist")
			}
		}
		addedBytes, addedNodes := 0, 0
		switch op {
		case "set":
			oldBytes, oldNodes := 0, 0
			if target != nil {
				oldBytes, oldNodes = target.bytes, target.nodes
			} else {
				encoded, _ := common.Marshal(key)
				addedBytes = len(encoded) + 1
				if len(ancestors[len(ancestors)-1].object) != 0 {
					addedBytes++
				}
			}
			budget := &jsonBudget{ctx: ctx, bytes: s.limit - s.root.bytes + oldBytes - addedBytes, nodes: maxJSONToolNodes - s.root.nodes + oldNodes}
			next, err := makeJSONNode(value, len(path), budget)
			if err != nil {
				return err
			}
			addedBytes += next.bytes - oldBytes
			addedNodes = next.nodes - oldNodes
			if len(ancestors) == 0 {
				s.root = next
			} else if parent := ancestors[len(ancestors)-1]; parent.object != nil {
				if target == nil {
					parent.keys = append(parent.keys, key)
				}
				parent.object[key] = next
			} else {
				parent.array[index] = next
			}
		case "append":
			if target == nil || target.array == nil {
				return errors.New("append requires an array target")
			}
			if len(target.array) != 0 {
				addedBytes++
			}
			budget := &jsonBudget{ctx: ctx, bytes: s.limit - s.root.bytes - addedBytes, nodes: maxJSONToolNodes - s.root.nodes}
			next, err := makeJSONNode(value, len(path)+1, budget)
			if err != nil {
				return err
			}
			addedBytes += next.bytes
			addedNodes = next.nodes
			target.array = append(target.array, next)
			target.bytes += addedBytes
			target.nodes += addedNodes
		case "appendText":
			if target == nil {
				return errors.New("appendText requires an existing string")
			}
			previous, isText := target.scalar.(string)
			text, ok := value.(string)
			if !isText || !ok || len(text) > s.limit-s.root.bytes {
				return errors.New("appendText requires bounded strings")
			}
			if !utf8.ValidString(text) {
				text = string([]rune(text))
			}
			encoded, _ := common.Marshal(text)
			addedBytes = len(encoded) - 2
			if addedBytes > s.limit-s.root.bytes {
				return errors.New("JSON state exceeds size limit")
			}
			if target.text == nil {
				target.text = &strings.Builder{}
				target.text.WriteString(previous)
				target.scalar = ""
			}
			target.text.WriteString(text)
			target.bytes += addedBytes
		default:
			return errors.New("unsupported JSON change operation")
		}
		for _, ancestor := range ancestors {
			ancestor.bytes += addedBytes
			ancestor.nodes += addedNodes
		}
	}
	return nil
}

// ApplyJSON retains insertion order inside each operation value.
func (s *JSONState) ApplyJSON(ctx context.Context, raw RawJSON) error {
	var changes []struct {
		Op    string          `json:"op"`
		Path  []any           `json:"path"`
		Value json.RawMessage `json:"value"`
	}
	var objects []map[string]json.RawMessage
	if json.Unmarshal(raw, &objects) != nil || json.Unmarshal(raw, &changes) != nil || objects == nil || len(changes) > 256 {
		s.failed = errors.New("invalid JSON changes array")
		return s.failed
	}
	items := make([]any, len(changes))
	for i, change := range changes {
		if len(objects[i]) != 3 || change.Path == nil || len(change.Value) == 0 {
			s.failed = errors.New("each change requires only op, path and value")
			return s.failed
		}
		var value any = RawJSON(change.Value)
		if change.Op == "appendText" {
			var text string
			if json.Unmarshal(change.Value, &text) != nil {
				s.failed = errors.New("appendText requires a string")
				return s.failed
			}
			value = text
		}
		items[i] = map[string]any{"op": change.Op, "path": change.Path, "value": value}
	}
	return s.Apply(ctx, items)
}

func (n *jsonNode) appendJSON(out []byte) ([]byte, error) {
	if n.object != nil {
		out = append(out, '{')
		for i, key := range n.keys {
			if i != 0 {
				out = append(out, ',')
			}
			encoded, _ := common.Marshal(key)
			out = append(out, encoded...)
			out = append(out, ':')
			var err error
			out, err = n.object[key].appendJSON(out)
			if err != nil {
				return nil, err
			}
		}
		return append(out, '}'), nil
	}
	if n.array != nil {
		out = append(out, '[')
		for i, child := range n.array {
			if i != 0 {
				out = append(out, ',')
			}
			var err error
			out, err = child.appendJSON(out)
			if err != nil {
				return nil, err
			}
		}
		return append(out, ']'), nil
	}
	value := n.scalar
	if n.text != nil {
		value = n.text.String()
	}
	encoded, err := common.Marshal(value)
	return append(out, encoded...), err
}

func (s *JSONState) RawValue() (RawJSON, error) {
	if s.failed != nil {
		return nil, s.failed
	}
	encoded, err := s.root.appendJSON(nil)
	if err != nil {
		return nil, err
	}
	if len(encoded) != s.root.bytes || len(encoded) > s.limit {
		return nil, errors.New("JSON state encoded size mismatch")
	}
	return RawJSON(encoded), nil
}

func (s *JSONState) Value() (any, error) {
	if s.failed != nil {
		return nil, s.failed
	}
	value := s.root.value()
	encoded, err := common.Marshal(value)
	if err != nil {
		return nil, err
	}
	if len(encoded) != s.root.bytes || len(encoded) > s.limit {
		return nil, errors.New("JSON state encoded size mismatch")
	}
	return value, nil
}
