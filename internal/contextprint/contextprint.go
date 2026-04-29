// Package contextprint formats the outbound model context for display.
package contextprint

import (
	"fmt"
	"strings"

	"nncode/internal/llm"
)

// StoredContextNote describes which context is persisted and printable.
const StoredContextNote = "Stored context includes the startup system prompt and saved conversation messages. " +
	"Loop-scoped system messages are generated during loop runs and are not stored here."

// Format renders the startup system prompt followed by stored messages.
func Format(systemPrompt string, messages []llm.Message) string {
	var builder strings.Builder

	if strings.TrimSpace(systemPrompt) != "" {
		writeBlock(&builder, "system", systemPrompt)
	}

	for _, msg := range messages {
		if builder.Len() > 0 {
			builder.WriteString("\n")
		}

		switch msg.Role {
		case llm.RoleSystem, llm.RoleUser, llm.RoleAssistant:
			writeBlock(&builder, string(msg.Role), msg.Content)
		case llm.RoleTool:
			label := "tool"
			if msg.ToolCallID != "" {
				label += " id=" + msg.ToolCallID
			}
			if msg.ToolName != "" {
				label += " name=" + msg.ToolName
			}
			writeBlock(&builder, label, msg.Content)
		default:
			writeBlock(&builder, string(msg.Role), msg.Content)
		}

		if len(msg.ToolCalls) > 0 {
			builder.WriteString("\ntool_calls:")
			for _, call := range msg.ToolCalls {
				builder.WriteString("\n")
				_, _ = fmt.Fprintf(&builder, "- id=%s name=%s args=%s", call.ID, call.Name, string(call.Args))
			}
			builder.WriteString("\n")
		}
	}

	return strings.TrimRight(builder.String(), "\n")
}

func writeBlock(builder *strings.Builder, label string, content string) {
	builder.WriteString("[")
	builder.WriteString(label)
	builder.WriteString("]\n")
	builder.WriteString(content)
	if !strings.HasSuffix(content, "\n") {
		builder.WriteString("\n")
	}
}
