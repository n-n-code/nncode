package contextprint

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"nncode/internal/llm"
)

func TestFormat_SystemPrompt(t *testing.T) {
	out := Format("system prompt", nil)
	assert.Contains(t, out, "[system]")
	assert.Contains(t, out, "system prompt")
}

func TestFormat_UserAndAssistant(t *testing.T) {
	out := Format("", []llm.Message{
		{Role: llm.RoleUser, Content: "hello"},
		{Role: llm.RoleAssistant, Content: "hi"},
	})

	assert.Contains(t, out, "[user]")
	assert.Contains(t, out, "hello")
	assert.Contains(t, out, "[assistant]")
	assert.Contains(t, out, "hi")
}

func TestFormat_ToolResultWithIDs(t *testing.T) {
	out := Format("", []llm.Message{
		{Role: llm.RoleTool, Content: "result data", ToolCallID: "call-1", ToolName: "read"},
	})

	assert.Contains(t, out, "[tool id=call-1 name=read]")
	assert.Contains(t, out, "result data")
}

func TestFormat_AssistantWithToolCalls(t *testing.T) {
	out := Format("", []llm.Message{
		{
			Role:    llm.RoleAssistant,
			Content: "using a tool",
			ToolCalls: []llm.ToolCall{
				{ID: "call-1", Name: "read", Args: []byte(`{"path":"README.md"}`)},
			},
		},
	})

	assert.Contains(t, out, "[assistant]")
	assert.Contains(t, out, "using a tool")
	assert.Contains(t, out, "tool_calls:")
	assert.Contains(t, out, "- id=call-1 name=read args={\"path\":\"README.md\"}")
}

func TestFormat_Empty(t *testing.T) {
	out := Format("", nil)
	assert.Empty(t, out)
}

func TestFormat_UnknownRole(t *testing.T) {
	out := Format("", []llm.Message{
		{Role: "custom", Content: "value"},
	})

	assert.Contains(t, out, "[custom]")
	assert.Contains(t, out, "value")
}
