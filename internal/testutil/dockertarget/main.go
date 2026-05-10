// Command dockertarget is a minimal stdio MCP server used for the
// transport_integration_test (real subprocess lifecycle) and for the
// integration_docker test set. It registers a single "echo" tool that
// returns the value of the "msg" argument.
//
// Build the binary with `go build` from the test setup; the binary path is
// then handed to mcpx via ServerConfig.Binary.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	ctx := context.Background()

	srv := mcp.NewServer(&mcp.Implementation{Name: "dockertarget", Version: "v0.0.0"}, nil)
	srv.AddTool(&mcp.Tool{
		Name:        "echo",
		Description: "echoes msg",
		InputSchema: &jsonschema.Schema{Type: "object"},
	}, func(_ context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var args map[string]any
		if req != nil && req.Params != nil && len(req.Params.Arguments) > 0 {
			_ = json.Unmarshal(req.Params.Arguments, &args)
		}
		var msg string
		if v, ok := args["msg"].(string); ok {
			msg = v
		} else {
			msg = "ok"
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: msg}}}, nil
	})

	if err := srv.Run(ctx, &mcp.StdioTransport{}); err != nil {
		fmt.Fprintln(os.Stderr, "dockertarget:", err)
		os.Exit(1)
	}
}
