package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"

	"nncode/internal/agent"
	"nncode/internal/agentloop"
	"nncode/internal/llm"
	"nncode/internal/session"
	"nncode/internal/skills"
)

// Update implements tea.Model.
//
//nolint:ireturn // Required by tea.Model interface.
func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.recalcLayout()
		m.syncViewportContent()

	case tea.KeyMsg:
		return m.handleKey(msg)

	case agentEventMsg:
		m.handleAgentEvent(msg.Event)
		cmds = append(cmds, nextEventCmd(m.eventCh))

	case agentDoneMsg:
		m.running = false
		m.sess.Messages = m.agent.Messages()
		m.textarea.Focus()

	case confirmReqMsg:
		m.pendingConfirm = &msg.req
		m.overlay = overlayConfirm

	case contextWindowMsg:
		if msg.window.Known() {
			m.contextWindow = msg.window
			if m.overlay == overlaySessionInfo {
				m.openSessionOverlay()
			}
		}

	case compressMsg:
		m.compressing = false
		m.textarea.Focus()
		if msg.err != nil {
			m.appendMessage(msgItem{Kind: kindError, Text: "Compression failed: " + msg.err.Error()})
		} else {
			m.agent.Reset()
			m.agent.AddSystemMessage(msg.summary)
			m.sess = session.New()
			m.sess.Messages = m.agent.Messages()
			m.messages = nil
			m.turnTextLen = 0
			m.inTurn = false
			m.syncViewportContent()
			m.appendMessage(msgItem{Kind: kindAssistant, Text: "Context compressed. Summary:\n" + msg.summary})
		}

	default:
		// Let bubbles handle their own messages (cursor blink, spinner tick, etc.).
		var cmd tea.Cmd
		m.textarea, cmd = m.textarea.Update(msg)
		cmds = append(cmds, cmd)

		m.spinner, cmd = m.spinner.Update(msg)
		cmds = append(cmds, cmd)

		m.viewport, cmd = m.viewport.Update(msg)
		cmds = append(cmds, cmd)
	}

	return m, tea.Batch(cmds...)
}

// handleKey routes key presses based on whether an overlay is active.
func (m *model) handleKey(msg tea.KeyMsg) (*model, tea.Cmd) {
	if msg.Type == tea.KeyCtrlC {
		return m.handleCtrlC()
	}

	if m.compressing {
		return m, nil
	}

	if m.overlay != overlayNone {
		return m.handleOverlayKey(msg)
	}

	if m.textarea.Focused() {
		//nolint:exhaustive // We only handle specific keys; all others pass to textarea.
		switch msg.Type {
		case tea.KeyEsc:
			m.textarea.Blur()
			return m, nil

		case tea.KeyEnter:
			if msg.Alt {
				// Alt+Enter inserts newline in textarea.
				var cmd tea.Cmd
				m.textarea, cmd = m.textarea.Update(msg)
				m.recalcLayout()

				return m, cmd
			}

			return m.sendInput()

		default:
			var cmd tea.Cmd
			m.textarea, cmd = m.textarea.Update(msg)
			m.recalcLayout()

			return m, cmd
		}
	}

	// Unfocused global keys.
	switch msg.String() {
	case "q", "Q":
		m.saveSession()
		return m, tea.Quit
	case "?":
		m.openHelpOverlay()
		return m, nil
	case "/":
		m.textarea.Focus()
		m.textarea.SetValue("/")
		return m, textarea.Blink
	case "i", "I":
		m.textarea.Focus()
		return m, textarea.Blink
	}

	//nolint:exhaustive // We only handle navigation keys.
	switch msg.Type {
	case tea.KeyUp:
		m.viewport.ScrollUp(1)
	case tea.KeyDown:
		m.viewport.ScrollDown(1)
	case tea.KeyPgUp:
		m.viewport.HalfPageUp()
	case tea.KeyPgDown:
		m.viewport.HalfPageDown()
	case tea.KeyHome:
		m.viewport.GotoTop()
	case tea.KeyEnd:
		m.viewport.GotoBottom()
	default:
	}

	return m, nil
}

func (m *model) handleCtrlC() (*model, tea.Cmd) {
	if m.running && m.runCancel != nil {
		m.runCancel()

		return m, nil
	}

	m.saveSession()

	return m, tea.Quit
}

// sendInput reads the textarea, handles slash commands or sends to agent.
func (m *model) sendInput() (*model, tea.Cmd) {
	input := strings.TrimSpace(m.textarea.Value())
	if input == "" {
		return m, nil
	}

	m.textarea.Reset()
	m.recalcLayout()

	if strings.HasPrefix(input, "/") {
		return m.handleSlashCommand(input)
	}

	m.appendMessage(msgItem{Kind: kindUser, Text: input})
	m.running = true
	m.textarea.Blur()

	var runCtx context.Context
	runCtx, m.runCancel = context.WithCancel(context.Background())
	m.eventCh = m.agent.Run(runCtx, input)

	return m, tea.Batch(
		nextEventCmd(m.eventCh),
		m.spinner.Tick,
	)
}

// handleSlashCommand processes CLI-style slash commands.
func (m *model) handleSlashCommand(line string) (*model, tea.Cmd) {
	if strings.HasPrefix(line, "/skill:") {
		return m.handleSkillCommand(line)
	}

	fields := strings.Fields(line)
	if len(fields) == 0 {
		return m, nil
	}

	switch fields[0] {
	case "/quit", "/exit":
		m.saveSession()
		return m, tea.Quit
	case "/help":
		m.openHelpOverlay()
	case "/reset":
		m.handleReset()
	case "/context":
		m.handleContextCommand(fields)
	case "/compress":
		return m.handleCompress()
	case "/session":
		m.openSessionOverlay()
	case "/sessions":
		m.loadSessionsOverlay()
	case "/resume":
		m.handleResume(fields)
	case "/tools":
		m.loadToolsOverlay()
	case "/skills":
		m.loadSkillsOverlay()
	case "/loops":
		m.loadLoopsOverlay()
	case "/loop":
		return m.handleLoopCommand(line)
	case "/loop-validate":
		m.validateLoopCommand(fields)
	case "/prompt":
		m.overlayItems = []string{m.agent.SystemPrompt()}
		m.overlay = overlayPrompt
	default:
		m.appendMessage(msgItem{Kind: kindError, Text: "Unknown command: " + line + " (try /help)"})
	}

	return m, nil
}

func (m *model) handleReset() {
	m.agent.Reset()
	if m.skillActivator != nil {
		m.skillActivator.Reset()
	}
	m.sess = session.New()
	m.messages = nil
	m.turnTextLen = 0
	m.inTurn = false
	m.syncViewportContent()
}

func (m *model) handleCompress() (*model, tea.Cmd) {
	m.compressing = true
	m.textarea.Blur()
	m.appendMessage(msgItem{Kind: kindAssistant, Text: "Compressing context..."})
	return m, m.compressCmd()
}

func (m *model) compressCmd() tea.Cmd {
	return func() tea.Msg {
		const compressTimeout = 60 * time.Second
		ctx, cancel := context.WithTimeout(context.Background(), compressTimeout)
		defer cancel()

		summary, err := m.agent.Compress(ctx)
		return compressMsg{summary: summary, err: err}
	}
}

func (m *model) handleContextCommand(fields []string) {
	if len(fields) != 2 {
		m.appendMessage(msgItem{Kind: kindError, Text: "Usage: /context -print|-reset"})
		return
	}

	switch fields[1] {
	case "-print":
		m.openContextOverlay()
	case "-reset":
		m.handleReset()
	default:
		m.appendMessage(msgItem{Kind: kindError, Text: "Usage: /context -print|-reset"})
	}
}

func (m *model) handleResume(fields []string) {
	if len(fields) != 2 {
		m.appendMessage(msgItem{Kind: kindError, Text: "Usage: /resume <session-id|path>"})
		return
	}
	m.resumeSession(fields[1])
}

func (m *model) handleLoopCommand(line string) (*model, tea.Cmd) {
	rest := strings.TrimSpace(strings.TrimPrefix(line, "/loop"))
	if rest == "" {
		m.appendMessage(msgItem{Kind: kindError, Text: "Usage: /loop <name|path> [message]"})

		return m, nil
	}

	ref, prompt, _ := strings.Cut(rest, " ")
	prompt = strings.TrimSpace(prompt)

	display := "/loop " + ref
	if prompt != "" {
		display += " " + prompt
	}

	m.appendMessage(msgItem{Kind: kindUser, Text: display})
	m.running = true
	m.textarea.Blur()

	var runCtx context.Context
	runCtx, m.runCancel = context.WithCancel(context.Background())

	runner := agentloop.Runner{
		Agent:        m.agent,
		Config:       m.cfg,
		StoreOptions: agentloop.StoreOptions{},
	}

	events, err := runner.Run(runCtx, ref, prompt)
	if err != nil {
		if m.runCancel != nil {
			m.runCancel()
		}

		m.running = false
		m.textarea.Focus()
		m.appendMessage(msgItem{Kind: kindError, Text: err.Error()})

		return m, nil
	}

	m.eventCh = events

	return m, tea.Batch(
		nextEventCmd(m.eventCh),
		m.spinner.Tick,
	)
}

func (m *model) validateLoopCommand(fields []string) {
	if len(fields) != 2 {
		m.appendMessage(msgItem{Kind: kindError, Text: "Usage: /loop-validate <name|path>"})

		return
	}

	summary, err := agentloop.Validate(fields[1], agentloop.StoreOptions{})
	if err != nil {
		m.appendMessage(msgItem{Kind: kindError, Text: "Agent Loop invalid: " + err.Error()})

		return
	}

	m.appendMessage(msgItem{
		Kind: kindAssistant,
		Text: "Agent Loop \"" + summary.Ref + "\" is valid (" + summary.Path + ").",
	})
}

// handleSkillCommand handles /skill:name [msg].
func (m *model) handleSkillCommand(line string) (*model, tea.Cmd) {
	if m.skillActivator == nil {
		m.appendMessage(msgItem{Kind: kindError, Text: "No Agent Skills are configured."})
		return m, nil
	}

	rest := strings.TrimSpace(strings.TrimPrefix(line, "/skill:"))
	if rest == "" {
		m.appendMessage(msgItem{Kind: kindError, Text: "Usage: /skill:name [message]"})
		return m, nil
	}

	name, prompt, _ := strings.Cut(rest, " ")
	name = strings.TrimSpace(name)
	prompt = strings.TrimSpace(prompt)

	activation, err := m.skillActivator.Activate(name, true)
	if err != nil {
		m.appendMessage(msgItem{Kind: kindError, Text: "Could not activate skill: " + err.Error()})
		return m, nil
	}

	if activation.Duplicate {
		m.appendMessage(msgItem{Kind: kindAssistant, Text: "Skill \"" + activation.Skill.Name + "\" is already active."})
	} else {
		m.agent.AddSystemMessage(skills.FormatActivation(activation))
		m.appendMessage(msgItem{Kind: kindAssistant, Text: "Activated skill \"" + activation.Skill.Name + "\"."})
	}

	m.sess.Messages = m.agent.Messages()
	if prompt != "" {
		m.appendMessage(msgItem{Kind: kindUser, Text: prompt})
		m.running = true
		m.textarea.Blur()
		m.eventCh = m.agent.Run(context.Background(), prompt)
		return m, tea.Batch(nextEventCmd(m.eventCh), m.spinner.Tick)
	}

	return m, nil
}

func (m *model) handleAgentEvent(ev agent.Event) {
	switch ev.Type {
	case agent.EventText:
		m.turnTextLen += len(ev.Text)
		m.updateLastAssistantText(ev.Text)
	case agent.EventToolCallStart:
		m.appendMessage(msgItem{Kind: kindToolCall, ToolName: ev.ToolName})
	case agent.EventToolCallEnd:
		if n := len(m.messages); n > 0 {
			last := &m.messages[n-1]
			if last.Kind == kindToolCall && last.ToolName == ev.ToolName {
				last.ToolArgs = ev.ToolArgs
				m.syncViewportContent()
			}
		}
	case agent.EventToolResult:
		m.appendMessage(msgItem{
			Kind:     kindToolResult,
			ToolName: ev.ToolName,
			Result:   ev.Result,
			IsError:  ev.IsError,
		})
	case agent.EventError:
		if m.err == nil {
			m.err = ev.Err
		}

		text := ev.Err.Error()
		if errors.Is(ev.Err, context.Canceled) {
			text = "Turn canceled by user."
		}
		m.appendMessage(msgItem{Kind: kindError, Text: text})
	case agent.EventTurnStart:
		m.turnTextLen = 0
		m.inTurn = true
	case agent.EventTurnEnd, agent.EventDone:
		m.inTurn = false
	case agent.EventLoopStart,
		agent.EventLoopIterationStart,
		agent.EventLoopNodeStart,
		agent.EventLoopNodeEnd,
		agent.EventLoopExitDecision:
		if text := ev.LoopText(); text != "" {
			m.appendMessage(msgItem{Kind: kindLoopStatus, Text: text})
		}
	default:
	}
}

// resumeSession loads a session by id or path.
func (m *model) resumeSession(ref string) {
	path, err := session.Resolve(ref)
	if err != nil {
		m.appendMessage(msgItem{Kind: kindError, Text: "Could not resolve session: " + err.Error()})
		return
	}

	loaded, err := session.Load(path)
	if err != nil {
		m.appendMessage(msgItem{Kind: kindError, Text: "Could not load session: " + err.Error()})
		return
	}

	m.sess = loaded
	m.agent.SetMessages(loaded.Messages)

	if m.skillActivator != nil {
		m.skillActivator.Reset()
		for _, msg := range loaded.Messages {
			m.skillActivator.MarkActivatedFromText(msg.Content)
		}
	}

	m.messages = nil
	for _, msg := range loaded.Messages {
		if item, ok := msgItemFromHistory(msg); ok {
			m.messages = append(m.messages, item)
		}
	}

	m.syncViewportContent()
	m.appendMessage(msgItem{Kind: kindAssistant, Text: fmt.Sprintf(
		"Resumed session %s (%d messages).", loaded.ID, len(loaded.Messages),
	)})
}

// msgItemFromHistory maps a stored message to its display form. System
// messages are skipped (returned ok=false).
func msgItemFromHistory(msg llm.Message) (msgItem, bool) {
	switch msg.Role {
	case llm.RoleUser:
		return msgItem{Kind: kindUser, Text: msg.Content}, true
	case llm.RoleAssistant:
		return msgItem{Kind: kindAssistant, Text: msg.Content}, true
	case llm.RoleTool:
		return msgItem{Kind: kindToolResult, ToolName: msg.ToolName, Result: msg.Content}, true
	case llm.RoleSystem:
		return msgItem{}, false
	default:
		return msgItem{}, false
	}
}
