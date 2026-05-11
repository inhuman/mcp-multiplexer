package mcpx

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Multiplexer connects to multiple MCP servers and exposes a unified API for
// listing and invoking tools across them.
type Multiplexer struct {
	mu                 sync.RWMutex
	servers            map[string]*serverEntry
	kindHints          map[string][]string
	cancel             context.CancelFunc
	opts               *options
	builtinCache       Cache
	cacheScopeWarnOnce sync.Once
	cacheTTLWarnMap    sync.Map
}

type serverEntry struct {
	config       ServerConfig
	mu           sync.RWMutex // guards session, tools, state
	session      *mcp.ClientSession
	tools        []ToolInfo
	state        ServerState
	reconnecting atomic.Bool   // true while reconnectServer goroutine is running
	refreshCh    chan struct{} // signals tool-list refresh; capacity 1, immutable after init
}

// New connects to all servers in cfg, caches their tool lists, and returns a
// ready Multiplexer. Errors from individual servers are logged but do not
// prevent the rest from initialising. Inspect ServerNames() to see which
// servers are live.
func New(ctx context.Context, cfg MultiplexerConfig, opts ...Option) (*Multiplexer, error) {
	o := defaultOptions()
	for _, opt := range opts {
		opt(o)
	}

	ctx, cancel := context.WithCancel(ctx)

	mx := &Multiplexer{
		servers:   make(map[string]*serverEntry, len(cfg.Servers)),
		kindHints: cfg.KindHints,
		cancel:    cancel,
		opts:      o,
	}

	// Initialize the built-in LRU unless the user provided a custom cache or
	// disabled caching entirely.
	if !o.cacheDisabled && o.cache == nil {
		mx.builtinCache = newLRUCache(o.cacheSize)
	}

	if o.healthCheckSet && o.healthCheckInterval <= 0 {
		cancel()
		return nil, fmt.Errorf("mcpx: WithHealthCheck interval must be positive")
	}

	seen := make(map[string]bool, len(cfg.Servers))
	for _, sc := range cfg.Servers {
		if err := sc.validate(); err != nil {
			cancel()
			return nil, err
		}
		if seen[sc.Name] {
			cancel()
			return nil, fmt.Errorf("mcpx: duplicate server name: %s", sc.Name)
		}
		seen[sc.Name] = true
	}

	if o.authFunc == nil {
		for _, sc := range cfg.Servers {
			if sc.Auth != nil {
				cancel()
				return nil, fmt.Errorf(
					"mcpx: server %q has auth config but no AuthFunc registered "+
						"(use mcpx.WithAuthFunc, e.g. mcpx.WithAuthFunc(auth.Bearer))",
					sc.Name)
			}
		}
	}

	var wg sync.WaitGroup
	var mu sync.Mutex

	for _, sc := range cfg.Servers {
		if ks, ok := cfg.KindSettings[sc.Kind]; ok {
			sc = sc.withKindDefaults(ks)
		}
		wg.Go(func() {
			entry, err := mx.connect(ctx, sc, nil)
			if err != nil {
				o.logger.Error("mcpx: failed to connect", F("server", sc.Name), F("error", err.Error()))
				return
			}
			mu.Lock()
			mx.servers[sc.Name] = entry
			mu.Unlock()
			if o.onConnect != nil {
				func() {
					defer func() { recover() }() //nolint:errcheck
					o.onConnect(sc.Name, entry.tools)
				}()
			}
		})
	}
	wg.Wait()

	for name, entry := range mx.servers {
		go mx.runToolRefresh(ctx, name, entry)
	}

	if o.healthCheckInterval > 0 {
		go mx.runSupervisor(ctx)
	}

	return mx, nil
}

// KindGroup groups servers of the same kind for prompt generation or routing.
type KindGroup struct {
	Kind    string
	Servers []string
	Tools   []string
	Hints   []string
}

// ConfigHints returns the kind_hints map from MultiplexerConfig (may be nil).
func (mx *Multiplexer) ConfigHints() map[string][]string { return mx.kindHints }

// ServerNames returns the sorted list of registered MCP server names.
func (mx *Multiplexer) ServerNames() []string {
	mx.mu.RLock()
	defer mx.mu.RUnlock()
	names := make([]string, 0, len(mx.servers))
	for n := range mx.servers {
		names = append(names, n)
	}
	slices.Sort(names)
	return names
}

// KindsForServers returns unique kinds for the given server names, preserving
// input order. If a server has no Kind set, its name is used as the kind.
func (mx *Multiplexer) KindsForServers(names []string) []string {
	mx.mu.RLock()
	defer mx.mu.RUnlock()
	seen := make(map[string]struct{}, len(names))
	var kinds []string
	for _, name := range names {
		entry, ok := mx.servers[name]
		if !ok {
			continue
		}
		kind := entry.config.Kind
		if kind == "" {
			kind = name
		}
		if _, dup := seen[kind]; !dup {
			seen[kind] = struct{}{}
			kinds = append(kinds, kind)
		}
	}
	return kinds
}

// KindGroups returns servers grouped by Kind (or by name if Kind is empty),
// with deduplicated tool names per group. Groups are sorted by Kind.
func (mx *Multiplexer) KindGroups() []KindGroup {
	mx.mu.RLock()
	defer mx.mu.RUnlock()

	toolSets := make(map[string]map[string]struct{})
	serversByKind := make(map[string][]string)

	for name, entry := range mx.servers {
		kind := entry.config.Kind
		if kind == "" {
			kind = name
		}
		serversByKind[kind] = append(serversByKind[kind], name)
		if toolSets[kind] == nil {
			toolSets[kind] = make(map[string]struct{})
		}
		for _, t := range entry.tools {
			toolSets[kind][t.Name] = struct{}{}
		}
	}

	kinds := slices.Sorted(maps.Keys(serversByKind))
	groups := make([]KindGroup, 0, len(kinds))
	for _, kind := range kinds {
		servers := serversByKind[kind]
		slices.Sort(servers)
		tools := slices.Sorted(maps.Keys(toolSets[kind]))
		groups = append(groups, KindGroup{
			Kind:    kind,
			Servers: servers,
			Tools:   tools,
			Hints:   mx.kindHints[kind],
		})
	}
	return groups
}

// ToolsForServers returns ToolInfo for tools across the given server names,
// in input order with stable per-server ordering. Each (server, tool) pair
// appears once.
func (mx *Multiplexer) ToolsForServers(names []string) []ToolInfo {
	mx.mu.RLock()
	defer mx.mu.RUnlock()
	seen := make(map[string]struct{})
	var result []ToolInfo
	for _, name := range names {
		entry, ok := mx.servers[name]
		if !ok {
			continue
		}
		entry.mu.RLock()
		tools := entry.tools
		entry.mu.RUnlock()
		for _, t := range tools {
			key := name + "/" + t.Name
			if _, dup := seen[key]; !dup {
				seen[key] = struct{}{}
				result = append(result, t)
			}
		}
	}
	return result
}

// AllTools returns every (server, tool) pair across all connected servers.
// Order is non-deterministic.
func (mx *Multiplexer) AllTools() []ToolInfo {
	mx.mu.RLock()
	defer mx.mu.RUnlock()
	var result []ToolInfo
	for _, entry := range mx.servers {
		entry.mu.RLock()
		result = append(result, entry.tools...)
		entry.mu.RUnlock()
	}
	return result
}

// ServerStatus returns a snapshot of the liveness state of every registered
// server. When health-check is disabled (WithHealthCheck not called), all
// values are [ServerStateConnected].
func (mx *Multiplexer) ServerStatus() map[string]ServerState {
	mx.mu.RLock()
	defer mx.mu.RUnlock()
	result := make(map[string]ServerState, len(mx.servers))
	for name, entry := range mx.servers {
		entry.mu.RLock()
		result[name] = entry.state
		entry.mu.RUnlock()
	}
	return result
}

// Close shuts down all MCP sessions and stops underlying subprocesses.
func (mx *Multiplexer) Close() {
	mx.cancel()
	mx.mu.Lock()
	defer mx.mu.Unlock()
	for name, entry := range mx.servers {
		entry.mu.RLock()
		sess := entry.session
		entry.mu.RUnlock()
		if sess == nil {
			continue
		}
		if err := sess.Close(); err != nil {
			mx.opts.logger.Error("mcpx: close session", F("server", name), F("error", err.Error()))
		}
	}
}

// connect establishes a new MCP session for cfg and returns a populated serverEntry.
// refreshCh is the channel the ToolListChangedHandler will signal; pass nil to allocate
// a new one. Pass the persistent entry's refreshCh on reconnect to reuse the same
// channel and drain goroutine.
func (mx *Multiplexer) connect(ctx context.Context, cfg ServerConfig, refreshCh chan struct{}) (*serverEntry, error) {
	if refreshCh == nil {
		refreshCh = make(chan struct{}, 1)
	}

	transport, err := newTransport(ctx, cfg, mx.opts)
	if err != nil {
		return nil, err
	}

	clientOpts := &mcp.ClientOptions{
		ToolListChangedHandler: func(_ context.Context, _ *mcp.ToolListChangedRequest) {
			select {
			case refreshCh <- struct{}{}:
			default: // already queued; coalesce
			}
		},
	}

	client := mcp.NewClient(&mcp.Implementation{
		Name:    mx.opts.clientName,
		Version: mx.opts.clientVersion,
	}, clientOpts)

	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return nil, fmt.Errorf("connect %s: %w", cfg.Name, err)
	}

	tools, err := mx.fetchTools(ctx, cfg.Name, session)
	if err != nil {
		_ = session.Close()
		return nil, fmt.Errorf("list tools %s: %w", cfg.Name, err)
	}

	return &serverEntry{config: cfg, session: session, tools: tools, state: ServerStateConnected, refreshCh: refreshCh}, nil
}

func (mx *Multiplexer) fetchTools(ctx context.Context, serverName string, session *mcp.ClientSession) ([]ToolInfo, error) {
	result, err := session.ListTools(ctx, nil)
	if err != nil {
		return nil, err
	}
	infos := make([]ToolInfo, 0, len(result.Tools))
	for _, t := range result.Tools {
		info := ToolInfo{
			Server:      serverName,
			Name:        t.Name,
			Description: t.Description,
		}
		if t.InputSchema != nil {
			if raw, err := json.Marshal(t.InputSchema); err == nil {
				info.InputSchema = raw
			}
		}
		if a := t.Annotations; a != nil {
			info.ReadOnly = a.ReadOnlyHint
			// DestructiveHint defaults to true for non-read-only tools per MCP spec.
			info.Destructive = a.DestructiveHint == nil || *a.DestructiveHint
			info.Idempotent = a.IdempotentHint
			if a.OpenWorldHint != nil {
				info.OpenWorld = *a.OpenWorldHint
			}
		} else {
			// Conservative default: assume non-readonly + destructive when
			// the server doesn't advertise annotations.
			info.Destructive = true
		}
		// Derived flag: a tool that mutates state without being destructive.
		info.Write = !info.ReadOnly && !info.Destructive

		for _, enricher := range mx.opts.metaEnrichers {
			info = enricher(ctx, serverName, info)
		}
		infos = append(infos, info)
	}
	safeRecordToolList(mx.opts.metrics, serverName, len(infos))
	return infos, nil
}
