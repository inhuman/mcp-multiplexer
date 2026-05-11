package eino

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	mcpx "github.com/inhuman/mcp-multiplexer"
)

// buildMX creates a Multiplexer from an in-process MCP server with the given tools.
func buildMX(t *testing.T, serverName string, specs []toolSpec) *mcpx.Multiplexer {
	t.Helper()
	srv := mcp.NewServer(&mcp.Implementation{Name: serverName, Version: "v0"}, nil)
	for _, s := range specs {
		tool := &mcp.Tool{
			Name:        s.name,
			Description: s.description,
			InputSchema: &jsonschema.Schema{Type: "object"},
		}
		srv.AddTool(tool, func(_ context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: "ok"}},
			}, nil
		})
	}

	serverT, clientT := mcp.NewInMemoryTransports()
	ctx := context.Background()
	if _, err := srv.Connect(ctx, serverT, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "v0"}, nil)
	cs, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })

	mx := mcpx.NewFromSessions(ctx, map[string]*mcp.ClientSession{serverName: cs})
	t.Cleanup(func() { mx.Close() })
	return mx
}

type toolSpec struct {
	name        string
	description string
}

func TestTools_CountMatchesAllServers(t *testing.T) {
	specs := []toolSpec{
		{name: "tool1", description: "first"},
		{name: "tool2", description: "second"},
		{name: "tool3", description: "third"},
	}
	mx := buildMX(t, "srv", specs)
	got := Tools(mx)
	if len(got) != 3 {
		t.Fatalf("expected 3 tools, got %d", len(got))
	}
}

func TestToolsForServer_FiltersCorrectly(t *testing.T) {
	specs := []toolSpec{
		{name: "a"}, {name: "b"},
	}
	mx := buildMX(t, "myserver", specs)
	got := ToolsForServer(mx, "myserver")
	if len(got) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(got))
	}
}

func TestToolsForServer_NotFound(t *testing.T) {
	mx := buildMX(t, "srv", []toolSpec{{name: "x"}})
	got := ToolsForServer(mx, "nonexistent")
	if len(got) != 0 {
		t.Fatalf("expected empty slice, got %d", len(got))
	}
}

func TestMcpxTool_Info(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`)
	tool := &mcpxTool{
		info: mcpx.ToolInfo{
			Name:        "list_dir",
			Description: "lists a directory",
			InputSchema: schema,
		},
	}
	info, err := tool.Info(context.Background())
	if err != nil {
		t.Fatalf("Info error: %v", err)
	}
	if info.Name != "list_dir" {
		t.Errorf("Name: got %q", info.Name)
	}
	if info.Desc != "lists a directory" {
		t.Errorf("Desc: got %q", info.Desc)
	}
	if info.ParamsOneOf == nil {
		t.Error("ParamsOneOf should not be nil for non-empty schema")
	}
}

func TestMcpxTool_InvokableRun(t *testing.T) {
	mx := buildMX(t, "srv", []toolSpec{{name: "echo"}})
	tools := Tools(mx)
	if len(tools) == 0 {
		t.Fatal("expected at least one tool")
	}
	result, err := tools[0].InvokableRun(context.Background(), `{}`)
	if err != nil {
		t.Fatalf("InvokableRun error: %v", err)
	}
	if result != "ok" {
		t.Errorf("expected result 'ok', got %q", result)
	}
}
