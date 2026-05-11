package mcpx

import (
	"maps"
	"strings"
)

// ArgsTransformer names a transformation applied to tool arguments before
// sending. Built-in values:
//
//   - "camelCase"            — converts snake_case map keys to camelCase
//     (needed for MCP servers like mcp/kubernetes that use camelCase).
//   - "joinArrays"           — converts string slices into space-joined
//     strings (some servers expect a single argv string).
//   - "singularResourceType" — if args contains a "resourceType" string with
//     a known plural form, normalises it to singular (pods→pod, etc.).
//
// Custom names can be registered via WithArgsTransformer.
type ArgsTransformer string

const (
	ArgsTransformerCamelCase        ArgsTransformer = "camelCase"
	ArgsTransformerJoinArrays       ArgsTransformer = "joinArrays"
	ArgsTransformerSingularResource ArgsTransformer = "singularResourceType"
)

// ArgsTransformers is an ordered list of transformer names applied left to right.
type ArgsTransformers []ArgsTransformer

// applyArgsTransformers runs all transformers (built-in and custom) in order.
func (ts ArgsTransformers) applyAll(args map[string]any, custom map[string]CustomTransformer, singularMap map[string]string) map[string]any {
	for _, t := range ts {
		args = applyArgsTransformer(t, args, custom, singularMap)
	}
	return args
}

func applyArgsTransformer(t ArgsTransformer, args map[string]any, custom map[string]CustomTransformer, singularMap map[string]string) map[string]any {
	switch t {
	case ArgsTransformerCamelCase:
		return snakeToCamelKeys(args)
	case ArgsTransformerJoinArrays:
		return joinStringArrays(args)
	case ArgsTransformerSingularResource:
		return singularizeResourceType(args, singularMap)
	}
	if custom != nil {
		if fn, ok := custom[string(t)]; ok && fn != nil {
			return fn(args)
		}
	}
	return args
}

// mergedSingularMap builds the effective plural→singular lookup by layering:
// built-in → globalCustom → perServer (last write wins).
func mergedSingularMap(globalCustom, perServer map[string]string) map[string]string {
	m := maps.Clone(k8sResourceSingular)
	maps.Copy(m, globalCustom)
	maps.Copy(m, perServer)
	return m
}

// k8sResourceSingular maps common plural/alias forms to canonical singular.
// Kept exported via DefaultResourceSingular() so callers can extend or replace it.
var k8sResourceSingular = map[string]string{
	"pods":         "pod",
	"deployments":  "deployment",
	"services":     "service",
	"namespaces":   "namespace",
	"nodes":        "node",
	"configmaps":   "configmap",
	"secrets":      "secret",
	"statefulsets": "statefulset",
	"daemonsets":   "daemonset",
	"replicasets":  "replicaset",
	"ingresses":    "ingress",
	"jobs":         "job",
	"cronjobs":     "cronjob",
}

// DefaultResourceSingular returns a copy of the built-in plural→singular map
// used by the "singularResourceType" transformer.
func DefaultResourceSingular() map[string]string {
	out := make(map[string]string, len(k8sResourceSingular))
	for k, v := range k8sResourceSingular {
		out[k] = v
	}
	return out
}

func singularizeResourceType(args map[string]any, singular map[string]string) map[string]any {
	v, ok := args["resourceType"]
	if !ok {
		return args
	}
	s, ok := v.(string)
	if !ok {
		return args
	}
	singularVal, ok := singular[strings.ToLower(s)]
	if !ok {
		return args
	}
	out := make(map[string]any, len(args))
	for k, v := range args {
		out[k] = v
	}
	out["resourceType"] = singularVal
	return out
}

func joinStringArrays(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		switch val := v.(type) {
		case map[string]any:
			out[k] = joinStringArrays(val)
		case []any:
			strs := make([]string, 0, len(val))
			allStrings := true
			for _, item := range val {
				s, ok := item.(string)
				if !ok {
					allStrings = false
					break
				}
				strs = append(strs, s)
			}
			if allStrings {
				out[k] = strings.Join(strs, " ")
			} else {
				out[k] = v
			}
		default:
			out[k] = v
		}
	}
	return out
}

func snakeToCamelKeys(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		if nested, ok := v.(map[string]any); ok {
			v = snakeToCamelKeys(nested)
		}
		out[snakeToCamel(k)] = v
	}
	return out
}

func applyFieldMap(args map[string]any, fm map[string]string) map[string]any {
	if len(fm) == 0 {
		return args
	}
	out := make(map[string]any, len(args))
	for k, v := range args {
		if renamed, ok := fm[k]; ok {
			out[renamed] = v
		} else {
			out[k] = v
		}
	}
	return out
}

func snakeToCamel(s string) string {
	parts := strings.Split(s, "_")
	if len(parts) == 1 {
		return s
	}
	var b strings.Builder
	b.WriteString(parts[0])
	for _, p := range parts[1:] {
		if len(p) == 0 {
			continue
		}
		b.WriteString(strings.ToUpper(p[:1]))
		b.WriteString(p[1:])
	}
	return b.String()
}
