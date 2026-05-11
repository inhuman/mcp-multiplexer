package mcpx

import (
	"container/list"
	"context"
	"encoding/json"
	"sync"
	"time"
)

// Cache is the storage backend for CallTool response caching.
// Implementations MUST be goroutine-safe and MUST store/return deep copies.
type Cache interface {
	// Get returns a deep copy of the cached result and true on hit.
	// Returns nil and false on miss or expiry.
	Get(ctx context.Context, key string) (*CallResult, bool)
	// Set stores a deep copy of value with the given TTL.
	// Set with nil value or zero TTL is a no-op.
	Set(ctx context.Context, key string, value *CallResult, ttl time.Duration)
}

// KeyFunc computes the cache key for a call. When registered via WithCacheKey
// it replaces the built-in canonicalisation entirely.
type KeyFunc func(ctx context.Context, server, tool string, args json.RawMessage) string

// --- context helpers ---------------------------------------------------------

type cacheScopeKey struct{}
type cacheHitKey struct{}

// WithCacheScope injects a per-call scope string into ctx for cache key
// isolation. Use it to prevent cross-tenant cache collisions.
func WithCacheScope(ctx context.Context, scope string) context.Context {
	return context.WithValue(ctx, cacheScopeKey{}, scope)
}

// CacheScope retrieves the scope set by WithCacheScope. Returns "" if not set.
func CacheScope(ctx context.Context) string {
	s, _ := ctx.Value(cacheScopeKey{}).(string)
	return s
}

// IsCacheHit reports whether the current call was served from cache.
// Valid to call from AfterCallHook; always false from BeforeCallHook.
func IsCacheHit(ctx context.Context) bool {
	v, _ := ctx.Value(cacheHitKey{}).(bool)
	return v
}

func markCacheHit(ctx context.Context) context.Context {
	return context.WithValue(ctx, cacheHitKey{}, true)
}

// --- built-in LRU ------------------------------------------------------------

type lruEntry struct {
	key       string
	value     *CallResult
	expiresAt time.Time
}

type lruCache struct {
	mu    sync.Mutex
	list  *list.List
	index map[string]*list.Element
	size  int
}

func newLRUCache(size int) *lruCache {
	return &lruCache{
		list:  list.New(),
		index: make(map[string]*list.Element, size),
		size:  size,
	}
}

func (c *lruCache) Get(_ context.Context, key string) (*CallResult, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.index[key]
	if !ok {
		return nil, false
	}
	e := el.Value.(*lruEntry)
	if time.Now().After(e.expiresAt) {
		c.list.Remove(el)
		delete(c.index, key)
		return nil, false
	}
	c.list.MoveToFront(el)
	return e.value.Clone(), true
}

func (c *lruCache) Set(_ context.Context, key string, value *CallResult, ttl time.Duration) {
	if value == nil || ttl <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.index[key]; ok {
		c.list.MoveToFront(el)
		e := el.Value.(*lruEntry)
		e.value = value.Clone()
		e.expiresAt = time.Now().Add(ttl)
		return
	}
	e := &lruEntry{key: key, value: value.Clone(), expiresAt: time.Now().Add(ttl)}
	el := c.list.PushFront(e)
	c.index[key] = el
	if c.list.Len() > c.size {
		back := c.list.Back()
		if back != nil {
			c.list.Remove(back)
			delete(c.index, back.Value.(*lruEntry).key)
		}
	}
}

// --- cacheability & key ------------------------------------------------------

func isCacheable(info ToolInfo) bool {
	if info.Destructive {
		return false
	}
	if info.ReadOnly && info.Idempotent {
		return true
	}
	return info.Custom["cacheable"] == "true"
}

// toolTTL returns the effective TTL for a tool, respecting Custom["cache_ttl"].
// Parse errors are warned once per (server/tool) key via warnMap.
func toolTTL(info ToolInfo, defaultTTL time.Duration, logger Logger, warnMap *sync.Map) time.Duration {
	raw, ok := info.Custom["cache_ttl"]
	if !ok {
		return defaultTTL
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		warnKey := info.Server + "/" + info.Name
		if _, loaded := warnMap.LoadOrStore(warnKey, struct{}{}); !loaded {
			logger.Warn("mcpx: invalid cache_ttl, using default",
				F("server", info.Server), F("tool", info.Name), F("value", raw))
		}
		return defaultTTL
	}
	return d
}

func defaultCacheKey(ctx context.Context, server, tool string, args json.RawMessage) string {
	scope := CacheScope(ctx)
	return scope + "|" + server + "|" + tool + "|" + canonicalJSON(args)
}

func canonicalJSON(args json.RawMessage) string {
	if len(args) == 0 {
		return "{}"
	}
	var v any
	if err := json.Unmarshal(args, &v); err != nil {
		return "{}"
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}
