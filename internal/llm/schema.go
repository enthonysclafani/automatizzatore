package llm

// strictSchema adapts a JSON Schema to what constrained decoding accepts:
// every object gets additionalProperties: false, and numeric, string and
// array constraints are dropped. The gateway still validates the answer
// against the original schema, so nothing is lost.
func strictSchema(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t)+1)
		for k, x := range t {
			switch k {
			case "minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum", "multipleOf",
				"minLength", "maxLength", "pattern", "minItems", "maxItems", "uniqueItems",
				"minProperties", "maxProperties", "$schema", "$id":
				continue
			}
			out[k] = strictSchema(x)
		}
		if isObjectSchema(t) {
			out["additionalProperties"] = false
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, x := range t {
			out[i] = strictSchema(x)
		}
		return out
	default:
		return v
	}
}

func isObjectSchema(m map[string]any) bool {
	if m["type"] == "object" {
		return true
	}
	if types, ok := m["type"].([]any); ok {
		for _, x := range types {
			if x == "object" {
				return true
			}
		}
	}
	_, hasProps := m["properties"]
	return hasProps
}
