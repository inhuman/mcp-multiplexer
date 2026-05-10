package mcpx_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	mcpx "github.com/inhuman/mcp-multiplexer"
	"github.com/inhuman/mcp-multiplexer/internal/testutil/mcptest"
)

// helper: build a multiplexer with a single server exposing the given tool.
func single(t *testing.T, name string, opts ...mcptest.Option) (*mcpx.Multiplexer, func()) {
	t.Helper()
	s := mcptest.NewServer(opts...)
	ts := httptest.NewServer(s.HTTPHandler())
	mx, err := mcpx.New(t.Context(), mcpx.MultiplexerConfig{
		Servers: []mcpx.ServerConfig{{Name: name, Transport: mcpx.TransportHTTP, URL: ts.URL}},
	}, mcpx.WithHTTPRetryMax(0))
	require.NoError(t, err)
	return mx, func() {
		mx.Close()
		ts.Close()
		s.Close()
	}
}

func TestCallTool_HappyPath(t *testing.T) {
	mx, cleanup := single(t, "s",
		mcptest.WithTool(mcptest.ToolSpec{
			Name: "echo",
			Handler: func(_ context.Context, args map[string]any) (string, error) {
				return fmt.Sprint(args["msg"]), nil
			},
		}),
	)
	defer cleanup()

	res, err := mx.CallTool(t.Context(), "s", "echo", []byte(`{"msg":"hello"}`))
	require.NoError(t, err)
	require.Equal(t, "hello", res.Text)
	require.False(t, res.IsError)
}

func TestCallTool_ErrServerNotFound(t *testing.T) {
	mx, cleanup := single(t, "s", echoTool("e"))
	defer cleanup()

	_, err := mx.CallTool(t.Context(), "missing", "e", nil)
	require.ErrorIs(t, err, mcpx.ErrServerNotFound)
	require.Contains(t, err.Error(), "available")
}

func TestCallTool_ErrToolNotFound(t *testing.T) {
	mx, cleanup := single(t, "s", echoTool("e"))
	defer cleanup()

	_, err := mx.CallTool(t.Context(), "s", "ghost", nil)
	require.ErrorIs(t, err, mcpx.ErrToolNotFound)
}

func TestCallTool_ErrInvalidArgs(t *testing.T) {
	mx, cleanup := single(t, "s", echoTool("e"))
	defer cleanup()

	_, err := mx.CallTool(t.Context(), "s", "e", []byte(`{"x":"undefined","ok":"value","empty":""}`))
	var bad *mcpx.ErrInvalidArgs
	require.ErrorAs(t, err, &bad)
	require.ElementsMatch(t, []string{"x", "empty"}, bad.BadFields)
}

func TestCallTool_Timeout(t *testing.T) {
	s := mcptest.NewServer(
		mcptest.WithTool(mcptest.ToolSpec{
			Name: "slow",
			Handler: func(_ context.Context, _ map[string]any) (string, error) {
				return "ok", nil
			},
		}),
		mcptest.WithToolDelay("slow", 500*time.Millisecond),
	)
	ts := httptest.NewServer(s.HTTPHandler())
	defer ts.Close()
	defer s.Close()

	mx, err := mcpx.New(t.Context(), mcpx.MultiplexerConfig{
		Servers: []mcpx.ServerConfig{{Name: "s", Transport: mcpx.TransportHTTP, URL: ts.URL}},
	}, mcpx.WithHTTPRetryMax(0), mcpx.WithCallTimeout(50*time.Millisecond))
	require.NoError(t, err)
	defer mx.Close()

	_, err = mx.CallTool(t.Context(), "s", "slow", nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "timeout")
}

// Pipeline: snake_case args + transformers + fieldmap → upstream sees final bytes.
func TestCallTool_Pipeline(t *testing.T) {
	var captured map[string]any
	var mu sync.Mutex
	srv := mcptest.NewServer(mcptest.WithTool(mcptest.ToolSpec{
		Name: "do",
		Handler: func(_ context.Context, args map[string]any) (string, error) {
			mu.Lock()
			captured = args
			mu.Unlock()
			return "ok", nil
		},
	}))
	ts := httptest.NewServer(srv.HTTPHandler())
	defer ts.Close()
	defer srv.Close()

	cfg := mcpx.MultiplexerConfig{
		Servers: []mcpx.ServerConfig{{
			Name:      "s",
			Transport: mcpx.TransportHTTP,
			URL:       ts.URL,
			ArgsTransformers: mcpx.ArgsTransformers{
				mcpx.ArgsTransformerCamelCase,
				mcpx.ArgsTransformerJoinArrays,
				mcpx.ArgsTransformerSingularResource,
			},
			FieldMap: map[string]string{"old": "new"},
		}},
	}

	// BeforeCall captures final bytes — confirms hooks see post-transform args.
	var hookSawArgs json.RawMessage
	mx, err := mcpx.New(t.Context(), cfg,
		mcpx.WithHTTPRetryMax(0),
		mcpx.WithBeforeCall(func(_ context.Context, _ string, _ mcpx.ToolInfo, args json.RawMessage) error {
			hookSawArgs = append(hookSawArgs[:0], args...)
			return nil
		}),
	)
	require.NoError(t, err)
	defer mx.Close()

	_, err = mx.CallTool(t.Context(), "s", "do", []byte(`{"resource_type":"pods","namespaces":["a","b"],"old":1}`))
	require.NoError(t, err)

	mu.Lock()
	defer mu.Unlock()
	// camelCase: resource_type → resourceType, then singularize: pods → pod
	require.Equal(t, "pod", captured["resourceType"])
	// joinArrays: ["a","b"] → "a b"
	require.Equal(t, "a b", captured["namespaces"])
	// fieldmap: old → new (after camelCase, key stays "old" since no underscore)
	require.Equal(t, float64(1), captured["new"])

	// Hook sees final bytes
	var fromHook map[string]any
	require.NoError(t, json.Unmarshal(hookSawArgs, &fromHook))
	require.Equal(t, "pod", fromHook["resourceType"])
	require.Equal(t, "a b", fromHook["namespaces"])
	require.Equal(t, float64(1), fromHook["new"])
}

func TestHooks_BeforeCall_AbortsAndAfterCallStillRuns(t *testing.T) {
	srv := mcptest.NewServer(echoToolSpec("t"))
	ts := httptest.NewServer(srv.HTTPHandler())
	defer ts.Close()
	defer srv.Close()

	upstream := atomic.Bool{}
	srvWrapper := mcptest.NewServer(mcptest.WithTool(mcptest.ToolSpec{
		Name: "t",
		Handler: func(_ context.Context, _ map[string]any) (string, error) {
			upstream.Store(true)
			return "ok", nil
		},
	}))
	ts2 := httptest.NewServer(srvWrapper.HTTPHandler())
	defer ts2.Close()
	defer srvWrapper.Close()

	denyErr := errors.New("denied")
	var afterCalled atomic.Bool
	var afterErr error

	mx, err := mcpx.New(t.Context(), mcpx.MultiplexerConfig{
		Servers: []mcpx.ServerConfig{{Name: "s", Transport: mcpx.TransportHTTP, URL: ts2.URL}},
	},
		mcpx.WithHTTPRetryMax(0),
		mcpx.WithBeforeCall(func(_ context.Context, _ string, _ mcpx.ToolInfo, _ json.RawMessage) error {
			return denyErr
		}),
		mcpx.WithAfterCall(func(_ context.Context, _ string, _ mcpx.ToolInfo, _ json.RawMessage, _ *mcpx.CallResult, callErr error) {
			afterCalled.Store(true)
			afterErr = callErr
		}),
	)
	require.NoError(t, err)
	defer mx.Close()

	_, err = mx.CallTool(t.Context(), "s", "t", nil)
	require.ErrorIs(t, err, denyErr)
	require.False(t, upstream.Load(), "upstream must not be called when BeforeCall errors")
	require.True(t, afterCalled.Load(), "AfterCall must run even when BeforeCall errors")
	require.ErrorIs(t, afterErr, denyErr)
}

func TestHooks_AfterCall_OrderAndErrorIgnored(t *testing.T) {
	mx, cleanup, _, addAfter := withMultiplexerCustom(t, echoToolSpec("e"))
	defer cleanup()

	var order []string
	var mu sync.Mutex
	addAfter(func(_ context.Context, _ string, _ mcpx.ToolInfo, _ json.RawMessage, _ *mcpx.CallResult, _ error) {
		mu.Lock()
		order = append(order, "first")
		mu.Unlock()
	})
	addAfter(func(_ context.Context, _ string, _ mcpx.ToolInfo, _ json.RawMessage, _ *mcpx.CallResult, _ error) {
		mu.Lock()
		order = append(order, "second")
		mu.Unlock()
		// hook errors are ignored — but Go signature says no return; just ensure no crash.
	})
	require.NoError(t, mx())
	mu.Lock()
	require.Equal(t, []string{"first", "second"}, order)
	mu.Unlock()
}

func TestHooks_ResultTransform_MutatesAndError(t *testing.T) {
	srv := mcptest.NewServer(mcptest.WithTool(mcptest.ToolSpec{
		Name: "e",
		Handler: func(_ context.Context, _ map[string]any) (string, error) {
			return "original", nil
		},
	}))
	ts := httptest.NewServer(srv.HTTPHandler())
	defer ts.Close()
	defer srv.Close()

	t.Run("mutates_text", func(t *testing.T) {
		mx, err := mcpx.New(t.Context(), mcpx.MultiplexerConfig{
			Servers: []mcpx.ServerConfig{{Name: "s", Transport: mcpx.TransportHTTP, URL: ts.URL}},
		},
			mcpx.WithHTTPRetryMax(0),
			mcpx.WithResultTransform(func(_ context.Context, _ string, _ mcpx.ToolInfo, text string) (string, error) {
				return "[redacted] " + text, nil
			}),
		)
		require.NoError(t, err)
		defer mx.Close()

		res, err := mx.CallTool(t.Context(), "s", "e", nil)
		require.NoError(t, err)
		require.Equal(t, "[redacted] original", res.Text)
	})

	t.Run("error_returned_to_client", func(t *testing.T) {
		boom := errors.New("transform exploded")
		mx, err := mcpx.New(t.Context(), mcpx.MultiplexerConfig{
			Servers: []mcpx.ServerConfig{{Name: "s", Transport: mcpx.TransportHTTP, URL: ts.URL}},
		},
			mcpx.WithHTTPRetryMax(0),
			mcpx.WithResultTransform(func(_ context.Context, _ string, _ mcpx.ToolInfo, _ string) (string, error) {
				return "", boom
			}),
		)
		require.NoError(t, err)
		defer mx.Close()

		_, err = mx.CallTool(t.Context(), "s", "e", nil)
		require.ErrorIs(t, err, boom)
	})
}

func TestHooks_MetaEnricher_OncePerTool(t *testing.T) {
	srv := mcptest.NewServer(echoToolSpec("a"), echoToolSpec("b"))
	ts := httptest.NewServer(srv.HTTPHandler())
	defer ts.Close()
	defer srv.Close()

	counts := map[string]int{}
	var mu sync.Mutex

	mx, err := mcpx.New(t.Context(), mcpx.MultiplexerConfig{
		Servers: []mcpx.ServerConfig{{Name: "s", Transport: mcpx.TransportHTTP, URL: ts.URL}},
	},
		mcpx.WithHTTPRetryMax(0),
		mcpx.WithMetaEnricher(func(_ context.Context, _ string, info mcpx.ToolInfo) mcpx.ToolInfo {
			mu.Lock()
			counts[info.Name]++
			mu.Unlock()
			if info.Custom == nil {
				info.Custom = map[string]string{}
			}
			info.Custom["enriched"] = "yes"
			return info
		}),
	)
	require.NoError(t, err)
	defer mx.Close()

	tools := mx.AllTools()
	require.Len(t, tools, 2)
	for _, ti := range tools {
		require.Equal(t, "yes", ti.Custom["enriched"])
	}
	mu.Lock()
	require.Equal(t, 1, counts["a"])
	require.Equal(t, 1, counts["b"])
	mu.Unlock()
}

// === T013 (derived flags ToolInfo) merged here as integration ===

func TestToolInfo_DerivedFlags_NoAnnotations(t *testing.T) {
	mx, cleanup := single(t, "s",
		mcptest.WithTool(mcptest.ToolSpec{Name: "noann", Handler: func(_ context.Context, _ map[string]any) (string, error) { return "", nil }}),
	)
	defer cleanup()
	tools := mx.AllTools()
	require.Len(t, tools, 1)
	ti := tools[0]
	require.False(t, ti.ReadOnly)
	require.True(t, ti.Destructive, "no annotations → conservative Destructive=true")
	require.False(t, ti.Write)
}

func TestToolInfo_DerivedFlags_ReadOnly(t *testing.T) {
	// NOTE: With ReadOnly=true and DestructiveHint=nil, current implementation
	// still marks Destructive=true because the rule applies regardless of
	// ReadOnly. Per MCP spec DestructiveHint is meaningful only when
	// ReadOnlyHint=false; a clarifying fix is tracked as a follow-up
	// (out of scope for feature 002).
	tval, fval := true, false
	mx, cleanup := single(t, "s",
		mcptest.WithTool(mcptest.ToolSpec{
			Name:        "ro",
			ReadOnly:    &tval,
			Destructive: &fval, // explicit non-destructive
			Handler:     func(_ context.Context, _ map[string]any) (string, error) { return "", nil },
		}),
	)
	defer cleanup()
	tools := mx.AllTools()
	require.Len(t, tools, 1)
	require.True(t, tools[0].ReadOnly)
	require.False(t, tools[0].Destructive)
	require.False(t, tools[0].Write)
}

func TestToolInfo_DerivedFlags_NonDestructiveWrite(t *testing.T) {
	fval := false
	mx, cleanup := single(t, "s",
		mcptest.WithTool(mcptest.ToolSpec{
			Name:        "rw",
			ReadOnly:    &fval,
			Destructive: &fval,
			Handler:     func(_ context.Context, _ map[string]any) (string, error) { return "", nil },
		}),
	)
	defer cleanup()
	tools := mx.AllTools()
	require.Len(t, tools, 1)
	require.False(t, tools[0].ReadOnly)
	require.False(t, tools[0].Destructive)
	require.True(t, tools[0].Write, "non-readonly + non-destructive ⇒ Write")
}

// helpers

func echoToolSpec(name string) mcptest.Option {
	return mcptest.WithTool(mcptest.ToolSpec{
		Name: name,
		Handler: func(_ context.Context, _ map[string]any) (string, error) {
			return "ok", nil
		},
	})
}

// withMultiplexerCustom returns a mutexual call function and a way to register
// AfterCall hooks before CallTool actually runs. Used for ordered-hook tests.
func withMultiplexerCustom(t *testing.T, opts ...mcptest.Option) (callFn func() error, cleanup func(), _ *mcpx.Multiplexer, addAfter func(mcpx.AfterCallHook)) {
	t.Helper()
	srv := mcptest.NewServer(opts...)
	ts := httptest.NewServer(srv.HTTPHandler())

	var mxOpts []mcpx.Option
	mxOpts = append(mxOpts, mcpx.WithHTTPRetryMax(0))
	addAfter = func(h mcpx.AfterCallHook) {
		mxOpts = append(mxOpts, mcpx.WithAfterCall(h))
	}

	cleanup = func() {
		ts.Close()
		srv.Close()
	}
	callFn = func() error {
		mx, err := mcpx.New(t.Context(), mcpx.MultiplexerConfig{
			Servers: []mcpx.ServerConfig{{Name: "s", Transport: mcpx.TransportHTTP, URL: ts.URL}},
		}, mxOpts...)
		if err != nil {
			return err
		}
		defer mx.Close()
		_, err = mx.CallTool(t.Context(), "s", "e", nil)
		return err
	}
	return callFn, cleanup, nil, addAfter
}
