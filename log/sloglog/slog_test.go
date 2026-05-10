package sloglog_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	mcpx "github.com/inhuman/mcp-multiplexer"
	"github.com/inhuman/mcp-multiplexer/log/sloglog"
)

func newJSONLogger(buf *bytes.Buffer) *slog.Logger {
	h := slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	return slog.New(h)
}

// parseLines parses one slog JSON record per non-empty line.
func parseLines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var rec map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &rec))
		out = append(out, rec)
	}
	return out
}

func TestNewWrapsSlog(t *testing.T) {
	var buf bytes.Buffer
	logger := sloglog.New(newJSONLogger(&buf))

	logger.Info("hello", mcpx.F("key", "val"), mcpx.F("n", 7))

	recs := parseLines(t, &buf)
	require.Len(t, recs, 1)
	require.Equal(t, "hello", recs[0]["msg"])
	require.Equal(t, "val", recs[0]["key"])
	require.EqualValues(t, 7, recs[0]["n"])
}

func TestNew_AllLevels(t *testing.T) {
	var buf bytes.Buffer
	logger := sloglog.New(newJSONLogger(&buf))

	logger.Debug("d")
	logger.Info("i")
	logger.Warn("w")
	logger.Error("e")

	recs := parseLines(t, &buf)
	require.Len(t, recs, 4)
	require.Equal(t, "DEBUG", recs[0]["level"])
	require.Equal(t, "INFO", recs[1]["level"])
	require.Equal(t, "WARN", recs[2]["level"])
	require.Equal(t, "ERROR", recs[3]["level"])
}

func TestNew_ErrorAsField(t *testing.T) {
	var buf bytes.Buffer
	logger := sloglog.New(newJSONLogger(&buf))

	logger.Error("failed", mcpx.F("err", errors.New("boom")))
	recs := parseLines(t, &buf)
	require.Len(t, recs, 1)
	require.Equal(t, "boom", recs[0]["err"])
}

func TestNew_NilReturnsNop(t *testing.T) {
	logger := sloglog.New(nil)
	require.NotPanics(t, func() {
		logger.Info("x")
		logger.Error("y", mcpx.F("k", "v"))
	})
}
