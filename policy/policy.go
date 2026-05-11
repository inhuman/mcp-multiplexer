package policy

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	mcpx "github.com/inhuman/mcp-multiplexer"
)

// ContextKey is the type for context keys defined by this package.
type ContextKey string

// RolesKey is the context key under which [RequireRoles] looks for the
// caller's roles. The value must be []string. Set it via:
//
//	ctx = context.WithValue(ctx, policy.RolesKey, []string{"admin"})
const RolesKey ContextKey = "mcpx-policy-roles"

// DenyDestructive returns a [mcpx.BeforeCallHook] that rejects any call
// where [mcpx.ToolInfo].Destructive is true. Non-destructive tools pass through.
func DenyDestructive() mcpx.BeforeCallHook {
	return func(_ context.Context, _ string, tool mcpx.ToolInfo, _ json.RawMessage) error {
		if tool.Destructive {
			return fmt.Errorf("mcpx/policy: tool %s is marked destructive and is not allowed", tool.Name)
		}
		return nil
	}
}

// RequireRoles returns a [mcpx.BeforeCallHook] that allows the call only when
// the context value at [RolesKey] (a []string) contains at least one of the
// required roles. Passing an empty roles list means no caller is ever allowed.
func RequireRoles(roles ...string) mcpx.BeforeCallHook {
	return func(ctx context.Context, _ string, _ mcpx.ToolInfo, _ json.RawMessage) error {
		v := ctx.Value(RolesKey)
		if v == nil {
			return fmt.Errorf("mcpx/policy: permission denied: required roles %v not satisfied", roles)
		}
		have, ok := v.([]string)
		if !ok {
			return fmt.Errorf("mcpx/policy: permission denied: required roles %v not satisfied", roles)
		}
		for _, required := range roles {
			for _, h := range have {
				if h == required {
					return nil
				}
			}
		}
		return fmt.Errorf("mcpx/policy: permission denied: required roles %v not satisfied", roles)
	}
}

// RateLimit returns a [mcpx.BeforeCallHook] that enforces a per-(server, tool)
// token-bucket limit. burst tokens are available immediately; one additional
// token is earned every per duration. Safe for concurrent use.
func RateLimit(per time.Duration, burst int) mcpx.BeforeCallHook {
	tb := newTokenBucket(per, burst)
	return func(_ context.Context, server string, tool mcpx.ToolInfo, _ json.RawMessage) error {
		key := server + "/" + tool.Name
		if !tb.allow(key) {
			return fmt.Errorf("mcpx/policy: rate limit exceeded for %s/%s", server, tool.Name)
		}
		return nil
	}
}

// AuditLog returns a [mcpx.AfterCallHook] that logs every call outcome via
// logger. On success it logs at Info level; on error at Error level. Args and
// result text are never logged.
func AuditLog(logger mcpx.Logger) mcpx.AfterCallHook {
	return func(_ context.Context, server string, tool mcpx.ToolInfo, _ json.RawMessage, _ *mcpx.CallResult, callErr error) {
		if callErr != nil {
			logger.Error("mcpx/policy: call failed",
				mcpx.F("server", server),
				mcpx.F("tool", tool.Name),
				mcpx.F("error", callErr.Error()),
			)
			return
		}
		logger.Info("mcpx/policy: call",
			mcpx.F("server", server),
			mcpx.F("tool", tool.Name),
		)
	}
}

// tokenBucket is a per-key token-bucket rate limiter using only stdlib.
type tokenBucket struct {
	mu      sync.Mutex
	buckets map[string]*tbucket
	per     time.Duration
	burst   int
}

type tbucket struct {
	tokens   float64
	lastTime time.Time
}

func newTokenBucket(per time.Duration, burst int) *tokenBucket {
	return &tokenBucket{
		buckets: make(map[string]*tbucket),
		per:     per,
		burst:   burst,
	}
}

func (tb *tokenBucket) allow(key string) bool {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	now := time.Now()
	b, ok := tb.buckets[key]
	if !ok {
		tb.buckets[key] = &tbucket{tokens: float64(tb.burst) - 1, lastTime: now}
		return true
	}

	elapsed := now.Sub(b.lastTime)
	refill := elapsed.Seconds() / tb.per.Seconds() * float64(tb.burst)
	b.tokens = min(float64(tb.burst), b.tokens+refill)
	b.lastTime = now

	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}
