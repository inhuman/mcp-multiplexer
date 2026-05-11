// Package main demonstrates basic mcpx usage: connecting to multiple MCP
// servers and calling a tool. Prerequisites: replace the placeholder URL /
// binary path with a real running server before executing.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	mcpx "github.com/inhuman/mcp-multiplexer"
)

func main() {
	cfg := mcpx.MultiplexerConfig{
		Servers: []mcpx.ServerConfig{
			{
				Name:      "files",
				Transport: mcpx.TransportStdio,
				Binary:    "/usr/bin/mcp-server-filesystem",
				Args:      []string{"/tmp"},
			},
			{
				Name:      "remote",
				Transport: mcpx.TransportHTTP,
				URL:       "http://localhost:3000",
			},
		},
	}

	ctx := context.Background()
	mx, err := mcpx.New(ctx, cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "connect:", err)
		os.Exit(1)
	}
	defer mx.Close()

	args, _ := json.Marshal(map[string]any{"path": "/tmp"})
	result, err := mx.CallTool(ctx, "files", "list_directory", args)
	if err != nil {
		fmt.Fprintln(os.Stderr, "call:", err)
		os.Exit(1)
	}
	fmt.Println(result.Text)
}
