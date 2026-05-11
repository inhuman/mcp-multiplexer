package mcpx

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ServerState reports the observed liveness of a single MCP server.
// It is set to [ServerStateConnected] at construction and updated by the
// health-check supervisor when [WithHealthCheck] is used.
type ServerState string

const (
	// ServerStateConnected means the server is reachable and calls proceed normally.
	ServerStateConnected ServerState = "connected"
	// ServerStateDown means the last health probe failed; [Multiplexer.CallTool]
	// returns [ErrServerDown] immediately for this server.
	ServerStateDown ServerState = "down"
)

// OnReconnectFunc is called by the health-check supervisor on every reconnect
// attempt. err is nil when the attempt succeeded, non-nil on failure.
// It is invoked synchronously from the supervisor goroutine and must not block
// for extended periods.
type OnReconnectFunc func(server string, err error)

// ErrServerDown is returned by [Multiplexer.CallTool] when the target server
// is currently unreachable and has been marked down by the health-check
// supervisor. Use [errors.Is] to distinguish it from [ErrServerNotFound].
var ErrServerDown = errors.New("mcpx: server is down")

// nextBackoff doubles cur, capping at 60 s. The initial call should pass 0
// to receive the first backoff of 1 s.
func nextBackoff(cur time.Duration) time.Duration {
	const (
		initial = time.Second
		max     = 60 * time.Second
	)
	if cur <= 0 {
		return initial
	}
	next := cur * 2
	if next > max {
		return max
	}
	return next
}

// probeServer calls ListTools on the server's session to verify liveness.
// It uses a probe timeout of min(interval/2, 10s).
func (mx *Multiplexer) probeServer(ctx context.Context, name string, interval time.Duration) error {
	mx.mu.RLock()
	entry, ok := mx.servers[name]
	mx.mu.RUnlock()
	if !ok {
		return fmt.Errorf("server %s not found", name)
	}

	timeout := interval / 2
	if timeout > 10*time.Second {
		timeout = 10 * time.Second
	}
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	entry.mu.RLock()
	sess := entry.session
	entry.mu.RUnlock()

	if sess == nil {
		return fmt.Errorf("no session for server %s", name)
	}
	_, err := sess.ListTools(probeCtx, nil)
	return err
}

// reconnectServer marks the server as down then loops with exponential backoff
// until it reconnects successfully or ctx is cancelled.
func (mx *Multiplexer) reconnectServer(ctx context.Context, name string) {
	mx.mu.RLock()
	entry, ok := mx.servers[name]
	mx.mu.RUnlock()
	if !ok {
		return
	}

	// Mark down.
	entry.mu.Lock()
	entry.state = ServerStateDown
	oldSession := entry.session
	entry.mu.Unlock()

	if oldSession != nil {
		_ = oldSession.Close()
	}

	var backoff time.Duration
	for {
		backoff = nextBackoff(backoff)

		select {
		case <-ctx.Done():
			entry.reconnecting.Store(false)
			return
		case <-time.After(backoff):
		}

		mx.mu.RLock()
		cfg := entry.config
		mx.mu.RUnlock()

		newEntry, err := mx.connect(ctx, cfg)
		if err != nil {
			mx.opts.logger.Error("mcpx: reconnect failed",
				F("server", name), F("error", err.Error()))
			if mx.opts.onReconnect != nil {
				mx.opts.onReconnect(name, err)
			}
			continue
		}

		// Swap session and tools atomically.
		entry.mu.Lock()
		entry.session = newEntry.session
		entry.tools = newEntry.tools
		entry.state = ServerStateConnected
		entry.mu.Unlock()

		entry.reconnecting.Store(false)
		mx.opts.logger.Info("mcpx: reconnected", F("server", name))
		if mx.opts.onReconnect != nil {
			mx.opts.onReconnect(name, nil)
		}
		return
	}
}

// runSupervisor is the health-check loop. It runs until ctx is cancelled.
func (mx *Multiplexer) runSupervisor(ctx context.Context) {
	ticker := time.NewTicker(mx.opts.healthCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			mx.mu.RLock()
			names := make([]string, 0, len(mx.servers))
			for name := range mx.servers {
				names = append(names, name)
			}
			mx.mu.RUnlock()

			for _, name := range names {
				mx.mu.RLock()
				entry, ok := mx.servers[name]
				mx.mu.RUnlock()
				if !ok {
					continue
				}

				// Skip servers already being reconnected.
				if entry.reconnecting.Load() {
					continue
				}

				if err := mx.probeServer(ctx, name, mx.opts.healthCheckInterval); err != nil {
					if ctx.Err() != nil {
						return
					}
					if entry.reconnecting.CompareAndSwap(false, true) {
						go mx.reconnectServer(ctx, name)
					}
				}
			}
		}
	}
}
