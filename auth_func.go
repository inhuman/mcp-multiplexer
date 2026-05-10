package mcpx

import (
	"context"
	"net/http"
)

// AuthFunc applies authentication to an outgoing HTTP/SSE request before it
// reaches the upstream MCP server.
//
// AuthFunc is invoked once per outbound HTTP request — including each retry
// attempt performed by the underlying retryable HTTP client. Implementations
// whose token derivation is expensive (for example OAuth2 client-credentials
// with refresh) should cache the result internally; the library does not
// memoise across attempts.
//
// The library calls fn on a *cloned* *http.Request so concurrent callers of
// the same connection do not race on Header / Body mutations. Mutate r in
// place; mutating r.Header is the common case but adjusting r.Body or r.URL
// is also allowed.
//
// data is ServerConfig.Auth — the parsed JSON "auth" block, opaque to the
// library. The function defines its own shape; missing or malformed fields
// should yield a descriptive error (the library does not validate it).
//
// Returning a non-nil error aborts the request: the upstream server is NOT
// contacted; the library wraps the error as `mcpx: auth <server>: <err>` and
// propagates it to the caller of CallTool.
//
// AuthFunc applies only to HTTP and SSE transports. Stdio transports ignore
// it because they have no HTTP layer.
//
// Register an AuthFunc with [WithAuthFunc]. See subpackage
// github.com/inhuman/mcp-multiplexer/auth for ready-made implementations
// covering the most common cases (Bearer, custom-header).
type AuthFunc func(ctx context.Context, server string, r *http.Request, data map[string]any) error
