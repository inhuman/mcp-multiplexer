package mcpx

// ToolInfo holds cached metadata about a single tool from an MCP server.
//
// The boolean flags map to MCP tool annotations (ReadOnlyHint, DestructiveHint,
// IdempotentHint, OpenWorldHint). Write is a derived flag — true when the tool
// is not read-only and not destructive (i.e. a non-destructive mutation).
//
// Custom is an open extension point for user-supplied labels added by a
// MetaEnricher hook (e.g. "category=database", "owner=team-a").
type ToolInfo struct {
	Server      string
	Name        string
	Description string
	InputSchema []byte // raw JSON schema

	ReadOnly    bool
	Write       bool
	Destructive bool
	Idempotent  bool
	OpenWorld   bool

	Custom map[string]string
}

// CallResult is the structured outcome of a tool call.
//
// Text is the joined text content of the result. Parts preserves the original
// content blocks (text, image, etc.) so callers can format them however they
// want without losing structure.
type CallResult struct {
	Text    string
	Parts   []ContentPart
	IsError bool
}

// ContentKind identifies the type of content in a CallResult part.
type ContentKind string

const (
	ContentText  ContentKind = "text"
	ContentImage ContentKind = "image"
	ContentOther ContentKind = "other"
)

// ContentPart is one block returned from an MCP tool call.
type ContentPart struct {
	Kind     ContentKind
	Text     string // populated when Kind == ContentText
	MIMEType string // populated when Kind == ContentImage
	Data     []byte // raw image bytes when Kind == ContentImage
	Raw      []byte // JSON-encoded original for ContentOther
}
