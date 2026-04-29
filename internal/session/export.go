package session

import (
	"fmt"
	"strings"

	"nncode/internal/llm"
)

// ExportMarkdown renders a session as a Markdown document.
func ExportMarkdown(sess *Session) string {
	var buf strings.Builder

	fmt.Fprintf(&buf, "# Session %s\n\n", sess.ID)
	fmt.Fprintf(&buf, "Messages: %d\n\n", len(sess.Messages))

	for i, msg := range sess.Messages {
		if i > 0 {
			buf.WriteString("\n")
		}

		writeMessageMarkdown(&buf, msg)
	}

	return buf.String()
}

func writeMessageMarkdown(buf *strings.Builder, msg llm.Message) {
	switch msg.Role {
	case llm.RoleSystem:
		buf.WriteString("## system\n\n")
		buf.WriteString(msg.Content)
		buf.WriteString("\n")
	case llm.RoleUser:
		buf.WriteString("## user\n\n")
		buf.WriteString(msg.Content)
		buf.WriteString("\n")
	case llm.RoleAssistant:
		buf.WriteString("## assistant\n\n")
		if msg.Content != "" {
			buf.WriteString(msg.Content)
			buf.WriteString("\n")
		}

		if len(msg.ToolCalls) > 0 {
			buf.WriteString("\n**Tool calls:**\n\n")
			for _, tc := range msg.ToolCalls {
				fmt.Fprintf(buf, "- `%s` (%s)\n", tc.Name, tc.ID)
				fmt.Fprintf(buf, "  ```json\n  %s\n  ```\n", string(tc.Args))
			}
		}

		if msg.Usage.TotalTokens > 0 {
			fmt.Fprintf(buf, "\n*Tokens: %d prompt / %d completion / %d total*\n",
				msg.Usage.PromptTokens, msg.Usage.CompletionTokens, msg.Usage.TotalTokens)
		}
	case llm.RoleTool:
		label := msg.ToolName
		if label == "" {
			label = "tool"
		}

		fmt.Fprintf(buf, "## %s\n\n", label)
		if msg.ToolCallID != "" {
			fmt.Fprintf(buf, "*Call ID: %s*\n\n", msg.ToolCallID)
		}

		buf.WriteString("```\n")
		buf.WriteString(msg.Content)
		buf.WriteString("\n```\n")
	default:
		fmt.Fprintf(buf, "## %s\n\n", msg.Role)
		buf.WriteString(msg.Content)
		buf.WriteString("\n")
	}
}
