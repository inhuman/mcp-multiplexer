# Changelog

## [Unreleased]

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
