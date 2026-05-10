package mcpx

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestKVToFields(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		require.Nil(t, kvToFields(nil))
		require.Nil(t, kvToFields([]any{}))
	})

	t.Run("odd_drops_last", func(t *testing.T) {
		got := kvToFields([]any{"k1", 1, "orphan"})
		require.Len(t, got, 1)
		require.Equal(t, "k1", got[0].Key)
		require.Equal(t, 1, got[0].Value)
	})

	t.Run("non_string_key_skipped", func(t *testing.T) {
		got := kvToFields([]any{42, "v", "k", "v2"})
		require.Len(t, got, 1)
		require.Equal(t, "k", got[0].Key)
		require.Equal(t, "v2", got[0].Value)
	})

	t.Run("complex_value_types", func(t *testing.T) {
		got := kvToFields([]any{"map", map[string]int{"a": 1}, "slice", []int{1, 2}})
		require.Len(t, got, 2)
		require.Equal(t, map[string]int{"a": 1}, got[0].Value)
	})
}

func TestBearerRoundTripper(t *testing.T) {
	t.Run("sets_authorization_header", func(t *testing.T) {
		var captured string
		ts := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			captured = r.Header.Get("Authorization")
		}))
		defer ts.Close()

		client := &http.Client{Transport: BearerRoundTripper("secret-tok", http.DefaultTransport)}
		resp, err := client.Get(ts.URL)
		require.NoError(t, err)
		_ = resp.Body.Close()
		require.Equal(t, "Bearer secret-tok", captured)
	})

	t.Run("preserves_base", func(t *testing.T) {
		base := http.DefaultTransport
		rt := BearerRoundTripper("t", base)
		require.NotNil(t, rt)
	})

	t.Run("empty_token_no_header", func(t *testing.T) {
		var captured string
		ts := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			captured = r.Header.Get("Authorization")
		}))
		defer ts.Close()
		client := &http.Client{Transport: BearerRoundTripper("", http.DefaultTransport)}
		resp, err := client.Get(ts.URL)
		require.NoError(t, err)
		_ = resp.Body.Close()
		require.Empty(t, captured)
	})
}
