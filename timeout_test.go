package mcpx_test

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	mcpx "github.com/inhuman/mcp-multiplexer"
	"github.com/inhuman/mcp-multiplexer/internal/testutil/mcptest"
)

// TestPerServerTimeout_ShortTimeoutFires verifies that a server with a short
// CallTimeout times out independently from a server with a longer timeout.
func TestPerServerTimeout_ShortTimeoutFires(t *testing.T) {
	ctx := t.Context()

	slowSrv := mcptest.NewServer(
		echoTool("fast_tool"),
		mcptest.WithToolDelay("fast_tool", 5*time.Second),
	)
	slowTS := httptest.NewServer(slowSrv.HTTPHandler())
	t.Cleanup(func() { slowTS.Close(); slowSrv.Close() })

	fastSrv := mcptest.NewServer(echoTool("fast_tool"))
	fastTS := httptest.NewServer(fastSrv.HTTPHandler())
	t.Cleanup(func() { fastTS.Close(); fastSrv.Close() })

	mx, err := mcpx.New(ctx, mcpx.MultiplexerConfig{
		Servers: []mcpx.ServerConfig{
			{
				Name:        "slow",
				Transport:   mcpx.TransportHTTP,
				URL:         slowTS.URL,
				CallTimeout: 150 * time.Millisecond,
			},
			{
				Name:      "fast",
				Transport: mcpx.TransportHTTP,
				URL:       fastTS.URL,
				// CallTimeout zero — inherits global 10s
			},
		},
	}, mcpx.WithCallTimeout(10*time.Second), mcpx.WithHTTPRetryMax(0))
	require.NoError(t, err)
	t.Cleanup(mx.Close)

	// Slow server must time out with its 150ms per-server limit.
	_, err = mx.CallTool(ctx, "slow", "fast_tool", json.RawMessage(`{}`))
	require.Error(t, err)
	require.Contains(t, err.Error(), "timeout")

	// Fast server should succeed (no artificial delay).
	result, err := mx.CallTool(ctx, "fast", "fast_tool", json.RawMessage(`{}`))
	require.NoError(t, err)
	require.NotNil(t, result)
}

// TestPerServerTimeout_ZeroInheritsGlobal verifies that a server with
// CallTimeout == 0 uses the multiplexer-wide global timeout.
func TestPerServerTimeout_ZeroInheritsGlobal(t *testing.T) {
	ctx := t.Context()

	// Slow server: delays 200ms — longer than the global 50ms but shorter than 1s.
	slowSrv := mcptest.NewServer(
		echoTool("slow_tool"),
		mcptest.WithToolDelay("slow_tool", 200*time.Millisecond),
	)
	slowTS := httptest.NewServer(slowSrv.HTTPHandler())
	t.Cleanup(func() { slowTS.Close(); slowSrv.Close() })

	mx, err := mcpx.New(ctx, mcpx.MultiplexerConfig{
		Servers: []mcpx.ServerConfig{
			{
				Name:        "srv",
				Transport:   mcpx.TransportHTTP,
				URL:         slowTS.URL,
				CallTimeout: 0, // inherit global
			},
		},
	}, mcpx.WithCallTimeout(50*time.Millisecond), mcpx.WithHTTPRetryMax(0))
	require.NoError(t, err)
	t.Cleanup(mx.Close)

	// Global 50ms must fire even though per-server is zero.
	_, err = mx.CallTool(ctx, "srv", "slow_tool", json.RawMessage(`{}`))
	require.Error(t, err)
	require.Contains(t, err.Error(), "timeout")
}

// TestPerServerTimeout_NegativeTreatedAsZero verifies that a negative
// CallTimeout falls back to the global timeout (same as zero).
func TestPerServerTimeout_NegativeTreatedAsZero(t *testing.T) {
	ctx := t.Context()

	slowSrv := mcptest.NewServer(
		echoTool("slow_tool"),
		mcptest.WithToolDelay("slow_tool", 200*time.Millisecond),
	)
	slowTS := httptest.NewServer(slowSrv.HTTPHandler())
	t.Cleanup(func() { slowTS.Close(); slowSrv.Close() })

	mx, err := mcpx.New(ctx, mcpx.MultiplexerConfig{
		Servers: []mcpx.ServerConfig{
			{
				Name:        "srv",
				Transport:   mcpx.TransportHTTP,
				URL:         slowTS.URL,
				CallTimeout: -1, // must be treated as zero → inherit global
			},
		},
	}, mcpx.WithCallTimeout(50*time.Millisecond), mcpx.WithHTTPRetryMax(0))
	require.NoError(t, err)
	t.Cleanup(mx.Close)

	_, err = mx.CallTool(ctx, "srv", "slow_tool", json.RawMessage(`{}`))
	require.Error(t, err)
	require.Contains(t, err.Error(), "timeout")
}
