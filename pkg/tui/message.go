package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"nncode/internal/skills"
)

// msgKind classifies a conversation item for rendering.
type msgKind int

const (
	kindUser msgKind = iota
	kindAssistant
	kindToolCall
	kindToolResult
	kindError
	kindLoopStatus
)

// msgItem is a single renderable unit in the conversation history.
type msgItem struct {
	Kind     msgKind
	Text     string // user, assistant, error
	ToolName string // tool call / result
	ToolArgs string // tool call
	Result   string // tool result
	IsError  bool   // tool result
	Expanded bool   // tool result expanded state
}

// Render returns the styled string for this message item.
func (m msgItem) Render(width int) string {
	switch m.Kind {
	case kindUser:
		return renderUserMsg(m.Text, width)
	case kindAssistant:
		return renderAssistantMsg(m.Text, width)
	case kindToolCall:
		return renderToolCall(m.ToolName, m.ToolArgs, width)
	case kindToolResult:
		return renderToolResult(m.ToolName, m.Result, m.IsError, m.Expanded, width)
	case kindError:
		return Error.Render("error: " + m.Text)
	case kindLoopStatus:
		return renderLoopStatus(m.Text, width)
	default:
		return m.Text
	}
}

func renderUserMsg(text string, width int) string {
	if text == "" {
		return ""
	}

	prefix := Prompt.Render("> ")
	prefixWidth := lipgloss.Width(prefix)
	contPrefix := Prompt.Render(strings.Repeat(" ", prefixWidth))
	padding := lipgloss.Width(UserMsg.Render(""))
	available := max(width-prefixWidth-padding, 1)

	wrapped := wrapLines(text, available)
	lines := strings.Split(wrapped, "\n")
	var builder strings.Builder

	for index, line := range lines {
		if index > 0 {
			builder.WriteString("\n")
			builder.WriteString(contPrefix)
		} else {
			builder.WriteString(prefix)
		}

		if lipgloss.Width(line) < available {
			line += strings.Repeat(" ", available-lipgloss.Width(line))
		}

		builder.WriteString(UserMsg.Render(line))
	}

	return builder.String()
}

func renderAssistantMsg(text string, width int) string {
	if text == "" {
		return ""
	}

	padding := lipgloss.Width(AssistantMsg.Render(""))
	available := max(width-padding, 1)
	wrapped := wrapLines(text, available)
	lines := strings.Split(wrapped, "\n")
	var builder strings.Builder

	for index, line := range lines {
		if index > 0 {
			builder.WriteString("\n")
		}

		if lipgloss.Width(line) < available {
			line += strings.Repeat(" ", available-lipgloss.Width(line))
		}

		builder.WriteString(AssistantMsg.Render(line))
	}

	return builder.String()
}

func renderToolCall(name, args string, width int) string {
	if name == "" {
		return ""
	}

	padding := lipgloss.Width(ToolCall.Render(""))
	preview := "▸ " + name
	if args != "" {
		preview += " " + truncateInline(args, max(width-lipgloss.Width(preview)-padding, 1))
	}

	if lipgloss.Width(preview) < width-padding {
		preview += strings.Repeat(" ", width-padding-lipgloss.Width(preview))
	}

	return ToolCall.Render(preview)
}

func renderToolResult(name, result string, isError, expanded bool, width int) string {
	if name == "" {
		return ""
	}

	prefix := "  "
	if isError {
		prefix += "✗ "
	} else {
		prefix += "✓ "
	}

	clean := skills.StripActivationMarkers(result)
	clean = strings.ReplaceAll(clean, "\n", " ")
	clean = strings.TrimSpace(clean)

	if expanded {
		// Show full result in a code block style.
		block := CodeBlock.Render(wrapLines(result, max(width-codeBlockPadding, 1)))

		return prefix + block
	}

	padding := lipgloss.Width(ToolResult.Render(""))
	available := max(width-lipgloss.Width(prefix)-padding, 1)
	preview := truncateInline(clean, available)
	if preview == "" {
		preview = "(no output)"
	}

	if lipgloss.Width(preview) < available {
		preview += strings.Repeat(" ", available-lipgloss.Width(preview))
	}

	if isError {
		return Error.Render(prefix + preview)
	}

	return ToolResult.Render(prefix + preview)
}

func renderLoopStatus(text string, width int) string {
	if text == "" {
		return ""
	}

	prefix := "↻ "
	padding := lipgloss.Width(ToolResult.Render(""))
	available := max(width-lipgloss.Width(prefix)-padding, 1)
	preview := truncateInline(text, available)
	if lipgloss.Width(preview) < available {
		preview += strings.Repeat(" ", available-lipgloss.Width(preview))
	}

	return ToolResult.Render(prefix + preview)
}

// wrapLines wraps text to width using rune-width and ANSI-escape aware logic.
//
// Prefers word boundaries; falls back to hard breaks for over-long runs.
func wrapLines(text string, width int) string {
	if width <= 0 {
		return text
	}

	return ansi.Wrap(text, width, " -")
}

// truncateInline shortens value to the given display width, appending an
// ellipsis when truncation occurs. Width is measured by display cells (not bytes).
func truncateInline(value string, limit int) string {
	if limit <= 0 {
		return ""
	}

	if lipgloss.Width(value) <= limit {
		return value
	}

	return ansi.Truncate(value, limit, "…")
}
