package mcpx

import (
	"context"
	"encoding/json"
)

// BeforeCallHook runs before a tool call is dispatched to the upstream MCP
// server. Returning a non-nil error aborts the call and the error is
// propagated to the caller. Use this for policy/RBAC enforcement, rate
// limiting, or pattern detection.
//
// Args is the JSON-encoded argument payload AFTER all transformers and field
// maps have been applied — i.e. the exact bytes that will be sent upstream.
type BeforeCallHook func(ctx context.Context, server string, tool ToolInfo, args json.RawMessage) error

// AfterCallHook runs after a tool call completes (success or error). Useful
// for logging, metrics, caching, or event sourcing. Errors returned from this
// hook are ignored — it must not affect the call result.
type AfterCallHook func(ctx context.Context, server string, tool ToolInfo, args json.RawMessage, result *CallResult, callErr error)

// ResultTransformHook runs after a successful tool call and may mutate or
// sanitize the joined text result (e.g. drift / prompt-injection detection,
// PII redaction, length capping).
//
// Returning an error short-circuits with the original Result intact and the
// error passed to AfterCallHook.
type ResultTransformHook func(ctx context.Context, server string, tool ToolInfo, text string) (string, error)

// MetaEnricher runs once per tool right after the multiplexer fetches the
// tool list from a server. It can return an updated ToolInfo with extra
// labels in Custom or adjusted boolean flags. Original input is never nil.
type MetaEnricher func(ctx context.Context, server string, info ToolInfo) ToolInfo

// CustomTransformer is a user-defined argument transformer registered via
// WithArgsTransformer and selectable from ServerConfig.ArgsTransformers by
// the same name.
type CustomTransformer func(args map[string]any) map[string]any
