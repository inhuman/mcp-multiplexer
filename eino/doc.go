// Package eino adapts every MCP tool from a [mcpx.Multiplexer] into an
// eino-native tool.InvokableTool value. Call [Tools] to obtain a slice ready
// for an eino agent's ToolsNode, or [ToolsForServer] for a single server's
// tools.
//
// This package lives in its own Go module
// (github.com/inhuman/mcp-multiplexer/eino) so that importing the root
// module does not pull in cloudwego/eino transitively.
package eino
