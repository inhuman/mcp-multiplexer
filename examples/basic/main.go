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
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
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
		return fmt.Errorf("connect: %w", err)
	}
	defer mx.Close()

	args, _ := json.Marshal(map[string]any{"path": "/tmp"})
	result, err := mx.CallTool(ctx, "files", "list_directory", args)
	if err != nil {
		return fmt.Errorf("call: %w", err)
	}
	fmt.Println(result.Text)
	return nil
}
