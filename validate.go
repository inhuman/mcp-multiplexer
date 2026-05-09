package mcpx

import "strings"

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
