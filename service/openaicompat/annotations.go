package openaicompat

// ResponsesAnnotationsToChat wraps Responses URL citations in the Chat schema.
// Unknown annotation kinds are retained so file/document references are not lost.
func ResponsesAnnotationsToChat(annotations []any, textOffset int) []any {
	converted := make([]any, 0, len(annotations))
	for _, annotation := range annotations {
		fields, ok := annotation.(map[string]any)
		if !ok || fields["type"] != "url_citation" || fields["url_citation"] != nil {
			converted = append(converted, annotation)
			continue
		}
		citation := make(map[string]any, len(fields))
		for name, value := range fields {
			if name != "type" {
				citation[name] = value
			}
		}
		for _, name := range []string{"start_index", "end_index"} {
			switch value := citation[name].(type) {
			case float64:
				citation[name] = value + float64(textOffset)
			case int:
				citation[name] = value + textOffset
			}
		}
		converted = append(converted, map[string]any{"type": "url_citation", "url_citation": citation})
	}
	return converted
}
