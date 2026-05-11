package mcpx

import (
	"context"
	"encoding/json"
	"time"
)

// BeforeCallHook runs before a tool call is dispatched to the upstream MCP
// server. Hooks chain in registration order; the first non-nil result or
// non-nil error stops the chain.
//
// Return semantics (in priority order):
//   - (_, _, err) where err != nil  — abort; both non-nil means error wins.
//   - (_, result, nil) where result != nil — short-circuit: upstream and
//     ResultTransform are skipped; AfterCall still fires.
//   - (newCtx, nil, nil) — continue; newCtx replaces ctx if non-nil.
//
// args is the JSON-encoded payload AFTER all transformers and field maps.
type BeforeCallHook func(ctx context.Context, server, tool string, info ToolInfo, args json.RawMessage) (context.Context, *CallResult, error)

// AfterCallHook runs after every tool call, on every code path (success,
// cache hit, short-circuit, upstream error, ResultTransform error, and all
// four rejection reasons). Errors returned from this hook are ignored.
// duration is wall time from CallTool entry.
type AfterCallHook func(ctx context.Context, server, tool string, info ToolInfo, args json.RawMessage, result *CallResult, callErr error, duration time.Duration)

// ResultTransformHook runs after a successful upstream tool call and mutates
// *CallResult in place. It can modify Text, Parts, and IsError (e.g. PII
// redaction, prompt-injection filtering across image parts).
// Returning an error aborts the call; AfterCall still fires with the error.
type ResultTransformHook func(ctx context.Context, server, tool string, info ToolInfo, result *CallResult) error

// MetaEnricher runs once per tool right after the multiplexer fetches the
// tool list from a server. It can return an updated ToolInfo with extra
// labels in Custom or adjusted boolean flags. Original input is never nil.
type MetaEnricher func(ctx context.Context, server string, info ToolInfo) ToolInfo

// CustomTransformer is a user-defined argument transformer registered via
// WithArgsTransformer and selectable from ServerConfig.ArgsTransformers by
// the same name.
type CustomTransformer func(args map[string]any) map[string]any

// RejectReason identifies why a CallTool request was rejected before reaching
// the upstream MCP server.
type RejectReason string

const (
	RejectUnknownServer   RejectReason = "unknown_server"
	RejectUnknownTool     RejectReason = "unknown_tool"
	RejectServerDown      RejectReason = "server_down"
	RejectBeforeHookAbort RejectReason = "before_hook_abort"
)

// OnRejectedCallFunc is called when CallTool is rejected before dispatch.
// It fires before AfterCall on rejection paths. Panics are recovered.
// reason identifies which rejection path was taken.
type OnRejectedCallFunc func(ctx context.Context, server, tool string, reason RejectReason, err error)

// OnConnectFunc is called once per server after the initial successful
// connection, before New returns. tools is the post-MetaEnricher tool list.
// Panics are recovered.
type OnConnectFunc func(server string, tools []ToolInfo)
