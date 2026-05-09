package mcpx

import "errors"

// MultiplexerConfig is the top-level config for New().
type MultiplexerConfig struct {
	Servers []ServerConfig `json:"servers"`
	// KindSettings provides shared transformer/field-map defaults for every
	// server with the matching Kind. Per-server settings take precedence.
	KindSettings map[string]KindSettings `json:"kind_settings,omitempty"`
	// KindHints is opaque metadata returned via KindGroup.Hints — useful for
	// downstream prompt generation, e.g. "kubernetes" -> ["use kubectl_logs for logs"].
	KindHints map[string][]string `json:"kind_hints,omitempty"`
}

// KindSettings holds shared defaults for all servers of a given kind.
type KindSettings struct {
	ArgsTransformers ArgsTransformers  `json:"args_transformer,omitempty"`
	FieldMap         map[string]string `json:"field_map,omitempty"`
}

// TransportType selects the underlying MCP transport.
type TransportType string

const (
	TransportStdio TransportType = "stdio"
	TransportHTTP  TransportType = "http"
	TransportSSE   TransportType = "sse"
)

// ServerConfig describes one MCP server.
type ServerConfig struct {
	Name string `json:"name"`
	// Kind is an optional semantic label grouping servers of the same type
	// (e.g. "kubernetes", "gitlab"). Empty kind is treated as a unique kind
	// equal to the server name.
	Kind      string        `json:"kind,omitempty"`
	Transport TransportType `json:"transport"`

	// Stdio only.
	Binary string   `json:"binary,omitempty"`
	Args   []string `json:"args,omitempty"`
	Env    []string `json:"env,omitempty"` // additional env vars for the subprocess

	// HTTP/SSE only.
	URL   string `json:"url,omitempty"`
	Token string `json:"token,omitempty"`
	// TokenHeader is the HTTP header name for the token. When empty or set to
	// "Authorization", the token is sent as `Authorization: Bearer <token>`.
	// For servers that expect the raw token in a custom header (e.g.
	// `X-MCP-AUTH: <token>`), set TokenHeader to that header name.
	TokenHeader string `json:"token_header,omitempty"`

	// ArgsTransformers is an ordered list of transformations applied to tool
	// arguments before sending. Built-in names: "camelCase", "joinArrays",
	// "singularResourceType". Custom names registered via
	// WithArgsTransformer are also resolved here.
	ArgsTransformers ArgsTransformers `json:"args_transformer,omitempty"`
	// FieldMap renames argument keys (top-level only) before sending.
	FieldMap map[string]string `json:"field_map,omitempty"`
}

func (c ServerConfig) withKindDefaults(ks KindSettings) ServerConfig {
	if len(c.ArgsTransformers) == 0 {
		c.ArgsTransformers = ks.ArgsTransformers
	}
	if len(c.FieldMap) == 0 {
		c.FieldMap = ks.FieldMap
	}
	return c
}

func (c ServerConfig) validate() error {
	if c.Name == "" {
		return errors.New("mcpx: server name is required")
	}
	switch c.Transport {
	case TransportStdio:
		if c.Binary == "" {
			return errors.New("mcpx: binary is required for stdio transport")
		}
	case TransportHTTP, TransportSSE:
		if c.URL == "" {
			return errors.New("mcpx: url is required for http/sse transport")
		}
	default:
		return errors.New("mcpx: unknown transport type: " + string(c.Transport))
	}
	return nil
}
