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
	builtIn := DefaultResourceSingular()
	// All known plurals should singularize.
	knownPlurals := []string{
		"pods", "deployments", "services", "namespaces", "nodes",
		"configmaps", "secrets", "statefulsets", "daemonsets",
		"replicasets", "ingresses", "jobs", "cronjobs",
	}
	for _, p := range knownPlurals {
		t.Run(p, func(t *testing.T) {
			out := singularizeResourceType(map[string]any{"resourceType": p}, builtIn)
			val, ok := out["resourceType"].(string)
			require.True(t, ok)
			require.NotEqual(t, p, val, "must convert %q to singular", p)
			require.Equal(t, builtIn[p], val)
		})
	}

	t.Run("unknown_passthrough", func(t *testing.T) {
		out := singularizeResourceType(map[string]any{"resourceType": "horses"}, builtIn)
		require.Equal(t, "horses", out["resourceType"])
	})
	t.Run("non_string_passthrough", func(t *testing.T) {
		out := singularizeResourceType(map[string]any{"resourceType": 42}, builtIn)
		require.Equal(t, 42, out["resourceType"])
	})
	t.Run("missing_passthrough", func(t *testing.T) {
		out := singularizeResourceType(map[string]any{"other": 1}, builtIn)
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
	builtIn := DefaultResourceSingular()
	t.Run("camelCase", func(t *testing.T) {
		out := applyArgsTransformer(ArgsTransformerCamelCase, map[string]any{"a_b": 1}, nil, nil)
		require.Equal(t, map[string]any{"aB": 1}, out)
	})
	t.Run("joinArrays", func(t *testing.T) {
		out := applyArgsTransformer(ArgsTransformerJoinArrays, map[string]any{"x": []any{"a", "b"}}, nil, nil)
		require.Equal(t, map[string]any{"x": "a b"}, out)
	})
	t.Run("singularResourceType", func(t *testing.T) {
		out := applyArgsTransformer(ArgsTransformerSingularResource, map[string]any{"resourceType": "pods"}, nil, builtIn)
		require.Equal(t, "pod", out["resourceType"])
	})
	t.Run("custom_by_name", func(t *testing.T) {
		custom := map[string]CustomTransformer{
			"upper": func(args map[string]any) map[string]any {
				args["marked"] = "yes"
				return args
			},
		}
		out := applyArgsTransformer("upper", map[string]any{"a": 1}, custom, nil)
		require.Equal(t, "yes", out["marked"])
	})
	t.Run("unknown_passthrough", func(t *testing.T) {
		in := map[string]any{"a": 1}
		out := applyArgsTransformer("nonexistent", in, nil, nil)
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
	out := ts.applyAll(in, nil, DefaultResourceSingular())
	require.Equal(t, "pod", out["resourceType"])
	require.Equal(t, "a b", out["namespaces"])
}

func TestMergedSingularMap_BuiltInOnly(t *testing.T) {
	m := mergedSingularMap(nil, nil)
	require.Equal(t, "pod", m["pods"])
	require.Equal(t, "deployment", m["deployments"])
}

func TestMergedSingularMap_GlobalCustomOverridesBuiltIn(t *testing.T) {
	global := map[string]string{"pods": "CUSTOM-POD", "widgets": "widget"}
	m := mergedSingularMap(global, nil)
	require.Equal(t, "CUSTOM-POD", m["pods"])
	require.Equal(t, "widget", m["widgets"])
}

func TestMergedSingularMap_PerServerOverridesGlobal(t *testing.T) {
	global := map[string]string{"widgets": "widget"}
	perServer := map[string]string{"widgets": "special-widget"}
	m := mergedSingularMap(global, perServer)
	require.Equal(t, "special-widget", m["widgets"])
	require.Equal(t, "pod", m["pods"]) // built-in still present
}

func TestMergedSingularMap_NilMapsAreNoOp(t *testing.T) {
	m := mergedSingularMap(nil, nil)
	require.Equal(t, len(k8sResourceSingular), len(m))
}

func TestWithResourceSingular_NilNoOp(t *testing.T) {
	opts := defaultOptions()
	WithResourceSingular(nil)(opts)
	require.Nil(t, opts.resourceSingular)
	WithResourceSingular(map[string]string{})(opts)
	require.Nil(t, opts.resourceSingular)
}

func TestWithResourceSingular_MultipleCallsMerge(t *testing.T) {
	opts := defaultOptions()
	WithResourceSingular(map[string]string{"widgets": "widget"})(opts)
	WithResourceSingular(map[string]string{"fluxconfigs": "fluxconfig"})(opts)
	require.Equal(t, "widget", opts.resourceSingular["widgets"])
	require.Equal(t, "fluxconfig", opts.resourceSingular["fluxconfigs"])
}

func TestDefaultResourceSingularReturnsCopy(t *testing.T) {
	a := DefaultResourceSingular()
	b := DefaultResourceSingular()
	require.True(t, reflect.DeepEqual(a, b))
	// Mutating returned map must not affect built-in.
	a["pods"] = "MUTATED"
	require.Equal(t, "pod", DefaultResourceSingular()["pods"])
}
