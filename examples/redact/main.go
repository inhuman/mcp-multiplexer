// Package main demonstrates using ResultTransformHook for PII redaction:
// any SSN pattern in tool result text is replaced with [REDACTED].
// Prerequisites: replace the placeholder binary/URL with a real MCP server.
package main

import (
	"context"
	"fmt"
	"os"
	"regexp"

	mcpx "github.com/inhuman/mcp-multiplexer"
)

var ssnPattern = regexp.MustCompile(`SSN:\s*\d{3}-\d{2}-\d{4}`)

func main() {
	redact := mcpx.ResultTransformHook(func(_ context.Context, _, _ string, _ mcpx.ToolInfo, result *mcpx.CallResult) error {
		result.Text = ssnPattern.ReplaceAllString(result.Text, "SSN: [REDACTED]")
		for i, p := range result.Parts {
			if p.Kind == mcpx.ContentText {
				result.Parts[i].Text = ssnPattern.ReplaceAllString(p.Text, "SSN: [REDACTED]")
			}
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
	mx, err := mcpx.New(ctx, cfg, mcpx.WithResultTransform(redact))
	if err != nil {
		fmt.Fprintln(os.Stderr, "connect:", err)
		os.Exit(1)
	}
	defer mx.Close()

	fmt.Println("Multiplexer ready. SSN values will be redacted from results.")
}
