# mcp-multiplexer

[![Go Reference](https://pkg.go.dev/badge/github.com/inhuman/mcp-multiplexer.svg)](https://pkg.go.dev/github.com/inhuman/mcp-multiplexer)
[![CI](https://github.com/inhuman/mcp-multiplexer/actions/workflows/ci.yml/badge.svg)](https://github.com/inhuman/mcp-multiplexer/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/inhuman/mcp-multiplexer)](https://goreportcard.com/report/github.com/inhuman/mcp-multiplexer)
[![Latest Release](https://img.shields.io/github/v/release/inhuman/mcp-multiplexer)](https://github.com/inhuman/mcp-multiplexer/releases)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

MCP multiplexer for Go — connect to many [Model Context Protocol](https://modelcontextprotocol.io/) servers at once, expose one tool list per server kind, and normalize tool arguments across them.

## Features

- **Multiple servers, one API.** Aggregate tools from any number of MCP servers behind a single `Multiplexer`.
- **Three transports.** `stdio` (subprocess), `http` (StreamableHTTP), and `sse`. Pluggable per-request auth (Bearer / custom header / OAuth2 / your own).
- **Kind grouping.** Tag servers with a `Kind` (e.g. `kubernetes`, `gitlab`) and the multiplexer deduplicates tool lists per kind — handy for prompt generation.
- **Argument transformers.** Built-in `camelCase`, `joinArrays`, `singularResourceType`. Register your own via `WithArgsTransformer`.
- **Field maps.** Rename argument keys per-server before they hit the wire.
- **Hooks for everything else.** Plug in policy enforcement, prompt-injection / drift detection, metrics, caching, eino tool wrapping — without forking the library.
- **Logger-agnostic.** A 4-method `Logger` interface plus zero-config shims for `zap` and `log/slog`.

## Install

```bash
go get github.com/inhuman/mcp-multiplexer
```

## Quick start

```go
package main

import (
    "context"
    "encoding/json"
    "fmt"
    "log/slog"
    "os"

    mcpx "github.com/inhuman/mcp-multiplexer"
    "github.com/inhuman/mcp-multiplexer/auth"
    "github.com/inhuman/mcp-multiplexer/log/sloglog"
)

func main() {
    ctx := context.Background()

    cfg := mcpx.MultiplexerConfig{
        Servers: []mcpx.ServerConfig{
            {
                Name:      "fs",
                Kind:      "filesystem",
                Transport: mcpx.TransportStdio,
                Binary:    "mcp-server-filesystem",
                Args:      []string{"/tmp"},
            },
            {
                Name:      "weather",
                Transport: mcpx.TransportHTTP,
                URL:       "https://example.com/mcp",
                Auth:      map[string]any{"token": os.Getenv("WEATHER_TOKEN")},
            },
        },
    }

    mx, err := mcpx.New(ctx, cfg,
        mcpx.WithLogger(sloglog.New(slog.Default())),
        mcpx.WithAuthFunc(auth.Bearer),
    )
    if err != nil {
        panic(err)
    }
    defer mx.Close()

    for _, g := range mx.KindGroups() {
        fmt.Printf("%s: servers=%v tools=%v\n", g.Kind, g.Servers, g.Tools)
    }

    args, _ := json.Marshal(map[string]any{"path": "/tmp/hello.txt"})
    res, err := mx.CallTool(ctx, "fs", "read_file", args)
    if err != nil {
        panic(err)
    }
    fmt.Println(res.Text)
}
```

## Hooks

All hooks are optional. Register any number; they chain in registration order.

```go
// Policy / RBAC — abort or short-circuit before going upstream.
mcpx.WithBeforeCall(func(ctx context.Context, server, tool string, info mcpx.ToolInfo, args json.RawMessage) (context.Context, *mcpx.CallResult, error) {
    if info.Destructive && !isAdmin(ctx) {
        return nil, nil, errors.New("destructive tools require admin")
    }
    return nil, nil, nil
}),

// OTel span — inject span into context, close it in AfterCall.
mcpx.WithBeforeCall(func(ctx context.Context, server, tool string, _ mcpx.ToolInfo, _ json.RawMessage) (context.Context, *mcpx.CallResult, error) {
    span := tracer.Start(ctx, "mcp."+tool)
    return span.Context(), nil, nil
}),

// Prompt-injection / drift detection — sanitize results (text and image parts).
mcpx.WithResultTransform(func(ctx context.Context, server, tool string, info mcpx.ToolInfo, result *mcpx.CallResult) error {
    result.Text = injection.Sanitize(result.Text)
    for i, p := range result.Parts {
        if p.Kind == mcpx.ContentText {
            result.Parts[i].Text = injection.Sanitize(p.Text)
        }
    }
    return nil
}),

// Metrics / events — observe every call with duration and cache status.
mcpx.WithAfterCall(func(ctx context.Context, server, tool string, info mcpx.ToolInfo, args json.RawMessage, res *mcpx.CallResult, err error, dur time.Duration) {
    source := "upstream"
    if mcpx.IsCacheHit(ctx) { source = "cache" }
    metrics.RecordToolCall(server, tool, err, dur, source)
}),

// Tag tools with custom metadata at fetch time.
mcpx.WithMetaEnricher(func(ctx context.Context, server string, info mcpx.ToolInfo) mcpx.ToolInfo {
    if strings.HasPrefix(info.Name, "kubectl_") {
        info.Custom = map[string]string{"category": "k8s"}
    }
    return info
}),
```

## Response caching

The multiplexer includes a bounded in-process LRU cache enabled by default (256 entries, 30 s TTL). A tool is cached when `ReadOnly && Idempotent`, or when `Custom["cacheable"] = "true"`. `Destructive` tools are never cached.

```go
// Default cache — no extra options needed.
mx, _ := mcpx.New(ctx, cfg)

// Isolate cache entries per tenant to prevent cross-tenant leaks.
tenantCtx := mcpx.WithCacheScope(ctx, userID)
result, _ := mx.CallTool(tenantCtx, "k8s", "list_pods", nil)

// Check cache hit in AfterCall.
mcpx.WithAfterCall(func(ctx context.Context, ..., dur time.Duration) {
    if mcpx.IsCacheHit(ctx) { /* served from cache */ }
}),

// Custom TTL for a specific tool via MetaEnricher.
mcpx.WithMetaEnricher(func(ctx context.Context, server string, info mcpx.ToolInfo) mcpx.ToolInfo {
    if info.Name == "list_nodes" {
        if info.Custom == nil { info.Custom = map[string]string{} }
        info.Custom["cache_ttl"] = "5m"
    }
    return info
}),

// Plug in Redis or any external cache.
mx, _ = mcpx.New(ctx, cfg, mcpx.WithCache(&redisCache{client: rdb}))

// Disable cache entirely.
mx, _ = mcpx.New(ctx, cfg, mcpx.WithoutCache())
```

Cache options: `WithCache(Cache)`, `WithCacheTTL(d)`, `WithCacheSize(n)`, `WithoutCache()`, `WithCacheKey(fn)`.

## Rejected-call observability

`OnRejectedCall` fires before `AfterCall` on every path that rejects a call before reaching upstream:

```go
mcpx.WithOnRejectedCall(func(ctx context.Context, server, tool string, reason mcpx.RejectReason, err error) {
    metrics.Inc("mcpx.rejected", "reason", string(reason))
}),
```

Reasons: `RejectUnknownServer`, `RejectUnknownTool`, `RejectServerDown`, `RejectBeforeHookAbort`.

## Connect callback

`OnConnect` fires once per server after a successful initial connection, before `New` returns. The tools list is post-`MetaEnricher`:

```go
mcpx.WithOnConnect(func(server string, tools []mcpx.ToolInfo) {
    log.Printf("connected to %s: %d tools", server, len(tools))
}),
```

## Migrating from v0.3.x

### `BeforeCallHook`

```go
// v0.3.x
func(ctx context.Context, server string, tool mcpx.ToolInfo, args json.RawMessage) error

// v0.4.0
func(ctx context.Context, server, tool string, info mcpx.ToolInfo, args json.RawMessage) (context.Context, *mcpx.CallResult, error)
```

Return `(nil, nil, err)` to abort, `(nil, result, nil)` to short-circuit, `(newCtx, nil, nil)` to continue.

### `AfterCallHook`

```go
// v0.3.x
func(ctx context.Context, server string, tool mcpx.ToolInfo, args json.RawMessage, result *mcpx.CallResult, callErr error)

// v0.4.0
func(ctx context.Context, server, tool string, info mcpx.ToolInfo, args json.RawMessage, result *mcpx.CallResult, callErr error, duration time.Duration)
```

AfterCall now fires on **all** paths including rejections and cache hits.

### `ResultTransformHook`

```go
// v0.3.x
func(ctx context.Context, server string, tool mcpx.ToolInfo, text string) (string, error)

// v0.4.0
func(ctx context.Context, server, tool string, info mcpx.ToolInfo, result *mcpx.CallResult) error
```

Mutate `result.Text` and `result.Parts` in place.

## Tool metadata

`ToolInfo` exposes the standard MCP annotation hints plus a derived `Write` flag and an open `Custom` map:

| Field         | Meaning                                                           |
| ------------- | ----------------------------------------------------------------- |
| `ReadOnly`    | Tool only reads state.                                            |
| `Write`       | Tool mutates state but is not destructive (derived).              |
| `Destructive` | Tool may make destructive updates (deletes, drops, irreversible). |
| `Idempotent`  | Repeated calls have no additional effect.                         |
| `OpenWorld`   | Tool interacts with the open world (network, external systems).   |
| `Custom`      | User-supplied labels added by a `MetaEnricher`.                   |

Use these in your `BeforeCall` hook to drive policy decisions (e.g. require approval for `Destructive` tools, log every `OpenWorld` call).

## Auth

Per-server authentication is configured declaratively in `ServerConfig.Auth`
(opaque map, parsed from JSON `"auth"`) and applied by a single global
`AuthFunc` registered via `mcpx.WithAuthFunc`. The library does not interpret
the shape of `Auth` — your `AuthFunc` does. Two ready-made helpers cover the
common cases:

```go
import "github.com/inhuman/mcp-multiplexer/auth"

// Bearer — for {"auth": {"token": "..."}} → Authorization: Bearer <token>
mcpx.WithAuthFunc(auth.Bearer)

// HeaderToken — for {"auth": {"tokenName": "X-MCP-AUTH", "value": "..."}}
//             → X-MCP-AUTH: <value>  (no Bearer prefix)
mcpx.WithAuthFunc(auth.HeaderToken)
```

For custom schemes (OAuth2 with refresh, AWS SigV4, HMAC, request-scoped JWT)
write your own dispatcher:

```go
mcpx.WithAuthFunc(func(ctx context.Context, server string, r *http.Request, data map[string]any) error {
    switch data["scheme"] {
    case "bearer":
        return auth.Bearer(ctx, server, r, data)
    case "oauth2":
        return mySignWithOAuth2(ctx, r, data)
    default:
        return fmt.Errorf("unknown scheme for %s: %v", server, data["scheme"])
    }
})
```

`AuthFunc` is called per outbound HTTP request including retries — cache
expensive token derivation inside your function. If a server has `Auth` set
but no `WithAuthFunc` was registered, `mcpx.New` returns an error before
opening any connection (security-relevant misconfig fails loud).

### Migrating from v0.0.x

The pre-v0.1.0 `ServerConfig.Token` / `ServerConfig.TokenHeader` fields are
removed. Translation:

| v0.0.x JSON                                    | v0.1.0 JSON                                          | + Code                                  |
|------------------------------------------------|------------------------------------------------------|-----------------------------------------|
| `{"token": "x"}`                               | `{"auth": {"token": "x"}}`                           | `mcpx.WithAuthFunc(auth.Bearer)`        |
| `{"token": "x", "token_header": "X-MCP-AUTH"}` | `{"auth": {"tokenName": "X-MCP-AUTH", "value": "x"}}`| `mcpx.WithAuthFunc(auth.HeaderToken)`   |

## Logger shims

```go
import "github.com/inhuman/mcp-multiplexer/log/zaplog"
mcpx.WithLogger(zaplog.New(zapLogger))

import "github.com/inhuman/mcp-multiplexer/log/sloglog"
mcpx.WithLogger(sloglog.New(slog.Default()))
```

Or implement the 4-method `mcpx.Logger` interface yourself.

## Filtering

`FilterByNames` produces a `View` restricted to a subset of servers — useful when you want to expose only some tools to a particular agent or user without re-connecting:

```go
view, err := mx.FilterByNames([]string{"fs"})
if err != nil { /* ... */ }
res, err := view.CallTool(ctx, "fs", "read_file", args)
```

## Testing

```bash
# Unit + in-process integration (default; no docker required):
go test -race -cover ./...

# Full set including container-based stdio lifecycle:
go test -tags integration_docker -race ./...

# Lint with the project config (golangci-lint v2.11.4 pinned):
golangci-lint run
```

In-process integration tests use the same `github.com/modelcontextprotocol/go-sdk`
that production code targets — no MCP-SDK mocks. Container tests use the
public `mcp/filesystem` image via dockertest; they are gated behind the
`integration_docker` build tag so the default test run never pulls Docker
into the dependency graph.

## License

MIT — see [LICENSE](./LICENSE).
