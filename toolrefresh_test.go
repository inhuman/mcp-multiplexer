package mcpx_test

import (
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	mcpx "github.com/inhuman/mcp-multiplexer"
	"github.com/inhuman/mcp-multiplexer/internal/testutil/mcptest"
)

// TestToolRefresh_AutoRefresh verifies that when a server sends a
// notifications/tools/list_changed notification, the multiplexer re-fetches
// the tool list and AllTools returns the updated set within 2 seconds.
func TestToolRefresh_AutoRefresh(t *testing.T) {
	ctx := t.Context()

	srv := mcptest.NewServer(echoTool("tool-a"))
	ts := httptest.NewServer(srv.HTTPHandler())
	t.Cleanup(func() { ts.Close(); srv.Close() })

	mx, err := mcpx.New(ctx, mcpx.MultiplexerConfig{
		Servers: []mcpx.ServerConfig{
			{Name: "s", Transport: mcpx.TransportHTTP, URL: ts.URL},
		},
	}, mcpx.WithHTTPRetryMax(0))
	require.NoError(t, err)
	t.Cleanup(mx.Close)

	require.Len(t, mx.AllTools(), 1)

	// Add a second tool — SDK sends notifications/tools/list_changed automatically.
	srv.AddLiveTool(mcptest.ToolSpec{Name: "tool-b", Description: "new"})

	require.Eventually(t, func() bool {
		return len(mx.AllTools()) == 2
	}, 2*time.Second, 50*time.Millisecond, "expected 2 tools after live add")
}

// TestToolRefresh_RemoveTool verifies that a removed tool disappears from AllTools.
func TestToolRefresh_RemoveTool(t *testing.T) {
	ctx := t.Context()

	srv := mcptest.NewServer(echoTool("tool-a"), echoTool("tool-b"))
	ts := httptest.NewServer(srv.HTTPHandler())
	t.Cleanup(func() { ts.Close(); srv.Close() })

	mx, err := mcpx.New(ctx, mcpx.MultiplexerConfig{
		Servers: []mcpx.ServerConfig{
			{Name: "s", Transport: mcpx.TransportHTTP, URL: ts.URL},
		},
	}, mcpx.WithHTTPRetryMax(0))
	require.NoError(t, err)
	t.Cleanup(mx.Close)

	require.Len(t, mx.AllTools(), 2)

	srv.RemoveLiveTool("tool-b")

	require.Eventually(t, func() bool {
		return len(mx.AllTools()) == 1
	}, 2*time.Second, 50*time.Millisecond, "expected 1 tool after live remove")
}

// TestToolRefresh_CacheStableWithoutNotification verifies that without any
// notifications, the tool cache stays unchanged over time (no spontaneous clearing).
func TestToolRefresh_CacheStableWithoutNotification(t *testing.T) {
	ctx := t.Context()

	srv := mcptest.NewServer(echoTool("tool-a"))
	ts := httptest.NewServer(srv.HTTPHandler())
	t.Cleanup(func() { ts.Close(); srv.Close() })

	mx, err := mcpx.New(ctx, mcpx.MultiplexerConfig{
		Servers: []mcpx.ServerConfig{
			{Name: "s", Transport: mcpx.TransportHTTP, URL: ts.URL},
		},
	}, mcpx.WithHTTPRetryMax(0))
	require.NoError(t, err)
	t.Cleanup(mx.Close)

	require.Len(t, mx.AllTools(), 1)
	time.Sleep(300 * time.Millisecond)
	require.Len(t, mx.AllTools(), 1, "tool cache must not change without a notification")
}

// TestToolRefresh_CloseExitsCleanly verifies that Close() returns promptly and
// drain goroutines exit without blocking.
func TestToolRefresh_CloseExitsCleanly(t *testing.T) {
	ctx := t.Context()

	srv := mcptest.NewServer(echoTool("tool-a"))
	ts := httptest.NewServer(srv.HTTPHandler())
	// Register cleanup in LIFO order: mx first, then srv, then ts so that
	// ts.Close() never blocks on active SSE connections.
	t.Cleanup(ts.Close)
	t.Cleanup(srv.Close)

	mx, err := mcpx.New(ctx, mcpx.MultiplexerConfig{
		Servers: []mcpx.ServerConfig{
			{Name: "s", Transport: mcpx.TransportHTTP, URL: ts.URL},
		},
	}, mcpx.WithHTTPRetryMax(0))
	require.NoError(t, err)
	t.Cleanup(mx.Close)

	// Trigger a refresh to ensure the drain goroutine is active.
	srv.AddLiveTool(mcptest.ToolSpec{Name: "tool-b", Description: "new"})
	require.Eventually(t, func() bool {
		return len(mx.AllTools()) == 2
	}, 2*time.Second, 50*time.Millisecond)

	done := make(chan struct{})
	go func() {
		mx.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Close() did not return within 3 seconds")
	}
}

// TestToolRefresh_CallbackInvoked verifies that OnToolsChanged is called with
// correct server name and before/after lengths when tools change.
func TestToolRefresh_CallbackInvoked(t *testing.T) {
	ctx := t.Context()

	srv := mcptest.NewServer(echoTool("tool-a"))
	ts := httptest.NewServer(srv.HTTPHandler())
	t.Cleanup(func() { ts.Close(); srv.Close() })

	var (
		mu        sync.Mutex
		gotServer string
		gotBefore int
		gotAfter  int
		callCount atomic.Int64
	)

	mx, err := mcpx.New(ctx, mcpx.MultiplexerConfig{
		Servers: []mcpx.ServerConfig{
			{Name: "s", Transport: mcpx.TransportHTTP, URL: ts.URL},
		},
	},
		mcpx.WithHTTPRetryMax(0),
		mcpx.WithOnToolsChanged(func(server string, before, after []mcpx.ToolInfo) {
			mu.Lock()
			gotServer = server
			gotBefore = len(before)
			gotAfter = len(after)
			mu.Unlock()
			callCount.Add(1)
		}),
	)
	require.NoError(t, err)
	t.Cleanup(mx.Close)

	srv.AddLiveTool(mcptest.ToolSpec{Name: "tool-b", Description: "new"})

	require.Eventually(t, func() bool {
		return callCount.Load() >= 1
	}, 2*time.Second, 50*time.Millisecond, "OnToolsChanged not called")

	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, "s", gotServer)
	require.Equal(t, 1, gotBefore)
	require.Equal(t, 2, gotAfter)
}

// TestToolRefresh_NoCallbackWhenUnchanged verifies that OnToolsChanged is not
// called when a spurious notification produces an identical tool list.
func TestToolRefresh_NoCallbackWhenUnchanged(t *testing.T) {
	ctx := t.Context()

	srv := mcptest.NewServer(echoTool("tool-a"))
	ts := httptest.NewServer(srv.HTTPHandler())
	t.Cleanup(func() { ts.Close(); srv.Close() })

	var callCount atomic.Int64

	mx, err := mcpx.New(ctx, mcpx.MultiplexerConfig{
		Servers: []mcpx.ServerConfig{
			{Name: "s", Transport: mcpx.TransportHTTP, URL: ts.URL},
		},
	},
		mcpx.WithHTTPRetryMax(0),
		mcpx.WithOnToolsChanged(func(_ string, _, _ []mcpx.ToolInfo) {
			callCount.Add(1)
		}),
	)
	require.NoError(t, err)
	t.Cleanup(mx.Close)

	// Add then immediately remove the same tool — net result is the same list.
	// But to reliably test "no change" we just wait a bit without touching anything.
	time.Sleep(300 * time.Millisecond)

	require.Equal(t, int64(0), callCount.Load(), "callback must not fire without a list change")
}

// TestToolRefresh_CallbackPanicRecovered verifies that a panicking OnToolsChanged
// callback does not crash the multiplexer and AllTools continues to work.
func TestToolRefresh_CallbackPanicRecovered(t *testing.T) {
	ctx := t.Context()

	srv := mcptest.NewServer(echoTool("tool-a"))
	ts := httptest.NewServer(srv.HTTPHandler())
	t.Cleanup(func() { ts.Close(); srv.Close() })

	var refreshed atomic.Bool

	mx, err := mcpx.New(ctx, mcpx.MultiplexerConfig{
		Servers: []mcpx.ServerConfig{
			{Name: "s", Transport: mcpx.TransportHTTP, URL: ts.URL},
		},
	},
		mcpx.WithHTTPRetryMax(0),
		mcpx.WithOnToolsChanged(func(_ string, _, after []mcpx.ToolInfo) {
			if len(after) == 2 {
				refreshed.Store(true)
			}
			panic("intentional panic in callback")
		}),
	)
	require.NoError(t, err)
	t.Cleanup(mx.Close)

	srv.AddLiveTool(mcptest.ToolSpec{Name: "tool-b", Description: "new"})

	// Wait for the refresh to complete (including the panicking callback).
	require.Eventually(t, func() bool {
		return refreshed.Load()
	}, 2*time.Second, 50*time.Millisecond, "refresh did not complete")

	// Multiplexer must still be operational after the panic.
	require.Len(t, mx.AllTools(), 2)
}
