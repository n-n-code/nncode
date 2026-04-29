// Package llm defines the minimal interface for streaming LLM completions.
//
// The package is intentionally small: one Client interface, one concrete
// implementation (OpenAIClient) that speaks Chat Completions and therefore
// works against both OpenAI cloud and any OpenAI-compatible server
// (Ollama, LM Studio, vLLM).
package llm

import (
	"context"
	"encoding/json"
)

type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

type Message struct {
	Role       Role           `json:"role"`
	Content    string         `json:"content"`
	ToolCalls  []ToolCall     `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
	ToolName   string         `json:"tool_name,omitempty"`
	Timestamp  int64          `json:"timestamp,omitempty"`
	Usage      Usage          `json:"usage,omitzero"`
	Model      string         `json:"model,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
	IsError    bool           `json:"is_error,omitempty"`
}

type ToolCall struct {
	ID   string          `json:"id"`
	Name string          `json:"name"`
	Args json.RawMessage `json:"args"`
}

type Tool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	// Parameters is a JSON-schema literal (raw JSON). It is emitted verbatim
	// into the outbound request body, so it must be valid JSON.
	Parameters string `json:"parameters"`
}

type Model struct {
	ID      string
	BaseURL string
}

type Request struct {
	Model       Model
	Messages    []Message
	Tools       []Tool
	APIKey      string
	MaxTokens   int
	Temperature float64
}

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// StreamEvent is a tagged union. Exactly one field (Text, ToolStart, ToolEnd,
// Done, or Err) is meaningful per event.
type StreamEvent struct {
	Text      string
	ToolStart *ToolCall
	ToolEnd   *ToolCall
	Done      *Done
	Err       error
}

type Done struct {
	StopReason string
	Usage      Usage
}

// Client streams a single completion request. The returned channel is closed
// when the stream ends. If Stream returns a non-nil error, the channel is nil.
type Client interface {
	Stream(ctx context.Context, req Request) (<-chan StreamEvent, error)
}
