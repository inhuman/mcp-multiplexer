package mcptest

import (
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// HTTPHandler returns an http.Handler that exposes the test server over the
// MCP Streamable HTTP transport. Wrap it in httptest.NewServer to obtain a
// URL you can plug into mcpx.ServerConfig{Transport: TransportHTTP, URL: …}.
func (s *Server) HTTPHandler() http.Handler {
	s.validateOrPanic()
	srv := s.build()
	return mcp.NewStreamableHTTPHandler(func(_ *http.Request) *mcp.Server { return srv }, nil)
}

// SSEHandler returns an http.Handler that exposes the test server over the
// SSE transport. Wrap with httptest.NewServer for use with
// mcpx.ServerConfig{Transport: TransportSSE, URL: …}.
func (s *Server) SSEHandler() http.Handler {
	s.validateOrPanic()
	srv := s.build()
	return mcp.NewSSEHandler(func(_ *http.Request) *mcp.Server { return srv }, nil)
}
