package mcpx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ErrServerNotFound is returned by CallTool when the named server is not registered.
var ErrServerNotFound = errors.New("mcpx: server not found")

// ErrToolNotFound is returned by CallTool when the named tool is not exposed by the server.
var ErrToolNotFound = errors.New("mcpx: tool not found")

// ErrInvalidArgs is returned by CallTool when arguments fail validation.
// BadFields lists argument paths that contain unresolved placeholder values.
// SchemaErrors lists JSON Schema violations; populated only when
// [WithSchemaValidation] is enabled and args do not conform to the tool schema.
type ErrInvalidArgs struct {
	BadFields    []string
	SchemaErrors []string
}

func (e *ErrInvalidArgs) Error() string {
	if len(e.SchemaErrors) == 0 {
		return "mcpx: argument(s) have invalid placeholder values: " + strings.Join(e.BadFields, ", ")
	}
	parts := make([]string, 0, 2)
	if len(e.BadFields) > 0 {
		parts = append(parts, "placeholder values: "+strings.Join(e.BadFields, ", "))
	}
	parts = append(parts, "schema violations: "+strings.Join(e.SchemaErrors, "; "))
	return "mcpx: invalid arguments: " + strings.Join(parts, "; ")
}

// CallTool invokes the named tool on the named server with the given JSON
// arguments. It runs all configured BeforeCall/AfterCall/ResultTransform
// hooks and consults the response cache when the tool is cacheable.
// The argsJSON parameter must be a JSON object (or empty/nil).
//
// Errors returned:
//   - ErrServerNotFound, ErrToolNotFound, ErrServerDown — caller mistake.
//   - *ErrInvalidArgs — args contain unresolved placeholders or schema violations.
//   - errors from BeforeCallHook are propagated as-is.
//   - upstream MCP errors are wrapped via fmt.Errorf("server %s: %w", ...).
func (mx *Multiplexer) CallTool(ctx context.Context, server, toolName string, argsJSON json.RawMessage) (*CallResult, error) {
	start := time.Now()

	mx.mu.RLock()
	entry, ok := mx.servers[server]
	mx.mu.RUnlock()

	if !ok {
		err := fmt.Errorf("%w: %q (available: %s)",
			ErrServerNotFound, server, strings.Join(mx.ServerNames(), ", "))
		mx.fireRejected(ctx, server, toolName, RejectUnknownServer, err)
		mx.runAfterCall(ctx, server, toolName, ToolInfo{}, argsJSON, nil, err, time.Since(start))
		return nil, err
	}

	entry.mu.RLock()
	entryState := entry.state
	entryTools := entry.tools
	entrySess := entry.session
	entry.mu.RUnlock()

	var toolMeta ToolInfo
	found := false
	for _, ti := range entryTools {
		if ti.Name == toolName {
			toolMeta = ti
			found = true
			break
		}
	}
	if !found {
		err := fmt.Errorf("%w: %s on server %s", ErrToolNotFound, toolName, server)
		mx.fireRejected(ctx, server, toolName, RejectUnknownTool, err)
		mx.runAfterCall(ctx, server, toolName, toolMeta, argsJSON, nil, err, time.Since(start))
		safeRecordCall(mx.opts.metrics, server, toolName, time.Since(start), err)
		return nil, err
	}

	if entryState == ServerStateDown {
		err := fmt.Errorf("%w: %q", ErrServerDown, server)
		mx.fireRejected(ctx, server, toolName, RejectServerDown, err)
		mx.runAfterCall(ctx, server, toolName, toolMeta, argsJSON, nil, err, time.Since(start))
		return nil, err
	}

	// --- args transform -------------------------------------------------------

	params := &mcp.CallToolParams{Name: toolName}
	finalArgs := argsJSON
	if len(argsJSON) > 0 {
		var rawArgs map[string]any
		if err := json.Unmarshal(argsJSON, &rawArgs); err != nil {
			return nil, fmt.Errorf("mcpx: invalid args json: %w", err)
		}
		if bad := findInvalidArgs(rawArgs); len(bad) > 0 {
			return nil, &ErrInvalidArgs{BadFields: bad}
		}
		singularMap := mergedSingularMap(mx.opts.resourceSingular, entry.config.ResourceSingular)
		transformed := entry.config.ArgsTransformers.applyAll(rawArgs, mx.opts.customTransformers, singularMap)
		transformed = applyFieldMap(transformed, entry.config.FieldMap)
		params.Arguments = transformed

		if reSerialised, err := json.Marshal(transformed); err == nil {
			finalArgs = reSerialised
		}
		mx.opts.logger.Debug("mcpx: call",
			F("server", server), F("tool", toolName), F("args", transformed))
	}

	if mx.opts.schemaValidation {
		if errs := validateSchema(toolMeta.InputSchema, finalArgs); len(errs) > 0 {
			ivErr := &ErrInvalidArgs{SchemaErrors: errs}
			safeRecordCall(mx.opts.metrics, server, toolName, time.Since(start), ivErr)
			return nil, ivErr
		}
	}

	// --- BeforeCall chain ----------------------------------------------------

	for _, hook := range mx.opts.beforeCall {
		newCtx, shortResult, err := hook(ctx, server, toolName, toolMeta, finalArgs)
		if err != nil {
			mx.fireRejected(ctx, server, toolName, RejectBeforeHookAbort, err)
			mx.runAfterCall(ctx, server, toolName, toolMeta, finalArgs, nil, err, time.Since(start))
			return nil, err
		}
		if shortResult != nil {
			// Short-circuit: skip upstream and ResultTransform.
			mx.runAfterCall(ctx, server, toolName, toolMeta, finalArgs, shortResult, nil, time.Since(start))
			return shortResult, nil
		}
		if newCtx != nil {
			ctx = newCtx
		}
	}

	// --- cache lookup --------------------------------------------------------

	activeCache := mx.activeCache()
	keyFn := mx.opts.cacheKey
	if keyFn == nil {
		keyFn = defaultCacheKey
	}

	if activeCache != nil && isCacheable(toolMeta) {
		if CacheScope(ctx) == "" {
			mx.cacheScopeWarnOnce.Do(func() {
				mx.opts.logger.Warn("mcpx: cache enabled but no scope set; results may leak across tenants — use WithCacheScope(ctx, id)")
			})
		}
		cacheKey := keyFn(ctx, server, toolName, finalArgs)
		if cached, ok := activeCache.Get(ctx, cacheKey); ok {
			hitCtx := markCacheHit(ctx)
			mx.runAfterCall(hitCtx, server, toolName, toolMeta, finalArgs, cached, nil, time.Since(start))
			return cached, nil
		}
	}

	// --- upstream call -------------------------------------------------------

	timeout := effectiveTimeout(entry.config.CallTimeout, mx.opts.callTimeout)
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	rawResult, callErr := entrySess.CallTool(callCtx, params)
	dur := time.Since(start)

	if callErr != nil {
		var wrapped error
		if callCtx.Err() != nil && errors.Is(callCtx.Err(), context.DeadlineExceeded) {
			wrapped = fmt.Errorf("mcpx: call timeout %s/%s after %s", server, toolName, timeout)
		} else {
			wrapped = fmt.Errorf("mcpx: server %s: %w", server, callErr)
		}
		mx.opts.logger.Error("mcpx: call failed",
			F("server", server), F("tool", toolName), F("error", wrapped.Error()))
		mx.runAfterCall(ctx, server, toolName, toolMeta, finalArgs, nil, wrapped, dur)
		safeRecordCall(mx.opts.metrics, server, toolName, dur, wrapped)
		return nil, wrapped
	}

	result := buildResult(rawResult)

	// --- ResultTransform chain -----------------------------------------------

	for _, hook := range mx.opts.resultTransform {
		if err := hook(ctx, server, toolName, toolMeta, result); err != nil {
			mx.runAfterCall(ctx, server, toolName, toolMeta, finalArgs, result, err, dur)
			safeRecordCall(mx.opts.metrics, server, toolName, dur, err)
			return nil, err
		}
	}

	// --- cache store ---------------------------------------------------------

	if activeCache != nil && isCacheable(toolMeta) && !result.IsError {
		ttl := toolTTL(toolMeta, mx.opts.cacheTTL, mx.opts.logger, &mx.cacheTTLWarnMap)
		cacheKey := keyFn(ctx, server, toolName, finalArgs)
		activeCache.Set(ctx, cacheKey, result, ttl)
	}

	mx.runAfterCall(ctx, server, toolName, toolMeta, finalArgs, result, nil, dur)
	safeRecordCall(mx.opts.metrics, server, toolName, dur, nil)
	return result, nil
}

// activeCache returns the cache to use, or nil when caching is disabled.
func (mx *Multiplexer) activeCache() Cache {
	if mx.opts.cacheDisabled {
		return nil
	}
	if mx.opts.cache != nil {
		return mx.opts.cache
	}
	return mx.builtinCache
}

func (mx *Multiplexer) runAfterCall(ctx context.Context, server, tool string, info ToolInfo, args json.RawMessage, result *CallResult, callErr error, duration time.Duration) {
	for _, hook := range mx.opts.afterCall {
		hook(ctx, server, tool, info, args, result, callErr, duration)
	}
}

func (mx *Multiplexer) fireRejected(ctx context.Context, server, tool string, reason RejectReason, err error) {
	if mx.opts.onRejectedCall == nil {
		return
	}
	func() {
		defer func() { recover() }() //nolint:errcheck
		mx.opts.onRejectedCall(ctx, server, tool, reason, err)
	}()
}

// effectiveTimeout returns perServer if positive, otherwise global.
func effectiveTimeout(perServer, global time.Duration) time.Duration {
	if perServer > 0 {
		return perServer
	}
	return global
}

func buildResult(r *mcp.CallToolResult) *CallResult {
	if r == nil {
		return &CallResult{}
	}
	parts := make([]ContentPart, 0, len(r.Content))
	textParts := make([]string, 0, len(r.Content))
	for _, c := range r.Content {
		switch v := c.(type) {
		case *mcp.TextContent:
			parts = append(parts, ContentPart{Kind: ContentText, Text: v.Text})
			textParts = append(textParts, v.Text)
		case *mcp.ImageContent:
			parts = append(parts, ContentPart{
				Kind:     ContentImage,
				MIMEType: v.MIMEType,
				Data:     v.Data,
			})
			textParts = append(textParts, "[image: "+v.MIMEType+"]")
		default:
			if b, err := json.Marshal(c); err == nil {
				parts = append(parts, ContentPart{Kind: ContentOther, Raw: b})
				textParts = append(textParts, string(b))
			}
		}
	}
	return &CallResult{
		Text:    strings.Join(textParts, "\n"),
		Parts:   parts,
		IsError: r.IsError,
	}
}

