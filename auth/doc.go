// Package auth provides ready-made [mcpx.AuthFunc] implementations for the
// two most common authentication shapes carried in [mcpx.ServerConfig.Auth].
//
//   - [Bearer] — for {"auth": {"token": "..."}}; sets
//     Authorization: Bearer <token>.
//   - [HeaderToken] — for {"auth": {"tokenName": "X-MCP-AUTH", "value": "..."}};
//     sets the named header verbatim, no Bearer prefix.
//
// Pass the helper directly to [mcpx.WithAuthFunc]:
//
//	import (
//	    mcpx "github.com/inhuman/mcp-multiplexer"
//	    "github.com/inhuman/mcp-multiplexer/auth"
//	)
//
//	mx, err := mcpx.New(ctx, cfg, mcpx.WithAuthFunc(auth.Bearer))
//
// For multi-scheme dispatch (mixing schemes per server) write your own
// [mcpx.AuthFunc] that switches on data["scheme"] and delegates to one of
// the helpers.
//
// The package depends only on the standard library and the parent module —
// importing it does not pull additional third-party dependencies.
package auth
