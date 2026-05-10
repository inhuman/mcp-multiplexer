# mcp-multiplexer

[![Go Reference](https://pkg.go.dev/badge/github.com/inhuman/mcp-multiplexer.svg)](https://pkg.go.dev/github.com/inhuman/mcp-multiplexer)

MCP multiplexer for Go — connect to many [Model Context Protocol](https://modelcontextprotocol.io/) servers at once, expose one tool list per server kind, and normalize tool arguments across them.

## Features

- **Multiple servers, one API.** Aggregate tools from any number of MCP servers behind a single `Multiplexer`.
- **Three transports.** `stdio` (subprocess), `http` (StreamableHTTP), and `sse`. Bearer auth or custom token header out of the box.
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
                Token:     os.Getenv("WEATHER_TOKEN"),
            },
        },
    }

    mx, err := mcpx.New(ctx, cfg,
        mcpx.WithLogger(sloglog.New(slog.Default())),
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
// Policy / RBAC — abort the call before it goes upstream.
mcpx.WithBeforeCall(func(ctx context.Context, server string, tool mcpx.ToolInfo, args json.RawMessage) error {
    if tool.Destructive && !isAdmin(ctx) {
        return errors.New("destructive tools require admin")
    }
    return nil
}),

// Prompt-injection / drift detection — sanitize results.
mcpx.WithResultTransform(func(ctx context.Context, server string, tool mcpx.ToolInfo, text string) (string, error) {
    return injection.Sanitize(ctx, text)
}),

// Metrics / events / cache — observe every call.
mcpx.WithAfterCall(func(ctx context.Context, server string, tool mcpx.ToolInfo, args json.RawMessage, res *mcpx.CallResult, err error) {
    metrics.RecordToolCall(server, tool.Name, err)
}),

// Tag tools with custom metadata at fetch time.
mcpx.WithMetaEnricher(func(ctx context.Context, server string, info mcpx.ToolInfo) mcpx.ToolInfo {
    if strings.HasPrefix(info.Name, "kubectl_") {
        info.Custom = map[string]string{"category": "k8s"}
    }
    return info
}),
```

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
