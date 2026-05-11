package mcpx

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// NewFromSessions builds a Multiplexer from already-connected MCP sessions.
// Useful in tests and integration harnesses where sessions are established
// before the multiplexer is constructed.
//
// Each session is queried for its tool list. The constructed Multiplexer's
// Close() will close the supplied sessions.
func NewFromSessions(ctx context.Context, sessions map[string]*mcp.ClientSession, opts ...Option) *Multiplexer {
	o := defaultOptions()
	for _, opt := range opts {
		opt(o)
	}
	ctx, cancel := context.WithCancel(ctx)
	mx := &Multiplexer{
		servers: make(map[string]*serverEntry, len(sessions)),
		cancel:  cancel,
		opts:    o,
	}
	for name, sess := range sessions {
		tools, _ := mx.fetchTools(ctx, name, sess)
		mx.servers[name] = &serverEntry{
			config:  ServerConfig{Name: name},
			session: sess,
			tools:   tools,
			state:   ServerStateConnected,
		}
	}
	return mx
}
