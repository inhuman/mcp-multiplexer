package mcpx_test

import (
	"context"
	"errors"
	"io"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	mcpx "github.com/inhuman/mcp-multiplexer"
	"github.com/inhuman/mcp-multiplexer/auth"
	"github.com/inhuman/mcp-multiplexer/internal/testutil/capturelog"
	"github.com/inhuman/mcp-multiplexer/internal/testutil/mcptest"
)

// captureStd swaps os.Stdout/os.Stderr for pipes during the supplied func and
// returns the bytes written to either. Restores the originals afterwards.
func captureStd(t *testing.T, fn func()) (stdout, stderr []byte) {
	t.Helper()
	origOut, origErr := os.Stdout, os.Stderr
	rOut, wOut, err := os.Pipe()
	require.NoError(t, err)
	rErr, wErr, err := os.Pipe()
	require.NoError(t, err)

	os.Stdout = wOut
	os.Stderr = wErr

	fn()

	_ = wOut.Close()
	_ = wErr.Close()
	os.Stdout = origOut
	os.Stderr = origErr

	stdout, _ = io.ReadAll(rOut)
	stderr, _ = io.ReadAll(rErr)
	_ = rOut.Close()
	_ = rErr.Close()
	return stdout, stderr
}

func TestSecurity_NoStdoutWithoutLogger(t *testing.T) {
	srv := mcptest.NewServer(echoTool("e"))
	defer srv.Close()
	ts := httptest.NewServer(srv.HTTPHandler())
	defer ts.Close()

	stdout, stderr := captureStd(t, func() {
		mx, err := mcpx.New(t.Context(), mcpx.MultiplexerConfig{
			Servers: []mcpx.ServerConfig{{Name: "s", Transport: mcpx.TransportHTTP, URL: ts.URL}},
		}, mcpx.WithHTTPRetryMax(0))
		if err != nil {
			return
		}
		_, _ = mx.CallTool(context.Background(), "s", "e", []byte(`{"msg":"hello"}`))
		_, _ = mx.CallTool(context.Background(), "missing", "ghost", nil) // error path
		mx.Close()
	})
	require.Empty(t, stdout, "library must not write to stdout: %q", string(stdout))
	require.Empty(t, stderr, "library must not write to stderr: %q", string(stderr))
}

func TestSecurity_NoArgsOrResultInLogs_NonDebug(t *testing.T) {
	const (
		argSecret    = "abc-payload-xyz"
		resultSecret = "server-secret-result-789"
	)
	srv := mcptest.NewServer(mcptest.WithTool(mcptest.ToolSpec{
		Name: "e",
		Handler: func(_ context.Context, _ map[string]any) (string, error) {
			return resultSecret, nil
		},
	}))
	defer srv.Close()
	ts := httptest.NewServer(srv.HTTPHandler())
	defer ts.Close()

	log := capturelog.New()
	mx, err := mcpx.New(t.Context(), mcpx.MultiplexerConfig{
		Servers: []mcpx.ServerConfig{{Name: "s", Transport: mcpx.TransportHTTP, URL: ts.URL}},
	}, mcpx.WithLogger(log), mcpx.WithHTTPRetryMax(0))
	require.NoError(t, err)
	defer mx.Close()

	_, err = mx.CallTool(t.Context(), "s", "e", []byte(`{"secret_field":"`+argSecret+`"}`))
	require.NoError(t, err)

	// Info, Warn, Error must NOT contain args value or result text.
	// Debug is excluded by design (multiplexer.go logs args at Debug level for
	// developer visibility; consumers control Debug routing).
	for _, lvl := range []capturelog.Level{capturelog.Info, capturelog.Warn, capturelog.Error} {
		require.False(t, log.ContainsAtLevel(lvl, argSecret),
			"level %s leaked argument value", lvl)
		require.False(t, log.ContainsAtLevel(lvl, resultSecret),
			"level %s leaked result text", lvl)
	}
}

func TestSecurity_TokenNotInLogsOrErrors(t *testing.T) {
	const tokenSecret = "secret-token-xyz-not-real"

	// Force an upstream error to exercise the error log path.
	srv := mcptest.NewServer(
		mcptest.WithTool(mcptest.ToolSpec{
			Name:    "boom",
			Handler: func(_ context.Context, _ map[string]any) (string, error) { return "", errors.New("upstream broken") },
		}),
	)
	defer srv.Close()
	ts := httptest.NewServer(srv.HTTPHandler())
	defer ts.Close()

	log := capturelog.New()
	mx, err := mcpx.New(t.Context(), mcpx.MultiplexerConfig{
		Servers: []mcpx.ServerConfig{{Name: "s", Transport: mcpx.TransportHTTP, URL: ts.URL,
			Auth: map[string]any{"token": tokenSecret}}},
	}, mcpx.WithLogger(log), mcpx.WithHTTPRetryMax(0), mcpx.WithAuthFunc(auth.Bearer))
	require.NoError(t, err)
	defer mx.Close()

	_, callErr := mx.CallTool(t.Context(), "s", "boom", nil)
	// Either upstream error returns, or nothing — both fine; we care about secrecy.
	if callErr != nil {
		require.False(t, strings.Contains(callErr.Error(), tokenSecret),
			"returned error must not contain token: %q", callErr.Error())
	}

	// Token must not appear at any level of the captured log.
	for _, lvl := range []capturelog.Level{capturelog.Debug, capturelog.Info, capturelog.Warn, capturelog.Error} {
		require.False(t, log.ContainsAtLevel(lvl, tokenSecret),
			"level %s leaked token", lvl)
	}
}

// TestSecurity_AuthValuesNotInMisconfigError ensures that values from
// ServerConfig.Auth do not leak into the error returned by mcpx.New when
// the consumer forgot to register WithAuthFunc.
func TestSecurity_AuthValuesNotInMisconfigError(t *testing.T) {
	const sensitive = "super-secret-marker-7891"

	_, err := mcpx.New(t.Context(), mcpx.MultiplexerConfig{
		Servers: []mcpx.ServerConfig{
			{Name: "k8s", Transport: mcpx.TransportHTTP, URL: "http://x",
				Auth: map[string]any{
					"token":     sensitive,
					"clientId":  sensitive + "-id",
					"someOther": sensitive + "-other",
				}},
		},
	})
	require.Error(t, err)
	// Error mentions the server name as a hint.
	require.Contains(t, err.Error(), `"k8s"`)
	require.Contains(t, err.Error(), "WithAuthFunc")
	// But never any value from Auth.
	require.NotContains(t, err.Error(), sensitive,
		"misconfig error must not leak Auth values: %q", err.Error())
}
