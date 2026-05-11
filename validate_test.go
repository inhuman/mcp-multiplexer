package mcpx

import (
	"encoding/json"
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

func TestValidateSchema_EmptySchema_Skips(t *testing.T) {
	errs := validateSchema(nil, json.RawMessage(`{"anything":"goes"}`))
	require.Empty(t, errs)
}

func TestValidateSchema_ValidArgs(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"}},"required":["name"]}`)
	errs := validateSchema(schema, json.RawMessage(`{"name":"alice"}`))
	require.Empty(t, errs)
}

func TestValidateSchema_MissingRequired(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","required":["name"]}`)
	errs := validateSchema(schema, json.RawMessage(`{}`))
	require.NotEmpty(t, errs)
	require.Contains(t, errs[0], "name")
}

func TestValidateSchema_WrongType(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"count":{"type":"integer"}}}`)
	errs := validateSchema(schema, json.RawMessage(`{"count":"not-a-number"}`))
	require.NotEmpty(t, errs)
}

func TestValidateSchema_EmptyArgs_ChecksRequired(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","required":["name"]}`)
	errs := validateSchema(schema, nil)
	require.NotEmpty(t, errs)
}

func TestValidateSchema_EmptyArgs_NoRequired_Valid(t *testing.T) {
	schema := json.RawMessage(`{"type":"object"}`)
	errs := validateSchema(schema, nil)
	require.Empty(t, errs)
}

func TestValidateSchema_AdditionalConstraints(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"age":{"type":"integer","minimum":0,"maximum":150}}}`)
	t.Run("valid", func(t *testing.T) {
		errs := validateSchema(schema, json.RawMessage(`{"age":25}`))
		require.Empty(t, errs)
	})
	t.Run("below_minimum", func(t *testing.T) {
		errs := validateSchema(schema, json.RawMessage(`{"age":-1}`))
		require.NotEmpty(t, errs)
	})
}
