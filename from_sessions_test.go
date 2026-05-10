package mcpx_test

import (
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	mcpx "github.com/inhuman/mcp-multiplexer"
	"github.com/inhuman/mcp-multiplexer/internal/testutil/mcptest"
)

func TestNewFromSessions(t *testing.T) {
	ctx := t.Context()

	a := mcptest.NewServer(echoTool("ta"))
	defer a.Close()
	csA, err := a.ConnectClient(ctx)
	require.NoError(t, err)

	b := mcptest.NewServer(echoTool("tb"))
	defer b.Close()
	csB, err := b.ConnectClient(ctx)
	require.NoError(t, err)

	mx := mcpx.NewFromSessions(ctx, map[string]*mcp.ClientSession{
		"a": csA,
		"b": csB,
	})
	defer mx.Close()

	require.ElementsMatch(t, []string{"a", "b"}, mx.ServerNames())
	tools := mx.AllTools()
	require.Len(t, tools, 2)

	res, err := mx.CallTool(ctx, "a", "ta", []byte(`{"msg":"hi"}`))
	require.NoError(t, err)
	require.Equal(t, "hi", res.Text)
}

func TestNewFromSessions_CloseClosesSessions(t *testing.T) {
	ctx := t.Context()
	s := mcptest.NewServer(echoTool("e"))
	defer s.Close()
	cs, err := s.ConnectClient(ctx)
	require.NoError(t, err)

	mx := mcpx.NewFromSessions(ctx, map[string]*mcp.ClientSession{"x": cs})
	mx.Close()
	// After Close, session should be closed; a follow-up call should fail.
	_, err = cs.ListTools(ctx, nil)
	require.Error(t, err)
}
