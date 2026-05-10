package mcpx_test

import (
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	mcpx "github.com/inhuman/mcp-multiplexer"
	"github.com/inhuman/mcp-multiplexer/internal/testutil/mcptest"
)

func threeServerMux(t *testing.T) (*mcpx.Multiplexer, func()) {
	t.Helper()
	a := mcptest.NewServer(echoTool("ta"))
	b := mcptest.NewServer(echoTool("tb"))
	c := mcptest.NewServer(echoTool("tc"))
	tsA := httptest.NewServer(a.HTTPHandler())
	tsB := httptest.NewServer(b.HTTPHandler())
	tsC := httptest.NewServer(c.HTTPHandler())
	mx, err := mcpx.New(t.Context(), mcpx.MultiplexerConfig{
		Servers: []mcpx.ServerConfig{
			{Name: "a", Transport: mcpx.TransportHTTP, URL: tsA.URL},
			{Name: "b", Transport: mcpx.TransportHTTP, URL: tsB.URL},
			{Name: "c", Transport: mcpx.TransportHTTP, URL: tsC.URL},
		},
	}, mcpx.WithHTTPRetryMax(0))
	require.NoError(t, err)
	cleanup := func() {
		mx.Close()
		tsA.Close()
		tsB.Close()
		tsC.Close()
		a.Close()
		b.Close()
		c.Close()
	}
	return mx, cleanup
}

func TestFilterByNames_Subset(t *testing.T) {
	mx, cleanup := threeServerMux(t)
	defer cleanup()

	view, err := mx.FilterByNames([]string{"a", "b"})
	require.NoError(t, err)
	require.Equal(t, []string{"a", "b"}, view.ServerNames())

	tools := view.Tools()
	names := make([]string, 0, len(tools))
	for _, ti := range tools {
		names = append(names, ti.Name)
	}
	require.ElementsMatch(t, []string{"ta", "tb"}, names)
}

func TestFilterByNames_UnknownName(t *testing.T) {
	mx, cleanup := threeServerMux(t)
	defer cleanup()

	_, err := mx.FilterByNames([]string{"a", "missing"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "missing")
	require.Contains(t, err.Error(), "available")
}

func TestView_CallToolOutsideViewRejected(t *testing.T) {
	mx, cleanup := threeServerMux(t)
	defer cleanup()

	view, err := mx.FilterByNames([]string{"a"})
	require.NoError(t, err)

	// Server "b" exists in parent but not in view.
	_, err = view.CallTool(t.Context(), "b", "tb", nil)
	require.ErrorIs(t, err, mcpx.ErrServerNotFound)
	require.Contains(t, err.Error(), "view")
}

func TestView_CallToolInViewWorks(t *testing.T) {
	mx, cleanup := threeServerMux(t)
	defer cleanup()

	view, err := mx.FilterByNames([]string{"a"})
	require.NoError(t, err)
	res, err := view.CallTool(t.Context(), "a", "ta", nil)
	require.NoError(t, err)
	require.NotNil(t, res)
}

func TestView_DoesNotAffectParent(t *testing.T) {
	mx, cleanup := threeServerMux(t)
	defer cleanup()

	_, err := mx.FilterByNames([]string{"a"})
	require.NoError(t, err)
	// Parent still sees all 3 servers; the view does not own a Close, so
	// nothing should have been torn down.
	require.Len(t, mx.ServerNames(), 3)

	// Parent still answers calls on b.
	res, err := mx.CallTool(t.Context(), "b", "tb", nil)
	require.NoError(t, err)
	require.NotNil(t, res)
}
