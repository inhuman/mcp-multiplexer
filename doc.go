// Package mcpx is a Model Context Protocol (MCP) multiplexer for Go.
//
// It connects to many MCP servers behind a single Multiplexer object and
// gives the consumer a uniform API to:
//
//   - aggregate per-server tool lists (with optional kind-based grouping
//     for prompt generation),
//   - normalise tool arguments before sending (built-in camelCase /
//     joinArrays / singularResourceType transformers, plus custom ones),
//   - intercept calls via BeforeCall / AfterCall / ResultTransform /
//     MetaEnricher hooks for policy, observability, sanitisation, and
//     metadata enrichment,
//   - filter the visible server set per consumer (View) without
//     re-establishing connections.
//
// Three transports are supported out of the box: stdio (subprocess),
// http (StreamableHTTP), and sse. Authentication is pluggable per
// outbound HTTP/SSE request: declare an opaque `auth` block per server
// in JSON, register a single AuthFunc via WithAuthFunc, and dispatch on
// data["scheme"] inside your function. Subpackage
// github.com/inhuman/mcp-multiplexer/auth ships ready-made helpers for
// the two most common shapes (Bearer, custom header).
//
// An opt-in health-check supervisor (WithHealthCheck) pings each server on a
// configurable interval and reconnects with exponential backoff when a server
// becomes unreachable. The per-server liveness state is queryable via
// ServerStatus; CallTool short-circuits with ErrServerDown when a server is
// marked down, avoiding unnecessary timeouts.
//
// The library is logger-agnostic via the Logger interface (4 methods).
// Adapters for go.uber.org/zap and log/slog are provided as separate
// packages under log/zaplog and log/sloglog so the core stays
// dependency-light.
//
// See README.md for usage examples and the project constitution for
// design principles (zero-dependency core, real-dependency testing,
// secure defaults).
package mcpx
