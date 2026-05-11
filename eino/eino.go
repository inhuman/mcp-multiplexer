package eino

import (
	"context"
	"encoding/json"

	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	jsschema "github.com/eino-contrib/jsonschema"

	mcpx "github.com/inhuman/mcp-multiplexer"
)

// Tools returns one [einotool.InvokableTool] per MCP tool across all connected
// servers. The order matches [mcpx.Multiplexer.AllTools]. Returns an empty
// slice if no servers are connected.
func Tools(mx *mcpx.Multiplexer) []einotool.InvokableTool {
	all := mx.AllTools()
	out := make([]einotool.InvokableTool, 0, len(all))
	for _, ti := range all {
		out = append(out, &mcpxTool{mx: mx, server: ti.Server, info: ti})
	}
	return out
}

// ToolsForServer returns [einotool.InvokableTool] values for the named server
// only. Returns an empty slice (not an error) if the server is not found.
func ToolsForServer(mx *mcpx.Multiplexer, server string) []einotool.InvokableTool {
	infos := mx.ToolsForServers([]string{server})
	out := make([]einotool.InvokableTool, 0, len(infos))
	for _, ti := range infos {
		out = append(out, &mcpxTool{mx: mx, server: server, info: ti})
	}
	return out
}

// mcpxTool implements [einotool.InvokableTool] by wrapping a single MCP tool.
type mcpxTool struct {
	mx     *mcpx.Multiplexer
	server string
	info   mcpx.ToolInfo
}

// Info maps mcpx.ToolInfo fields to *schema.ToolInfo for the eino framework.
func (t *mcpxTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name:        t.info.Name,
		Desc:        t.info.Description,
		ParamsOneOf: inputSchemaToParams(t.info.InputSchema),
	}, nil
}

// InvokableRun calls the underlying MCP tool and returns its text result.
func (t *mcpxTool) InvokableRun(ctx context.Context, argumentsInJSON string, _ ...einotool.Option) (string, error) {
	result, err := t.mx.CallTool(ctx, t.server, t.info.Name, json.RawMessage(argumentsInJSON))
	if err != nil {
		return "", err
	}
	return result.Text, nil
}

// inputSchemaToParams converts raw JSON schema bytes to *schema.ParamsOneOf.
// Returns nil when the schema is empty or cannot be parsed.
func inputSchemaToParams(raw []byte) *schema.ParamsOneOf {
	if len(raw) == 0 {
		return nil
	}
	var s jsschema.Schema
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil
	}
	return schema.NewParamsOneOfByJSONSchema(&s)
}
