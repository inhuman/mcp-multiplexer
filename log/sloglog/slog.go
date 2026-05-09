// Package sloglog adapts a *slog.Logger to mcpx.Logger.
//
// Usage:
//
//	mx, err := mcpx.New(ctx, cfg, mcpx.WithLogger(sloglog.New(slog.Default())))
package sloglog

import (
	"log/slog"

	"github.com/inhuman/mcp-multiplexer"
)

// New wraps a slog.Logger as an mcpx.Logger.
func New(l *slog.Logger) mcpx.Logger {
	if l == nil {
		return mcpx.NopLogger()
	}
	return &adapter{l: l}
}

type adapter struct{ l *slog.Logger }

func (a *adapter) Debug(msg string, f ...mcpx.Field) { a.l.Debug(msg, toAttrs(f)...) }
func (a *adapter) Info(msg string, f ...mcpx.Field)  { a.l.Info(msg, toAttrs(f)...) }
func (a *adapter) Warn(msg string, f ...mcpx.Field)  { a.l.Warn(msg, toAttrs(f)...) }
func (a *adapter) Error(msg string, f ...mcpx.Field) { a.l.Error(msg, toAttrs(f)...) }

func toAttrs(in []mcpx.Field) []any {
	if len(in) == 0 {
		return nil
	}
	out := make([]any, 0, len(in)*2)
	for _, f := range in {
		out = append(out, slog.Any(f.Key, f.Value))
	}
	return out
}
