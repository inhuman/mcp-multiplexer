package mcpx

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// FilterByNames returns a view (View) of the multiplexer that exposes only the
// requested servers. The view shares all sessions and configuration with the
// parent — no new connections are opened. Closing the view is a no-op; only
// the parent Multiplexer's Close() shuts down sessions.
//
// Returns an error if any requested server name is unknown.
func (mx *Multiplexer) FilterByNames(names []string) (*View, error) {
	mx.mu.RLock()
	available := make([]string, 0, len(mx.servers))
	for name := range mx.servers {
		available = append(available, name)
	}
	slices.Sort(available)

	var missing []string
	servers := make(map[string]*serverEntry, len(names))
	for _, name := range names {
		entry, ok := mx.servers[name]
		if !ok {
			missing = append(missing, name)
			continue
		}
		servers[name] = entry
	}
	mx.mu.RUnlock()

	if len(missing) > 0 {
		return nil, fmt.Errorf("mcpx: unknown server(s) %s; available: %s",
			strings.Join(missing, ", "),
			strings.Join(available, ", "))
	}

	return &View{parent: mx, names: names, servers: servers}, nil
}

// View is a subset of a Multiplexer scoped to a fixed list of server names.
// Methods mirror the parent's read API so a view can be passed to code that
// expects a Multiplexer-like surface for a restricted server set.
type View struct {
	parent  *Multiplexer
	names   []string
	servers map[string]*serverEntry
}

// ServerNames returns the names visible through this view, in input order.
func (v *View) ServerNames() []string { return slices.Clone(v.names) }

// Tools returns ToolInfo for all tools across the view's servers.
func (v *View) Tools() []ToolInfo {
	return v.parent.ToolsForServers(v.names)
}

// CallTool delegates to the parent multiplexer after verifying the server is
// part of this view. Returns ErrServerNotFound if the server is hidden.
func (v *View) CallTool(ctx context.Context, server, tool string, argsJSON json.RawMessage) (*CallResult, error) {
	if _, ok := v.servers[server]; !ok {
		return nil, fmt.Errorf("%w: %q is not in this view", ErrServerNotFound, server)
	}
	return v.parent.CallTool(ctx, server, tool, argsJSON)
}
