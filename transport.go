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
	cmd := exec.CommandContext(ctx, cfg.Binary, cfg.Args...) //nolint:gosec
	if len(cfg.Env) > 0 {
		cmd.Env = append(os.Environ(), cfg.Env...)
	}
	return &mcp.CommandTransport{Command: cmd}
}

func newHTTPTransport(cfg ServerConfig, opts *options) mcp.Transport {
	return &mcp.StreamableClientTransport{
		Endpoint:   cfg.URL,
		HTTPClient: retryableHTTPClient(cfg.Token, cfg.TokenHeader, opts),
	}
}

func newSSETransport(cfg ServerConfig, opts *options) mcp.Transport {
	return &mcp.SSEClientTransport{
		Endpoint:   cfg.URL,
		HTTPClient: retryableHTTPClient(cfg.Token, cfg.TokenHeader, opts),
	}
}

// retryableHTTPClient builds an *http.Client with exponential-backoff retries
// for transient errors (connection refused, 502/503/504). Token injection is
// added via tokenRoundTripper wrapping the retryable transport.
func retryableHTTPClient(token, header string, opts *options) *http.Client {
	rc := retryablehttp.NewClient()
	rc.RetryMax = opts.httpRetryMax
	rc.RetryWaitMin = 1 * time.Second
	rc.RetryWaitMax = 8 * time.Second
	rc.Logger = &leveledLoggerAdapter{log: opts.logger}

	base := rc.StandardClient()
	if token == "" {
		return base
	}
	base.Transport = &tokenRoundTripper{token: token, header: header, base: base.Transport}
	return base
}

// tokenRoundTripper injects an auth header into every request.
//
// If header is empty or "Authorization", sends `Authorization: Bearer <token>`.
// Otherwise sends the header verbatim with the raw token value (e.g.
// `X-MCP-AUTH: <token>`).
type tokenRoundTripper struct {
	header string
	token  string
	base   http.RoundTripper
}

func (t *tokenRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	if t.token != "" {
		r = r.Clone(r.Context())
		if t.header == "" || t.header == "Authorization" {
			r.Header.Set("Authorization", "Bearer "+t.token)
		} else {
			r.Header.Set(t.header, t.token)
		}
	}
	return t.base.RoundTrip(r)
}

// BearerRoundTripper returns an http.RoundTripper that injects a
// `Authorization: Bearer <token>` header into every request.
func BearerRoundTripper(token string, base http.RoundTripper) http.RoundTripper {
	return &tokenRoundTripper{token: token, base: base}
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
