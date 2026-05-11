package mcpx

import (
	"runtime/debug"
	"time"
)

// Option configures a Multiplexer at construction time.
type Option func(*options)

type options struct {
	logger              Logger
	beforeCall          []BeforeCallHook
	afterCall           []AfterCallHook
	resultTransform     []ResultTransformHook
	metaEnrichers       []MetaEnricher
	customTransformers  map[string]CustomTransformer
	resourceSingular    map[string]string
	authFunc            AuthFunc
	metrics             Metrics
	callTimeout         time.Duration
	httpRetryMax        int
	clientName          string
	clientVersion       string
	healthCheckInterval time.Duration // 0 = disabled
	healthCheckSet      bool          // true when WithHealthCheck was called
	onReconnect         OnReconnectFunc
	onToolsChanged      OnToolsChangedFunc
}

func defaultOptions() *options {
	return &options{
		logger:             NopLogger(),
		customTransformers: make(map[string]CustomTransformer),
		metrics:            nopMetrics{},
		callTimeout:        30 * time.Second,
		httpRetryMax:       5,
		clientName:         "mcpx",
		clientVersion:      defaultClientVersion(),
	}
}

// defaultClientVersion reads the consuming module's version from build info.
// Falls back to "dev" when build info is unavailable or the version is the
// Go sentinel "(devel)" (produced by go run and untagged builds).
func defaultClientVersion() string {
	if bi, ok := debug.ReadBuildInfo(); ok {
		if v := bi.Main.Version; v != "" && v != "(devel)" {
			return v
		}
	}
	return "dev"
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

// WithAuthFunc registers the global [AuthFunc] applied to every server
// whose [ServerConfig.Auth] is non-nil. It is REQUIRED whenever any
// server has Auth set; otherwise [New] returns an error before opening
// any connection.
//
// There is no per-server registry — dispatch on data["scheme"] (or the
// server name) inside the function if multiple schemes coexist.
//
// Calling WithAuthFunc more than once overwrites the previous value;
// no chaining is provided.
func WithAuthFunc(fn AuthFunc) Option {
	return func(o *options) { o.authFunc = fn }
}

// WithHealthCheck enables the liveness supervisor. The supervisor probes each
// server at the given interval using a ListTools call and reconnects with
// exponential backoff (1 s → 2 s → … → 60 s) on failure.
// interval must be positive; zero or negative values cause [New] to return an error.
// Without this option the supervisor does not start and [Multiplexer.ServerStatus]
// always returns [ServerStateConnected] for every registered server.
func WithHealthCheck(interval time.Duration) Option {
	return func(o *options) {
		o.healthCheckInterval = interval
		o.healthCheckSet = true
	}
}

// WithOnReconnect registers a callback invoked on every reconnect attempt.
// err is nil on success, non-nil on failure. Registering more than once
// overwrites the previous value. The callback runs synchronously from the
// supervisor goroutine and must not block for extended periods.
func WithOnReconnect(fn OnReconnectFunc) Option {
	return func(o *options) { o.onReconnect = fn }
}

// OnToolsChangedFunc is called after a successful tool-list refresh that
// produces a different set of tools than the previously cached list.
// server is the name of the server whose tool list changed.
// before is a snapshot of the tool list prior to the refresh.
// after is the updated tool list.
//
// The callback runs synchronously from the per-server drain goroutine and
// must not block for extended periods. Panics inside the callback are
// recovered by the library; the multiplexer continues operating normally.
type OnToolsChangedFunc func(server string, before, after []ToolInfo)

// WithOnToolsChanged registers a callback invoked after each successful
// tool-list refresh that changes the cached tool list for a server.
// The callback receives the server name and before/after snapshots.
// Registering more than once overwrites the previous value; passing nil
// clears any previously registered callback.
func WithOnToolsChanged(fn OnToolsChangedFunc) Option {
	return func(o *options) { o.onToolsChanged = fn }
}

// WithMetrics registers a [Metrics] implementation that receives call-level
// and tool-list events. Passing nil is a no-op (leaves the default no-op
// implementation in place). Calling WithMetrics more than once overwrites
// the previous value.
func WithMetrics(m Metrics) Option {
	return func(o *options) {
		if m != nil {
			o.metrics = m
		}
	}
}

// WithResourceSingular merges m into the global custom singular map used by
// the "singularResourceType" argument transformer. Entries in m override
// built-in entries with the same key. Passing nil or an empty map is a no-op.
// Call multiple times to accumulate entries (each call merges, not replaces).
func WithResourceSingular(m map[string]string) Option {
	return func(o *options) {
		if len(m) == 0 {
			return
		}
		if o.resourceSingular == nil {
			o.resourceSingular = make(map[string]string, len(m))
		}
		for k, v := range m {
			o.resourceSingular[k] = v
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
