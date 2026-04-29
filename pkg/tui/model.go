package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"nncode/internal/agent"
	"nncode/internal/agentloop"
	"nncode/internal/config"
	"nncode/internal/llm"
	"nncode/internal/session"
	"nncode/internal/skills"
)

// layout constants (in lines or cells).
const (
	headerHeight          = 1
	statusHeight          = 1
	textareaMinLines      = 3
	dividerHeight         = 1
	defaultTermWidth      = 80
	defaultVPHHeight      = 20
	viewportMinHeight     = 3
	viewportHorizontalPad = 1
	codeBlockPadding      = 4
	statusGapMin          = 1
	previewPadding        = 4
	descriptionPreviewLen = 36
	overlayPadding        = 6
	overlayVerticalFrame  = 4
	barHorizontalPadding  = 1 // matches Padding(0, 1) on HeaderBar/StatusBar.
	dividersInFrame       = 3

	// tokenEstimateDivisor is the rough chars-per-token heuristic used for
	// live completion-token estimates while the stream is in progress.
	tokenEstimateDivisor = 4
	// token formatting thresholds.
	tokenThousand = 1_000
	tokenMillion  = 1_000_000
)

// toolConfirmReq carries an effectful tool confirmation request from the agent.
type toolConfirmReq struct {
	Name string
	Args string
	Turn int
	Resp chan agent.ConfirmDecision
}

// confirmReqMsg is delivered into the Bubble Tea loop when the agent asks for
// confirmation before executing an effectful tool.
type confirmReqMsg struct {
	req toolConfirmReq
}

// overlayKind identifies which modal is active.
type overlayKind int

const (
	overlayNone overlayKind = iota
	overlayHelp
	overlaySessions
	overlaySkills
	overlayTools
	overlayLoops
	overlayPrompt
	overlaySessionInfo
	overlayConfirm
)

// model is the Bubble Tea model for the nncode TUI.
type model struct {
	// Core dependencies.
	agent          *agent.Agent
	cfg            *config.Config
	sess           *session.Session
	skillRegistry  *skills.Registry
	skillActivator *skills.Activator

	// Bubbles components.
	viewport viewport.Model
	textarea textarea.Model
	spinner  spinner.Model

	// Conversation state.
	messages []msgItem
	running  bool

	// Dimensions.
	width  int
	height int

	// Overlay state.
	overlay       overlayKind
	overlayItems  []string
	overlayIndex  int
	loopSummaries []agentloop.Summary

	// Streaming state.
	eventCh     <-chan agent.Event
	turnTextLen int
	inTurn      bool

	// Effectful tool confirmation.
	confirmReqCh   chan toolConfirmReq
	pendingConfirm *toolConfirmReq

	// Turn cancellation.
	runCancel context.CancelFunc

	// Exit/error state.
	err error
}

// newModel creates a fresh TUI model.
func newModel(
	agentM *agent.Agent,
	cfg *config.Config,
	sess *session.Session,
	reg *skills.Registry,
	activator *skills.Activator,
) *model {
	ta := textarea.New()
	ta.Placeholder = "Type a message or /help ..."
	ta.ShowLineNumbers = false
	ta.Prompt = ""
	ta.CharLimit = 0
	ta.SetHeight(textareaMinLines)
	ta.SetWidth(defaultTermWidth)
	ta.Focus()

	// Style the textarea with CRT palette. Inner styles intentionally omit
	// background colors so the outer Input.Render() in fillBlock() can fill
	// every cell uniformly without inner ANSI resets breaking the run.
	ta.FocusedStyle.Base = Input
	ta.FocusedStyle.CursorLine = lipgloss.NewStyle()
	ta.FocusedStyle.EndOfBuffer = lipgloss.NewStyle().Foreground(ColorCRTShadow)
	ta.FocusedStyle.Placeholder = lipgloss.NewStyle().Foreground(ColorMutedGreen)
	ta.BlurredStyle.Base = Input
	ta.BlurredStyle.Placeholder = lipgloss.NewStyle().Foreground(ColorMutedGreen)

	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = Spinner

	vp := viewport.New(defaultTermWidth, defaultVPHHeight)
	vp.Style = Body

	mod := &model{
		agent:          agentM,
		cfg:            cfg,
		sess:           sess,
		skillRegistry:  reg,
		skillActivator: activator,
		viewport:       vp,
		textarea:       ta,
		spinner:        sp,
		messages:       make([]msgItem, 0),
		overlay:        overlayNone,
		confirmReqCh:   make(chan toolConfirmReq),
	}

	mod.agent.SetEffectfulToolConfirm(mod.effectfulToolConfirm)

	return mod
}

// Init implements tea.Model.
func (m *model) Init() tea.Cmd {
	return tea.Batch(
		textarea.Blink,
		m.spinner.Tick,
		m.waitForConfirmReq(),
	)
}

func (m *model) waitForConfirmReq() tea.Cmd {
	return func() tea.Msg {
		req := <-m.confirmReqCh
		return confirmReqMsg{req: req}
	}
}

// recalcLayout sets viewport and textarea sizes based on current terminal dimensions.
func (m *model) recalcLayout() {
	if m.width <= 0 || m.height <= 0 {
		return
	}

	inputHeight := max(m.textarea.Height(), textareaMinLines)
	vpHeight := max(m.height-headerHeight-statusHeight-inputHeight-dividerHeight*dividersInFrame, viewportMinHeight)

	m.viewport.Width = max(m.width-2*viewportHorizontalPad, 1)
	m.viewport.Height = vpHeight

	m.textarea.SetWidth(m.width)
}

// syncViewportContent rebuilds the viewport content from messages and scrolls to bottom.
func (m *model) syncViewportContent() {
	if len(m.messages) == 0 {
		m.viewport.SetContent(m.welcomeView())

		return
	}

	var builder strings.Builder
	for i, msg := range m.messages {
		if i > 0 {
			builder.WriteString("\n")
		}

		builder.WriteString(msg.Render(m.viewport.Width))
	}

	m.viewport.SetContent(builder.String())
	m.viewport.GotoBottom()
}

// welcomeView renders the centered empty-state branding as a single
// rectangular card. All lines share the card width and background so the
// composition stays a clean rectangle when centered.
func (m *model) welcomeView() string {
	const sidePad = 4

	lines := []struct {
		text string
		fg   lipgloss.TerminalColor
		bold bool
	}{
		{"nⁿcode", ColorBrightGreen, true},
		{"", ColorBlack, false},
		{"Type a message to start coding with AI.", ColorCRTGreenSoft, false},
		{"Press ? for help, ctrl+c to quit.", ColorMutedGreen, false},
	}

	innerWidth := 0
	for _, line := range lines {
		if w := lipgloss.Width(line.text); w > innerWidth {
			innerWidth = w
		}
	}
	cardWidth := innerWidth + sidePad*2

	rendered := make([]string, len(lines))
	for i, line := range lines {
		style := lipgloss.NewStyle().
			Foreground(line.fg).
			Background(ColorBlack).
			Width(cardWidth).
			Align(lipgloss.Center).
			Bold(line.bold)
		rendered[i] = style.Render(line.text)
	}

	card := strings.Join(rendered, "\n")

	return lipgloss.Place(m.viewport.Width, m.viewport.Height, lipgloss.Center, lipgloss.Center, card)
}

// appendMessage adds a message and refreshes the viewport.
func (m *model) appendMessage(msg msgItem) {
	m.messages = append(m.messages, msg)
	m.syncViewportContent()
}

// updateLastAssistantText appends text to the most recent assistant message.
func (m *model) updateLastAssistantText(text string) {
	if len(m.messages) == 0 {
		m.appendMessage(msgItem{Kind: kindAssistant, Text: text})

		return
	}

	last := &m.messages[len(m.messages)-1]
	if last.Kind != kindAssistant {
		m.appendMessage(msgItem{Kind: kindAssistant, Text: text})

		return
	}

	last.Text += text
	m.syncViewportContent()
}

// effectfulToolConfirm is the callback registered with the agent. It blocks
// until the user responds via the TUI confirmation overlay.
func (m *model) effectfulToolConfirm(ctx context.Context, req agent.ConfirmRequest) (agent.ConfirmDecision, error) {
	resp := make(chan agent.ConfirmDecision, 1)
	m.confirmReqCh <- toolConfirmReq{Name: req.Name, Args: req.Args, Turn: req.Turn, Resp: resp}
	select {
	case <-ctx.Done():
		return agent.ConfirmStop, fmt.Errorf("confirmation cancelled: %w", ctx.Err())
	case r := <-resp:
		return r, nil
	}
}

// saveSession persists the current session if there are messages.
func (m *model) saveSession() {
	m.sess.Messages = m.agent.Messages()
	if len(m.sess.Messages) == 0 {
		return
	}

	if err := m.sess.Save(""); err != nil {
		// Silent failure — same as CLI.
		_ = err
	}
}

// headerView renders the top header bar spanning the full width.
func (m *model) headerView() string {
	logo := HeaderLogo.Render("nⁿcode")
	info := HeaderInfo.Render("model " + m.agent.Model().ID)

	return m.renderBar(HeaderBar, logo, info)
}

// tokenStats holds computed token totals.
type tokenStats struct {
	lastTurn     int
	sessionTotal int
	byModel      map[string]int
}

// computeTokenStats scans assistant messages for usage and per-model totals.
func (m *model) computeTokenStats() tokenStats {
	var stats tokenStats
	stats.byModel = make(map[string]int)

	for _, msg := range m.agent.Messages() {
		if msg.Role == llm.RoleAssistant {
			stats.sessionTotal += msg.Usage.TotalTokens
			stats.byModel[msg.Model] += msg.Usage.TotalTokens
		}
	}

	for i := len(m.agent.Messages()) - 1; i >= 0; i-- {
		if m.agent.Messages()[i].Role == llm.RoleAssistant {
			stats.lastTurn = m.agent.Messages()[i].Usage.TotalTokens
			break
		}
	}

	return stats
}

// statusView renders the status bar.
func (m *model) statusView() string {
	mode := "ready"
	if m.running {
		mode = m.spinner.View() + " thinking"
	}

	const groupGap = 4

	barBG := StatusBar.GetBackground()
	dot := lipgloss.NewStyle().Foreground(ColorCRTGreenDim).Background(barBG).Render("·")

	stats := m.computeTokenStats()
	turnTokens := stats.lastTurn
	if m.running && m.inTurn {
		estimate := m.turnTextLen / tokenEstimateDivisor
		if estimate > turnTokens {
			turnTokens = estimate
		}
	}

	tokenStr := formatTokenCount(turnTokens) + " / " + formatTokenCount(stats.sessionTotal)

	left := StatusKey.Render("MODE") + barSpacer(1, barBG) + StatusValue.Render(mode) +
		barSpacer(2, barBG) + dot + barSpacer(2, barBG) +
		StatusKey.Render("MSGS") + barSpacer(1, barBG) +
		StatusValue.Render(strconv.Itoa(len(m.agent.Messages()))) +
		barSpacer(2, barBG) + dot + barSpacer(2, barBG) +
		StatusKey.Render("TOKENS") + barSpacer(1, barBG) +
		StatusValue.Render(tokenStr)

	right := StatusKey.Render("?") + barSpacer(1, barBG) + StatusValue.Render("help") +
		barSpacer(groupGap, barBG) +
		StatusKey.Render("ctrl+c") + barSpacer(1, barBG) + StatusValue.Render("quit")

	return m.renderBar(StatusBar, left, right)
}

// formatTokenCount returns a compact human-readable token count.
func formatTokenCount(n int) string {
	if n >= tokenMillion {
		return fmt.Sprintf("%.1fM", float64(n)/tokenMillion)
	}
	if n >= tokenThousand {
		return fmt.Sprintf("%.1fk", float64(n)/tokenThousand)
	}
	return strconv.Itoa(n)
}

// renderBar lays out a full-width bar with left content, a flexible spacer,
// and right content. The bar is pinned to the terminal width so the
// background fills end to end; the gap between left and right is rendered in
// the bar's background so no terminal-default cells leak through inner ANSI
// resets emitted by per-segment styles.
func (m *model) renderBar(style lipgloss.Style, left, right string) string {
	inner := max(m.width-barHorizontalPadding*2, 0)
	right = truncateInline(right, inner)
	rightWidth := lipgloss.Width(right)

	leftLimit := max(inner-rightWidth-statusGapMin, 0)
	left = truncateInline(left, leftLimit)

	gap := max(inner-lipgloss.Width(left)-rightWidth, 0)
	spacer := barSpacer(gap, style.GetBackground())

	return style.Width(m.width).Render(left + spacer + right)
}

// barSpacer renders a run of width cells in the given background, so that
// inter-segment whitespace inside a bar does not show the terminal default
// background through the gap.
func barSpacer(width int, bg lipgloss.TerminalColor) string {
	if width <= 0 {
		return ""
	}

	return lipgloss.NewStyle().Background(bg).Render(strings.Repeat(" ", width))
}

// fillBlock pads or truncates rendered content to a fixed terminal cell box.
// Every cell in the block is guaranteed to carry the style's background, so
// that inner ANSI resets emitted by per-segment styles (textarea internals,
// lipgloss.Place padding, etc.) cannot leave terminal-default cells visible.
func fillBlock(style lipgloss.Style, content string, width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}

	lines := strings.Split(content, "\n")
	if len(lines) > height {
		lines = lines[:height]
	}

	for len(lines) < height {
		lines = append(lines, "")
	}

	bg := style.GetBackground()
	for index, line := range lines {
		line = fitLine(line, width, bg)
		lines[index] = ensureBackground(line, bg)
	}

	return style.Render(strings.Join(lines, "\n"))
}

func fitLine(line string, width int, padBG lipgloss.TerminalColor) string {
	if width <= 0 {
		return ""
	}

	if lipgloss.Width(line) > width {
		line = truncateInline(line, width)
	}

	extra := width - lipgloss.Width(line)
	if extra <= 0 {
		return line
	}

	return line + barSpacer(extra, padBG)
}
