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

// ErrInvalidArgs is returned by CallTool when arguments contain unresolved
// placeholder values (empty strings, "undefined", "null", etc.).
type ErrInvalidArgs struct {
	BadFields []string
}

func (e *ErrInvalidArgs) Error() string {
	return fmt.Sprintf("mcpx: argument(s) have invalid placeholder values: %s",
		strings.Join(e.BadFields, ", "))
}

// CallTool invokes the named tool on the named server with the given JSON
// arguments. It runs all configured BeforeCall/AfterCall/ResultTransform
// hooks. The argsJSON parameter must be a JSON object (or empty/nil).
//
// Errors returned:
//   - ErrServerNotFound, ErrToolNotFound — caller mistake.
//   - *ErrInvalidArgs — args contain unresolved placeholders.
//   - errors from BeforeCallHook are propagated as-is.
//   - upstream MCP errors are wrapped via fmt.Errorf("server %s: %w", ...).
func (mx *Multiplexer) CallTool(ctx context.Context, server, toolName string, argsJSON json.RawMessage) (*CallResult, error) {
	mx.mu.RLock()
	entry, ok := mx.servers[server]
	mx.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("%w: %q (available: %s)",
			ErrServerNotFound, server, strings.Join(mx.ServerNames(), ", "))
	}

	// Fast-fail if the supervisor has marked this server as down.
	entry.mu.RLock()
	entryState := entry.state
	entryTools := entry.tools
	entrySess := entry.session
	entry.mu.RUnlock()

	if entryState == ServerStateDown {
		return nil, fmt.Errorf("%w: %q", ErrServerDown, server)
	}

	start := time.Now()

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
		safeRecordCall(mx.opts.metrics, server, toolName, time.Since(start), err)
		return nil, err
	}

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
		transformed := entry.config.ArgsTransformers.applyAll(rawArgs, mx.opts.customTransformers)
		transformed = applyFieldMap(transformed, entry.config.FieldMap)
		params.Arguments = transformed

		// Re-serialise after transform so hooks see the exact bytes that go upstream.
		if reSerialised, err := json.Marshal(transformed); err == nil {
			finalArgs = reSerialised
		}
		mx.opts.logger.Debug("mcpx: call",
			F("server", server), F("tool", toolName), F("args", transformed))
	}

	for _, hook := range mx.opts.beforeCall {
		if err := hook(ctx, server, toolMeta, finalArgs); err != nil {
			mx.runAfterCall(ctx, server, toolMeta, finalArgs, nil, err)
			return nil, err
		}
	}

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
		mx.runAfterCall(ctx, server, toolMeta, finalArgs, nil, wrapped)
		safeRecordCall(mx.opts.metrics, server, toolName, dur, wrapped)
		return nil, wrapped
	}

	result := buildResult(rawResult)

	for _, hook := range mx.opts.resultTransform {
		newText, err := hook(ctx, server, toolMeta, result.Text)
		if err != nil {
			mx.runAfterCall(ctx, server, toolMeta, finalArgs, result, err)
			safeRecordCall(mx.opts.metrics, server, toolName, dur, err)
			return nil, err
		}
		result.Text = newText
	}

	mx.runAfterCall(ctx, server, toolMeta, finalArgs, result, nil)
	safeRecordCall(mx.opts.metrics, server, toolName, dur, nil)
	return result, nil
}

func (mx *Multiplexer) runAfterCall(ctx context.Context, server string, tool ToolInfo, args json.RawMessage, result *CallResult, callErr error) {
	for _, hook := range mx.opts.afterCall {
		hook(ctx, server, tool, args, result, callErr)
	}
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
