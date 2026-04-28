package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nncode/internal/agent"
	"nncode/internal/config"
	"nncode/internal/llm"
	"nncode/internal/session"
)

type mockClient struct {
	events  []llm.StreamEvent
	scripts [][]llm.StreamEvent
	err     error
	calls   int
}

func (m *mockClient) Stream(ctx context.Context, req llm.Request) (<-chan llm.StreamEvent, error) {
	m.calls++
	if m.err != nil {
		return nil, m.err
	}

	events := m.events
	if len(m.scripts) > 0 {
		events = m.scripts[0]
		m.scripts = m.scripts[1:]
	}

	ch := make(chan llm.StreamEvent, len(events))
	go func() {
		defer close(ch)
		for _, ev := range events {
			select {
			case <-ctx.Done():
				return
			case ch <- ev:
			}
		}
	}()
	return ch, nil
}

func textEvents(text string) []llm.StreamEvent {
	return []llm.StreamEvent{
		{Text: text},
		{Done: &llm.Done{StopReason: "stop"}},
	}
}

func testConfig() *config.Config {
	return &config.Config{
		DefaultModel: "test",
		Models: map[string]config.Model{
			"test": {APIType: config.APITypeOpenAICompletions, Provider: "openai"},
		},
	}
}

func newTestModel(client llm.Client) *model {
	ag := agent.New(agent.Config{Model: llm.Model{ID: "test"}, Client: client}, "system")
	return newModel(ag, testConfig(), session.New(), nil, nil)
}

func TestNewModelInitialState(t *testing.T) {
	m := newTestModel(&mockClient{})
	assert.False(t, m.running)
	assert.Equal(t, overlayNone, m.overlay)
	assert.Empty(t, m.messages)
	assert.True(t, m.textarea.Focused())
}

func TestSendUserMessageStreamsText(t *testing.T) {
	client := &mockClient{events: textEvents("hello world")}
	m := newTestModel(client)
	m.width = 80
	m.height = 24
	m.recalcLayout()

	// Simulate typing and sending.
	m.textarea.SetValue("say hi")
	updated, _ := m.sendInput()
	m = updated.(*model)
	require.True(t, m.running)
	require.Len(t, m.messages, 1)
	assert.Equal(t, kindUser, m.messages[0].Kind)
	assert.Equal(t, "say hi", m.messages[0].Text)

	// Drain events until we get text or done (first event is usually TurnStart).
	var textEv agent.Event
	for {
		msg := nextEventCmd(m.eventCh)()
		require.IsType(t, agentEventMsg{}, msg)
		ev := msg.(agentEventMsg).Event
		updated, _ = m.Update(msg)
		m = updated.(*model)
		if ev.Type == agent.EventText {
			textEv = ev
			break
		}
		if ev.Type == agent.EventDone {
			break
		}
	}

	assert.Equal(t, agent.EventText, textEv.Type)
	assert.Equal(t, "hello world", textEv.Text)
	require.Len(t, m.messages, 2)
	assert.Equal(t, kindAssistant, m.messages[1].Kind)
	assert.Contains(t, m.messages[1].Text, "hello world")
}

func TestSlashCommandReset(t *testing.T) {
	m := newTestModel(&mockClient{})
	m.messages = []msgItem{{Kind: kindUser, Text: " prior"}}
	m.agent.SetMessages([]llm.Message{{Role: llm.RoleUser, Content: "prior"}})

	updated, _ := m.handleSlashCommand("/reset")
	m = updated.(*model)
	assert.Empty(t, m.messages)
	assert.Empty(t, m.agent.Messages())
}

func TestSlashCommandHelpOpensOverlay(t *testing.T) {
	m := newTestModel(&mockClient{})
	updated, _ := m.handleSlashCommand("/help")
	m = updated.(*model)
	assert.Equal(t, overlayHelp, m.overlay)
	assert.NotEmpty(t, m.overlayItems)
}

func TestSlashCommandLoopsOpensOverlay(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	writeTUITestLoop(t, root, "run-me", `{
		"schema_version": 1,
		"name": "display-name",
		"description": "review loop",
		"nodes": [
			{"id":"entry","type":"entry_prompt","content":"Start"},
			{"id":"prompt","type":"prompt","content":"Work"},
			{"id":"exit","type":"exit_criteria","content":"Done?"}
		]
	}`)

	m := newTestModel(&mockClient{})
	m.width = 100
	m.height = 30
	m.recalcLayout()
	updated, _ := m.handleSlashCommand("/loops")
	m = updated.(*model)
	assert.Equal(t, overlayLoops, m.overlay)
	items := strings.Join(m.overlayItems, "\n")
	assert.Contains(t, items, "run-me")
	assert.Contains(t, items, "OK")
	require.Len(t, m.loopSummaries, 1)
	assert.Equal(t, "display-name", m.loopSummaries[0].Name)
	assert.Contains(t, m.overlayView(), "nodes:")
}

func TestLoopsOverlayEnterRunsSelectedLoop(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	t.Chdir(root)
	writeTUITestLoop(t, root, "run-me", `{
		"schema_version": 1,
		"name": "run-me",
		"settings": {"max_iterations": 1},
		"nodes": [
			{"id":"entry","type":"entry_prompt","content":"Start"},
			{"id":"prompt","type":"prompt","content":"Work"},
			{"id":"exit","type":"exit_criteria","content":"Done?"}
		]
	}`)

	client := &mockClient{scripts: [][]llm.StreamEvent{
		textEvents("entry ok"),
		textEvents("prompt ok"),
		textEvents("LOOP_EXIT: yes"),
	}}
	m := newTestModel(client)
	updated, _ := m.handleSlashCommand("/loops")
	m = updated.(*model)

	next, cmd := m.selectOverlayItem()
	m = next

	require.True(t, m.running)
	require.NotNil(t, cmd)
	assert.Equal(t, overlayNone, m.overlay)
	assert.Contains(t, m.messages[0].Text, "/loop run-me")

	for {
		msg := nextEventCmd(m.eventCh)()
		teaModel, _ := m.Update(msg)
		m = teaModel.(*model)
		if _, ok := msg.(agentDoneMsg); ok {
			break
		}
	}

	assert.False(t, m.running)
	assert.Equal(t, 3, client.calls)
}

func TestSlashCommandLoopValidate(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	writeTUITestLoop(t, root, "review", `{
		"schema_version": 1,
		"name": "review",
		"nodes": [
			{"id":"entry","type":"entry_prompt","content":"Start"},
			{"id":"prompt","type":"prompt","content":"Work"},
			{"id":"exit","type":"exit_criteria","content":"Done?"}
		]
	}`)

	m := newTestModel(&mockClient{})
	updated, _ := m.handleSlashCommand("/loop-validate review")
	m = updated.(*model)
	require.Len(t, m.messages, 1)
	assert.Equal(t, kindAssistant, m.messages[0].Kind)
	assert.Contains(t, m.messages[0].Text, "Agent Loop \"review\" is valid")
}

func TestSlashCommandLoopRuns(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	t.Chdir(root)
	writeTUITestLoop(t, root, "review", `{
		"schema_version": 1,
		"name": "review",
		"settings": {"max_iterations": 1},
		"nodes": [
			{"id":"entry","type":"entry_prompt","content":"Start {{input}}"},
			{"id":"prompt","type":"prompt","content":"Work"},
			{"id":"exit","type":"exit_criteria","content":"Done?"}
		]
	}`)

	client := &mockClient{scripts: [][]llm.StreamEvent{
		textEvents("entry ok"),
		textEvents("prompt ok"),
		textEvents("LOOP_EXIT: yes"),
	}}
	m := newTestModel(client)
	updated, _ := m.handleSlashCommand("/loop review ship it")
	m = updated.(*model)

	require.True(t, m.running)
	require.Len(t, m.messages, 1)
	assert.Equal(t, kindUser, m.messages[0].Kind)
	assert.Contains(t, m.messages[0].Text, "/loop review ship it")

	for {
		msg := nextEventCmd(m.eventCh)()
		updated, _ = m.Update(msg)
		m = updated.(*model)
		if _, ok := msg.(agentDoneMsg); ok {
			break
		}
	}

	assert.False(t, m.running)
	assert.Equal(t, 3, client.calls)
	assert.True(t, containsMessageText(m.messages, "prompt ok"))
	assert.True(t, containsMessageText(m.messages, "exit criteria: exit"))
}

func TestSlashCommandSessionUsesSessionOverlay(t *testing.T) {
	m := newTestModel(&mockClient{})
	m.width = 80
	m.height = 24
	m.recalcLayout()

	updated, _ := m.handleSlashCommand("/session")
	m = updated.(*model)
	assert.Equal(t, overlaySessionInfo, m.overlay)
	assert.Contains(t, m.overlayView(), "Session")
}

func TestSlashCommandUnknown(t *testing.T) {
	m := newTestModel(&mockClient{})
	updated, _ := m.handleSlashCommand("/unknown")
	m = updated.(*model)
	require.Len(t, m.messages, 1)
	assert.Equal(t, kindError, m.messages[0].Kind)
	assert.Contains(t, m.messages[0].Text, "Unknown command")
}

func TestToolCallAndResultRendering(t *testing.T) {
	m := newTestModel(&mockClient{})
	m.width = 80
	m.height = 24
	m.recalcLayout()

	// Simulate tool call start.
	m.handleAgentEvent(agent.Event{Type: agent.EventToolCallStart, ToolName: "read"})
	require.Len(t, m.messages, 1)
	assert.Equal(t, kindToolCall, m.messages[0].Kind)
	assert.Equal(t, "read", m.messages[0].ToolName)

	// Simulate tool call end with args.
	m.handleAgentEvent(agent.Event{Type: agent.EventToolCallEnd, ToolName: "read", ToolArgs: `{"path":"main.go"}`})
	assert.JSONEq(t, `{"path":"main.go"}`, m.messages[0].ToolArgs)

	// Simulate tool result.
	m.handleAgentEvent(agent.Event{Type: agent.EventToolResult, ToolName: "read", Result: "package main", IsError: false})
	require.Len(t, m.messages, 2)
	assert.Equal(t, kindToolResult, m.messages[1].Kind)
	assert.Equal(t, "read", m.messages[1].ToolName)
	assert.Equal(t, "package main", m.messages[1].Result)
}

func TestErrorEventRendersErrorMessage(t *testing.T) {
	m := newTestModel(&mockClient{})
	m.width = 80
	m.height = 24
	m.recalcLayout()

	m.handleAgentEvent(agent.Event{Type: agent.EventError, Err: assert.AnError})
	require.Len(t, m.messages, 1)
	assert.Equal(t, kindError, m.messages[0].Kind)
	assert.Contains(t, m.messages[0].Text, assert.AnError.Error())
}

func TestViewContainsHeaderAndStatus(t *testing.T) {
	m := newTestModel(&mockClient{})
	m.width = 80
	m.height = 24
	m.recalcLayout()

	view := m.View()
	assert.Contains(t, view, "nⁿcode")
	assert.Contains(t, view, "MODE")
	assert.Contains(t, view, "test")
}

func TestViewRowsFillTerminalWidth(t *testing.T) {
	m := newTestModel(&mockClient{})
	m.width = 80
	m.height = 24
	m.recalcLayout()

	lines := strings.Split(m.View(), "\n")
	require.Len(t, lines, m.height)
	for _, line := range lines {
		assert.Equal(t, m.width, lipgloss.Width(line))
	}
}

func TestOverlayViewRendersHelp(t *testing.T) {
	m := newTestModel(&mockClient{})
	m.width = 80
	m.height = 24
	m.recalcLayout()
	m.openHelpOverlay()

	view := m.overlayView()
	assert.Contains(t, view, "Help")
	assert.Contains(t, view, "/reset")
	assert.Contains(t, view, "/skill:name")
	assert.Contains(t, view, "esc/q close")
}

func TestResumeSessionReplacesMessages(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	saved := session.New()
	saved.AddMessage(llm.Message{Role: llm.RoleUser, Content: "hello"})
	saved.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: "hi"})
	require.NoError(t, saved.Save(""))

	m := newTestModel(&mockClient{})
	m.width = 80
	m.height = 24
	m.recalcLayout()

	m.resumeSession(saved.ID)
	require.Len(t, m.agent.Messages(), 2)
	require.Len(t, m.messages, 3) // user + assistant + resume confirmation
	assert.Equal(t, kindUser, m.messages[0].Kind)
	assert.Equal(t, kindAssistant, m.messages[1].Kind)
	assert.Contains(t, m.messages[2].Text, "Resumed session")
}

func TestSaveSessionUsesAgentMessages(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	client := &mockClient{events: textEvents("reply")}
	m := newTestModel(client)
	m.width = 80
	m.height = 24
	m.recalcLayout()

	m.textarea.SetValue("hello")
	updated, _ := m.sendInput()
	m = updated.(*model)

	// Drain events until done.
	for {
		msg := nextEventCmd(m.eventCh)()
		if msg == nil {
			break
		}
		updated, _ = m.Update(msg)
		m = updated.(*model)
		if _, ok := msg.(agentDoneMsg); ok {
			break
		}
		// Continue reading next event for agentEventMsg.
	}

	m.saveSession()
	assert.NotEmpty(t, m.sess.FilePath)
	assert.NotEmpty(t, m.sess.Messages)
}

func TestKeyQuitSavesAndExits(t *testing.T) {
	m := newTestModel(&mockClient{})
	m.textarea.Blur()
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	require.NotNil(t, cmd)
	quitMsg := cmd()
	require.IsType(t, tea.QuitMsg{}, quitMsg)
}

func TestKeyCtrlCQuits(t *testing.T) {
	m := newTestModel(&mockClient{})
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	require.NotNil(t, cmd)
	quitMsg := cmd()
	require.IsType(t, tea.QuitMsg{}, quitMsg)
}

func writeTUITestLoop(t *testing.T, root string, name string, body string) {
	t.Helper()

	dir := filepath.Join(root, ".nncode", "loops")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, name+".json"), []byte(body), 0o644))
}

func containsMessageText(messages []msgItem, text string) bool {
	for _, msg := range messages {
		if strings.Contains(msg.Text, text) {
			return true
		}
	}

	return false
}
