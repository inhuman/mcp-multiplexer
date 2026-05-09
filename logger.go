package mcpx

// Logger is the minimal logging interface mcpx uses. Implement it once and
// pass via WithLogger. Adapters for zap and log/slog ship in subpackages
// log/zaplog and log/sloglog respectively.
type Logger interface {
	Debug(msg string, fields ...Field)
	Info(msg string, fields ...Field)
	Warn(msg string, fields ...Field)
	Error(msg string, fields ...Field)
}

// Field is a structured key/value pair logged alongside a message.
type Field struct {
	Key   string
	Value any
}

// F is a convenience constructor for Field.
func F(key string, value any) Field { return Field{Key: key, Value: value} }

// NopLogger returns a Logger that discards all messages.
func NopLogger() Logger { return nopLogger{} }

type nopLogger struct{}

func (nopLogger) Debug(string, ...Field) {}
func (nopLogger) Info(string, ...Field)  {}
func (nopLogger) Warn(string, ...Field)  {}
func (nopLogger) Error(string, ...Field) {}
