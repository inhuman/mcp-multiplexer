// Package mcptest provides an in-process MCP server factory for use from
// tests inside the mcp-multiplexer module. The server is built on the same
// github.com/modelcontextprotocol/go-sdk that the library targets in
// production, so tests exercise the real protocol path.
//
// Three transport adapters are exposed on the same Server:
//   - ConnectClient — returns a *mcp.ClientSession over an in-memory transport,
//     suitable for mcpx.NewFromSessions.
//   - HTTPHandler   — http.Handler for use with httptest.NewServer (Streamable HTTP).
//   - SSEHandler    — http.Handler for use with httptest.NewServer (SSE).
package mcptest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ToolHandler is the user-supplied implementation of one tool registered on a
// test server.
type ToolHandler func(ctx context.Context, args map[string]any) (string, error)

// ToolSpec describes a tool the test server should expose.
//
// Annotations are optional pointers; nil means the server does not advertise
// the corresponding hint, matching the way real MCP servers behave when they
// omit annotations.
type ToolSpec struct {
	Name        string
	Description string
	Handler     ToolHandler

	ReadOnly    *bool
	Destructive *bool
	Idempotent  *bool
	OpenWorld   *bool

	// InputSchema overrides the default empty-object schema. Most tests can
	// leave this nil.
	InputSchema *jsonschema.Schema
}

// Option configures a Server.
type Option func(*Server)

// WithTool registers a tool. May be passed multiple times.
func WithTool(spec ToolSpec) Option {
	return func(s *Server) {
		s.specs = append(s.specs, spec)
	}
}

// WithToolDelay makes the named tool sleep for d before responding. Useful
// for timeout tests.
func WithToolDelay(name string, d time.Duration) Option {
	return func(s *Server) {
		s.delays[name] = d
	}
}

// WithToolError makes the named tool always return err. Useful for error-
// path tests.
func WithToolError(name string, err error) Option {
	return func(s *Server) {
		s.errors[name] = err
	}
}

// Server is a configurable in-process MCP server.
type Server struct {
	specs  []ToolSpec
	delays map[string]time.Duration
	errors map[string]error

	mu        sync.Mutex
	mcpServer *mcp.Server
	closeFns  []func()
}

// NewServer constructs a Server with the given options. The underlying
// *mcp.Server is created lazily on first use of a transport adapter so
// configuration is immutable after that point.
func NewServer(opts ...Option) *Server {
	s := &Server{
		delays: map[string]time.Duration{},
		errors: map[string]error{},
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// build returns the underlying *mcp.Server, building it on first call.
func (s *Server) build() *mcp.Server {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.mcpServer != nil {
		return s.mcpServer
	}
	srv := mcp.NewServer(&mcp.Implementation{Name: "mcptest", Version: "v0.0.0"}, nil)
	for _, spec := range s.specs {
		s.addTool(srv, spec)
	}
	s.mcpServer = srv
	return srv
}

func (s *Server) addTool(srv *mcp.Server, spec ToolSpec) {
	schema := spec.InputSchema
	if schema == nil {
		schema = &jsonschema.Schema{Type: "object"}
	}
	tool := &mcp.Tool{
		Name:        spec.Name,
		Description: spec.Description,
		InputSchema: schema,
	}
	if spec.ReadOnly != nil || spec.Destructive != nil || spec.Idempotent != nil || spec.OpenWorld != nil {
		ann := &mcp.ToolAnnotations{}
		if spec.ReadOnly != nil {
			ann.ReadOnlyHint = *spec.ReadOnly
		}
		ann.DestructiveHint = spec.Destructive
		if spec.Idempotent != nil {
			ann.IdempotentHint = *spec.Idempotent
		}
		ann.OpenWorldHint = spec.OpenWorld
		tool.Annotations = ann
	}
	delay := s.delays[spec.Name]
	forcedErr := s.errors[spec.Name]
	handler := spec.Handler

	srv.AddTool(tool, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if delay > 0 {
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		if forcedErr != nil {
			return nil, forcedErr
		}
		var args map[string]any
		if req != nil && req.Params != nil && len(req.Params.Arguments) > 0 {
			_ = json.Unmarshal(req.Params.Arguments, &args)
		}
		var text string
		var err error
		if handler != nil {
			text, err = handler(ctx, args)
		}
		if err != nil {
			return nil, err
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: text}},
		}, nil
	})
}

// AddLiveTool dynamically adds a tool to a running server and sends a
// notifications/tools/list_changed notification to all connected clients.
// It is safe to call after a transport adapter has been started.
func (s *Server) AddLiveTool(spec ToolSpec) {
	s.addTool(s.build(), spec)
}

// RemoveLiveTool removes the named tool from a running server and sends a
// notifications/tools/list_changed notification to all connected clients.
func (s *Server) RemoveLiveTool(name string) {
	srv := s.build()
	srv.RemoveTools(name)
}

// Close stops every transport that was started for this server.
func (s *Server) Close() {
	s.mu.Lock()
	fns := s.closeFns
	s.closeFns = nil
	s.mu.Unlock()
	for _, fn := range fns {
		fn()
	}
}

// ErrToolNotConfigured indicates a delay/error was attached to an unknown tool.
var ErrToolNotConfigured = errors.New("mcptest: tool not configured")

// ensureKnown verifies that delays/errors reference a registered tool name.
// Called from helpers; surfaces typos in tests early.
func (s *Server) ensureKnown() error {
	known := map[string]struct{}{}
	for _, sp := range s.specs {
		known[sp.Name] = struct{}{}
	}
	for n := range s.delays {
		if _, ok := known[n]; !ok {
			return fmt.Errorf("%w: delay attached to %q", ErrToolNotConfigured, n)
		}
	}
	for n := range s.errors {
		if _, ok := known[n]; !ok {
			return fmt.Errorf("%w: error attached to %q", ErrToolNotConfigured, n)
		}
	}
	return nil
}

// trackClose registers a cleanup function to be invoked from Close.
func (s *Server) trackClose(fn func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closeFns = append(s.closeFns, fn)
}

// validateOrPanic is called by transport adapters; misconfiguration in tests
// should fail loudly rather than silently silently misbehave.
func (s *Server) validateOrPanic() {
	if err := s.ensureKnown(); err != nil {
		panic(err)
	}
}
