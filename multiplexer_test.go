package mcpx_test

import (
	"context"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	mcpx "github.com/inhuman/mcp-multiplexer"
	"github.com/inhuman/mcp-multiplexer/internal/testutil/mcptest"
)

// httpServer spins up an in-process MCP server behind httptest.Server.
// Returns the URL and a cleanup function.
func httpServer(t *testing.T, opts ...mcptest.Option) (string, func()) {
	t.Helper()
	s := mcptest.NewServer(opts...)
	ts := httptest.NewServer(s.HTTPHandler())
	return ts.URL, func() {
		ts.Close()
		s.Close()
	}
}

func echoTool(name string) mcptest.Option {
	return mcptest.WithTool(mcptest.ToolSpec{
		Name:        name,
		Description: "echoes msg back",
		Handler: func(_ context.Context, args map[string]any) (string, error) {
			if v, ok := args["msg"]; ok {
				return fmt.Sprint(v), nil
			}
			return "ok", nil
		},
	})
}

func TestNew_ParallelConnect_ManyServers(t *testing.T) {
	ctx := t.Context()
	const N = 10
	var cleanups []func()
	servers := make([]mcpx.ServerConfig, 0, N)
	for i := range N {
		url, cleanup := httpServer(t, echoTool("echo"))
		cleanups = append(cleanups, cleanup)
		servers = append(servers, mcpx.ServerConfig{
			Name:      fmt.Sprintf("s%d", i),
			Transport: mcpx.TransportHTTP,
			URL:       url,
		})
	}
	defer func() {
		for _, c := range cleanups {
			c()
		}
	}()

	mx, err := mcpx.New(ctx, mcpx.MultiplexerConfig{Servers: servers}, mcpx.WithHTTPRetryMax(0))
	require.NoError(t, err)
	defer mx.Close()
	require.Len(t, mx.ServerNames(), N)
}

func TestNew_FailingServerDoesNotBlockOthers(t *testing.T) {
	ctx := t.Context()
	good, cleanup := httpServer(t, echoTool("e"))
	defer cleanup()

	cfg := mcpx.MultiplexerConfig{
		Servers: []mcpx.ServerConfig{
			{Name: "good", Transport: mcpx.TransportHTTP, URL: good},
			{Name: "bad", Transport: mcpx.TransportHTTP, URL: "http://127.0.0.1:1"}, // unreachable
		},
	}
	mx, err := mcpx.New(ctx, cfg, mcpx.WithHTTPRetryMax(0))
	require.NoError(t, err)
	defer mx.Close()

	names := mx.ServerNames()
	require.Contains(t, names, "good")
	require.NotContains(t, names, "bad")
}

func TestNew_DuplicateServerName(t *testing.T) {
	ctx := t.Context()
	cfg := mcpx.MultiplexerConfig{
		Servers: []mcpx.ServerConfig{
			{Name: "x", Transport: mcpx.TransportHTTP, URL: "http://x"},
			{Name: "x", Transport: mcpx.TransportHTTP, URL: "http://y"},
		},
	}
	_, err := mcpx.New(ctx, cfg)
	require.Error(t, err)
	require.Contains(t, err.Error(), "duplicate")
}

func TestServerConfig_Validation(t *testing.T) {
	ctx := t.Context()
	cases := []struct {
		name string
		cfg  mcpx.ServerConfig
		want string
	}{
		{"empty_name", mcpx.ServerConfig{Transport: mcpx.TransportHTTP, URL: "http://x"}, "name"},
		{"unknown_transport", mcpx.ServerConfig{Name: "x", Transport: "weird"}, "transport"},
		{"stdio_no_binary", mcpx.ServerConfig{Name: "x", Transport: mcpx.TransportStdio}, "binary"},
		{"http_no_url", mcpx.ServerConfig{Name: "x", Transport: mcpx.TransportHTTP}, "url"},
		{"sse_no_url", mcpx.ServerConfig{Name: "x", Transport: mcpx.TransportSSE}, "url"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := mcpx.New(ctx, mcpx.MultiplexerConfig{Servers: []mcpx.ServerConfig{tc.cfg}})
			require.Error(t, err)
			require.Contains(t, strings.ToLower(err.Error()), tc.want)
		})
	}
}

func TestMultiplexer_KindGroups(t *testing.T) {
	ctx := t.Context()
	url1, c1 := httpServer(t, echoTool("a"), echoTool("b"))
	defer c1()
	url2, c2 := httpServer(t, echoTool("a"), echoTool("c"))
	defer c2()
	url3, c3 := httpServer(t, echoTool("z"))
	defer c3()

	hints := map[string][]string{"k8s": {"hint-1"}}
	cfg := mcpx.MultiplexerConfig{
		Servers: []mcpx.ServerConfig{
			{Name: "k1", Kind: "k8s", Transport: mcpx.TransportHTTP, URL: url1},
			{Name: "k2", Kind: "k8s", Transport: mcpx.TransportHTTP, URL: url2},
			{Name: "lone", Transport: mcpx.TransportHTTP, URL: url3}, // empty Kind = own kind
		},
		KindHints: hints,
	}
	mx, err := mcpx.New(ctx, cfg)
	require.NoError(t, err)
	defer mx.Close()

	groups := mx.KindGroups()
	require.Len(t, groups, 2)

	// Sorted by Kind alphabetically: "k8s" < "lone"
	k8s := groups[0]
	require.Equal(t, "k8s", k8s.Kind)
	require.ElementsMatch(t, []string{"k1", "k2"}, k8s.Servers)
	require.ElementsMatch(t, []string{"a", "b", "c"}, k8s.Tools, "tools must be deduplicated")
	require.Equal(t, []string{"hint-1"}, k8s.Hints)

	lone := groups[1]
	require.Equal(t, "lone", lone.Kind, "empty Kind defaults to server name")
	require.Equal(t, []string{"lone"}, lone.Servers)
	require.Equal(t, []string{"z"}, lone.Tools)
}

func TestMultiplexer_KindsForServers(t *testing.T) {
	ctx := t.Context()
	u1, c1 := httpServer(t, echoTool("a"))
	defer c1()
	u2, c2 := httpServer(t, echoTool("a"))
	defer c2()

	mx, err := mcpx.New(ctx, mcpx.MultiplexerConfig{
		Servers: []mcpx.ServerConfig{
			{Name: "x", Kind: "k", Transport: mcpx.TransportHTTP, URL: u1},
			{Name: "y", Kind: "k", Transport: mcpx.TransportHTTP, URL: u2},
		},
	})
	require.NoError(t, err)
	defer mx.Close()

	require.Equal(t, []string{"k"}, mx.KindsForServers([]string{"x", "y"}))
	require.Empty(t, mx.KindsForServers([]string{"unknown"}))
}

func TestMultiplexer_ToolsForServersAndAllTools(t *testing.T) {
	ctx := t.Context()
	u1, c1 := httpServer(t, echoTool("t1"))
	defer c1()
	u2, c2 := httpServer(t, echoTool("t2"))
	defer c2()

	mx, err := mcpx.New(ctx, mcpx.MultiplexerConfig{
		Servers: []mcpx.ServerConfig{
			{Name: "a", Transport: mcpx.TransportHTTP, URL: u1},
			{Name: "b", Transport: mcpx.TransportHTTP, URL: u2},
		},
	})
	require.NoError(t, err)
	defer mx.Close()

	tools := mx.ToolsForServers([]string{"a"})
	require.Len(t, tools, 1)
	require.Equal(t, "t1", tools[0].Name)

	all := mx.AllTools()
	require.Len(t, all, 2)
}

func TestMultiplexer_KindSettingsInheritance(t *testing.T) {
	ctx := t.Context()
	url, cleanup := httpServer(t, mcptest.WithTool(mcptest.ToolSpec{
		Name:        "do",
		Description: "echoes back the namespaces field",
		Handler: func(_ context.Context, args map[string]any) (string, error) {
			return fmt.Sprintf("%v", args["namespaces"]), nil
		},
	}))
	defer cleanup()

	cfg := mcpx.MultiplexerConfig{
		Servers: []mcpx.ServerConfig{
			{Name: "x", Kind: "k", Transport: mcpx.TransportHTTP, URL: url},
		},
		KindSettings: map[string]mcpx.KindSettings{
			"k": {ArgsTransformers: mcpx.ArgsTransformers{mcpx.ArgsTransformerJoinArrays}},
		},
	}
	mx, err := mcpx.New(ctx, cfg)
	require.NoError(t, err)
	defer mx.Close()

	res, err := mx.CallTool(ctx, "x", "do", []byte(`{"namespaces":["a","b"]}`))
	require.NoError(t, err)
	require.Contains(t, res.Text, "a b") // joinArrays applied via kind defaults
}

func TestMultiplexer_CloseIdempotent(t *testing.T) {
	ctx := t.Context()
	url, cleanup := httpServer(t, echoTool("e"))
	defer cleanup()

	mx, err := mcpx.New(ctx, mcpx.MultiplexerConfig{
		Servers: []mcpx.ServerConfig{{Name: "s", Transport: mcpx.TransportHTTP, URL: url}},
	})
	require.NoError(t, err)
	mx.Close()
	require.NotPanics(t, func() { mx.Close() })
}

// === v0.4.0: OnConnect ===

func TestOnConnect_FiresOncePerServer(t *testing.T) {
	url1, cleanup1 := httpServer(t, echoTool("a"), echoTool("b"))
	defer cleanup1()
	url2, cleanup2 := httpServer(t, echoTool("c"))
	defer cleanup2()

	type connectEvent struct {
		server string
		tools  []string
	}
	var mu sync.Mutex
	var events []connectEvent

	mx, err := mcpx.New(t.Context(), mcpx.MultiplexerConfig{
		Servers: []mcpx.ServerConfig{
			{Name: "s1", Transport: mcpx.TransportHTTP, URL: url1},
			{Name: "s2", Transport: mcpx.TransportHTTP, URL: url2},
		},
	},
		mcpx.WithHTTPRetryMax(0),
		mcpx.WithOnConnect(func(server string, tools []mcpx.ToolInfo) {
			names := make([]string, len(tools))
			for i, ti := range tools {
				names[i] = ti.Name
			}
			mu.Lock()
			events = append(events, connectEvent{server: server, tools: names})
			mu.Unlock()
		}),
	)
	require.NoError(t, err)
	defer mx.Close()

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, events, 2, "OnConnect must fire once per server")
	servers := map[string][]string{}
	for _, e := range events {
		servers[e.server] = e.tools
	}
	require.ElementsMatch(t, []string{"a", "b"}, servers["s1"])
	require.ElementsMatch(t, []string{"c"}, servers["s2"])
}

func TestOnConnect_PanicRecovered(t *testing.T) {
	url, cleanup := httpServer(t, echoTool("e"))
	defer cleanup()

	require.NotPanics(t, func() {
		mx, err := mcpx.New(t.Context(), mcpx.MultiplexerConfig{
			Servers: []mcpx.ServerConfig{{Name: "s", Transport: mcpx.TransportHTTP, URL: url}},
		},
			mcpx.WithHTTPRetryMax(0),
			mcpx.WithOnConnect(func(_ string, _ []mcpx.ToolInfo) {
				panic("boom")
			}),
		)
		if err == nil {
			mx.Close()
		}
	})
}

// keep linter happy
var _ = time.Second
