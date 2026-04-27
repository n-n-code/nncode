package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// View implements tea.Model.
func (m *model) View() string {
	if m.width <= 0 || m.height <= 0 {
		return ""
	}

	body := fillBlock(Body, m.viewport.View(), m.width, m.viewport.Height)
	if m.overlay != overlayNone {
		body = m.bodyWithOverlay()
	}

	return m.frame(body)
}

// frame renders the main chrome (header, dividers, status bar, textarea)
// around an arbitrary body region. Callers swap the body for an
// overlay-composited region when a modal is open.
func (m *model) frame(body string) string {
	divider := Divider.Render(strings.Repeat("─", m.width))

	var builder strings.Builder

	builder.WriteString(m.headerView())
	builder.WriteString("\n")
	builder.WriteString(divider)
	builder.WriteString("\n")
	builder.WriteString(body)
	builder.WriteString("\n")
	builder.WriteString(divider)
	builder.WriteString("\n")
	builder.WriteString(m.statusView())
	builder.WriteString("\n")
	builder.WriteString(divider)
	builder.WriteString("\n")
	builder.WriteString(fillBlock(Input, m.textarea.View(), m.width, max(m.textarea.Height(), textareaMinLines)))

	return builder.String()
}

// bodyWithOverlay places the active modal centered within a body-sized region
// so the surrounding chrome stays visible behind it.
func (m *model) bodyWithOverlay() string {
	box := m.overlayView()
	if box == "" {
		return m.viewport.View()
	}

	height := max(m.viewport.Height, 1)
	placed := lipgloss.Place(m.width, height, lipgloss.Center, lipgloss.Center, box)

	return fillBlock(Body, placed, m.width, height)
}
