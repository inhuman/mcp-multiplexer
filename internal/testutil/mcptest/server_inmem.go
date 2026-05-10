package mcptest

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ConnectClient wires the test server to a fresh client over an in-memory
// transport pair and returns the connected client session. The session is
// suitable for direct use with mcpx.NewFromSessions.
//
// Both the client and the server are torn down when (*Server).Close is
// invoked.
func (s *Server) ConnectClient(ctx context.Context) (*mcp.ClientSession, error) {
	s.validateOrPanic()
	srv := s.build()

	serverT, clientT := mcp.NewInMemoryTransports()
	if _, err := srv.Connect(ctx, serverT, nil); err != nil {
		return nil, fmt.Errorf("mcptest: server connect: %w", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "mcptest-client", Version: "v0.0.0"}, nil)
	cs, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		return nil, fmt.Errorf("mcptest: client connect: %w", err)
	}
	s.trackClose(func() {
		_ = cs.Close()
	})
	return cs, nil
}

// MustConnectClient is ConnectClient that panics on error; convenient inside
// table-driven tests.
func (s *Server) MustConnectClient(ctx context.Context) *mcp.ClientSession {
	cs, err := s.ConnectClient(ctx)
	if err != nil {
		panic(err)
	}
	return cs
}
