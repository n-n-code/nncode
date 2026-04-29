package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"nncode/internal/agent"
	"nncode/internal/agentloop"
	"nncode/internal/llm"
	"nncode/internal/session"
)

const (
	overlayFooterClose       = "esc/q close"
	overlayFooterSelect      = "enter select  esc/q close  up/down navigate"
	overlayFooterScroll      = "esc/q close  up/down scroll"
	overlayHorizontalPadding = 4 // Overlay has Padding(1, 2): 2 left + 2 right.
	loopDetailDivisor        = 3
	loopDetailMinRows        = 3
	previewMaxLen            = 40
	idMaxLen                 = 12
	hoursPerDay              = 24
	daysPerWeek              = 7
)

func (m *model) handleOverlayKey(msg tea.KeyMsg) (*model, tea.Cmd) {
	if m.overlay == overlayConfirm {
		var decision agent.ConfirmDecision
		switch msg.String() {
		case "y", "Y":
			decision = agent.ConfirmAllow
		case "n", "N":
			decision = agent.ConfirmStop
		case "q", "Q", "esc":
			decision = agent.ConfirmSkip
		default:
			// Ignore other keys while waiting for confirmation. The generic
			// esc/q dismiss path below would leak the response channel.
			return m, nil
		}

		m.pendingConfirm.Resp <- decision
		m.pendingConfirm = nil
		m.dismissOverlay()

		return m, m.waitForConfirmReq()
	}

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
	case overlayLoops:
		if m.overlayIndex < len(m.loopSummaries) {
			summary := m.loopSummaries[m.overlayIndex]
			if summary.Err != nil {
				m.appendMessage(msgItem{Kind: kindError, Text: "Agent Loop invalid: " + summary.Err.Error()})

				return m, nil
			}

			m.dismissOverlay()

			updated, cmd := m.handleLoopCommand("/loop " + summary.Ref)
			next, ok := updated.(*model)
			if !ok {
				return m, cmd
			}

			return next, cmd
		}
	case overlayHelp, overlayTools, overlayPrompt, overlaySessionInfo:
		m.dismissOverlay()
	case overlayConfirm:
		// Unreachable: confirm overlay key handler returns before this switch.
		// Listed so callers reading the switch see the case is intentional.
	default:
		// no-op
	}

	return m, nil
}

func (m *model) dismissOverlay() {
	m.overlay = overlayNone
	m.overlayItems = nil
	m.overlayIndex = 0
	m.loopSummaries = nil
}

func (m *model) openHelpOverlay() {
	m.overlayItems = helpOverlayItems()
	m.overlayIndex = 0
	m.overlay = overlayHelp
}

func (m *model) openSessionOverlay() {
	items := []string{
		"ID:       " + m.sess.ID,
		"Messages: " + strconv.Itoa(len(m.agent.Messages())),
	}
	stats := m.computeTokenStats()
	items = append(items, "Tokens (last turn): "+strconv.Itoa(stats.lastTurn))
	items = append(items, "Tokens (session):   "+strconv.Itoa(stats.sessionTotal))
	if len(stats.byModel) > 0 {
		items = append(items, "Per model:")
		for modelID, count := range stats.byModel {
			items = append(items, "  "+modelID+": "+strconv.Itoa(count))
		}
	}

	var turns []string
	for _, msg := range m.agent.Messages() {
		if msg.Role != llm.RoleAssistant {
			continue
		}
		turnNum, _ := msg.Metadata["turn"].(int)
		line := fmt.Sprintf("  turn %d: %d tokens (%s)", turnNum, msg.Usage.TotalTokens, msg.Model)
		if loopName, ok := msg.Metadata["loop_name"].(string); ok {
			line += fmt.Sprintf(" [%s node=%s]", loopName, msg.Metadata["loop_node_id"])
		}
		turns = append(turns, line)
	}
	if len(turns) > 0 {
		items = append(items, "Turns:")
		items = append(items, turns...)
	}

	if m.sess.FilePath != "" {
		items = append(items, "File:     "+m.sess.FilePath)
	}
	m.overlayItems = items
	m.overlayIndex = 0
	m.overlay = overlaySessionInfo
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
				fmt.Sprintf("%-18s (unreadable: %v)", filepath.Base(file), err))
			continue
		}

		info, statErr := os.Stat(file)
		age := ""
		if statErr == nil {
			age = formatAge(time.Since(info.ModTime()))
		}

		tokens, model := sessionStats(loaded.Messages)
		preview := firstUserPreview(loaded.Messages)
		id := loaded.ID
		if len(id) > idMaxLen {
			id = id[:idMaxLen]
		}
		modelStr := model
		if len(modelStr) > 10 {
			modelStr = modelStr[:10]
		}

		m.overlayItems = append(m.overlayItems,
			fmt.Sprintf("%-12s %3d msgs %6s %10s %4s  %s",
				id, len(loaded.Messages), formatTokenCount(tokens), modelStr, age, preview))
	}
	m.overlayIndex = 0
	m.overlay = overlaySessions
}

// formatAge returns a compact human-readable duration.
func formatAge(dur time.Duration) string {
	switch {
	case dur < time.Minute:
		return "now"
	case dur < time.Hour:
		return fmt.Sprintf("%dm", int(dur.Minutes()))
	case dur < hoursPerDay*time.Hour:
		return fmt.Sprintf("%dh", int(dur.Hours()))
	case dur < daysPerWeek*hoursPerDay*time.Hour:
		return fmt.Sprintf("%dd", int(dur.Hours()/hoursPerDay))
	default:
		return fmt.Sprintf("%dw", int(dur.Hours()/hoursPerDay/daysPerWeek))
	}
}

// firstUserPreview returns the first previewMaxLen chars of the first user message.
func firstUserPreview(msgs []llm.Message) string {
	for _, msg := range msgs {
		if msg.Role == llm.RoleUser {
			text := strings.TrimSpace(msg.Content)
			if len(text) > previewMaxLen {
				return text[:previewMaxLen] + "…"
			}
			return text
		}
	}
	return ""
}

// sessionStats computes total tokens and the dominant model from a session.
func sessionStats(msgs []llm.Message) (int, string) {
	var total int
	var dominant string
	byModel := make(map[string]int)
	for _, msg := range msgs {
		if msg.Role == llm.RoleAssistant {
			total += msg.Usage.TotalTokens
			byModel[msg.Model] += msg.Usage.TotalTokens
		}
	}
	most := 0
	for model, count := range byModel {
		if count > most {
			most = count
			dominant = model
		}
	}
	return total, dominant
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

func (m *model) loadLoopsOverlay() {
	m.overlayItems = nil
	m.loopSummaries = nil

	summaries, err := agentloop.List(agentloop.StoreOptions{})
	if err != nil {
		m.appendMessage(msgItem{Kind: kindError, Text: "Failed to list Agent Loops: " + err.Error()})

		return
	}

	if len(summaries) == 0 {
		m.appendMessage(msgItem{Kind: kindAssistant, Text: "No Agent Loops configured."})

		return
	}

	m.overlayItems = make([]string, 0, len(summaries))
	m.loopSummaries = summaries
	for _, summary := range summaries {
		if summary.Err != nil {
			m.overlayItems = append(m.overlayItems,
				fmt.Sprintf("%-20s [%s] ERR %s", summary.Ref, summary.Scope,
					truncateInline(summary.Err.Error(), descriptionPreviewLen)))

			continue
		}

		description := summary.Description
		if summary.Name != summary.Ref {
			description = "name: " + summary.Name + "  " + description
		}

		m.overlayItems = append(m.overlayItems,
			fmt.Sprintf("%-20s [%s] OK  %s", summary.Ref, summary.Scope,
				truncateInline(description, descriptionPreviewLen)))
	}
	m.overlayIndex = 0
	m.overlay = overlayLoops
}

//nolint:gochecknoglobals // Static lookup table for overlayKind→title.
var overlayTitles = map[overlayKind]string{
	overlayHelp:        "Help",
	overlaySessions:    "Sessions",
	overlaySkills:      "Skills",
	overlayTools:       "Tools",
	overlayLoops:       "Agent Loops",
	overlayPrompt:      "System Prompt",
	overlaySessionInfo: "Session",
	// overlayConfirm intentionally omitted; confirmOverlayContent renders a
	// dynamic title that includes the turn number.
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

	if m.overlay == overlayLoops {
		return m.loopOverlayContent(width, height)
	}

	if m.overlay == overlayConfirm {
		return m.confirmOverlayContent(width, height)
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

func (m *model) loopOverlayContent(width, height int) string {
	rowWidth := max(width-barHorizontalPadding*2, 1)
	rows := m.overlayRows(rowWidth)
	footer := m.overlayFooter()
	rowLimit := overlayRowLimit(height, footer)
	detailRows := m.selectedLoopDetailRows(rowWidth)
	detailLimit := min(len(detailRows), max(rowLimit/loopDetailDivisor, loopDetailMinRows))
	listLimit := max(rowLimit-detailLimit-1, 1)
	start, end := m.overlayWindow(len(rows), listLimit)

	var builder strings.Builder
	builder.WriteString(Title.Width(width).Render(overlayTitles[m.overlay]))
	builder.WriteString("\n")
	builder.WriteString("\n")

	for index := start; index < end; index++ {
		builder.WriteString(m.overlayRowView(index, rows[index], rowWidth))
		builder.WriteString("\n")
	}

	if len(detailRows) > 0 {
		builder.WriteString(TableCell.Width(rowWidth).Render(""))
		builder.WriteString("\n")
		for index := range min(detailLimit, len(detailRows)) {
			builder.WriteString(TableCell.Width(rowWidth).Render(truncateInline(detailRows[index], rowWidth)))
			builder.WriteString("\n")
		}
	}

	if footer != "" {
		builder.WriteString(OverlayHint.Width(width).Render(truncateInline(footer, width)))
	}

	return fillBlock(OverlaySurface, builder.String(), width, height)
}

func (m *model) selectedLoopDetailRows(width int) []string {
	if m.overlayIndex >= len(m.loopSummaries) {
		return nil
	}

	summary := m.loopSummaries[m.overlayIndex]
	rows := []string{
		"path: " + summary.Path,
	}

	if summary.Err != nil {
		return append(rows, "error: "+summary.Err.Error())
	}

	rows = append(rows,
		fmt.Sprintf("schema: v%d", summary.SchemaVersion),
		"nodes: "+formatNodeSummaries(summary.Nodes),
	)

	description := strings.TrimSpace(summary.Description)
	if summary.Name != summary.Ref {
		rows = append(rows, "name: "+summary.Name)
	}

	if description != "" {
		rows = append(rows, "description: "+truncateInline(description, max(width-len("description: "), 1)))
	}

	return rows
}

func formatNodeSummaries(nodes []agentloop.NodeSummary) string {
	if len(nodes) == 0 {
		return "(none)"
	}

	parts := make([]string, 0, len(nodes))
	for _, node := range nodes {
		label := node.ID + ":" + string(node.Type)
		if node.Locked {
			label += ":locked"
		}
		parts = append(parts, label)
	}

	return strings.Join(parts, " -> ")
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
		{"/skill:name [msg]", "Activate skill, optionally run msg"},
		{"/loops", "Browse configured Agent Loops"},
		{"/loop <name> [msg]", "Run an Agent Loop, optionally with msg"},
		{"/loop-validate <name|path>", "Validate Agent Loop file"},
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
	return m.overlay == overlaySessions || m.overlay == overlaySkills || m.overlay == overlayLoops
}

// confirmOverlayContent renders the tool confirmation overlay.
func (m *model) confirmOverlayContent(width, height int) string {
	// Overlay applies Padding(1, 2); subtract it so inner widgets don't
	// overflow the visible content area.
	effectiveWidth := max(width-overlayHorizontalPadding, 1)

	title := "Confirm?"
	if m.pendingConfirm != nil && m.pendingConfirm.Turn > 0 {
		title = fmt.Sprintf("Confirm?  ·  turn %d", m.pendingConfirm.Turn)
	}

	var builder strings.Builder
	builder.WriteString(Title.Width(effectiveWidth).Render(title))
	builder.WriteString("\n")
	builder.WriteString("\n")

	intro := "The agent wants to execute an effectful tool:"
	builder.WriteString(TableCell.Width(effectiveWidth).Render(truncateInline(intro, effectiveWidth)))
	builder.WriteString("\n")
	builder.WriteString(TableCell.Width(effectiveWidth).Render(""))
	builder.WriteString("\n")

	box := renderToolBox(m.pendingConfirm.Name, m.pendingConfirm.Args, effectiveWidth)
	for line := range strings.SplitSeq(box, "\n") {
		builder.WriteString(fitLine(line, effectiveWidth, OverlaySurface.GetBackground()))
		builder.WriteString("\n")
	}

	builder.WriteString(TableCell.Width(effectiveWidth).Render(""))
	builder.WriteString("\n")
	builder.WriteString(TableCell.Width(effectiveWidth).Render(truncateInline("Allow this action?", effectiveWidth)))
	builder.WriteString("\n")
	builder.WriteString(TableCell.Width(effectiveWidth).Render(""))
	builder.WriteString("\n")

	for line := range strings.SplitSeq(renderConfirmKeys(), "\n") {
		builder.WriteString(fitLine(line, effectiveWidth, OverlaySurface.GetBackground()))
		builder.WriteString("\n")
	}

	return fillBlock(OverlaySurface, builder.String(), effectiveWidth, height)
}

// renderToolBox renders the inner bordered box that summarizes the effectful
// tool the agent wants to run. The border color signals the tool's blast
// radius (amber for bash, green for file edits).
func renderToolBox(name, rawArgs string, width int) string {
	borderColor := ColorCRTGreenSoft
	if name == "bash" {
		borderColor = ColorWarningAmber
	}

	style := lipgloss.NewStyle().
		Foreground(ColorCRTGreen).
		Background(ColorNearBlack).
		Border(lipgloss.NormalBorder()).
		BorderForeground(borderColor).
		BorderBackground(ColorNearBlack).
		Padding(0, 1)

	const (
		borderAndPadding  = 4 // 2 border + 2 horizontal padding
		minToolBoxContent = 8
	)
	innerWidth := max(width-borderAndPadding, minToolBoxContent)

	header := lipgloss.NewStyle().
		Foreground(ColorCRTGreenSoft).
		Background(ColorNearBlack).
		Bold(true).
		Render(name)

	body := formatToolArgs(name, rawArgs, innerWidth)

	// Pad each content line to innerWidth so the bordered box renders as a
	// single uniform rectangle rather than auto-sizing to the longest line.
	bg := lipgloss.NewStyle().Background(ColorNearBlack)
	lines := append([]string{header}, strings.Split(body, "\n")...)
	for i, line := range lines {
		extra := innerWidth - lipgloss.Width(line)
		if extra > 0 {
			lines[i] = line + bg.Render(strings.Repeat(" ", extra))
		}
	}

	return style.Render(strings.Join(lines, "\n"))
}

// formatToolArgs returns a per-tool human-readable summary of the tool args
// suitable for the confirm modal's inner box. It falls back to truncated raw
// JSON for unknown tools or unparseable payloads.
func formatToolArgs(name, rawArgs string, width int) string {
	var parsed map[string]any
	if err := json.Unmarshal([]byte(rawArgs), &parsed); err != nil {
		return truncateInline(rawArgs, width)
	}

	switch name {
	case "bash":
		return formatBashArgs(parsed, rawArgs, width)
	case "write":
		return formatWriteArgs(parsed, rawArgs, width)
	case "edit":
		return formatEditArgs(parsed, rawArgs, width)
	case "patch":
		return formatPatchArgs(parsed, rawArgs, width)
	default:
		return truncateInline(rawArgs, width)
	}
}

func formatBashArgs(parsed map[string]any, rawArgs string, width int) string {
	command, _ := parsed["command"].(string)
	if command == "" {
		return truncateInline(rawArgs, width)
	}

	return truncateInline("$ "+command, width)
}

func formatWriteArgs(parsed map[string]any, rawArgs string, width int) string {
	path, _ := parsed["path"].(string)
	if path == "" {
		path, _ = parsed["file_path"].(string)
	}

	if path == "" {
		return truncateInline(rawArgs, width)
	}

	content, _ := parsed["content"].(string)
	if content == "" {
		content, _ = parsed["text"].(string)
	}

	lines := []string{truncateInline("path: "+path, width)}
	if content != "" {
		lines = append(lines, truncateInline("size: "+humanByteCount(len(content)), width))
	}

	return strings.Join(lines, "\n")
}

func formatEditArgs(parsed map[string]any, rawArgs string, width int) string {
	path, _ := parsed["path"].(string)
	if path == "" {
		path, _ = parsed["file_path"].(string)
	}

	if path == "" {
		return truncateInline(rawArgs, width)
	}

	oldStr, _ := parsed["old_string"].(string)
	newStr, _ := parsed["new_string"].(string)

	lines := []string{truncateInline("path: "+path, width)}
	if oldStr != "" || newStr != "" {
		summary := fmt.Sprintf("replace: %d → %d chars", len(oldStr), len(newStr))
		lines = append(lines, truncateInline(summary, width))
	}

	return strings.Join(lines, "\n")
}

func formatPatchArgs(parsed map[string]any, rawArgs string, width int) string {
	path, _ := parsed["path"].(string)
	if path == "" {
		path, _ = parsed["file_path"].(string)
	}

	if path == "" {
		return truncateInline(rawArgs, width)
	}

	return truncateInline("path: "+path, width)
}

func humanByteCount(n int) string {
	const (
		kilobyte = 1024
		megabyte = 1024 * 1024
	)

	switch {
	case n >= megabyte:
		return fmt.Sprintf("%.1f MB", float64(n)/megabyte)
	case n >= kilobyte:
		return fmt.Sprintf("%.1f KB", float64(n)/kilobyte)
	default:
		return fmt.Sprintf("%d B", n)
	}
}

// renderConfirmKeys returns the three stacked action chips for the confirm
// overlay: y allow, n stop, esc/q find another way.
func renderConfirmKeys() string {
	keyStyle := lipgloss.NewStyle().
		Foreground(ColorCRTGreenSoft).
		Background(ColorNearBlack).
		Bold(true)
	labelStyle := lipgloss.NewStyle().
		Foreground(ColorCRTGreen).
		Background(ColorNearBlack)

	rows := []struct {
		key   string
		label string
	}{
		{"[y]", "Yes, allow"},
		{"[n]", "No, stop"},
		{"[esc/q]", "Find another way"},
	}

	const keyColumnWidth = 9 // wide enough for "[esc/q] " plus a small gap

	out := make([]string, len(rows))
	for i, r := range rows {
		key := keyStyle.Render(r.key)
		pad := strings.Repeat(" ", max(keyColumnWidth-lipgloss.Width(r.key), 1))
		bgPad := lipgloss.NewStyle().Background(ColorNearBlack).Render(pad)
		out[i] = "  " + key + bgPad + labelStyle.Render(r.label)
	}

	return strings.Join(out, "\n")
}

func (m *model) overlayFooter() string {
	if m.overlay == overlayLoops {
		return "enter run  esc/q close  up/down navigate"
	}

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
	if m.overlay == overlayConfirm {
		// The confirm overlay renders action chips inside the body, so no
		// footer row is reserved.
		return ""
	}
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
		"/skill:name [msg]  Activate skill, optionally run msg",
		"/loops             Browse configured Agent Loops",
		"/loop <name> [msg] Run an Agent Loop, optionally with msg",
		"/loop-validate <name|path> Validate Agent Loop file",
		"/prompt            Show the current system prompt",
		"alt+enter          Insert a newline while typing",
		"esc                Leave input focus or close a popup",
	}
}
