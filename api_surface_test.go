package mcpx_test

// This file pins references to every exported public symbol of mcpx so the
// API-coverage check in .github/scripts/check_api_coverage.sh stays honest.
// Each symbol is referenced in its most natural form for tests; behaviour
// of these symbols is exercised in the dedicated *_test.go files.

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	mcpx "github.com/inhuman/mcp-multiplexer"
	"github.com/inhuman/mcp-multiplexer/auth"
)

// cacheStub satisfies the Cache interface for API surface pinning.
type cacheStub struct{}

func (*cacheStub) Get(_ context.Context, _ string) (*mcpx.CallResult, bool)             { return nil, false }
func (*cacheStub) Set(_ context.Context, _ string, _ *mcpx.CallResult, _ time.Duration) {}

// Anchor references for symbol coverage; values are not invoked.
var (
	_ mcpx.ArgsTransformer = mcpx.ArgsTransformer("custom")
	_ mcpx.TransportType   = mcpx.TransportHTTP
	_ mcpx.BeforeCallHook  = func(_ context.Context, _, _ string, _ mcpx.ToolInfo, _ json.RawMessage) (context.Context, *mcpx.CallResult, error) {
		return nil, nil, nil
	}
	_ mcpx.AfterCallHook = func(_ context.Context, _, _ string, _ mcpx.ToolInfo, _ json.RawMessage, _ *mcpx.CallResult, _ error, _ time.Duration) {
	}
	_ mcpx.ResultTransformHook = func(_ context.Context, _, _ string, _ mcpx.ToolInfo, _ *mcpx.CallResult) error { return nil }
	_ mcpx.MetaEnricher        = func(_ context.Context, _ string, info mcpx.ToolInfo) mcpx.ToolInfo { return info }
	_ mcpx.CustomTransformer   = func(args map[string]any) map[string]any { return args }
	_ mcpx.AuthFunc            = auth.Bearer
	_ mcpx.AuthFunc            = auth.HeaderToken
	_ mcpx.OnReconnectFunc     = func(_ string, _ error) {}
	_ mcpx.ServerState         = mcpx.ServerStateConnected
	_ mcpx.OnToolsChangedFunc  = func(_ string, _, _ []mcpx.ToolInfo) {}
	_ mcpx.RejectReason        = mcpx.RejectUnknownServer
	_ mcpx.OnRejectedCallFunc  = func(_ context.Context, _, _ string, _ mcpx.RejectReason, _ error) {}
	_ mcpx.OnConnectFunc       = func(_ string, _ []mcpx.ToolInfo) {}
	_ mcpx.Cache               = (*cacheStub)(nil)
	_ mcpx.KeyFunc             = func(_ context.Context, _, _ string, _ json.RawMessage) string { return "" }
)

func TestAPISurface_NopLoggerAndField(t *testing.T) {
	log := mcpx.NopLogger()
	require.NotNil(t, log)
	log.Info("ok", mcpx.Field{Key: "k", Value: "v"})
}

func TestAPISurface_ConfigHintsAndKindGroup(t *testing.T) {
	mx, cleanup := single(t, "s", echoTool("e"))
	defer cleanup()
	require.Nil(t, mx.ConfigHints()) // no hints supplied in single()

	groups := mx.KindGroups()
	require.Len(t, groups, 1)
	g := groups[0]
	require.IsType(t, mcpx.KindGroup{}, g)
}

func TestAPISurface_ContentTypes(t *testing.T) {
	cp := mcpx.ContentPart{Kind: mcpx.ContentText, Text: "x"}
	require.Equal(t, mcpx.ContentKind("text"), cp.Kind)
}

func TestAPISurface_ViewType(t *testing.T) {
	mx, cleanup := single(t, "s", echoTool("e"))
	defer cleanup()
	v, err := mx.FilterByNames([]string{"s"})
	require.NoError(t, err)
	require.IsType(t, &mcpx.View{}, v)
}

func TestAPISurface_WithArgsTransformerAndClientIdentity(t *testing.T) {
	custom := mcpx.WithArgsTransformer("my", func(args map[string]any) map[string]any {
		args["marked"] = true
		return args
	})
	identity := mcpx.WithClientIdentity("test-client", "0.0.1")
	require.NotNil(t, custom)
	require.NotNil(t, identity)
}

func TestAPISurface_WithAuthFunc(t *testing.T) {
	opt := mcpx.WithAuthFunc(auth.Bearer)
	require.NotNil(t, opt)
}

func TestAPISurface_WithOnToolsChanged(t *testing.T) {
	opt := mcpx.WithOnToolsChanged(func(_ string, _, _ []mcpx.ToolInfo) {})
	require.NotNil(t, opt)
}

func TestAPISurface_WithSchemaValidation(t *testing.T) {
	opt := mcpx.WithSchemaValidation()
	require.NotNil(t, opt)
}
