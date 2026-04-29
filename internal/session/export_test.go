package session

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"nncode/internal/llm"
)

func TestExportMarkdown(t *testing.T) {
	sess := &Session{
		ID: "test-session",
		Messages: []llm.Message{
			{Role: llm.RoleSystem, Content: "system prompt"},
			{Role: llm.RoleUser, Content: "hello"},
			{
				Role:    llm.RoleAssistant,
				Content: "hi",
				Usage:   llm.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
			},
			{
				Role:    llm.RoleAssistant,
				Content: "using tool",
				ToolCalls: []llm.ToolCall{
					{ID: "call-1", Name: "read", Args: []byte(`{"path":"main.go"}`)},
				},
			},
			{Role: llm.RoleTool, Content: "file contents", ToolCallID: "call-1", ToolName: "read"},
		},
	}

	md := ExportMarkdown(sess)
	assert.Contains(t, md, "# Session test-session")
	assert.Contains(t, md, "## system")
	assert.Contains(t, md, "system prompt")
	assert.Contains(t, md, "## user")
	assert.Contains(t, md, "hello")
	assert.Contains(t, md, "## assistant")
	assert.Contains(t, md, "hi")
	assert.Contains(t, md, "**Tool calls:**")
	assert.Contains(t, md, "`read` (call-1)")
	assert.Contains(t, md, "```json")
	assert.Contains(t, md, "*Tokens: 10 prompt / 5 completion / 15 total*")
	assert.Contains(t, md, "## read")
	assert.Contains(t, md, "*Call ID: call-1*")
	assert.Contains(t, md, "```")
	assert.Contains(t, md, "file contents")
}

func TestExportMarkdownEmpty(t *testing.T) {
	sess := &Session{ID: "empty"}
	md := ExportMarkdown(sess)
	assert.Contains(t, md, "# Session empty")
	assert.Contains(t, md, "Messages: 0")
}
