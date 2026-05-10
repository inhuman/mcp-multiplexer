// Package sloglog adapts a *log/slog.Logger to the mcpx.Logger interface.
// Pass the result of sloglog.New into mcpx.WithLogger to route every event
// the multiplexer produces through your existing slog setup.
//
// The package is intentionally a thin shim: no defaults, no global state,
// no init() side effects. slog is in the standard library; importing this
// adapter pulls no third-party dependencies.
package sloglog
