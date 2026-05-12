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

	"github.com/google/jsonschema-go/jsonschema"
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
		mcpx.WithBeforeCall(func(_ context.Context, _, _ string, _ mcpx.ToolInfo, args json.RawMessage) (context.Context, *mcpx.CallResult, error) {
			hookSawArgs = append(hookSawArgs[:0], args...)
			return nil, nil, nil
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
		mcpx.WithBeforeCall(func(_ context.Context, _, _ string, _ mcpx.ToolInfo, _ json.RawMessage) (context.Context, *mcpx.CallResult, error) {
			return nil, nil, denyErr
		}),
		mcpx.WithAfterCall(func(_ context.Context, _, _ string, _ mcpx.ToolInfo, _ json.RawMessage, _ *mcpx.CallResult, callErr error, _ time.Duration) {
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
	addAfter(func(_ context.Context, _, _ string, _ mcpx.ToolInfo, _ json.RawMessage, _ *mcpx.CallResult, _ error, _ time.Duration) {
		mu.Lock()
		order = append(order, "first")
		mu.Unlock()
	})
	addAfter(func(_ context.Context, _, _ string, _ mcpx.ToolInfo, _ json.RawMessage, _ *mcpx.CallResult, _ error, _ time.Duration) {
		mu.Lock()
		order = append(order, "second")
		mu.Unlock()
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
			mcpx.WithResultTransform(func(_ context.Context, _, _ string, _ mcpx.ToolInfo, result *mcpx.CallResult) error {
				result.Text = "[redacted] " + result.Text
				return nil
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
			mcpx.WithResultTransform(func(_ context.Context, _, _ string, _ mcpx.ToolInfo, _ *mcpx.CallResult) error {
				return boom
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
func withMultiplexerCustom(t *testing.T, opts ...mcptest.Option) (callFn func() error, cleanup func(), _ *mcpx.Multiplexer, addAfter func(mcpx.AfterCallHook)) { //nolint:unparam
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

// singleWithSchemaAndMxOpts builds a multiplexer with a single server, the
// given tool schema, and additional multiplexer options.
func singleWithSchemaAndMxOpts(t *testing.T, schema *jsonschema.Schema, mxOpts ...mcpx.Option) (*mcpx.Multiplexer, func()) {
	t.Helper()
	srv := mcptest.NewServer(
		mcptest.WithTool(mcptest.ToolSpec{
			Name: "tool",
			Handler: func(_ context.Context, args map[string]any) (string, error) {
				if v, ok := args["name"]; ok {
					return fmt.Sprint(v), nil
				}
				return "ok", nil
			},
			InputSchema: schema,
		}),
	)
	ts := httptest.NewServer(srv.HTTPHandler())
	allOpts := append([]mcpx.Option{mcpx.WithHTTPRetryMax(0)}, mxOpts...)
	mx, err := mcpx.New(t.Context(), mcpx.MultiplexerConfig{
		Servers: []mcpx.ServerConfig{{Name: "s", Transport: mcpx.TransportHTTP, URL: ts.URL}},
	}, allOpts...)
	require.NoError(t, err)
	return mx, func() { mx.Close(); ts.Close(); srv.Close() }
}

func TestCallTool_SchemaValidation_ValidArgs(t *testing.T) {
	schema := &jsonschema.Schema{
		Type:     "object",
		Required: []string{"name"},
		Properties: map[string]*jsonschema.Schema{
			"name": {Type: "string"},
		},
	}
	mx, cleanup := singleWithSchemaAndMxOpts(t, schema, mcpx.WithSchemaValidation())
	defer cleanup()

	res, err := mx.CallTool(t.Context(), "s", "tool", []byte(`{"name":"alice"}`))
	require.NoError(t, err)
	require.Equal(t, "alice", res.Text)
}

func TestCallTool_SchemaValidation_MissingRequired(t *testing.T) {
	schema := &jsonschema.Schema{
		Type:     "object",
		Required: []string{"name"},
		Properties: map[string]*jsonschema.Schema{
			"name": {Type: "string"},
		},
	}
	mx, cleanup := singleWithSchemaAndMxOpts(t, schema, mcpx.WithSchemaValidation())
	defer cleanup()

	_, err := mx.CallTool(t.Context(), "s", "tool", []byte(`{}`))
	require.Error(t, err)
	var ivErr *mcpx.ErrInvalidArgs
	require.True(t, errors.As(err, &ivErr))
	require.NotEmpty(t, ivErr.SchemaErrors)
	require.Contains(t, err.Error(), "schema violations")
}

func TestCallTool_SchemaValidation_WrongType(t *testing.T) {
	schema := &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"count": {Type: "integer"},
		},
	}
	mx, cleanup := singleWithSchemaAndMxOpts(t, schema, mcpx.WithSchemaValidation())
	defer cleanup()

	_, err := mx.CallTool(t.Context(), "s", "tool", []byte(`{"count":"not-a-number"}`))
	require.Error(t, err)
	var ivErr *mcpx.ErrInvalidArgs
	require.True(t, errors.As(err, &ivErr))
	require.NotEmpty(t, ivErr.SchemaErrors)
}

func TestCallTool_SchemaValidation_EmptyArgs_RequiredField(t *testing.T) {
	schema := &jsonschema.Schema{
		Type:     "object",
		Required: []string{"name"},
	}
	mx, cleanup := singleWithSchemaAndMxOpts(t, schema, mcpx.WithSchemaValidation())
	defer cleanup()

	_, err := mx.CallTool(t.Context(), "s", "tool", nil)
	require.Error(t, err)
	var ivErr *mcpx.ErrInvalidArgs
	require.True(t, errors.As(err, &ivErr))
	require.NotEmpty(t, ivErr.SchemaErrors)
}

func TestCallTool_SchemaValidation_Disabled_ByDefault(t *testing.T) {
	schema := &jsonschema.Schema{
		Type:     "object",
		Required: []string{"name"},
	}
	mx, cleanup := singleWithSchemaAndMxOpts(t, schema)
	defer cleanup()

	_, err := mx.CallTool(t.Context(), "s", "tool", []byte(`{}`))
	require.NoError(t, err)
}

func TestCallTool_SchemaValidation_NoSchema_Skips(t *testing.T) {
	srv := mcptest.NewServer(echoTool("tool"))
	ts := httptest.NewServer(srv.HTTPHandler())
	defer ts.Close()
	defer srv.Close()

	mx, err := mcpx.New(t.Context(), mcpx.MultiplexerConfig{
		Servers: []mcpx.ServerConfig{{Name: "s", Transport: mcpx.TransportHTTP, URL: ts.URL}},
	}, mcpx.WithHTTPRetryMax(0), mcpx.WithSchemaValidation())
	require.NoError(t, err)
	defer mx.Close()

	_, err = mx.CallTool(t.Context(), "s", "tool", []byte(`{"msg":"hi"}`))
	require.NoError(t, err)
}

// === v0.4.0: BeforeCall short-circuit ===

func TestBeforeCall_ShortCircuit_SkipsUpstream(t *testing.T) {
	var upstreamCalled atomic.Bool
	srv := mcptest.NewServer(mcptest.WithTool(mcptest.ToolSpec{
		Name: "t",
		Handler: func(_ context.Context, _ map[string]any) (string, error) {
			upstreamCalled.Store(true)
			return "upstream", nil
		},
	}))
	ts := httptest.NewServer(srv.HTTPHandler())
	defer ts.Close()
	defer srv.Close()

	precomputed := &mcpx.CallResult{Text: "from-before"}
	var afterResult *mcpx.CallResult
	var afterErr error

	mx, err := mcpx.New(t.Context(), mcpx.MultiplexerConfig{
		Servers: []mcpx.ServerConfig{{Name: "s", Transport: mcpx.TransportHTTP, URL: ts.URL}},
	},
		mcpx.WithHTTPRetryMax(0),
		mcpx.WithoutCache(),
		mcpx.WithBeforeCall(func(_ context.Context, _, _ string, _ mcpx.ToolInfo, _ json.RawMessage) (context.Context, *mcpx.CallResult, error) {
			return nil, precomputed, nil
		}),
		mcpx.WithAfterCall(func(_ context.Context, _, _ string, _ mcpx.ToolInfo, _ json.RawMessage, result *mcpx.CallResult, callErr error, _ time.Duration) {
			afterResult = result
			afterErr = callErr
		}),
	)
	require.NoError(t, err)
	defer mx.Close()

	res, err := mx.CallTool(t.Context(), "s", "t", nil)
	require.NoError(t, err)
	require.Equal(t, "from-before", res.Text)
	require.False(t, upstreamCalled.Load(), "upstream must not be called when Before short-circuits")
	require.Equal(t, precomputed, afterResult, "AfterCall must receive the short-circuit result")
	require.NoError(t, afterErr)
}

func TestBeforeCall_BothResultAndError_ErrorWins(t *testing.T) {
	srv := mcptest.NewServer(echoToolSpec("t"))
	ts := httptest.NewServer(srv.HTTPHandler())
	defer ts.Close()
	defer srv.Close()

	boom := errors.New("error-wins")
	mx, err := mcpx.New(t.Context(), mcpx.MultiplexerConfig{
		Servers: []mcpx.ServerConfig{{Name: "s", Transport: mcpx.TransportHTTP, URL: ts.URL}},
	},
		mcpx.WithHTTPRetryMax(0),
		mcpx.WithoutCache(),
		mcpx.WithBeforeCall(func(_ context.Context, _, _ string, _ mcpx.ToolInfo, _ json.RawMessage) (context.Context, *mcpx.CallResult, error) {
			return nil, &mcpx.CallResult{Text: "ignored"}, boom
		}),
	)
	require.NoError(t, err)
	defer mx.Close()

	_, err = mx.CallTool(t.Context(), "s", "t", nil)
	require.ErrorIs(t, err, boom)
}

func TestBeforeCall_CtxPropagation(t *testing.T) {
	srv := mcptest.NewServer(echoToolSpec("t"))
	ts := httptest.NewServer(srv.HTTPHandler())
	defer ts.Close()
	defer srv.Close()

	type ctxKey struct{}
	var afterCtxVal any

	mx, err := mcpx.New(t.Context(), mcpx.MultiplexerConfig{
		Servers: []mcpx.ServerConfig{{Name: "s", Transport: mcpx.TransportHTTP, URL: ts.URL}},
	},
		mcpx.WithHTTPRetryMax(0),
		mcpx.WithoutCache(),
		mcpx.WithBeforeCall(func(ctx context.Context, _, _ string, _ mcpx.ToolInfo, _ json.RawMessage) (context.Context, *mcpx.CallResult, error) {
			return context.WithValue(ctx, ctxKey{}, "injected"), nil, nil
		}),
		mcpx.WithAfterCall(func(ctx context.Context, _, _ string, _ mcpx.ToolInfo, _ json.RawMessage, _ *mcpx.CallResult, _ error, _ time.Duration) {
			afterCtxVal = ctx.Value(ctxKey{})
		}),
	)
	require.NoError(t, err)
	defer mx.Close()

	_, err = mx.CallTool(t.Context(), "s", "t", nil)
	require.NoError(t, err)
	require.Equal(t, "injected", afterCtxVal)
}

func TestBeforeCall_Chain_StopsOnFirstResult(t *testing.T) {
	srv := mcptest.NewServer(echoToolSpec("t"))
	ts := httptest.NewServer(srv.HTTPHandler())
	defer ts.Close()
	defer srv.Close()

	var secondCalled atomic.Bool
	mx, err := mcpx.New(t.Context(), mcpx.MultiplexerConfig{
		Servers: []mcpx.ServerConfig{{Name: "s", Transport: mcpx.TransportHTTP, URL: ts.URL}},
	},
		mcpx.WithHTTPRetryMax(0),
		mcpx.WithoutCache(),
		mcpx.WithBeforeCall(func(_ context.Context, _, _ string, _ mcpx.ToolInfo, _ json.RawMessage) (context.Context, *mcpx.CallResult, error) {
			return nil, &mcpx.CallResult{Text: "first"}, nil
		}),
		mcpx.WithBeforeCall(func(_ context.Context, _, _ string, _ mcpx.ToolInfo, _ json.RawMessage) (context.Context, *mcpx.CallResult, error) {
			secondCalled.Store(true)
			return nil, nil, nil
		}),
	)
	require.NoError(t, err)
	defer mx.Close()

	res, err := mx.CallTool(t.Context(), "s", "t", nil)
	require.NoError(t, err)
	require.Equal(t, "first", res.Text)
	require.False(t, secondCalled.Load(), "second BeforeCall must not run after first short-circuited")
}

// === v0.4.0: ResultTransform mutates *CallResult ===

func TestResultTransform_MutatesParts(t *testing.T) {
	srv := mcptest.NewServer(mcptest.WithTool(mcptest.ToolSpec{
		Name:    "t",
		Handler: func(_ context.Context, _ map[string]any) (string, error) { return "sensitive", nil },
	}))
	ts := httptest.NewServer(srv.HTTPHandler())
	defer ts.Close()
	defer srv.Close()

	mx, err := mcpx.New(t.Context(), mcpx.MultiplexerConfig{
		Servers: []mcpx.ServerConfig{{Name: "s", Transport: mcpx.TransportHTTP, URL: ts.URL}},
	},
		mcpx.WithHTTPRetryMax(0),
		mcpx.WithoutCache(),
		mcpx.WithResultTransform(func(_ context.Context, _, _ string, _ mcpx.ToolInfo, result *mcpx.CallResult) error {
			result.Text = "redacted"
			for i, p := range result.Parts {
				if p.Kind == mcpx.ContentText {
					result.Parts[i].Text = "redacted"
				}
			}
			return nil
		}),
	)
	require.NoError(t, err)
	defer mx.Close()

	res, err := mx.CallTool(t.Context(), "s", "t", nil)
	require.NoError(t, err)
	require.Equal(t, "redacted", res.Text)
	for _, p := range res.Parts {
		if p.Kind == mcpx.ContentText {
			require.Equal(t, "redacted", p.Text)
		}
	}
}

// === v0.4.0: AfterCall receives duration on all paths ===

func TestAfterCall_Duration_NonZero(t *testing.T) {
	mx, cleanup := single(t, "s", echoToolSpec("t"))
	defer cleanup()

	var gotDur time.Duration
	mx2, err := mcpx.New(t.Context(), mcpx.MultiplexerConfig{
		Servers: []mcpx.ServerConfig{{Name: "s", Transport: mcpx.TransportHTTP, URL: func() string {
			srv := mcptest.NewServer(echoToolSpec("t"))
			ts := httptest.NewServer(srv.HTTPHandler())
			t.Cleanup(func() { ts.Close(); srv.Close() })
			return ts.URL
		}()}},
	},
		mcpx.WithHTTPRetryMax(0),
		mcpx.WithoutCache(),
		mcpx.WithAfterCall(func(_ context.Context, _, _ string, _ mcpx.ToolInfo, _ json.RawMessage, _ *mcpx.CallResult, _ error, dur time.Duration) {
			gotDur = dur
		}),
	)
	require.NoError(t, err)
	defer mx2.Close()
	_ = mx // keep original single() cleanup alive

	_, err = mx2.CallTool(t.Context(), "s", "t", nil)
	require.NoError(t, err)
	require.Positive(t, gotDur, "duration must be > 0")
}

// === v0.4.0: OnRejectedCall ===

func TestOnRejectedCall_UnknownServer(t *testing.T) {
	mx, cleanup := single(t, "s", echoToolSpec("t"))
	defer cleanup()

	var gotReason mcpx.RejectReason
	mx2, err := mcpx.New(t.Context(), mcpx.MultiplexerConfig{
		Servers: []mcpx.ServerConfig{{Name: "s", Transport: mcpx.TransportHTTP, URL: func() string {
			srv := mcptest.NewServer(echoToolSpec("t"))
			ts := httptest.NewServer(srv.HTTPHandler())
			t.Cleanup(func() { ts.Close(); srv.Close() })
			return ts.URL
		}()}},
	},
		mcpx.WithHTTPRetryMax(0),
		mcpx.WithoutCache(),
		mcpx.WithOnRejectedCall(func(_ context.Context, _, _ string, reason mcpx.RejectReason, _ error) {
			gotReason = reason
		}),
	)
	require.NoError(t, err)
	defer mx2.Close()
	_ = mx

	_, err = mx2.CallTool(t.Context(), "nope", "t", nil)
	require.ErrorIs(t, err, mcpx.ErrServerNotFound)
	require.Equal(t, mcpx.RejectUnknownServer, gotReason)
}

func TestOnRejectedCall_UnknownTool(t *testing.T) {
	var gotReason mcpx.RejectReason
	srv := mcptest.NewServer(echoToolSpec("t"))
	ts := httptest.NewServer(srv.HTTPHandler())
	defer ts.Close()
	defer srv.Close()

	mx, err := mcpx.New(t.Context(), mcpx.MultiplexerConfig{
		Servers: []mcpx.ServerConfig{{Name: "s", Transport: mcpx.TransportHTTP, URL: ts.URL}},
	},
		mcpx.WithHTTPRetryMax(0),
		mcpx.WithoutCache(),
		mcpx.WithOnRejectedCall(func(_ context.Context, _, _ string, reason mcpx.RejectReason, _ error) {
			gotReason = reason
		}),
	)
	require.NoError(t, err)
	defer mx.Close()

	_, err = mx.CallTool(t.Context(), "s", "ghost", nil)
	require.ErrorIs(t, err, mcpx.ErrToolNotFound)
	require.Equal(t, mcpx.RejectUnknownTool, gotReason)
}

func TestOnRejectedCall_BeforeHookAbort(t *testing.T) {
	var gotReason mcpx.RejectReason
	var afterFired atomic.Bool
	srv := mcptest.NewServer(echoToolSpec("t"))
	ts := httptest.NewServer(srv.HTTPHandler())
	defer ts.Close()
	defer srv.Close()

	boom := errors.New("policy-deny")
	mx, err := mcpx.New(t.Context(), mcpx.MultiplexerConfig{
		Servers: []mcpx.ServerConfig{{Name: "s", Transport: mcpx.TransportHTTP, URL: ts.URL}},
	},
		mcpx.WithHTTPRetryMax(0),
		mcpx.WithoutCache(),
		mcpx.WithBeforeCall(func(_ context.Context, _, _ string, _ mcpx.ToolInfo, _ json.RawMessage) (context.Context, *mcpx.CallResult, error) {
			return nil, nil, boom
		}),
		mcpx.WithOnRejectedCall(func(_ context.Context, _, _ string, reason mcpx.RejectReason, _ error) {
			gotReason = reason
		}),
		mcpx.WithAfterCall(func(_ context.Context, _, _ string, _ mcpx.ToolInfo, _ json.RawMessage, _ *mcpx.CallResult, _ error, _ time.Duration) {
			afterFired.Store(true)
		}),
	)
	require.NoError(t, err)
	defer mx.Close()

	_, err = mx.CallTool(t.Context(), "s", "t", nil)
	require.ErrorIs(t, err, boom)
	require.Equal(t, mcpx.RejectBeforeHookAbort, gotReason)
	require.True(t, afterFired.Load(), "AfterCall must fire even on BeforeHookAbort")
}

func TestOnRejectedCall_PanicRecovered(t *testing.T) {
	srv := mcptest.NewServer(echoToolSpec("t"))
	ts := httptest.NewServer(srv.HTTPHandler())
	defer ts.Close()
	defer srv.Close()

	mx, err := mcpx.New(t.Context(), mcpx.MultiplexerConfig{
		Servers: []mcpx.ServerConfig{{Name: "s", Transport: mcpx.TransportHTTP, URL: ts.URL}},
	},
		mcpx.WithHTTPRetryMax(0),
		mcpx.WithoutCache(),
		mcpx.WithOnRejectedCall(func(_ context.Context, _, _ string, _ mcpx.RejectReason, _ error) {
			panic("boom")
		}),
	)
	require.NoError(t, err)
	defer mx.Close()

	// Should not panic — multiplexer recovers from callback panics.
	require.NotPanics(t, func() {
		_, _ = mx.CallTool(t.Context(), "s", "ghost", nil)
	})
}

func TestOnRejectedCall_InvalidArgs(t *testing.T) {
	srv := mcptest.NewServer(echoToolSpec("t"))
	ts := httptest.NewServer(srv.HTTPHandler())
	defer ts.Close()
	defer srv.Close()

	var gotReason mcpx.RejectReason
	var afterFired atomic.Bool
	var afterErr error
	mx, err := mcpx.New(t.Context(), mcpx.MultiplexerConfig{
		Servers: []mcpx.ServerConfig{{Name: "s", Transport: mcpx.TransportHTTP, URL: ts.URL}},
	},
		mcpx.WithHTTPRetryMax(0),
		mcpx.WithoutCache(),
		mcpx.WithOnRejectedCall(func(_ context.Context, _, _ string, reason mcpx.RejectReason, _ error) {
			gotReason = reason
		}),
		mcpx.WithAfterCall(func(_ context.Context, _, _ string, _ mcpx.ToolInfo, _ json.RawMessage, _ *mcpx.CallResult, callErr error, _ time.Duration) {
			afterFired.Store(true)
			afterErr = callErr
		}),
	)
	require.NoError(t, err)
	defer mx.Close()

	_, err = mx.CallTool(t.Context(), "s", "t", json.RawMessage(`{"x":"undefined"}`))
	var ivErr *mcpx.ErrInvalidArgs
	require.ErrorAs(t, err, &ivErr)
	require.Equal(t, mcpx.RejectInvalidArgs, gotReason)
	require.True(t, afterFired.Load(), "AfterCall must fire on RejectInvalidArgs")
	require.ErrorAs(t, afterErr, &ivErr)
}

// === v0.4.0: Cache ===

func buildCacheableSrv(t *testing.T) (url string, calls *atomic.Int64) {
	t.Helper()
	n := new(atomic.Int64)
	tval, fval := true, false
	srv := mcptest.NewServer(mcptest.WithTool(mcptest.ToolSpec{
		Name:        "list",
		ReadOnly:    &tval,
		Idempotent:  &tval,
		Destructive: &fval,
		Handler: func(_ context.Context, _ map[string]any) (string, error) {
			n.Add(1)
			return "result", nil
		},
	}))
	ts := httptest.NewServer(srv.HTTPHandler())
	t.Cleanup(func() { ts.Close(); srv.Close() })
	return ts.URL, n
}

func TestCache_IdempotentTool_CachedAfterFirstCall(t *testing.T) {
	url, calls := buildCacheableSrv(t)
	mx, err := mcpx.New(t.Context(), mcpx.MultiplexerConfig{
		Servers: []mcpx.ServerConfig{{Name: "s", Transport: mcpx.TransportHTTP, URL: url}},
	}, mcpx.WithHTTPRetryMax(0), mcpx.WithCacheTTL(10*time.Second))
	require.NoError(t, err)
	defer mx.Close()

	ctx := mcpx.WithCacheScope(t.Context(), "tenant-1")
	res1, err := mx.CallTool(ctx, "s", "list", nil)
	require.NoError(t, err)
	res2, err := mx.CallTool(ctx, "s", "list", nil)
	require.NoError(t, err)

	require.Equal(t, res1.Text, res2.Text)
	require.Equal(t, int64(1), calls.Load(), "upstream must be called exactly once")
}

func TestCache_DestructiveTool_NeverCached(t *testing.T) {
	var n atomic.Int64
	tval := true
	srv := mcptest.NewServer(mcptest.WithTool(mcptest.ToolSpec{
		Name:        "del",
		Destructive: &tval,
		Handler: func(_ context.Context, _ map[string]any) (string, error) {
			n.Add(1)
			return "deleted", nil
		},
	}))
	ts := httptest.NewServer(srv.HTTPHandler())
	defer ts.Close()
	defer srv.Close()

	mx, err := mcpx.New(t.Context(), mcpx.MultiplexerConfig{
		Servers: []mcpx.ServerConfig{{Name: "s", Transport: mcpx.TransportHTTP, URL: ts.URL}},
	}, mcpx.WithHTTPRetryMax(0))
	require.NoError(t, err)
	defer mx.Close()

	ctx := mcpx.WithCacheScope(t.Context(), "t1")
	_, _ = mx.CallTool(ctx, "s", "del", nil)
	_, _ = mx.CallTool(ctx, "s", "del", nil)
	require.Equal(t, int64(2), n.Load(), "destructive tool must always hit upstream")
}

func TestCache_DifferentScopes_IndependentEntries(t *testing.T) {
	url, calls := buildCacheableSrv(t)
	mx, err := mcpx.New(t.Context(), mcpx.MultiplexerConfig{
		Servers: []mcpx.ServerConfig{{Name: "s", Transport: mcpx.TransportHTTP, URL: url}},
	}, mcpx.WithHTTPRetryMax(0), mcpx.WithCacheTTL(10*time.Second))
	require.NoError(t, err)
	defer mx.Close()

	ctx1 := mcpx.WithCacheScope(t.Context(), "tenant-A")
	ctx2 := mcpx.WithCacheScope(t.Context(), "tenant-B")
	_, _ = mx.CallTool(ctx1, "s", "list", nil)
	_, _ = mx.CallTool(ctx2, "s", "list", nil)
	require.Equal(t, int64(2), calls.Load(), "different scopes must have independent cache entries")
}

func TestCache_TTLExpiry_ReCallsUpstream(t *testing.T) {
	url, calls := buildCacheableSrv(t)
	mx, err := mcpx.New(t.Context(), mcpx.MultiplexerConfig{
		Servers: []mcpx.ServerConfig{{Name: "s", Transport: mcpx.TransportHTTP, URL: url}},
	}, mcpx.WithHTTPRetryMax(0), mcpx.WithCacheTTL(50*time.Millisecond))
	require.NoError(t, err)
	defer mx.Close()

	ctx := mcpx.WithCacheScope(t.Context(), "t1")
	_, _ = mx.CallTool(ctx, "s", "list", nil)
	require.Equal(t, int64(1), calls.Load())

	time.Sleep(100 * time.Millisecond)
	_, _ = mx.CallTool(ctx, "s", "list", nil)
	require.Equal(t, int64(2), calls.Load(), "expired entry must re-call upstream")
}

func TestCache_IsError_NotCached(t *testing.T) {
	var n atomic.Int64
	tval, fval := true, false
	srv := mcptest.NewServer(mcptest.WithTool(mcptest.ToolSpec{
		Name:        "errTool",
		ReadOnly:    &tval,
		Idempotent:  &tval,
		Destructive: &fval,
		Handler: func(_ context.Context, _ map[string]any) (string, error) {
			n.Add(1)
			return "", errors.New("tool error")
		},
	}))
	ts := httptest.NewServer(srv.HTTPHandler())
	defer ts.Close()
	defer srv.Close()

	mx, err := mcpx.New(t.Context(), mcpx.MultiplexerConfig{
		Servers: []mcpx.ServerConfig{{Name: "s", Transport: mcpx.TransportHTTP, URL: ts.URL}},
	}, mcpx.WithHTTPRetryMax(0), mcpx.WithCacheTTL(10*time.Second))
	require.NoError(t, err)
	defer mx.Close()

	ctx := mcpx.WithCacheScope(t.Context(), "t1")
	_, _ = mx.CallTool(ctx, "s", "errTool", nil)
	_, _ = mx.CallTool(ctx, "s", "errTool", nil)
	require.GreaterOrEqual(t, n.Load(), int64(2), "upstream errors must not be cached")
}

func TestCache_WithoutCache_Disabled(t *testing.T) {
	url, calls := buildCacheableSrv(t)
	mx, err := mcpx.New(t.Context(), mcpx.MultiplexerConfig{
		Servers: []mcpx.ServerConfig{{Name: "s", Transport: mcpx.TransportHTTP, URL: url}},
	}, mcpx.WithHTTPRetryMax(0), mcpx.WithoutCache())
	require.NoError(t, err)
	defer mx.Close()

	ctx := mcpx.WithCacheScope(t.Context(), "t1")
	_, _ = mx.CallTool(ctx, "s", "list", nil)
	_, _ = mx.CallTool(ctx, "s", "list", nil)
	require.Equal(t, int64(2), calls.Load(), "WithoutCache must disable caching")
}

func TestCache_IsCacheHit_InAfterCall(t *testing.T) {
	url, _ := buildCacheableSrv(t)
	var hitCount atomic.Int64
	mx, err := mcpx.New(t.Context(), mcpx.MultiplexerConfig{
		Servers: []mcpx.ServerConfig{{Name: "s", Transport: mcpx.TransportHTTP, URL: url}},
	},
		mcpx.WithHTTPRetryMax(0),
		mcpx.WithCacheTTL(10*time.Second),
		mcpx.WithAfterCall(func(ctx context.Context, _, _ string, _ mcpx.ToolInfo, _ json.RawMessage, _ *mcpx.CallResult, _ error, _ time.Duration) {
			if mcpx.IsCacheHit(ctx) {
				hitCount.Add(1)
			}
		}),
	)
	require.NoError(t, err)
	defer mx.Close()

	ctx := mcpx.WithCacheScope(t.Context(), "t1")
	_, _ = mx.CallTool(ctx, "s", "list", nil) // miss
	_, _ = mx.CallTool(ctx, "s", "list", nil) // hit
	require.Equal(t, int64(1), hitCount.Load(), "IsCacheHit must be true only on cache hits")
}

func TestCache_LRUEviction(t *testing.T) {
	var n atomic.Int64
	tval, fval := true, false
	srv := mcptest.NewServer(
		mcptest.WithTool(mcptest.ToolSpec{
			Name: "a", ReadOnly: &tval, Idempotent: &tval, Destructive: &fval,
			Handler: func(_ context.Context, _ map[string]any) (string, error) { n.Add(1); return "a", nil },
		}),
		mcptest.WithTool(mcptest.ToolSpec{
			Name: "b", ReadOnly: &tval, Idempotent: &tval, Destructive: &fval,
			Handler: func(_ context.Context, _ map[string]any) (string, error) { n.Add(1); return "b", nil },
		}),
		mcptest.WithTool(mcptest.ToolSpec{
			Name: "c", ReadOnly: &tval, Idempotent: &tval, Destructive: &fval,
			Handler: func(_ context.Context, _ map[string]any) (string, error) { n.Add(1); return "c", nil },
		}),
	)
	ts := httptest.NewServer(srv.HTTPHandler())
	defer ts.Close()
	defer srv.Close()

	mx, err := mcpx.New(t.Context(), mcpx.MultiplexerConfig{
		Servers: []mcpx.ServerConfig{{Name: "s", Transport: mcpx.TransportHTTP, URL: ts.URL}},
	}, mcpx.WithHTTPRetryMax(0), mcpx.WithCacheSize(2), mcpx.WithCacheTTL(10*time.Second))
	require.NoError(t, err)
	defer mx.Close()

	ctx := mcpx.WithCacheScope(t.Context(), "t1")
	_, _ = mx.CallTool(ctx, "s", "a", nil) // fills slot 1
	_, _ = mx.CallTool(ctx, "s", "b", nil) // fills slot 2; a is LRU
	_, _ = mx.CallTool(ctx, "s", "c", nil) // fills slot 3; a evicted

	nBefore := n.Load()
	_, _ = mx.CallTool(ctx, "s", "b", nil) // b still cached
	_, _ = mx.CallTool(ctx, "s", "c", nil) // c still cached
	require.Equal(t, nBefore, n.Load(), "b and c must still be in cache")

	_, _ = mx.CallTool(ctx, "s", "a", nil) // a was evicted → upstream re-called
	require.Equal(t, nBefore+1, n.Load(), "evicted entry must re-call upstream")
}

func TestCache_DeepCopy_TransformDoesNotCorrupt(t *testing.T) {
	url, calls := buildCacheableSrv(t)
	mx, err := mcpx.New(t.Context(), mcpx.MultiplexerConfig{
		Servers: []mcpx.ServerConfig{{Name: "s", Transport: mcpx.TransportHTTP, URL: url}},
	},
		mcpx.WithHTTPRetryMax(0),
		mcpx.WithCacheTTL(10*time.Second),
		mcpx.WithResultTransform(func(_ context.Context, _, _ string, _ mcpx.ToolInfo, result *mcpx.CallResult) error {
			result.Text = "mutated"
			return nil
		}),
	)
	require.NoError(t, err)
	defer mx.Close()

	ctx := mcpx.WithCacheScope(t.Context(), "t1")
	res1, _ := mx.CallTool(ctx, "s", "list", nil) // first call: mutated by transform
	res2, _ := mx.CallTool(ctx, "s", "list", nil) // second call: from cache, also mutated by transform
	require.Equal(t, int64(1), calls.Load())
	require.Equal(t, "mutated", res1.Text)
	require.Equal(t, "mutated", res2.Text)
}

// === v0.4.0: CallResult.Clone ===

func TestCallResult_Clone_NilReceiver(t *testing.T) {
	var r *mcpx.CallResult
	require.Nil(t, r.Clone())
}

func TestCallResult_Clone_Independence(t *testing.T) {
	orig := &mcpx.CallResult{
		Text:  "hello",
		Parts: []mcpx.ContentPart{{Kind: mcpx.ContentText, Text: "hello"}, {Kind: mcpx.ContentImage, Data: []byte{1, 2, 3}}},
	}
	clone := orig.Clone()
	clone.Text = "changed"
	clone.Parts[0].Text = "changed"
	clone.Parts[1].Data[0] = 99

	require.Equal(t, "hello", orig.Text)
	require.Equal(t, "hello", orig.Parts[0].Text)
	require.Equal(t, byte(1), orig.Parts[1].Data[0], "Data must be independently allocated")
}
