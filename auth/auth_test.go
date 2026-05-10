package auth_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/inhuman/mcp-multiplexer/auth"
)

func newReq(t *testing.T) *http.Request {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "http://x/", nil)
	r.Header = http.Header{}
	return r
}

func TestBearer_HappyPath(t *testing.T) {
	r := newReq(t)
	err := auth.Bearer(t.Context(), "s", r, map[string]any{"token": "abc"})
	require.NoError(t, err)
	require.Equal(t, "Bearer abc", r.Header.Get("Authorization"))
}

func TestBearer_MissingToken(t *testing.T) {
	r := newReq(t)
	err := auth.Bearer(t.Context(), "s", r, map[string]any{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "missing")
	require.Contains(t, err.Error(), `"token"`)
}

func TestBearer_TokenNotString(t *testing.T) {
	r := newReq(t)
	err := auth.Bearer(t.Context(), "s", r, map[string]any{"token": 42})
	require.Error(t, err)
	require.Contains(t, err.Error(), "not a string")
}

func TestBearer_EmptyToken(t *testing.T) {
	r := newReq(t)
	err := auth.Bearer(t.Context(), "s", r, map[string]any{"token": ""})
	require.Error(t, err)
	require.Contains(t, err.Error(), "empty")
}

func TestHeaderToken_HappyPath(t *testing.T) {
	r := newReq(t)
	err := auth.HeaderToken(t.Context(), "s", r, map[string]any{
		"tokenName": "X-MCP-AUTH",
		"value":     "raw-tok",
	})
	require.NoError(t, err)
	require.Equal(t, "raw-tok", r.Header.Get("X-MCP-AUTH"))
	require.Empty(t, r.Header.Get("Authorization"), "must not set Authorization header")
}

func TestHeaderToken_MissingName(t *testing.T) {
	r := newReq(t)
	err := auth.HeaderToken(t.Context(), "s", r, map[string]any{"value": "v"})
	require.Error(t, err)
	require.Contains(t, err.Error(), `"tokenName"`)
}

func TestHeaderToken_NonStringName(t *testing.T) {
	r := newReq(t)
	err := auth.HeaderToken(t.Context(), "s", r, map[string]any{"tokenName": 1, "value": "v"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "not a string")
}

func TestHeaderToken_EmptyName(t *testing.T) {
	r := newReq(t)
	err := auth.HeaderToken(t.Context(), "s", r, map[string]any{"tokenName": "", "value": "v"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "empty")
}

func TestHeaderToken_MissingValue(t *testing.T) {
	r := newReq(t)
	err := auth.HeaderToken(t.Context(), "s", r, map[string]any{"tokenName": "X"})
	require.Error(t, err)
	require.Contains(t, err.Error(), `"value"`)
}

func TestHeaderToken_NonStringValue(t *testing.T) {
	r := newReq(t)
	err := auth.HeaderToken(t.Context(), "s", r, map[string]any{"tokenName": "X", "value": 1})
	require.Error(t, err)
	require.Contains(t, err.Error(), "not a string")
}

func TestHeaderToken_EmptyValue(t *testing.T) {
	r := newReq(t)
	err := auth.HeaderToken(t.Context(), "s", r, map[string]any{"tokenName": "X", "value": ""})
	require.Error(t, err)
	require.Contains(t, err.Error(), "empty")
}
