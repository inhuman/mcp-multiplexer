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
// http (StreamableHTTP), and sse. Authentication is per-server: bearer
// in Authorization or in a custom header, on a per-request basis.
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
