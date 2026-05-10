package zaplog_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	mcpx "github.com/inhuman/mcp-multiplexer"
	"github.com/inhuman/mcp-multiplexer/log/zaplog"
)

func TestNewWrapsZap(t *testing.T) {
	core, obs := observer.New(zapcore.DebugLevel)
	logger := zaplog.New(zap.New(core))

	logger.Info("hello", mcpx.F("key", "val"), mcpx.F("n", 7))

	all := obs.All()
	require.Len(t, all, 1)
	require.Equal(t, "hello", all[0].Message)
	require.Equal(t, zapcore.InfoLevel, all[0].Level)
	ctx := all[0].ContextMap()
	require.Equal(t, "val", ctx["key"])
	require.Equal(t, int64(7), ctx["n"])
}

func TestNew_AllLevels(t *testing.T) {
	core, obs := observer.New(zapcore.DebugLevel)
	logger := zaplog.New(zap.New(core))

	logger.Debug("d")
	logger.Info("i")
	logger.Warn("w")
	logger.Error("e")

	all := obs.All()
	require.Len(t, all, 4)
	require.Equal(t, zapcore.DebugLevel, all[0].Level)
	require.Equal(t, zapcore.InfoLevel, all[1].Level)
	require.Equal(t, zapcore.WarnLevel, all[2].Level)
	require.Equal(t, zapcore.ErrorLevel, all[3].Level)
}

func TestNew_ErrorAsStructuredField(t *testing.T) {
	core, obs := observer.New(zapcore.DebugLevel)
	logger := zaplog.New(zap.New(core))

	myErr := errors.New("boom")
	logger.Error("failed", mcpx.F("err", myErr))

	all := obs.All()
	require.Len(t, all, 1)
	ctx := all[0].ContextMap()
	// zap.Any preserves structure: error value is captured (rendered as string by ContextMap).
	require.Equal(t, "boom", ctx["err"])
}

func TestNew_NilLoggerReturnsNop(t *testing.T) {
	logger := zaplog.New(nil)
	// Should not panic regardless of how many calls.
	require.NotPanics(t, func() {
		logger.Info("x")
		logger.Error("y", mcpx.F("k", "v"))
	})
}
