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
// The multiplexer automatically subscribes to notifications/tools/list_changed
// from each connected server and refreshes the per-server tool cache when the
// server's tool set changes at runtime (e.g. due to plugins, feature flags, or
// permission changes). An optional WithOnToolsChanged callback notifies the
// consumer after each refresh that produces a different tool list.
//
// Per-server call timeouts are supported via ServerConfig.CallTimeout; a zero
// value inherits the global default set via WithCallTimeout (30 s by default).
//
// Typed observability is available via the Metrics interface (RecordCall,
// RecordToolList). Register an implementation via WithMetrics; the default
// is a no-op. Panics inside Metrics methods are recovered by the library.
// The MCP handshake automatically advertises the consuming module's real
// version (read from build info); it falls back to "dev" when build info is
// unavailable. Override both name and version with WithClientIdentity.
//
// The library is logger-agnostic via the Logger interface (4 methods).
// Adapters for go.uber.org/zap and log/slog are provided as separate
// packages under log/zaplog and log/sloglog so the core stays
// dependency-light.
//
// The "singularResourceType" transformer accepts a configurable plural→singular
// map: extend or override the built-in Kubernetes map globally via
// WithResourceSingular, or per-server via ServerConfig.ResourceSingular
// (per-server entries win over global, which in turn wins over built-in).
//
// Runnable examples demonstrating common patterns live in the examples/
// directory: examples/basic (multi-server setup), examples/policy
// (BeforeCallHook gate), and examples/redact (ResultTransformHook PII
// redaction).
//
// See README.md for usage examples and the project constitution for
// design principles (zero-dependency core, real-dependency testing,
// secure defaults).
package mcpx
