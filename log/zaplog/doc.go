// Package zaplog adapts a *go.uber.org/zap.Logger to the mcpx.Logger
// interface. Pass the result of zaplog.New into mcpx.WithLogger to route
// every event the multiplexer produces through your existing zap setup.
//
// The package is intentionally a thin shim: no defaults, no global state,
// no init() side effects. zap remains an optional dependency — consumers
// that want slog (or anything else) do not pull zap into their build.
package zaplog
