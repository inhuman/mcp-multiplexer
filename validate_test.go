package mcpx

import (
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFindInvalidArgs(t *testing.T) {
	t.Run("empty_string_invalid", func(t *testing.T) {
		bad := findInvalidArgs(map[string]any{"k": ""})
		require.Equal(t, []string{"k"}, bad)
	})

	t.Run("placeholders_case_insensitive", func(t *testing.T) {
		for _, ph := range []string{"undefined", "UNDEFINED", "Null", "NoNe", "Unknown", "<unknown>", "<NONE>"} {
			bad := findInvalidArgs(map[string]any{"x": ph})
			require.Equal(t, []string{"x"}, bad, "%q should be flagged", ph)
		}
	})

	t.Run("nested_returns_dotted_path", func(t *testing.T) {
		bad := findInvalidArgs(map[string]any{
			"outer": map[string]any{
				"inner": map[string]any{
					"deep": "undefined",
				},
				"good": "value",
			},
		})
		require.Equal(t, []string{"outer.inner.deep"}, bad)
	})

	t.Run("multiple_bad_collected", func(t *testing.T) {
		bad := findInvalidArgs(map[string]any{
			"a": "undefined",
			"b": "valid",
			"c": "",
		})
		sort.Strings(bad)
		require.Equal(t, []string{"a", "c"}, bad)
	})

	t.Run("valid_args_no_findings", func(t *testing.T) {
		bad := findInvalidArgs(map[string]any{
			"a": "real value",
			"b": 42,
			"c": true,
			"d": []any{"x"},
		})
		require.Empty(t, bad)
	})

	t.Run("non_string_values_ignored", func(t *testing.T) {
		bad := findInvalidArgs(map[string]any{
			"int":   0,
			"bool":  false,
			"slice": []any{},
			"map":   map[string]any{},
		})
		require.Empty(t, bad)
	})

	t.Run("whitespace_only_string_invalid", func(t *testing.T) {
		bad := findInvalidArgs(map[string]any{"x": "   "})
		require.Equal(t, []string{"x"}, bad)
	})
}
