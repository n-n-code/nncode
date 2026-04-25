package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nncode/internal/agent"
	"nncode/internal/config"
	"nncode/internal/llm"
	"nncode/internal/session"
	"nncode/internal/skills"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

func toolCallEvents(id string, name string, args string) []llm.StreamEvent {
	call := llm.ToolCall{ID: id, Name: name, Args: json.RawMessage(args)}

	return []llm.StreamEvent{
		{ToolStart: &call},
		{ToolEnd: &call},
		{Done: &llm.Done{StopReason: "tool_calls"}},
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

func TestRun_PipedEmptyStdinExitsWithoutCallingAgent(t *testing.T) {
	client := &mockClient{events: textEvents("unused")}
	ag := agent.New(agent.Config{Model: llm.Model{ID: "test"}, Client: client}, "system")

	var out, errOut bytes.Buffer

	c := New(ag, testConfig(), session.New(), WithIO(strings.NewReader(""), &out, &errOut, false))

	err := c.RunContext(context.Background())

	require.NoError(t, err)
	assert.Zero(t, client.calls)
	assert.Empty(t, out.String())
	assert.Empty(t, errOut.String())
}

func TestRun_PipedPromptStreamsAndSavesSession(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	client := &mockClient{events: textEvents("hello")}
	ag := agent.New(agent.Config{Model: llm.Model{ID: "test"}, Client: client}, "system")

	var out, errOut bytes.Buffer

	c := New(ag, testConfig(), session.New(), WithIO(strings.NewReader("say hi"), &out, &errOut, false))

	err := c.RunContext(context.Background())

	require.NoError(t, err)
	assert.Equal(t, "hello\n", out.String())
	assert.Empty(t, errOut.String())
	require.Len(t, c.sess.Messages, 2)
	assert.NotEmpty(t, c.sess.FilePath)
}

func TestRun_PipedWarnsOnSilentReadOnlyTurn(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	client := &mockClient{scripts: [][]llm.StreamEvent{
		toolCallEvents("c1", "read", `{"path":"README.md"}`),
		{{Done: &llm.Done{StopReason: "stop"}}},
	}}
	tool := agent.Tool{
		Name:       "read",
		Parameters: "{}",
		Execute: func(ctx context.Context, args json.RawMessage) (agent.ToolResult, error) {
			return agent.ToolResult{
				Content: "contents",
				Metadata: map[string]any{
					"path":       "README.md",
					"bytes_read": 8,
				},
			}, nil
		},
	}
	ag := agent.New(agent.Config{Model: llm.Model{ID: "test"}, Client: client, Tools: []agent.Tool{tool}}, "system")

	var out, errOut bytes.Buffer

	c := New(ag, testConfig(), session.New(), WithIO(strings.NewReader("inspect"), &out, &errOut, false))

	err := c.RunContext(context.Background())

	require.NoError(t, err)
	assert.Contains(t, errOut.String(), "warning: agent completed without a response or effectful tool call")
	assert.Contains(t, out.String(), "contents")
	assert.NotEmpty(t, c.sess.FilePath)
}

func TestRun_PipedStrictReturnsIncompleteOnSilentReadOnlyTurn(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	client := &mockClient{scripts: [][]llm.StreamEvent{
		toolCallEvents("c1", "read", `{"path":"README.md"}`),
		{{Done: &llm.Done{StopReason: "stop"}}},
	}}
	tool := agent.Tool{
		Name:       "read",
		Parameters: "{}",
		Execute: func(ctx context.Context, args json.RawMessage) (agent.ToolResult, error) {
			return agent.ToolResult{Content: "contents"}, nil
		},
	}
	ag := agent.New(agent.Config{Model: llm.Model{ID: "test"}, Client: client, Tools: []agent.Tool{tool}}, "system")

	var out, errOut bytes.Buffer

	c := New(ag, testConfig(), session.New(), WithIO(strings.NewReader("inspect"), &out, &errOut, false), WithStrictPiped(true))

	err := c.RunContext(context.Background())

	require.ErrorIs(t, err, ErrIncompleteTurn)
	assert.Contains(t, errOut.String(), "warning: agent completed without a response or effectful tool call")
	assert.NotEmpty(t, c.sess.FilePath)
}

func TestRun_PipedDoesNotWarnOnEffectfulToolOnlyTurn(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	client := &mockClient{scripts: [][]llm.StreamEvent{
		toolCallEvents("c1", "write", `{"path":"main.go","content":"package main"}`),
		{{Done: &llm.Done{StopReason: "stop"}}},
	}}
	tool := agent.Tool{
		Name:       "write",
		Parameters: "{}",
		Execute: func(ctx context.Context, args json.RawMessage) (agent.ToolResult, error) {
			return agent.ToolResult{
				Content: "wrote",
				Metadata: map[string]any{
					"path":          "main.go",
					"bytes_written": 12,
				},
			}, nil
		},
	}
	ag := agent.New(agent.Config{Model: llm.Model{ID: "test"}, Client: client, Tools: []agent.Tool{tool}}, "system")

	var out, errOut bytes.Buffer

	c := New(ag, testConfig(), session.New(), WithIO(strings.NewReader("write"), &out, &errOut, false), WithStrictPiped(true))

	err := c.RunContext(context.Background())

	require.NoError(t, err)
	assert.Empty(t, errOut.String())
	assert.Contains(t, out.String(), "wrote")
}

func TestRun_PipedPromptReturnsAgentError(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	client := &mockClient{err: errors.New("boom")}
	ag := agent.New(agent.Config{Model: llm.Model{ID: "test"}, Client: client}, "system")

	var out, errOut bytes.Buffer

	c := New(ag, testConfig(), session.New(), WithIO(strings.NewReader("fail"), &out, &errOut, false))

	err := c.RunContext(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "boom")
	assert.Contains(t, errOut.String(), "[error]")
	assert.NotEmpty(t, c.sess.FilePath)
}

func TestRun_InteractiveCommands(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	tool := agent.Tool{Name: "read", Description: "reads files", Parameters: "{}"}
	ag := agent.New(agent.Config{Model: llm.Model{ID: "test"}, Client: &mockClient{}, Tools: []agent.Tool{tool}}, "system prompt")

	var out, errOut bytes.Buffer

	input := strings.NewReader("/help\n/tools\n/prompt\n/session\n/quit\n")
	c := New(ag, testConfig(), session.New(), WithIO(input, &out, &errOut, true))

	err := c.RunContext(context.Background())

	require.NoError(t, err)

	text := out.String()
	assert.Contains(t, text, "/sessions")
	assert.Contains(t, text, "/resume <id|path>")
	assert.Contains(t, text, "/skills")
	assert.Contains(t, text, "read")
	assert.Contains(t, text, "system prompt")
	assert.Contains(t, text, "ID:")
	assert.Empty(t, errOut.String())
}

func TestRun_InteractiveSkillsCommandAndManualActivation(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, ".git"), 0755))
	writeCLITestSkill(t, root, "go", "name: go\ndescription: go skill\n", "# Go")
	writeCLITestSkill(t, root, "manual", "name: manual\ndescription: manual skill\ndisable-model-invocation: true\n", "# Manual")
	reg := skills.Discover(skills.DiscoverOptions{CWD: root, HomeDir: t.TempDir()})
	activator := skills.NewActivator(reg)
	ag := agent.New(agent.Config{Model: llm.Model{ID: "test"}, Client: &mockClient{}}, "system")

	var out, errOut bytes.Buffer

	input := strings.NewReader("/skills\n/skill:manual\n/session\n/quit\n")
	c := New(ag, testConfig(), session.New(), WithIO(input, &out, &errOut, true), WithSkills(reg, activator))

	err := c.RunContext(context.Background())

	require.NoError(t, err)

	text := out.String()
	assert.Contains(t, text, "go")
	assert.Contains(t, text, "[manual]")
	assert.Contains(t, text, "Activated skill \"manual\"")
	assert.Contains(t, text, "Messages: 1")
	require.Len(t, ag.Messages(), 1)
	assert.Equal(t, llm.RoleSystem, ag.Messages()[0].Role)
	assert.Contains(t, ag.Messages()[0].Content, "# Manual")
	assert.Empty(t, errOut.String())
}

func TestRun_SkillsCommandTruncatesLongDescriptions(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, ".git"), 0755))

	longDescription := strings.Repeat("long description ", 20)
	writeCLITestSkill(t, root, "verbose", "name: verbose\ndescription: "+longDescription+"\n", "# Verbose")
	reg := skills.Discover(skills.DiscoverOptions{CWD: root, HomeDir: t.TempDir()})
	ag := agent.New(agent.Config{Model: llm.Model{ID: "test"}, Client: &mockClient{}}, "system")

	var out, errOut bytes.Buffer

	input := strings.NewReader("/skills\n/quit\n")
	c := New(ag, testConfig(), session.New(), WithIO(input, &out, &errOut, true), WithSkills(reg, skills.NewActivator(reg)))

	err := c.RunContext(context.Background())

	require.NoError(t, err)

	text := out.String()
	assert.Contains(t, text, "verbose")
	assert.Contains(t, text, "…")
	assert.NotContains(t, text, longDescription)
	assert.Empty(t, errOut.String())
}

func TestRun_SkillCommandWithPromptRunsAfterActivation(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, ".git"), 0755))
	writeCLITestSkill(t, root, "go", "name: go\ndescription: go skill\n", "# Go")
	reg := skills.Discover(skills.DiscoverOptions{CWD: root, HomeDir: t.TempDir()})
	activator := skills.NewActivator(reg)
	client := &mockClient{events: textEvents("done")}
	ag := agent.New(agent.Config{Model: llm.Model{ID: "test"}, Client: client}, "system")

	var out, errOut bytes.Buffer

	input := strings.NewReader("/skill:go use it\n/quit\n")
	c := New(ag, testConfig(), session.New(), WithIO(input, &out, &errOut, true), WithSkills(reg, activator))

	err := c.RunContext(context.Background())

	require.NoError(t, err)
	assert.Equal(t, 1, client.calls)
	assert.Contains(t, out.String(), "done")

	msgs := ag.Messages()
	require.Len(t, msgs, 3)
	assert.Equal(t, llm.RoleSystem, msgs[0].Role)
	assert.Equal(t, llm.RoleUser, msgs[1].Role)
	assert.Equal(t, "use it", msgs[1].Content)
	assert.Empty(t, errOut.String())
}

func TestRun_ToolResultPreviewHidesActivationMarker(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	client := &mockClient{scripts: [][]llm.StreamEvent{
		toolCallEvents("c1", "activate_skill", `{"name":"go"}`),
		textEvents("done"),
	}}
	tool := agent.Tool{
		Name:       "activate_skill",
		Parameters: "{}",
		Execute: func(ctx context.Context, args json.RawMessage) (agent.ToolResult, error) {
			return agent.ToolResult{Content: `<activated_skill>{"name":"go"}</activated_skill> Skill "go" activated.`}, nil
		},
	}
	ag := agent.New(agent.Config{Model: llm.Model{ID: "test"}, Client: client, Tools: []agent.Tool{tool}}, "system")

	var out, errOut bytes.Buffer

	c := New(ag, testConfig(), session.New(), WithIO(strings.NewReader("use go"), &out, &errOut, false))

	err := c.RunContext(context.Background())

	require.NoError(t, err)
	assert.Contains(t, out.String(), `Skill "go" activated.`)
	assert.NotContains(t, out.String(), "<activated_skill>")
	require.Len(t, c.sess.Messages, 4)
	assert.Contains(t, c.sess.Messages[2].Content, "<activated_skill>")
	assert.Empty(t, errOut.String())
}

func TestRun_InteractiveListsAndResumesSession(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	saved := session.New()
	saved.AddMessage(llm.Message{Role: llm.RoleUser, Content: "hello"})
	saved.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: "hi"})
	require.NoError(t, saved.Save(""))

	ag := agent.New(agent.Config{Model: llm.Model{ID: "test"}, Client: &mockClient{}}, "system")

	var out, errOut bytes.Buffer

	input := strings.NewReader("/sessions\n/resume " + saved.ID + "\n/session\n/quit\n")
	c := New(ag, testConfig(), session.New(), WithIO(input, &out, &errOut, true))

	err := c.RunContext(context.Background())

	require.NoError(t, err)
	require.Len(t, ag.Messages(), 2)

	text := out.String()
	assert.Contains(t, text, saved.ID)
	assert.Contains(t, text, "Resumed session")
	assert.Contains(t, text, "Messages: 2")
	assert.Empty(t, errOut.String())
}

func writeCLITestSkill(t *testing.T, root string, dirName string, frontmatter string, body string) {
	t.Helper()

	dir := filepath.Join(root, ".agents", "skills", dirName)
	require.NoError(t, os.MkdirAll(dir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\n"+frontmatter+"---\n"+body), 0644))
}
