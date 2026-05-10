package mcpx

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"time"

	retryablehttp "github.com/hashicorp/go-retryablehttp"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func newTransport(ctx context.Context, cfg ServerConfig, opts *options) (mcp.Transport, error) {
	switch cfg.Transport {
	case TransportStdio:
		return newStdioTransport(ctx, cfg), nil
	case TransportHTTP:
		return newHTTPTransport(cfg, opts), nil
	case TransportSSE:
		return newSSETransport(cfg, opts), nil
	default:
		return nil, fmt.Errorf("mcpx: unknown transport: %s", cfg.Transport)
	}
}

func newStdioTransport(ctx context.Context, cfg ServerConfig) mcp.Transport {
	cmd := exec.CommandContext(ctx, cfg.Binary, cfg.Args...) //nolint:gosec // G204: cfg.Binary is supplied by the library consumer (caller responsibility), not external untrusted input
	if len(cfg.Env) > 0 {
		cmd.Env = append(os.Environ(), cfg.Env...)
	}
	return &mcp.CommandTransport{Command: cmd}
}

func newHTTPTransport(cfg ServerConfig, opts *options) mcp.Transport {
	return &mcp.StreamableClientTransport{
		Endpoint:   cfg.URL,
		HTTPClient: retryableHTTPClient(cfg, opts),
	}
}

func newSSETransport(cfg ServerConfig, opts *options) mcp.Transport {
	return &mcp.SSEClientTransport{
		Endpoint:   cfg.URL,
		HTTPClient: retryableHTTPClient(cfg, opts),
	}
}

// retryableHTTPClient builds an *http.Client with exponential-backoff retries
// for transient errors (connection refused, 502/503/504). When cfg.Auth is
// non-nil, the transport is wrapped with authFuncRoundTripper that delegates
// to opts.authFunc on every outbound request.
func retryableHTTPClient(cfg ServerConfig, opts *options) *http.Client {
	rc := retryablehttp.NewClient()
	rc.RetryMax = opts.httpRetryMax
	rc.RetryWaitMin = 1 * time.Second
	rc.RetryWaitMax = 8 * time.Second
	rc.Logger = &leveledLoggerAdapter{log: opts.logger}

	base := rc.StandardClient()
	if cfg.Auth == nil {
		return base
	}
	base.Transport = &authFuncRoundTripper{
		server: cfg.Name,
		data:   cfg.Auth,
		fn:     opts.authFunc,
		base:   base.Transport,
	}
	return base
}

// authFuncRoundTripper invokes the user-supplied AuthFunc on every outbound
// request. It clones the request before mutation so retryablehttp can safely
// resubmit on transient errors and concurrent callers do not race on
// header state.
type authFuncRoundTripper struct {
	server string
	data   map[string]any
	fn     AuthFunc
	base   http.RoundTripper
}

func (a *authFuncRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	cloned := r.Clone(r.Context())
	if err := a.fn(r.Context(), a.server, cloned, a.data); err != nil {
		return nil, fmt.Errorf("mcpx: auth %s: %w", a.server, err)
	}
	return a.base.RoundTrip(cloned)
}

// BearerRoundTripper returns an http.RoundTripper that injects an
// `Authorization: Bearer <token>` header into every request.
//
// This is a low-level helper for users assembling their own *http.Client
// outside the [ServerConfig] flow. For the config-driven path, prefer
// [WithAuthFunc] together with the auth.Bearer helper from
// github.com/inhuman/mcp-multiplexer/auth.
func BearerRoundTripper(token string, base http.RoundTripper) http.RoundTripper {
	return &bearerRoundTripper{token: token, base: base}
}

type bearerRoundTripper struct {
	token string
	base  http.RoundTripper
}

func (t *bearerRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	if t.token != "" {
		r = r.Clone(r.Context())
		r.Header.Set("Authorization", "Bearer "+t.token)
	}
	return t.base.RoundTrip(r)
}

// leveledLoggerAdapter bridges Logger to retryablehttp.LeveledLogger.
type leveledLoggerAdapter struct{ log Logger }

func (l *leveledLoggerAdapter) Error(msg string, kv ...any) { l.log.Error(msg, kvToFields(kv)...) }
func (l *leveledLoggerAdapter) Warn(msg string, kv ...any)  { l.log.Warn(msg, kvToFields(kv)...) }
func (l *leveledLoggerAdapter) Info(msg string, kv ...any)  { l.log.Info(msg, kvToFields(kv)...) }
func (l *leveledLoggerAdapter) Debug(msg string, kv ...any) { l.log.Debug(msg, kvToFields(kv)...) }

func kvToFields(kv []any) []Field {
	if len(kv) == 0 {
		return nil
	}
	out := make([]Field, 0, len(kv)/2)
	for i := 0; i+1 < len(kv); i += 2 {
		key, ok := kv[i].(string)
		if !ok {
			continue
		}
		out = append(out, Field{Key: key, Value: kv[i+1]})
	}
	return out
}
