// Package capturelog provides an in-memory implementation of mcpx.Logger
// that records every event for later assertion. Used by integration and
// security tests.
package capturelog

import (
	"fmt"
	"strings"
	"sync"

	mcpx "github.com/inhuman/mcp-multiplexer"
)

// Level identifies a log level.
type Level int

// Logging levels.
const (
	Debug Level = iota
	Info
	Warn
	Error
)

// String returns a human-readable name of the level.
func (l Level) String() string {
	switch l {
	case Debug:
		return "DEBUG"
	case Info:
		return "INFO"
	case Warn:
		return "WARN"
	case Error:
		return "ERROR"
	}
	return fmt.Sprintf("LEVEL(%d)", int(l))
}

// Entry is one captured log event.
type Entry struct {
	Level   Level
	Message string
	Fields  map[string]any
}

// Logger captures events in memory. Safe for concurrent use.
type Logger struct {
	mu      sync.Mutex
	entries []Entry
}

// New returns an empty capturing logger.
func New() *Logger { return &Logger{} }

// Reset drops all captured events.
func (l *Logger) Reset() {
	l.mu.Lock()
	l.entries = nil
	l.mu.Unlock()
}

// Entries returns a copy of all captured events in arrival order.
func (l *Logger) Entries() []Entry {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]Entry, len(l.entries))
	copy(out, l.entries)
	return out
}

// EntriesAtLevel returns a copy of captured events at the given level.
func (l *Logger) EntriesAtLevel(lvl Level) []Entry {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []Entry
	for _, e := range l.entries {
		if e.Level == lvl {
			out = append(out, e)
		}
	}
	return out
}

// Contains reports whether substr appears in the message of any captured
// event or in the rendered string form of any field value.
func (l *Logger) Contains(substr string) bool {
	if substr == "" {
		return false
	}
	for _, e := range l.Entries() {
		if strings.Contains(e.Message, substr) {
			return true
		}
		for _, v := range e.Fields {
			if strings.Contains(fmt.Sprint(v), substr) {
				return true
			}
		}
	}
	return false
}

// ContainsAtLevel is Contains but limited to the given level.
func (l *Logger) ContainsAtLevel(lvl Level, substr string) bool {
	if substr == "" {
		return false
	}
	for _, e := range l.EntriesAtLevel(lvl) {
		if strings.Contains(e.Message, substr) {
			return true
		}
		for _, v := range e.Fields {
			if strings.Contains(fmt.Sprint(v), substr) {
				return true
			}
		}
	}
	return false
}

// ContainsField reports whether any captured event has a field with the
// given key whose value equals (or stringifies to the same as) value.
func (l *Logger) ContainsField(key string, value any) bool {
	target := fmt.Sprint(value)
	for _, e := range l.Entries() {
		if v, ok := e.Fields[key]; ok {
			if fmt.Sprint(v) == target {
				return true
			}
		}
	}
	return false
}

// Debug records a Debug-level event.
func (l *Logger) Debug(msg string, fields ...mcpx.Field) { l.append(Debug, msg, fields) }

// Info records an Info-level event.
func (l *Logger) Info(msg string, fields ...mcpx.Field) { l.append(Info, msg, fields) }

// Warn records a Warn-level event.
func (l *Logger) Warn(msg string, fields ...mcpx.Field) { l.append(Warn, msg, fields) }

// Error records an Error-level event.
func (l *Logger) Error(msg string, fields ...mcpx.Field) { l.append(Error, msg, fields) }

func (l *Logger) append(lvl Level, msg string, fields []mcpx.Field) {
	fm := make(map[string]any, len(fields))
	for _, f := range fields {
		fm[f.Key] = f.Value
	}
	l.mu.Lock()
	l.entries = append(l.entries, Entry{Level: lvl, Message: msg, Fields: fm})
	l.mu.Unlock()
}
