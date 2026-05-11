package mcpx

import (
	"encoding/json"
	"strings"

	"github.com/xeipuuv/gojsonschema"
)

// invalidArgValues are placeholder strings indicating the model did not
// resolve the actual value before invoking the tool.
var invalidArgValues = map[string]bool{
	"undefined": true,
	"null":      true,
	"none":      true,
	"unknown":   true,
	"<unknown>": true,
	"<none>":    true,
}

// findInvalidArgs walks args recursively and returns dotted paths whose
// string values are known placeholders (case-insensitive) or empty strings.
func findInvalidArgs(args map[string]any) []string {
	var bad []string
	walkArgs(args, "", &bad)
	return bad
}

// validateSchema validates args against a raw JSON Schema. Returns nil when
// args are valid or schema is empty. Returns a non-empty slice of human-
// readable violation strings otherwise. Empty or nil args are treated as
// an empty object ({}) so required-field checks fire correctly.
func validateSchema(schema, args json.RawMessage) []string {
	if len(schema) == 0 {
		return nil
	}
	if len(args) == 0 {
		args = json.RawMessage("{}")
	}
	result, err := gojsonschema.Validate(
		gojsonschema.NewBytesLoader(schema),
		gojsonschema.NewBytesLoader(args),
	)
	if err != nil {
		return []string{err.Error()}
	}
	if result.Valid() {
		return nil
	}
	out := make([]string, len(result.Errors()))
	for i, e := range result.Errors() {
		out[i] = e.String()
	}
	return out
}

func walkArgs(v any, path string, bad *[]string) {
	switch val := v.(type) {
	case map[string]any:
		for k, child := range val {
			key := k
			if path != "" {
				key = path + "." + k
			}
			walkArgs(child, key, bad)
		}
	case string:
		norm := strings.TrimSpace(strings.ToLower(val))
		if norm == "" || invalidArgValues[norm] {
			*bad = append(*bad, path)
		}
	}
}
