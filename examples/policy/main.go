// Package main demonstrates using BeforeCallHook as a policy gate: any tool
// marked Destructive is blocked before the call reaches the upstream server.
// Prerequisites: replace the placeholder binary/URL with a real MCP server.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	mcpx "github.com/inhuman/mcp-multiplexer"
)

func main() {
	blockDestructive := mcpx.BeforeCallHook(func(_ context.Context, _ string, tool mcpx.ToolInfo, _ json.RawMessage) error {
		if tool.Destructive {
			return errors.New("blocked: destructive tool")
		}
		return nil
	})

	cfg := mcpx.MultiplexerConfig{
		Servers: []mcpx.ServerConfig{
			{
				Name:      "myserver",
				Transport: mcpx.TransportHTTP,
				URL:       "http://localhost:3000",
			},
		},
	}

	ctx := context.Background()
	mx, err := mcpx.New(ctx, cfg, mcpx.WithBeforeCall(blockDestructive))
	if err != nil {
		fmt.Fprintln(os.Stderr, "connect:", err)
		os.Exit(1)
	}
	defer mx.Close()

	fmt.Println("Multiplexer ready. Destructive tools are blocked.")
}
