package mcpx

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSnakeToCamel(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"foo", "foo"},
		{"foo_bar", "fooBar"},
		{"foo_bar_baz", "fooBarBaz"},
		{"_leading", "Leading"},
		{"trailing_", "trailing"},
		{"a__b", "aB"},
		{"already_Camel_case", "alreadyCamelCase"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			require.Equal(t, tc.want, snakeToCamel(tc.in))
		})
	}
}

func TestSnakeToCamelKeysRecursive(t *testing.T) {
	in := map[string]any{
		"foo_bar": 1,
		"nested_map": map[string]any{
			"inner_key": "v",
			"deep": map[string]any{
				"more_keys": true,
			},
		},
	}
	got := snakeToCamelKeys(in)
	require.Equal(t, 1, got["fooBar"])
	nested, ok := got["nestedMap"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "v", nested["innerKey"])
	deep, ok := nested["deep"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, true, deep["moreKeys"])
}

func TestJoinStringArrays(t *testing.T) {
	in := map[string]any{
		"strs":  []any{"a", "b", "c"},
		"mixed": []any{"a", 1, "c"},
		"nested": map[string]any{
			"sub": []any{"x", "y"},
		},
		"scalar": 42,
	}
	got := joinStringArrays(in)
	require.Equal(t, "a b c", got["strs"])
	// mixed types — leave untouched
	mixed, ok := got["mixed"].([]any)
	require.True(t, ok, "mixed-type slice must be preserved as []any")
	require.Equal(t, []any{"a", 1, "c"}, mixed)
	nested, ok := got["nested"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "x y", nested["sub"])
	require.Equal(t, 42, got["scalar"])
}

func TestSingularizeResourceType(t *testing.T) {
	// All known plurals should singularize.
	knownPlurals := []string{
		"pods", "deployments", "services", "namespaces", "nodes",
		"configmaps", "secrets", "statefulsets", "daemonsets",
		"replicasets", "ingresses", "jobs", "cronjobs",
	}
	for _, p := range knownPlurals {
		t.Run(p, func(t *testing.T) {
			out := singularizeResourceType(map[string]any{"resourceType": p})
			val, ok := out["resourceType"].(string)
			require.True(t, ok)
			require.NotEqual(t, p, val, "must convert %q to singular", p)
			require.Equal(t, DefaultResourceSingular()[p], val)
		})
	}

	t.Run("unknown_passthrough", func(t *testing.T) {
		out := singularizeResourceType(map[string]any{"resourceType": "horses"})
		require.Equal(t, "horses", out["resourceType"])
	})
	t.Run("non_string_passthrough", func(t *testing.T) {
		out := singularizeResourceType(map[string]any{"resourceType": 42})
		require.Equal(t, 42, out["resourceType"])
	})
	t.Run("missing_passthrough", func(t *testing.T) {
		out := singularizeResourceType(map[string]any{"other": 1})
		require.Equal(t, map[string]any{"other": 1}, out)
	})
}

func TestApplyFieldMap(t *testing.T) {
	t.Run("empty_map_passthrough", func(t *testing.T) {
		in := map[string]any{"a": 1}
		out := applyFieldMap(in, nil)
		require.Equal(t, in, out)
	})
	t.Run("rename", func(t *testing.T) {
		in := map[string]any{"old": 1, "untouched": 2}
		out := applyFieldMap(in, map[string]string{"old": "new"})
		require.Equal(t, map[string]any{"new": 1, "untouched": 2}, out)
	})
}

func TestApplyArgsTransformer(t *testing.T) {
	t.Run("camelCase", func(t *testing.T) {
		out := applyArgsTransformer(ArgsTransformerCamelCase, map[string]any{"a_b": 1}, nil)
		require.Equal(t, map[string]any{"aB": 1}, out)
	})
	t.Run("joinArrays", func(t *testing.T) {
		out := applyArgsTransformer(ArgsTransformerJoinArrays, map[string]any{"x": []any{"a", "b"}}, nil)
		require.Equal(t, map[string]any{"x": "a b"}, out)
	})
	t.Run("singularResourceType", func(t *testing.T) {
		out := applyArgsTransformer(ArgsTransformerSingularResource, map[string]any{"resourceType": "pods"}, nil)
		require.Equal(t, "pod", out["resourceType"])
	})
	t.Run("custom_by_name", func(t *testing.T) {
		custom := map[string]CustomTransformer{
			"upper": func(args map[string]any) map[string]any {
				args["marked"] = "yes"
				return args
			},
		}
		out := applyArgsTransformer("upper", map[string]any{"a": 1}, custom)
		require.Equal(t, "yes", out["marked"])
	})
	t.Run("unknown_passthrough", func(t *testing.T) {
		in := map[string]any{"a": 1}
		out := applyArgsTransformer("nonexistent", in, nil)
		require.Equal(t, in, out)
	})
}

func TestArgsTransformersApplyAll(t *testing.T) {
	in := map[string]any{
		"resource_type": "pods",
		"namespaces":    []any{"a", "b"},
	}
	ts := ArgsTransformers{
		ArgsTransformerCamelCase,
		ArgsTransformerJoinArrays,
		ArgsTransformerSingularResource,
	}
	out := ts.applyAll(in, nil)
	require.Equal(t, "pod", out["resourceType"])
	require.Equal(t, "a b", out["namespaces"])
}

func TestDefaultResourceSingularReturnsCopy(t *testing.T) {
	a := DefaultResourceSingular()
	b := DefaultResourceSingular()
	require.True(t, reflect.DeepEqual(a, b))
	// Mutating returned map must not affect built-in.
	a["pods"] = "MUTATED"
	require.Equal(t, "pod", DefaultResourceSingular()["pods"])
}
