// Package policy provides ready-made BeforeCallHook and AfterCallHook builders
// for common call-control patterns. Import alongside
// github.com/inhuman/mcp-multiplexer; no additional dependencies are required.
//
// Available builders:
//
//   - [DenyDestructive] — rejects any tool marked as destructive before the RPC.
//   - [RequireRoles] — enforces role-based access by reading roles from the context.
//   - [RateLimit] — per-(server, tool) token-bucket rate limiting.
//   - [AuditLog] — logs every call outcome via an injected [mcpx.Logger].
//
// Hooks compose naturally with [mcpx.WithBeforeCall] and [mcpx.WithAfterCall]
// and chain in registration order.
package policy
