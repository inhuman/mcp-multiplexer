# Changelog

## [Unreleased] — v0.4.0

### Breaking changes

- **`BeforeCallHook`** signature changed: now returns `(context.Context, *CallResult, error)`.
  - `(_, _, err)` where `err != nil` → abort (as before, but new return shape).
  - `(_, result, nil)` where `result != nil` → short-circuit: upstream and ResultTransform are skipped; AfterCall still fires.
  - `(newCtx, nil, nil)` → continue; `newCtx` replaces `ctx` for all subsequent hooks and downstream steps.
  - Both `result` and `err` non-nil → error wins; result is discarded.
- **`AfterCallHook`** gains a final `duration time.Duration` parameter (wall time from `CallTool` entry).
  AfterCall now fires on **all** code paths: success, cache hit, short-circuit, upstream error, ResultTransform error, and all four rejection reasons.
- **`ResultTransformHook`** now receives `*CallResult` and mutates it in place; it no longer returns `(string, error)`.
  All parts (text and image) are accessible for sanitization.

### Added

- **Built-in response cache** — bounded LRU, enabled by default (256 entries, 30 s TTL).
  - Cacheability: `ReadOnly && Idempotent` → cached; `Destructive` → never cached; `Custom["cacheable"]="true"` → opt-in override.
  - `IsError` results and upstream errors are never cached.
  - New options: `WithCache(Cache)`, `WithCacheTTL(time.Duration)`, `WithCacheSize(int)`, `WithoutCache()`, `WithCacheKey(KeyFunc)`.
  - `Cache` interface (`Get`/`Set`) for plugging in Redis or any external store.
  - `KeyFunc` type for replacing the built-in `scope|server|tool|canonicalJSON(args)` key.
  - Context helpers: `WithCacheScope(ctx, scope)`, `CacheScope(ctx)`, `IsCacheHit(ctx)`.
  - Per-tool TTL via `ToolInfo.Custom["cache_ttl"]` (parses `time.Duration` format).
  - Warn-once (per Multiplexer lifetime) when a cacheable call has no scope set.
- **`(*CallResult).Clone() *CallResult`** — exported deep-copy method; `Data` and `Raw` byte slices are independently allocated. Nil receiver returns nil.
- **`RejectReason`** type and four constants: `RejectUnknownServer`, `RejectUnknownTool`, `RejectServerDown`, `RejectBeforeHookAbort`.
- **`OnRejectedCallFunc`** + `WithOnRejectedCall` option — fires before AfterCall on every rejection path; panics recovered.
- **`OnConnectFunc`** + `WithOnConnect` option — fires once per server after initial successful connect (before `New` returns); tools list is post-MetaEnricher; panics recovered.

### Migrating from v0.3.x

#### `BeforeCallHook`

```go
// v0.3.x
func myHook(ctx context.Context, server string, tool mcpx.ToolInfo, args json.RawMessage) error {
    if tool.Destructive { return errDenied }
    return nil
}

// v0.4.0
func myHook(ctx context.Context, server, tool string, info mcpx.ToolInfo, args json.RawMessage) (context.Context, *mcpx.CallResult, error) {
    if info.Destructive { return nil, nil, errDenied }
    return nil, nil, nil
}
```

#### `AfterCallHook`

```go
// v0.3.x
func myAfter(ctx context.Context, server string, tool mcpx.ToolInfo, args json.RawMessage, result *mcpx.CallResult, callErr error) { ... }

// v0.4.0
func myAfter(ctx context.Context, server, tool string, info mcpx.ToolInfo, args json.RawMessage, result *mcpx.CallResult, callErr error, duration time.Duration) { ... }
```

#### `ResultTransformHook`

```go
// v0.3.x
func myTransform(ctx context.Context, server string, tool mcpx.ToolInfo, text string) (string, error) {
    return strings.ReplaceAll(text, secret, "[REDACTED]"), nil
}

// v0.4.0
func myTransform(ctx context.Context, server, tool string, info mcpx.ToolInfo, result *mcpx.CallResult) error {
    result.Text = strings.ReplaceAll(result.Text, secret, "[REDACTED]")
    for i, p := range result.Parts {
        if p.Kind == mcpx.ContentText {
            result.Parts[i].Text = strings.ReplaceAll(p.Text, secret, "[REDACTED]")
        }
    }
    return nil
}
```

**Note**: AfterCall now fires on previously-silent paths (rejected calls, short-circuits). Users with naive metric aggregations should expect higher AfterCall invocation counts. Filter by `callErr` type or `IsCacheHit(ctx)` to distinguish paths.

---

## [v0.3.0] — 2026-05-12

### Added

- **Configurable resource singularizer** — the `"singularResourceType"` argument
  transformer now accepts a custom plural→singular map in addition to the built-in
  Kubernetes map. Resolution order: per-server (`ServerConfig.ResourceSingular`) →
  global custom (`WithResourceSingular`) → built-in. Passing `nil` or an empty map
  to `WithResourceSingular` is a no-op; multiple calls accumulate entries.
  - `WithResourceSingular(m map[string]string) Option` — merges `m` into the global
    custom map used by every server.
  - `ServerConfig.ResourceSingular map[string]string` (`json:"resource_singular,omitempty"`)
    — per-server override; wins over `WithResourceSingular` and the built-in map.
- **Examples directory** — three compilable `package main` programs under `examples/`:
  - `examples/basic` — connecting to stdio + HTTP servers and calling a tool.
  - `examples/policy` — `BeforeCallHook` gate blocking tools with `Destructive == true`.
  - `examples/redact` — `ResultTransformHook` replacing SSN patterns with `[REDACTED]`.
  Each example compiles with `go build ./examples/...` and uses only the root `go.mod`.
- **`policy` subpackage** — ready-made `BeforeCallHook` and `AfterCallHook` builders
  (`github.com/inhuman/mcp-multiplexer/policy`). Ships as a separate import; the core
  stays policy-free.
  - `policy.DenyDestructive()` — blocks any tool with `ToolInfo.Destructive == true`.
  - `policy.RequireRoles(roles ...string)` — allows only callers whose context carries
    one of the required roles under `policy.RolesKey`.
  - `policy.RateLimit(per time.Duration, burst int)` — per-(server, tool) token-bucket
    limiter using only stdlib; no external dependencies.
  - `policy.AuditLog(logger mcpx.Logger)` — `AfterCallHook` that logs every call
    outcome (success → Info, error → Error) without blocking the call.
- **`eino` subpackage** — Cloudwego/eino framework adapter
  (`github.com/inhuman/mcp-multiplexer/eino`). Ships with its own `go.mod` so
  cloudwego/eino is not pulled into the core dependency graph.
  - `eino.Tools(mx)` — returns one `tool.InvokableTool` per MCP tool across all servers.
  - `eino.ToolsForServer(mx, server)` — returns tools for a specific server only.
  - Each tool's `Info()` maps `mcpx.ToolInfo` → `*schema.ToolInfo` including the input
    JSON schema. `InvokableRun` delegates to `mx.CallTool`.
- **`Metrics` interface** — `RecordCall(server, tool string, dur time.Duration, err error)` and
  `RecordToolList(server string, count int)`. Register an implementation via `WithMetrics(m Metrics)`.
  Passing `nil` is a no-op. Panics inside any method are recovered by the library. Both
  `WithMetrics` and `AfterCallHook` may be registered simultaneously.
- **`RecordCall`** is invoked after every `CallTool` invocation (success, error, and pre-RPC
  failures such as `ErrToolNotFound`). `dur` measures the wall-clock time of the upstream MCP
  call only; for pre-RPC failures it is near-zero and accurately reflects that no RPC was made.
- **`RecordToolList`** is invoked after every successful tool-list fetch — both on initial connect
  and after a live `notifications/tools/list_changed` refresh.
- **Auto-detected `clientInfo.version`**: the MCP handshake now advertises the consuming module's
  real version read via `runtime/debug.ReadBuildInfo()`. Falls back to `"dev"` when build info is
  unavailable or the version is the Go sentinel `"(devel)"` (produced by `go run` and untagged
  builds). `WithClientIdentity` continues to override both name and version.

All changes are purely additive; no existing API is modified.

## [v0.2.0] — 2026-05-11

### Added

- **`ServerConfig.CallTimeout time.Duration`** — per-server call timeout override.
  A zero or negative value inherits the multiplexer-wide default set via
  `WithCallTimeout` (default 30 s). Use a shorter value for local stdio servers
  and a longer value for HTTP servers that may need retries.
- **`WithHealthCheck(interval time.Duration) Option`** — opt-in liveness supervisor.
  Probes each server on the given interval via `ListTools`; reconnects with
  exponential backoff (1 s → 2 s → … → 60 s cap) when a server is unreachable.
  interval must be positive; zero or negative values cause `New` to return an error.
- **`WithOnReconnect(fn OnReconnectFunc) Option`** — registers a callback invoked
  on every reconnect attempt. `err` is nil on success, non-nil on failure.
- **`type ServerState string`** with constants `ServerStateConnected` / `ServerStateDown`.
- **`var ErrServerDown`** — returned by `CallTool` when the target server is in the
  `down` state. Detectable via `errors.Is`.
- **`(*Multiplexer).ServerStatus() map[string]ServerState`** — snapshot of per-server
  liveness. When health-check is disabled, always returns `ServerStateConnected`.
- **Fast-fail in `CallTool`**: when a server is `down`, returns `ErrServerDown`
  immediately without waiting for the call timeout.
- **Automatic tool-list refresh**: the multiplexer subscribes to
  `notifications/tools/list_changed` from every connected server. When a server
  adds or removes tools at runtime, the per-server cache is updated automatically
  without restarting the multiplexer. Burst notifications are coalesced (at most
  one queued refresh per server at any time). If the refresh fails, the stale
  cache is retained and the error is logged. No new configuration is required.
- **`type OnToolsChangedFunc func(server string, before, after []ToolInfo)`** —
  callback type invoked after each successful refresh that changes the tool list.
  Panics in the callback are recovered by the library.
- **`WithOnToolsChanged(fn OnToolsChangedFunc) Option`** — registers the callback.
  Passing nil clears any previously registered callback.

All changes are purely additive; no existing API is modified.

All notable changes to `github.com/inhuman/mcp-multiplexer`. Format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versioning per
project [constitution](./.specify/memory/constitution.md) (pre-1.0: breaking
changes allowed between minor versions).

## [v0.1.0] — 2026-05-10

### Breaking

- **Removed `ServerConfig.Token` and `ServerConfig.TokenHeader`**. Static
  bearer/header injection no longer happens through these fields; both forms
  are now expressed through the new pluggable auth surface (see Added).

### Added

- **`ServerConfig.Auth map[string]any`** — opaque parameter block parsed
  verbatim from JSON `"auth"`. The library forwards it to the registered
  `AuthFunc` without interpretation.
- **`mcpx.AuthFunc`** — `func(ctx context.Context, server string, r *http.Request, data map[string]any) error`. Mutates the outbound request to apply authentication.
- **`mcpx.WithAuthFunc(fn AuthFunc) Option`** — registers the global
  `AuthFunc`. Required whenever any `ServerConfig.Auth` is non-nil; otherwise
  `mcpx.New` returns a descriptive error before opening any connection.
- **Subpackage `github.com/inhuman/mcp-multiplexer/auth`** with ready-made
  helpers:
  - `auth.Bearer` — for `{"auth": {"token": "..."}}` → `Authorization: Bearer <token>`.
  - `auth.HeaderToken` — for `{"auth": {"tokenName": "X-MCP-AUTH", "value": "..."}}` → header set verbatim, no Bearer prefix.
- Security tests confirm values from `Auth` (including tokens) do not leak
  into library logs or returned error messages.

### Migration

| v0.0.x JSON                                    | v0.1.0 JSON                                          | + Code                                  |
|------------------------------------------------|------------------------------------------------------|-----------------------------------------|
| `{"token": "x"}`                               | `{"auth": {"token": "x"}}`                           | `mcpx.WithAuthFunc(auth.Bearer)`        |
| `{"token": "x", "token_header": "X-MCP-AUTH"}` | `{"auth": {"tokenName": "X-MCP-AUTH", "value": "x"}}`| `mcpx.WithAuthFunc(auth.HeaderToken)`   |

For custom schemes (OAuth2 with refresh, signed bodies, request-scoped JWT)
write your own `AuthFunc` and dispatch on `data["scheme"]` (or any other
field you choose). `AuthFunc` is called on every outbound HTTP request
including retries — cache expensive derivations inside your function.

### Notes

- `BearerRoundTripper` (low-level helper for assembling custom
  `*http.Client`) is retained unchanged. For config-driven flow, prefer
  `mcpx.WithAuthFunc(auth.Bearer)` instead.
- Stdio transport is unaffected — `Auth` only applies to HTTP/SSE.

## [v0.0.1] — 2026-05-10

Initial public release. Core multiplexer (three transports, hooks,
argument transformers, kind grouping, View, logger-agnostic interface),
GitHub Actions CI, full test coverage on real MCP-SDK.
