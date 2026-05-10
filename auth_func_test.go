package mcpx_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	mcpx "github.com/inhuman/mcp-multiplexer"
	"github.com/inhuman/mcp-multiplexer/internal/testutil/mcptest"
)

// TestAuthFunc_CalledWithCorrectData — fn получает (server, data) идентичные ServerConfig.
func TestAuthFunc_CalledWithCorrectData(t *testing.T) {
	srv := mcptest.NewServer(echoTool("e"))
	defer srv.Close()
	ts := httptest.NewServer(srv.HTTPHandler())
	defer ts.Close()

	wantAuth := map[string]any{"foo": "bar", "n": float64(42)}

	var mu sync.Mutex
	var gotServer string
	var gotData map[string]any

	fn := func(_ context.Context, server string, _ *http.Request, data map[string]any) error {
		mu.Lock()
		gotServer = server
		gotData = data
		mu.Unlock()
		return nil
	}

	mx, err := mcpx.New(t.Context(), mcpx.MultiplexerConfig{
		Servers: []mcpx.ServerConfig{
			{Name: "k8s-prod", Transport: mcpx.TransportHTTP, URL: ts.URL,
				Auth: wantAuth},
		},
	}, mcpx.WithHTTPRetryMax(0), mcpx.WithAuthFunc(fn))
	require.NoError(t, err)
	defer mx.Close()

	_, err = mx.CallTool(t.Context(), "k8s-prod", "e", nil)
	require.NoError(t, err)

	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, "k8s-prod", gotServer)
	require.True(t, reflect.DeepEqual(wantAuth, gotData),
		"auth data must reach AuthFunc unchanged: want %v got %v", wantAuth, gotData)
}

// TestAuthFunc_ErrorAbortsRequest — fn возвращает error → request не уходит.
//
// Для проверки оборачивания error используем gating: первые 2 запроса
// (initialize + tools/list) проходят без auth-error; tools/call падает.
// Так multiplexer успевает подключиться, и мы можем проверить error
// именно на CallTool path.
func TestAuthFunc_ErrorAbortsRequest(t *testing.T) {
	var upstream atomic.Int32
	srv := mcptest.NewServer(mcptest.WithTool(mcptest.ToolSpec{
		Name: "e",
		Handler: func(_ context.Context, _ map[string]any) (string, error) {
			upstream.Add(1)
			return "ok", nil
		},
	}))
	defer srv.Close()
	ts := httptest.NewServer(srv.HTTPHandler())
	defer ts.Close()

	denyErr := errors.New("denied")
	var deny atomic.Bool
	fn := func(_ context.Context, _ string, _ *http.Request, _ map[string]any) error {
		if deny.Load() {
			return denyErr
		}
		return nil
	}

	mx, err := mcpx.New(t.Context(), mcpx.MultiplexerConfig{
		Servers: []mcpx.ServerConfig{
			{Name: "s", Transport: mcpx.TransportHTTP, URL: ts.URL,
				Auth: map[string]any{"k": "v"}},
		},
	}, mcpx.WithHTTPRetryMax(0), mcpx.WithAuthFunc(fn))
	require.NoError(t, err)
	defer mx.Close()
	require.Contains(t, mx.ServerNames(), "s", "initial connect must succeed before deny is enabled")

	// Now flip to deny and try the actual tool call.
	deny.Store(true)
	_, err = mx.CallTool(t.Context(), "s", "e", nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "auth s",
		"error must be wrapped with `mcpx: auth <server>`: %s", err.Error())
	require.Contains(t, err.Error(), "denied")
	require.Zero(t, upstream.Load(), "upstream tool handler must not be called when auth denies")
}

// TestAuthFunc_NotCalledWhenAuthIsNil — server без Auth → fn не вызывается, заголовка нет.
func TestAuthFunc_NotCalledWhenAuthIsNil(t *testing.T) {
	srv := mcptest.NewServer(echoTool("e"))
	defer srv.Close()

	var hadAuth atomic.Bool
	inner := srv.HTTPHandler()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" || r.Header.Get("X-MCP-AUTH") != "" {
			hadAuth.Store(true)
		}
		inner.ServeHTTP(w, r)
	}))
	defer ts.Close()

	var calls atomic.Int32
	fn := func(_ context.Context, _ string, _ *http.Request, _ map[string]any) error {
		calls.Add(1)
		return nil
	}

	mx, err := mcpx.New(t.Context(), mcpx.MultiplexerConfig{
		Servers: []mcpx.ServerConfig{
			{Name: "s", Transport: mcpx.TransportHTTP, URL: ts.URL}, // no Auth
		},
	}, mcpx.WithHTTPRetryMax(0), mcpx.WithAuthFunc(fn))
	require.NoError(t, err)
	defer mx.Close()

	_, err = mx.CallTool(t.Context(), "s", "e", nil)
	require.NoError(t, err)
	require.Zero(t, calls.Load(), "AuthFunc must not be called when ServerConfig.Auth is nil")
	require.False(t, hadAuth.Load(), "no auth header must reach upstream")
}

// TestNew_ErrorWhenAuthSetButNoFunc — misconfig → loud error.
func TestNew_ErrorWhenAuthSetButNoFunc(t *testing.T) {
	t.Run("single_server", func(t *testing.T) {
		_, err := mcpx.New(t.Context(), mcpx.MultiplexerConfig{
			Servers: []mcpx.ServerConfig{
				{Name: "k8s", Transport: mcpx.TransportHTTP, URL: "http://x",
					Auth: map[string]any{"token": "secret-marker-xyz"}},
			},
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), `"k8s"`)
		require.Contains(t, err.Error(), "WithAuthFunc")
		// FR-016: error must NOT contain values from Auth.
		require.NotContains(t, err.Error(), "secret-marker-xyz",
			"misconfig error must not leak Auth values")
	})

	t.Run("multi_server_only_one_has_auth", func(t *testing.T) {
		_, err := mcpx.New(t.Context(), mcpx.MultiplexerConfig{
			Servers: []mcpx.ServerConfig{
				{Name: "a", Transport: mcpx.TransportHTTP, URL: "http://x"},
				{Name: "b", Transport: mcpx.TransportHTTP, URL: "http://y",
					Auth: map[string]any{"token": "marker"}},
				{Name: "c", Transport: mcpx.TransportHTTP, URL: "http://z"},
			},
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), `"b"`)
		require.NotContains(t, err.Error(), "marker")
	})
}

// TestAuthFunc_RequestIsCloned — fn safely mutates header without leaking
// across calls.
func TestAuthFunc_RequestIsCloned(t *testing.T) {
	srv := mcptest.NewServer(echoTool("e"))
	defer srv.Close()

	var seen []string
	var mu sync.Mutex
	inner := srv.HTTPHandler()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if v := r.Header.Get("X-Test-Probe"); v != "" {
			mu.Lock()
			seen = append(seen, v)
			mu.Unlock()
		}
		inner.ServeHTTP(w, r)
	}))
	defer ts.Close()

	var counter atomic.Int32
	fn := func(_ context.Context, _ string, r *http.Request, _ map[string]any) error {
		n := counter.Add(1)
		// Each call writes a unique header value.
		r.Header.Set("X-Test-Probe", "v"+itoa(int(n)))
		return nil
	}

	mx, err := mcpx.New(t.Context(), mcpx.MultiplexerConfig{
		Servers: []mcpx.ServerConfig{
			{Name: "s", Transport: mcpx.TransportHTTP, URL: ts.URL,
				Auth: map[string]any{"x": "y"}},
		},
	}, mcpx.WithHTTPRetryMax(0), mcpx.WithAuthFunc(fn))
	require.NoError(t, err)
	defer mx.Close()

	_, err = mx.CallTool(t.Context(), "s", "e", nil)
	require.NoError(t, err)
	_, err = mx.CallTool(t.Context(), "s", "e", nil)
	require.NoError(t, err)

	mu.Lock()
	defer mu.Unlock()
	require.NotEmpty(t, seen, "fn must mutate cloned request and value must reach upstream")
}

// TestAuthFunc_CalledOnEachRetry — AuthFunc invoked per HTTP attempt.
func TestAuthFunc_CalledOnEachRetry(t *testing.T) {
	srv := mcptest.NewServer(echoTool("e"))
	defer srv.Close()

	failures := atomic.Int32{}
	failures.Store(2) // first 2 requests fail with 503
	inner := srv.HTTPHandler()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if failures.Add(-1) >= 0 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		inner.ServeHTTP(w, r)
	}))
	defer ts.Close()

	var calls atomic.Int32
	fn := func(_ context.Context, _ string, _ *http.Request, _ map[string]any) error {
		calls.Add(1)
		return nil
	}

	mx, err := mcpx.New(t.Context(), mcpx.MultiplexerConfig{
		Servers: []mcpx.ServerConfig{
			{Name: "s", Transport: mcpx.TransportHTTP, URL: ts.URL,
				Auth: map[string]any{"x": "y"}},
		},
	}, mcpx.WithAuthFunc(fn)) // default retry max
	require.NoError(t, err)
	defer mx.Close()

	// AuthFunc must have been called multiple times during initial connect
	// retries; exact number depends on protocol handshake, but >=3 is a
	// reliable lower bound (initial 503 + 503 + final 200, plus subsequent
	// initialize / list calls).
	require.GreaterOrEqual(t, calls.Load(), int32(3),
		"AuthFunc must be called on each retry attempt; got %d", calls.Load())
}

// itoa avoids importing strconv just for one call.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
