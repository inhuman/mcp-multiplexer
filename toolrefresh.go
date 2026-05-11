package mcpx

import (
	"context"
	"fmt"
)

// runToolRefresh is the per-server drain goroutine started by New. It reads
// signals from entry.refreshCh and calls applyToolRefresh for each one.
// It exits when ctx is cancelled (i.e. when Close is called).
func (mx *Multiplexer) runToolRefresh(ctx context.Context, name string, entry *serverEntry) {
	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-entry.refreshCh:
			if !ok {
				return
			}
			mx.applyToolRefresh(ctx, name, entry)
		}
	}
}

// applyToolRefresh re-fetches the tool list for the named server and updates
// the cache atomically. It is a no-op when a reconnect is already in progress
// (reconnectServer will supply fresh tools on success).
func (mx *Multiplexer) applyToolRefresh(ctx context.Context, name string, entry *serverEntry) {
	if entry.reconnecting.Load() {
		return
	}

	entry.mu.RLock()
	sess := entry.session
	entry.mu.RUnlock()
	if sess == nil {
		return
	}

	refreshCtx, cancel := context.WithTimeout(ctx, mx.opts.callTimeout)
	defer cancel()

	newTools, err := mx.fetchTools(refreshCtx, name, sess)
	if err != nil {
		mx.opts.logger.Error("mcpx: tool refresh failed", F("server", name), F("error", err.Error()))
		return
	}

	entry.mu.Lock()
	before := entry.tools
	entry.tools = newTools
	entry.mu.Unlock()

	if mx.opts.onToolsChanged != nil && !toolListsEqual(before, newTools) {
		func() {
			defer func() {
				if r := recover(); r != nil {
					mx.opts.logger.Error("mcpx: OnToolsChanged panic recovered",
						F("server", name), F("panic", fmt.Sprint(r)))
				}
			}()
			mx.opts.onToolsChanged(name, before, newTools)
		}()
	}
}

// toolListsEqual reports whether a and b contain the same (Server, Name) pairs
// in the same order. It is used to suppress spurious OnToolsChanged callbacks.
func toolListsEqual(a, b []ToolInfo) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Server != b[i].Server || a[i].Name != b[i].Name {
			return false
		}
	}
	return true
}
