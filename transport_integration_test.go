package mcpx_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	mcpx "github.com/inhuman/mcp-multiplexer"
	"github.com/inhuman/mcp-multiplexer/internal/testutil/mcptest"
)

// buildStdioBinary compiles internal/testutil/dockertarget into a temp dir
// and returns its path. Used for stdio-transport integration tests.
func buildStdioBinary(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "dockertarget")
	cmd := exec.Command("go", "build", "-o", bin, "./internal/testutil/dockertarget")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "go build failed: %s", out)
	return bin
}

func TestTransport_Stdio(t *testing.T) {
	bin := buildStdioBinary(t)

	mx, err := mcpx.New(t.Context(), mcpx.MultiplexerConfig{
		Servers: []mcpx.ServerConfig{
			{Name: "s", Transport: mcpx.TransportStdio, Binary: bin},
		},
	})
	require.NoError(t, err)
	defer mx.Close()

	require.Contains(t, mx.ServerNames(), "s")
	res, err := mx.CallTool(t.Context(), "s", "echo", []byte(`{"msg":"hello"}`))
	require.NoError(t, err)
	require.Equal(t, "hello", res.Text)
}

// authCapture records the most recent non-empty header value seen for
// headerName. Safe for concurrent HTTP handler invocations.
type authCapture struct {
	mu    sync.Mutex
	value string
}

func (c *authCapture) get() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.value
}

func authCapturingHandler(headerName string, captured *authCapture, inner http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if v := r.Header.Get(headerName); v != "" {
			captured.mu.Lock()
			captured.value = v
			captured.mu.Unlock()
		}
		inner.ServeHTTP(w, r)
	})
}

func TestTransport_HTTP_BearerDefault(t *testing.T) {
	srv := mcptest.NewServer(echoTool("e"))
	defer srv.Close()

	var got authCapture
	ts := httptest.NewServer(authCapturingHandler("Authorization", &got, srv.HTTPHandler()))
	defer ts.Close()

	mx, err := mcpx.New(t.Context(), mcpx.MultiplexerConfig{
		Servers: []mcpx.ServerConfig{
			{Name: "s", Transport: mcpx.TransportHTTP, URL: ts.URL, Token: "secret-tok"},
		},
	}, mcpx.WithHTTPRetryMax(0))
	require.NoError(t, err)
	defer mx.Close()

	_, err = mx.CallTool(t.Context(), "s", "e", nil)
	require.NoError(t, err)
	require.Equal(t, "Bearer secret-tok", got.get())
}

func TestTransport_HTTP_CustomHeader(t *testing.T) {
	srv := mcptest.NewServer(echoTool("e"))
	defer srv.Close()

	var got authCapture
	ts := httptest.NewServer(authCapturingHandler("X-MCP-AUTH", &got, srv.HTTPHandler()))
	defer ts.Close()

	mx, err := mcpx.New(t.Context(), mcpx.MultiplexerConfig{
		Servers: []mcpx.ServerConfig{
			{Name: "s", Transport: mcpx.TransportHTTP, URL: ts.URL, Token: "raw-tok", TokenHeader: "X-MCP-AUTH"},
		},
	}, mcpx.WithHTTPRetryMax(0))
	require.NoError(t, err)
	defer mx.Close()

	_, err = mx.CallTool(t.Context(), "s", "e", nil)
	require.NoError(t, err)
	require.Equal(t, "raw-tok", got.get(), "custom header carries raw token without Bearer prefix")
}

func TestTransport_SSE(t *testing.T) {
	srv := mcptest.NewServer(echoTool("e"))
	defer srv.Close()

	ts := httptest.NewServer(srv.SSEHandler())
	defer ts.Close()

	mx, err := mcpx.New(t.Context(), mcpx.MultiplexerConfig{
		Servers: []mcpx.ServerConfig{
			{Name: "s", Transport: mcpx.TransportSSE, URL: ts.URL},
		},
	}, mcpx.WithHTTPRetryMax(0))
	require.NoError(t, err)
	defer mx.Close()

	_, err = mx.CallTool(t.Context(), "s", "e", []byte(`{"msg":"sse-hi"}`))
	require.NoError(t, err)
}

// retryHandler returns 503 N times then delegates to inner.
type retryHandler struct {
	failures atomic.Int32
	inner    http.Handler
}

func (r *retryHandler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if r.failures.Add(-1) >= 0 {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	r.inner.ServeHTTP(w, req)
}

func TestTransport_HTTP_RetryOnTransient(t *testing.T) {
	srv := mcptest.NewServer(echoTool("e"))
	defer srv.Close()

	rh := &retryHandler{inner: srv.HTTPHandler()}
	rh.failures.Store(2)
	ts := httptest.NewServer(rh)
	defer ts.Close()

	// Allow up to 5 retries (default).
	mx, err := mcpx.New(t.Context(), mcpx.MultiplexerConfig{
		Servers: []mcpx.ServerConfig{
			{Name: "s", Transport: mcpx.TransportHTTP, URL: ts.URL},
		},
	})
	require.NoError(t, err)
	defer mx.Close()
	// After retries, server is registered (initial connect succeeded).
	require.Contains(t, mx.ServerNames(), "s")
}

// keep linter happy
var _ = context.Background
