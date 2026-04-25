package agent

import (
	"context"
	"encoding/json"
)

// Tool is an executable capability exposed to the LLM. The Parameters field
// is a JSON-schema literal emitted verbatim in the request body; it must be
// valid JSON.
type Tool struct {
	Name        string
	Description string
	Parameters  string
	Execute     func(ctx context.Context, args json.RawMessage) (ToolResult, error)
}

// ToolResult is what a tool returns to the loop. IsError=true surfaces the
// result to the model tagged as an error but does not abort the loop.
type ToolResult struct {
	Content  string
	IsError  bool
	Metadata map[string]any
}
