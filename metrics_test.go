package mcpx_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	mcpx "github.com/inhuman/mcp-multiplexer"
	"github.com/inhuman/mcp-multiplexer/internal/testutil/mcptest"
)

// captureMetrics is a test Metrics implementation that records all calls.
type captureMetrics struct {
	mu      sync.Mutex
	calls   []recordedCall
	lists   []recordedList
	onCall  func(server, tool string, dur time.Duration, err error)
	onList  func(server string, count int)
	panicky bool // if true, all methods panic
}

type recordedCall struct {
	server, tool string
	dur          time.Duration
	err          error
}
type recordedList struct {
	server string
	count  int
}

func (m *captureMetrics) RecordCall(server, tool string, dur time.Duration, err error) {
	if m.panicky {
		panic("intentional panic in RecordCall")
	}
	m.mu.Lock()
	m.calls = append(m.calls, recordedCall{server, tool, dur, err})
	m.mu.Unlock()
	if m.onCall != nil {
		m.onCall(server, tool, dur, err)
	}
}

func (m *captureMetrics) RecordToolList(server string, count int) {
	if m.panicky {
		panic("intentional panic in RecordToolList")
	}
	m.mu.Lock()
	m.lists = append(m.lists, recordedList{server, count})
	m.mu.Unlock()
	if m.onList != nil {
		m.onList(server, count)
	}
}

func (m *captureMetrics) Calls() []recordedCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]recordedCall(nil), m.calls...)
}

func (m *captureMetrics) Lists() []recordedList {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]recordedList(nil), m.lists...)
}

// newMetricsMux creates a multiplexer backed by srv with the given metrics.
func newMetricsMux(t *testing.T, srv *mcptest.Server, m mcpx.Metrics) (*mcpx.Multiplexer, func()) {
	t.Helper()
	ts := httptest.NewServer(srv.HTTPHandler())
	mx, err := mcpx.New(t.Context(), mcpx.MultiplexerConfig{
		Servers: []mcpx.ServerConfig{{Name: "s", Transport: mcpx.TransportHTTP, URL: ts.URL}},
	}, mcpx.WithHTTPRetryMax(0), mcpx.WithMetrics(m))
	require.NoError(t, err)
	return mx, func() {
		mx.Close()
		ts.Close()
		srv.Close()
	}
}

func TestMetrics_RecordCall_Success(t *testing.T) {
	srv := mcptest.NewServer(mcptest.WithTool(mcptest.ToolSpec{
		Name:    "echo",
		Handler: func(_ context.Context, _ map[string]any) (string, error) { return "ok", nil },
	}))
	m := &captureMetrics{}
	mx, cleanup := newMetricsMux(t, srv, m)
	defer cleanup()

	_, err := mx.CallTool(t.Context(), "s", "echo", nil)
	require.NoError(t, err)

	calls := m.Calls()
	require.Len(t, calls, 1)
	require.Equal(t, "s", calls[0].server)
	require.Equal(t, "echo", calls[0].tool)
	require.Positive(t, calls[0].dur)
	require.NoError(t, calls[0].err)
}

func TestMetrics_RecordCall_Error(t *testing.T) {
	srv := mcptest.NewServer(mcptest.WithTool(mcptest.ToolSpec{Name: "echo"}))
	m := &captureMetrics{}
	mx, cleanup := newMetricsMux(t, srv, m)
	defer cleanup()

	// tool exists but we call a non-existent one to trigger ErrToolNotFound
	_, err := mx.CallTool(t.Context(), "s", "missing", nil)
	require.Error(t, err)

	calls := m.Calls()
	require.Len(t, calls, 1)
	require.Equal(t, "s", calls[0].server)
	require.Equal(t, "missing", calls[0].tool)
	// ErrToolNotFound is returned before any RPC so dur should be 0 (call
	// never reached the transport). But the metric must still be recorded.
	require.Error(t, calls[0].err)
}

func TestMetrics_RecordToolList(t *testing.T) {
	srv := mcptest.NewServer(
		mcptest.WithTool(mcptest.ToolSpec{Name: "a"}),
		mcptest.WithTool(mcptest.ToolSpec{Name: "b"}),
		mcptest.WithTool(mcptest.ToolSpec{Name: "c"}),
	)
	m := &captureMetrics{}
	_, cleanup := newMetricsMux(t, srv, m)
	defer cleanup()

	lists := m.Lists()
	require.Len(t, lists, 1)
	require.Equal(t, "s", lists[0].server)
	require.Equal(t, 3, lists[0].count)
}

func TestMetrics_PanicRecovered(t *testing.T) {
	srv := mcptest.NewServer(mcptest.WithTool(mcptest.ToolSpec{
		Name:    "echo",
		Handler: func(_ context.Context, _ map[string]any) (string, error) { return "ok", nil },
	}))
	m := &captureMetrics{panicky: true}

	ts := httptest.NewServer(srv.HTTPHandler())
	defer ts.Close()
	defer srv.Close()

	mx, err := mcpx.New(t.Context(), mcpx.MultiplexerConfig{
		Servers: []mcpx.ServerConfig{{Name: "s", Transport: mcpx.TransportHTTP, URL: ts.URL}},
	}, mcpx.WithHTTPRetryMax(0), mcpx.WithMetrics(m))
	require.NoError(t, err) // RecordToolList panics during connect but must not propagate
	defer mx.Close()

	// RecordCall panics but call must still succeed
	result, err := mx.CallTool(t.Context(), "s", "echo", nil)
	require.NoError(t, err)
	require.Equal(t, "ok", result.Text)
}

func TestMetrics_NilNoOp(t *testing.T) {
	srv := mcptest.NewServer(mcptest.WithTool(mcptest.ToolSpec{
		Name:    "echo",
		Handler: func(_ context.Context, _ map[string]any) (string, error) { return "ok", nil },
	}))

	ts := httptest.NewServer(srv.HTTPHandler())
	defer ts.Close()
	defer srv.Close()

	// WithMetrics(nil) must not panic and must behave like no option was passed.
	mx, err := mcpx.New(t.Context(), mcpx.MultiplexerConfig{
		Servers: []mcpx.ServerConfig{{Name: "s", Transport: mcpx.TransportHTTP, URL: ts.URL}},
	}, mcpx.WithHTTPRetryMax(0), mcpx.WithMetrics(nil))
	require.NoError(t, err)
	defer mx.Close()

	result, err := mx.CallTool(t.Context(), "s", "echo", nil)
	require.NoError(t, err)
	require.Equal(t, "ok", result.Text)
}

func TestMetrics_WithAfterCallCoexists(t *testing.T) {
	srv := mcptest.NewServer(mcptest.WithTool(mcptest.ToolSpec{
		Name:    "echo",
		Handler: func(_ context.Context, _ map[string]any) (string, error) { return "ok", nil },
	}))
	m := &captureMetrics{}

	var afterCallFired atomic.Bool
	hook := mcpx.AfterCallHook(func(_ context.Context, _, _ string, _ mcpx.ToolInfo, _ json.RawMessage, _ *mcpx.CallResult, _ error, _ time.Duration) {
		afterCallFired.Store(true)
	})

	ts := httptest.NewServer(srv.HTTPHandler())
	defer ts.Close()
	defer srv.Close()

	mx, err := mcpx.New(t.Context(), mcpx.MultiplexerConfig{
		Servers: []mcpx.ServerConfig{{Name: "s", Transport: mcpx.TransportHTTP, URL: ts.URL}},
	}, mcpx.WithHTTPRetryMax(0), mcpx.WithMetrics(m), mcpx.WithAfterCall(hook))
	require.NoError(t, err)
	defer mx.Close()

	_, err = mx.CallTool(t.Context(), "s", "echo", nil)
	require.NoError(t, err)

	require.True(t, afterCallFired.Load(), "AfterCallHook must fire alongside Metrics")
	require.Len(t, m.Calls(), 1, "Metrics.RecordCall must fire alongside AfterCallHook")
}
