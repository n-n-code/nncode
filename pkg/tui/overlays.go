package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"nncode/internal/session"
)

const (
	overlayFooterClose       = "esc/q close"
	overlayFooterSelect      = "enter select  esc/q close  up/down navigate"
	overlayFooterScroll      = "esc/q close  up/down scroll"
	overlayHorizontalPadding = 4 // Overlay has Padding(1, 2): 2 left + 2 right.
)

func (m *model) handleOverlayKey(msg tea.KeyMsg) (*model, tea.Cmd) {
	//nolint:exhaustive // We only handle specific overlay keys.
	switch msg.Type {
	case tea.KeyEsc, tea.KeyCtrlC:
		m.dismissOverlay()

		return m, nil
	case tea.KeyUp, tea.KeyShiftTab:
		if m.overlayIndex > 0 {
			m.overlayIndex--
		}

		return m, nil
	case tea.KeyDown, tea.KeyTab:
		if m.overlayIndex < m.overlayMaxIndex() {
			m.overlayIndex++
		}

		return m, nil
	case tea.KeyEnter:
		return m.selectOverlayItem()
	default:
		// no-op
	}

	if msg.String() == "q" {
		m.dismissOverlay()
	}

	return m, nil
}

// selectOverlayItem acts on the currently highlighted overlay item.
func (m *model) selectOverlayItem() (*model, tea.Cmd) {
	//nolint:exhaustive // All relevant overlay kinds are handled.
	switch m.overlay {
	case overlaySessions:
		if m.overlayIndex < len(m.overlayItems) {
			// Items are formatted like "id  messages  path"
			fields := strings.Fields(m.overlayItems[m.overlayIndex])
			if len(fields) > 0 {
				m.dismissOverlay()
				m.resumeSession(fields[0])
			}
		}
	case overlaySkills:
		if m.overlayIndex < len(m.overlayItems) {
			fields := strings.Fields(m.overlayItems[m.overlayIndex])
			if len(fields) > 0 {
				name := fields[0]
				m.dismissOverlay()
				_, cmd := m.handleSkillCommand("/skill:" + name)

				return m, cmd
			}
		}
	case overlayHelp, overlayTools, overlayPrompt, overlaySessionInfo:
		m.dismissOverlay()
	default:
		// no-op
	}

	return m, nil
}

func (m *model) dismissOverlay() {
	m.overlay = overlayNone
	m.overlayItems = nil
	m.overlayIndex = 0
}

func (m *model) openHelpOverlay() {
	m.overlayItems = helpOverlayItems()
	m.overlayIndex = 0
	m.overlay = overlayHelp
}

// loadSessionsOverlay populates the overlay with saved sessions.
func (m *model) loadSessionsOverlay() {
	files, err := session.List()
	if err != nil {
		m.appendMessage(msgItem{Kind: kindError, Text: "Failed to list sessions: " + err.Error()})
		return
	}

	if len(files) == 0 {
		m.appendMessage(msgItem{Kind: kindAssistant, Text: "No saved sessions."})
		return
	}

	m.overlayItems = make([]string, 0, len(files))
	for _, file := range files {
		loaded, err := session.Load(file)
		if err != nil {
			m.overlayItems = append(m.overlayItems,
				fmt.Sprintf("%-24s (unreadable: %v)", file, err))
			continue
		}
		m.overlayItems = append(m.overlayItems,
			fmt.Sprintf("%-24s %4d messages  %s", loaded.ID, len(loaded.Messages), file))
	}
	m.overlayIndex = 0
	m.overlay = overlaySessions
}

// loadToolsOverlay populates the overlay with available tools.
func (m *model) loadToolsOverlay() {
	if len(m.agent.Tools()) == 0 {
		m.appendMessage(msgItem{Kind: kindAssistant, Text: "No tools available."})
		return
	}

	m.overlayItems = make([]string, 0, len(m.agent.Tools()))
	for _, tool := range m.agent.Tools() {
		m.overlayItems = append(m.overlayItems,
			fmt.Sprintf("%-12s %s", tool.Name, tool.Description))
	}
	m.overlayIndex = 0
	m.overlay = overlayTools
}

// loadSkillsOverlay populates the overlay with discovered skills.
func (m *model) loadSkillsOverlay() {
	if m.skillRegistry == nil || len(m.skillRegistry.Skills()) == 0 {
		m.appendMessage(msgItem{Kind: kindAssistant, Text: "No Agent Skills discovered."})
		return
	}

	m.overlayItems = make([]string, 0, len(m.skillRegistry.Skills()))
	for _, skill := range m.skillRegistry.Skills() {
		visibility := "model"
		if skill.DisableModelInvocation {
			visibility = "manual"
		}
		m.overlayItems = append(m.overlayItems,
			fmt.Sprintf("%-20s [%s] %s", skill.Name, visibility,
				truncateInline(skill.Description, descriptionPreviewLen)))
	}
	m.overlayIndex = 0
	m.overlay = overlaySkills
}

//nolint:gochecknoglobals // Static lookup table for overlayKind→title.
var overlayTitles = map[overlayKind]string{
	overlayHelp:        "Help",
	overlaySessions:    "Sessions",
	overlaySkills:      "Skills",
	overlayTools:       "Tools",
	overlayPrompt:      "System Prompt",
	overlaySessionInfo: "Session",
}

// overlayView renders the active overlay as a styled, sized modal box.
//
// The returned string is the box itself; callers are responsible for placing
// it within the surrounding chrome (see view.go).
func (m *model) overlayView() string {
	if m.overlay == overlayNone {
		return ""
	}

	contentWidth := m.overlayContentWidth()
	contentHeight := m.overlayContentHeight()
	content := m.overlayContent(contentWidth, contentHeight)

	return Overlay.Width(contentWidth).Height(contentHeight).Render(content)
}

func (m *model) overlayContent(width, height int) string {
	if m.overlay == overlayHelp {
		return m.helpOverlayContent(width, height)
	}

	rowWidth := max(width-barHorizontalPadding*2, 1)
	rows := m.overlayRows(rowWidth)
	footer := m.overlayFooter()
	rowLimit := overlayRowLimit(height, footer)
	start, end := m.overlayWindow(len(rows), rowLimit)

	var builder strings.Builder
	builder.WriteString(Title.Width(width).Render(overlayTitles[m.overlay]))
	builder.WriteString("\n")
	builder.WriteString("\n")

	for index := start; index < end; index++ {
		builder.WriteString(m.overlayRowView(index, rows[index], rowWidth))
		builder.WriteString("\n")
	}

	if footer != "" {
		builder.WriteString(OverlayHint.Width(width).Render(truncateInline(footer, width)))
	}

	return fillBlock(OverlaySurface, builder.String(), width, height)
}

// helpOverlayContent renders the help modal as an aligned two-column table.
func (m *model) helpOverlayContent(width, height int) string {
	type helpRow struct {
		key  string
		desc string
	}

	rows := []helpRow{
		{"/help", "Show this help"},
		{"/quit, /exit", "Exit the agent"},
		{"/reset", "Clear conversation history"},
		{"/session", "Show current session info"},
		{"/sessions", "List saved sessions"},
		{"/resume <id|path>", "Load a saved session"},
		{"/tools", "List available tools"},
		{"/skills", "List discovered Agent Skills"},
		{"/skill:name [msg]", "Activate a skill, optionally then run msg"},
		{"/prompt", "Show the current system prompt"},
		{"alt+enter", "Insert a newline while typing"},
		{"esc", "Leave input focus or close a popup"},
	}

	maxKey := 0
	for _, r := range rows {
		if w := lipgloss.Width(r.key); w > maxKey {
			maxKey = w
		}
	}

	// Overlay has Padding(1, 2), so effective inner width is smaller by horizontal padding.
	effectiveWidth := max(width-overlayHorizontalPadding, 1)

	keyStyle := lipgloss.NewStyle().
		Foreground(ColorCRTGreenSoft).
		Background(ColorNearBlack).
		Bold(true).
		Width(maxKey+2).
		Padding(0, 1)

	descStyle := lipgloss.NewStyle().
		Foreground(ColorCRTGreen).
		Background(ColorNearBlack).
		Width(effectiveWidth-maxKey-2).
		Padding(0, 1)

	footer := overlayFooterClose
	rowLimit := overlayRowLimit(height, footer)
	start, end := m.overlayWindow(len(rows), rowLimit)

	var builder strings.Builder
	builder.WriteString(Title.Width(effectiveWidth).Render("Help"))
	builder.WriteString("\n")
	builder.WriteString("\n")

	for i := start; i < end && i < len(rows); i++ {
		row := lipgloss.JoinHorizontal(
			lipgloss.Top,
			keyStyle.Render(rows[i].key),
			descStyle.Render(rows[i].desc),
		)
		builder.WriteString(row)
		builder.WriteString("\n")
	}

	builder.WriteString(OverlayHint.Width(effectiveWidth).Render(truncateInline(footer, effectiveWidth)))

	return fillBlock(OverlaySurface, builder.String(), effectiveWidth, height)
}

func (m *model) overlayRows(width int) []string {
	if m.overlay == overlayHelp && len(m.overlayItems) == 0 {
		return helpOverlayItems()
	}

	if m.overlay != overlayPrompt {
		return m.overlayItems
	}

	return wrapOverlayText(m.overlayItems, width)
}

func (m *model) overlayRowView(index int, row string, width int) string {
	row = truncateInline(row, width)
	if m.overlaySelectable() && index == m.overlayIndex {
		return SelectedRow.Width(width).Render(row)
	}

	return TableCell.Width(width).Render(row)
}

func (m *model) overlaySelectable() bool {
	return m.overlay == overlaySessions || m.overlay == overlaySkills
}

func (m *model) overlayFooter() string {
	if m.overlaySelectable() {
		return overlayFooterSelect
	}

	if m.overlayMaxIndex() > 0 {
		return overlayFooterScroll
	}

	return overlayFooterClose
}

func (m *model) overlayWindow(total, limit int) (int, int) {
	if total == 0 || limit <= 0 {
		return 0, 0
	}

	if total <= limit {
		return 0, min(total, limit)
	}

	if !m.overlaySelectable() {
		start := min(m.overlayIndex, total-limit)
		start = max(start, 0)

		return start, start + limit
	}

	start := m.overlayIndex - limit/2
	start = max(start, 0)
	start = min(start, total-limit)

	return start, start + limit
}

func (m *model) overlayMaxIndex() int {
	width := max(m.overlayContentWidth()-barHorizontalPadding*2, 1)
	rows := m.overlayRows(width)
	if m.overlaySelectable() {
		return max(len(rows)-1, 0)
	}

	limit := overlayRowLimit(m.overlayContentHeight(), m.overlayFooterBase())

	return max(len(rows)-limit, 0)
}

func (m *model) overlayContentWidth() int {
	return max(m.width-overlayPadding, 1)
}

func (m *model) overlayContentHeight() int {
	return max(m.viewport.Height-overlayVerticalFrame, 1)
}

func (m *model) overlayFooterBase() string {
	if m.overlaySelectable() {
		return overlayFooterSelect
	}

	return overlayFooterClose
}

func overlayRowLimit(height int, footer string) int {
	const (
		titleRows          = 2
		footerRows         = 1
		minOverlayRowLimit = 1
	)

	reserved := titleRows
	if footer != "" {
		reserved += footerRows
	}

	return max(height-reserved, minOverlayRowLimit)
}

func wrapOverlayText(items []string, width int) []string {
	rows := make([]string, 0, len(items))
	for _, item := range items {
		for line := range strings.SplitSeq(item, "\n") {
			wrapped := wrapLines(line, width)
			rows = append(rows, strings.Split(wrapped, "\n")...)
		}
	}

	return rows
}

func helpOverlayItems() []string {
	return []string{
		"/help              Show this help",
		"/quit, /exit       Exit the agent",
		"/reset             Clear conversation history",
		"/session           Show current session info",
		"/sessions          List saved sessions",
		"/resume <id|path>  Load a saved session",
		"/tools             List available tools",
		"/skills            List discovered Agent Skills",
		"/skill:name [msg]  Activate a skill, optionally then run msg",
		"/prompt            Show the current system prompt",
		"alt+enter          Insert a newline while typing",
		"esc                Leave input focus or close a popup",
	}
}
