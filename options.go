package mcpx

import "time"

// Option configures a Multiplexer at construction time.
type Option func(*options)

type options struct {
	logger             Logger
	beforeCall         []BeforeCallHook
	afterCall          []AfterCallHook
	resultTransform    []ResultTransformHook
	metaEnrichers      []MetaEnricher
	customTransformers map[string]CustomTransformer
	callTimeout        time.Duration
	httpRetryMax       int
	clientName         string
	clientVersion      string
}

func defaultOptions() *options {
	return &options{
		logger:             NopLogger(),
		customTransformers: make(map[string]CustomTransformer),
		callTimeout:        30 * time.Second,
		httpRetryMax:       5,
		clientName:         "mcpx",
		clientVersion:      "0.1.0",
	}
}

// WithLogger attaches a Logger. Default: NopLogger.
func WithLogger(l Logger) Option {
	return func(o *options) {
		if l != nil {
			o.logger = l
		}
	}
}

// WithBeforeCall registers a hook that runs before every tool call.
// Multiple hooks run in registration order and any error aborts the call.
func WithBeforeCall(h BeforeCallHook) Option {
	return func(o *options) { o.beforeCall = append(o.beforeCall, h) }
}

// WithAfterCall registers a hook that runs after every tool call.
// Multiple hooks run in registration order; their errors are ignored.
func WithAfterCall(h AfterCallHook) Option {
	return func(o *options) { o.afterCall = append(o.afterCall, h) }
}

// WithResultTransform registers a hook that may rewrite a successful result's
// text. Hooks chain in registration order; the first error short-circuits.
func WithResultTransform(h ResultTransformHook) Option {
	return func(o *options) { o.resultTransform = append(o.resultTransform, h) }
}

// WithMetaEnricher registers a hook that adjusts ToolInfo metadata after the
// initial fetch. Multiple enrichers chain in registration order.
func WithMetaEnricher(h MetaEnricher) Option {
	return func(o *options) { o.metaEnrichers = append(o.metaEnrichers, h) }
}

// WithArgsTransformer registers a custom transformer under the given name.
// Reference it from ServerConfig.ArgsTransformers (or KindSettings) by name.
func WithArgsTransformer(name string, fn CustomTransformer) Option {
	return func(o *options) { o.customTransformers[name] = fn }
}

// WithCallTimeout overrides the per-call timeout. Default: 30s.
func WithCallTimeout(d time.Duration) Option {
	return func(o *options) {
		if d > 0 {
			o.callTimeout = d
		}
	}
}

// WithHTTPRetryMax overrides the maximum retries for HTTP/SSE transports.
// Default: 5.
func WithHTTPRetryMax(n int) Option {
	return func(o *options) {
		if n >= 0 {
			o.httpRetryMax = n
		}
	}
}

// WithClientIdentity overrides the MCP client name/version sent during
// handshake. Default: "mcpx" / library version.
func WithClientIdentity(name, version string) Option {
	return func(o *options) {
		if name != "" {
			o.clientName = name
		}
		if version != "" {
			o.clientVersion = version
		}
	}
}
