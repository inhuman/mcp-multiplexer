// Package zaplog adapts a *zap.Logger to mcpx.Logger.
//
// Usage:
//
//	mx, err := mcpx.New(ctx, cfg, mcpx.WithLogger(zaplog.New(zapLogger)))
package zaplog

import (
	"go.uber.org/zap"

	mcpx "github.com/inhuman/mcp-multiplexer"
)

// New wraps a zap.Logger as an mcpx.Logger.
func New(l *zap.Logger) mcpx.Logger {
	if l == nil {
		return mcpx.NopLogger()
	}
	return &adapter{l: l}
}

type adapter struct{ l *zap.Logger }

func (a *adapter) Debug(msg string, f ...mcpx.Field) { a.l.Debug(msg, toZap(f)...) }
func (a *adapter) Info(msg string, f ...mcpx.Field)  { a.l.Info(msg, toZap(f)...) }
func (a *adapter) Warn(msg string, f ...mcpx.Field)  { a.l.Warn(msg, toZap(f)...) }
func (a *adapter) Error(msg string, f ...mcpx.Field) { a.l.Error(msg, toZap(f)...) }

func toZap(in []mcpx.Field) []zap.Field {
	if len(in) == 0 {
		return nil
	}
	out := make([]zap.Field, len(in))
	for i, f := range in {
		out[i] = zap.Any(f.Key, f.Value)
	}
	return out
}
