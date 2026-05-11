package mcpx

import "time"

// Metrics is an optional observability sink for the multiplexer. Implement
// this interface to receive call-level and tool-list events and forward them
// to any backend (Prometheus, OpenTelemetry, statsd, …). Register via
// [WithMetrics].
//
// Implementations must be safe for concurrent use. Panics inside any method
// are recovered by the library and do not propagate to callers.
type Metrics interface {
	// RecordCall is invoked after every [Multiplexer.CallTool] invocation.
	// dur is the wall-clock time of the upstream MCP call only (argument
	// validation and hook overhead are excluded).
	// err is nil on success and matches the error returned to the caller.
	RecordCall(server, tool string, dur time.Duration, err error)

	// RecordToolList is invoked after every successful tool-list fetch —
	// both on initial connect and after a live notifications/tools/list_changed
	// refresh. count is the number of tools the server currently exposes.
	RecordToolList(server string, count int)
}

type nopMetrics struct{}

func (nopMetrics) RecordCall(_ string, _ string, _ time.Duration, _ error) {}
func (nopMetrics) RecordToolList(_ string, _ int)                          {}

func safeRecordCall(m Metrics, server, tool string, dur time.Duration, err error) {
	defer func() { recover() }() //nolint:errcheck
	m.RecordCall(server, tool, dur, err)
}

func safeRecordToolList(m Metrics, server string, count int) {
	defer func() { recover() }() //nolint:errcheck
	m.RecordToolList(server, count)
}
