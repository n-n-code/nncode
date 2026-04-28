package agentloop

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nncode/internal/agent"
	"nncode/internal/config"
	"nncode/internal/llm"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockClient struct {
	scripts  [][]llm.StreamEvent
	requests []llm.Request
}

func (m *mockClient) Stream(ctx context.Context, req llm.Request) (<-chan llm.StreamEvent, error) {
	m.requests = append(m.requests, req)

	var events []llm.StreamEvent
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

func drain(events <-chan agent.Event) []agent.Event {
	var out []agent.Event
	for ev := range events {
		out = append(out, ev)
	}

	return out
}

func loopEvents(events []agent.Event) []agent.Event {
	out := make([]agent.Event, 0, len(events))
	for _, ev := range events {
		//nolint:exhaustive // This helper intentionally filters only loop lifecycle events.
		switch ev.Type {
		case agent.EventLoopStart,
			agent.EventLoopIterationStart,
			agent.EventLoopNodeStart,
			agent.EventLoopNodeEnd,
			agent.EventLoopExitDecision:
			out = append(out, ev)
		default:
			continue
		}
	}

	return out
}

func toolResults(events []agent.Event, name string) []agent.Event {
	out := make([]agent.Event, 0, len(events))
	for _, ev := range events {
		if ev.Type == agent.EventToolResult && ev.ToolName == name {
			out = append(out, ev)
		}
	}

	return out
}

func eventErrors(events []agent.Event) []agent.Event {
	out := make([]agent.Event, 0, len(events))
	for _, ev := range events {
		if ev.Type == agent.EventError {
			out = append(out, ev)
		}
	}

	return out
}

func metadataInt64(metadata map[string]any, key string) int64 {
	switch value := metadata[key].(type) {
	case int:
		return int64(value)
	case int64:
		return value
	case int32:
		return int64(value)
	default:
		return 0
	}
}

func testConfig() *config.Config {
	return &config.Config{
		DefaultModel: "base",
		Models: map[string]config.Model{
			"base": {APIType: config.APITypeOpenAICompletions, Provider: "openai", MaxTokens: 100},
			"loop": {
				ID:        "loop-provider-id",
				APIType:   config.APITypeOpenAICompletions,
				Provider:  "local",
				BaseURL:   "http://127.0.0.1:8033/v1",
				MaxTokens: 200,
			},
			"node": {
				ID:        "node-provider-id",
				APIType:   config.APITypeOpenAICompletions,
				Provider:  "local",
				BaseURL:   "http://127.0.0.1:8034/v1",
				MaxTokens: 300,
			},
		},
	}
}

func writeLoop(t *testing.T, dir string, name string, content string) string {
	t.Helper()

	loopDir := filepath.Join(dir, ".nncode", "loops")
	require.NoError(t, os.MkdirAll(loopDir, 0o755))
	path := filepath.Join(loopDir, name+".json")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))

	return path
}

func TestLoadResolvesProjectBeforeGlobal(t *testing.T) {
	projectDir := t.TempDir()
	homeDir := t.TempDir()

	writeLoop(t, homeDir, "review", `{
		"schema_version": 1,
		"name": "review",
		"description": "global",
		"nodes": [
			{"id":"entry","type":"entry_prompt","content":"global"},
			{"id":"prompt","type":"prompt","content":"prompt"},
			{"id":"exit","type":"exit_criteria","content":"done"}
		]
	}`)
	projectPath := writeLoop(t, projectDir, "review", `{
		"schema_version": 1,
		"name": "review",
		"description": "project",
		"nodes": [
			{"id":"entry","type":"entry_prompt","content":"project"},
			{"id":"prompt","type":"prompt","content":"prompt"},
			{"id":"exit","type":"exit_criteria","content":"done"}
		]
	}`)

	def, path, err := Load("review", StoreOptions{CWD: projectDir, HomeDir: homeDir})

	require.NoError(t, err)
	assert.Equal(t, projectPath, path)
	assert.Equal(t, "project", def.Description)
}

func TestListUsesRunnableFileRefWhenDefinitionNameDiffers(t *testing.T) {
	projectDir := t.TempDir()
	writeLoop(t, projectDir, "file-ref", `{
		"schema_version": 1,
		"name": "display-name",
		"description": "uses a different display name",
		"nodes": [
			{"id":"entry","type":"entry_prompt","content":"entry"},
			{"id":"prompt","type":"prompt","content":"prompt"},
			{"id":"exit","type":"exit_criteria","content":"done"}
		]
	}`)

	summaries, err := List(StoreOptions{CWD: projectDir, HomeDir: t.TempDir()})

	require.NoError(t, err)
	require.Len(t, summaries, 1)
	assert.Equal(t, "file-ref", summaries[0].Ref)
	assert.Equal(t, "display-name", summaries[0].Name)
}

func TestExampleLoopFileValid(t *testing.T) {
	path := filepath.Join("..", "..", "examples", "loops", "implement-review-fix.json")

	def, err := LoadFile(path)

	require.NoError(t, err)
	assert.Equal(t, "implement-review-fix", def.Name)
}

func TestLoadFileRejectsUnknownFields(t *testing.T) {
	projectDir := t.TempDir()
	path := writeLoop(t, projectDir, "unknown", `{
		"schema_version": 1,
		"name": "unknown",
		"metadata": {"owner": "user"},
		"nodes": [
			{"id":"entry","type":"entry_prompt","content":"entry"},
			{"id":"prompt","type":"prompt","content":"prompt"},
			{"id":"exit","type":"exit_criteria","content":"done"}
		]
	}`)

	_, err := LoadFile(path)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown field")
}

func TestLoadFileRequiresSchemaVersion(t *testing.T) {
	projectDir := t.TempDir()
	path := writeLoop(t, projectDir, "missing-version", `{
		"name": "missing-version",
		"nodes": [
			{"id":"entry","type":"entry_prompt","content":"entry"},
			{"id":"prompt","type":"prompt","content":"prompt"},
			{"id":"exit","type":"exit_criteria","content":"done"}
		]
	}`)

	_, err := LoadFile(path)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "schema_version is required")
}

func TestLoadFileRejectsUnsupportedSchemaVersion(t *testing.T) {
	projectDir := t.TempDir()
	path := writeLoop(t, projectDir, "future-version", `{
		"schema_version": 99,
		"name": "future-version",
		"nodes": [
			{"id":"entry","type":"entry_prompt","content":"entry"},
			{"id":"prompt","type":"prompt","content":"prompt"},
			{"id":"exit","type":"exit_criteria","content":"done"}
		]
	}`)

	_, err := LoadFile(path)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "schema_version is unsupported")
}

func TestLoadFileRejectsInvalidCmdSettings(t *testing.T) {
	projectDir := t.TempDir()
	path := writeLoop(t, projectDir, "bad-cmd", `{
		"schema_version": 1,
		"name": "bad-cmd",
		"nodes": [
			{"id":"entry","type":"entry_prompt","content":"entry"},
			{"id":"cmd","type":"cmd","settings":{"model":"node"},"content":"echo bad"},
			{"id":"exit","type":"exit_criteria","content":"done"}
		]
	}`)

	_, err := LoadFile(path)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "cmd node settings cannot include model")
}

func TestValidateReportsValidLoop(t *testing.T) {
	projectDir := t.TempDir()
	path := writeLoop(t, projectDir, "valid", `{
		"schema_version": 1,
		"name": "valid",
		"nodes": [
			{"id":"entry","type":"entry_prompt","content":"entry"},
			{"id":"prompt","type":"prompt","content":"prompt"},
			{"id":"exit","type":"exit_criteria","content":"done"}
		]
	}`)

	summary, err := Validate("valid", StoreOptions{CWD: projectDir, HomeDir: t.TempDir()})

	require.NoError(t, err)
	assert.Equal(t, "valid", summary.Ref)
	assert.Equal(t, path, summary.Path)
}

func TestRunnerUsesEntryInputAndModelPrecedence(t *testing.T) {
	projectDir := t.TempDir()
	writeLoop(t, projectDir, "implement", `{
		"schema_version": 1,
		"name": "implement",
		"settings": {"model": "loop", "max_iterations": 1},
		"nodes": [
			{"id":"entry","type":"entry_prompt","locked":true,"content":"Start: {{input}}"},
			{"id":"prompt","type":"prompt","settings":{"model":"node"},"content":"Do work"},
			{"id":"exit","type":"exit_criteria","content":"done?"},
			{"id":"final","type":"exit_prompt","content":"Summarize"}
		]
	}`)

	client := &mockClient{scripts: [][]llm.StreamEvent{
		textEvents("entry ok"),
		textEvents("prompt ok"),
		textEvents("Looks done.\nLOOP_EXIT: yes"),
		textEvents("final ok"),
	}}
	ag := agent.New(agent.Config{Model: llm.Model{ID: "base"}, Client: client}, "system")
	runner := Runner{Agent: ag, Config: testConfig(), StoreOptions: StoreOptions{CWD: projectDir, HomeDir: t.TempDir()}}

	eventCh, err := runner.Run(context.Background(), "implement", "ship it")
	require.NoError(t, err)
	events := drain(eventCh)

	require.Len(t, client.requests, 4)
	assert.Equal(t, "loop-provider-id", client.requests[0].Model.ID)
	assert.Equal(t, 200, client.requests[0].MaxTokens)
	assert.Contains(t, client.requests[0].Messages[len(client.requests[0].Messages)-1].Content, "Start: ship it")
	assert.Equal(t, "node-provider-id", client.requests[1].Model.ID)
	assert.Equal(t, 300, client.requests[1].MaxTokens)
	assert.Equal(t, "loop-provider-id", client.requests[2].Model.ID)
	assert.Equal(t, "loop-provider-id", client.requests[3].Model.ID)

	for _, msg := range ag.Messages() {
		assert.NotEqual(t, llm.RoleSystem, msg.Role)
		assert.NotContains(t, msg.Content, "<agent_loop>")
	}

	require.GreaterOrEqual(t, len(client.requests[0].Messages), 2)
	assert.Contains(t, client.requests[0].Messages[1].Content, "<agent_loop>")

	lifecycle := loopEvents(events)
	require.Len(t, lifecycle, 11)
	assert.Equal(t, agent.EventLoopStart, lifecycle[0].Type)
	assert.Equal(t, "implement", lifecycle[0].LoopName)
	assert.NotEmpty(t, lifecycle[0].LoopPath)
	assert.Equal(t, agent.EventLoopNodeStart, lifecycle[1].Type)
	assert.Equal(t, "entry", lifecycle[1].LoopNodeID)
	assert.Equal(t, string(NodeEntryPrompt), lifecycle[1].LoopNodeType)
	assert.Equal(t, agent.EventLoopIterationStart, lifecycle[3].Type)
	assert.Equal(t, 1, lifecycle[3].LoopIteration)
	assert.Equal(t, agent.EventLoopNodeStart, lifecycle[4].Type)
	assert.Equal(t, "prompt", lifecycle[4].LoopNodeID)
	assert.Equal(t, agent.EventLoopNodeStart, lifecycle[6].Type)
	assert.Equal(t, "exit", lifecycle[6].LoopNodeID)
	assert.Equal(t, agent.EventLoopExitDecision, lifecycle[8].Type)
	assert.True(t, lifecycle[8].LoopExit)
	assert.True(t, lifecycle[8].LoopExitMarkerFound)
	assert.Equal(t, "exit", lifecycle[8].LoopNodeID)
	assert.Equal(t, agent.EventLoopNodeStart, lifecycle[9].Type)
	assert.Equal(t, "final", lifecycle[9].LoopNodeID)
}

func TestRunnerContinuesUntilExitCriteriaPasses(t *testing.T) {
	projectDir := t.TempDir()
	writeLoop(t, projectDir, "repeat", `{
		"schema_version": 1,
		"name": "repeat",
		"settings": {"max_iterations": 2},
		"nodes": [
			{"id":"entry","type":"entry_prompt","content":"Start"},
			{"id":"prompt","type":"prompt","content":"Do work"},
			{"id":"exit","type":"exit_criteria","content":"done?"}
		]
	}`)

	client := &mockClient{scripts: [][]llm.StreamEvent{
		textEvents("entry ok"),
		textEvents("first prompt"),
		textEvents("not yet\nLOOP_EXIT: no"),
		textEvents("second prompt"),
		textEvents("done\nLOOP_EXIT: yes"),
	}}
	ag := agent.New(agent.Config{Model: llm.Model{ID: "base"}, Client: client}, "system")
	runner := Runner{Agent: ag, Config: testConfig(), StoreOptions: StoreOptions{CWD: projectDir, HomeDir: t.TempDir()}}

	events, err := runner.Run(context.Background(), "repeat", "")
	require.NoError(t, err)
	drain(events)

	require.Len(t, client.requests, 5)
	assert.Contains(t, client.requests[3].Messages[len(client.requests[3].Messages)-1].Content, "Do work")
}

func TestRunnerRestoresLockedNodesAndUsesUnlockedEditsImmediately(t *testing.T) {
	projectDir := t.TempDir()
	loopPath := writeLoop(t, projectDir, "editable", `{
		"schema_version": 1,
		"name": "editable",
		"settings": {"max_iterations": 1},
		"nodes": [
			{"id":"entry","type":"entry_prompt","locked":true,"content":"locked entry"},
			{"id":"prompt","type":"prompt","content":"original prompt"},
			{"id":"exit","type":"exit_criteria","content":"done?"}
		]
	}`)

	client := &mockClient{scripts: [][]llm.StreamEvent{
		toolCallEvents("c1", "mutate_loop", `{}`),
		textEvents("entry done"),
		textEvents("prompt done"),
		textEvents("LOOP_EXIT: yes"),
	}}
	tool := agent.Tool{
		Name:       "mutate_loop",
		Parameters: "{}",
		Execute: func(ctx context.Context, args json.RawMessage) (agent.ToolResult, error) {
			data, err := os.ReadFile(loopPath)
			if err != nil {
				return agent.ToolResult{}, err
			}

			edited := string(data)
			edited = strings.ReplaceAll(edited, "locked entry", "changed locked entry")
			edited = strings.ReplaceAll(edited, "original prompt", "updated prompt")
			if err := os.WriteFile(loopPath, []byte(edited), 0o644); err != nil {
				return agent.ToolResult{}, err
			}

			return agent.ToolResult{Content: "mutated"}, nil
		},
	}
	ag := agent.New(agent.Config{Model: llm.Model{ID: "base"}, Client: client, Tools: []agent.Tool{tool}}, "system")
	runner := Runner{Agent: ag, Config: testConfig(), StoreOptions: StoreOptions{CWD: projectDir, HomeDir: t.TempDir()}}

	events, err := runner.Run(context.Background(), "editable", "")
	require.NoError(t, err)
	drain(events)

	require.Len(t, client.requests, 4)
	assert.Contains(t, client.requests[2].Messages[len(client.requests[2].Messages)-1].Content, "updated prompt")

	data, err := os.ReadFile(loopPath)
	require.NoError(t, err)
	assert.Contains(t, string(data), "locked entry")
	assert.NotContains(t, string(data), "changed locked entry")
	assert.Contains(t, string(data), "updated prompt")
}

func TestRunnerExecutesCmdNodeAndPersistsObservation(t *testing.T) {
	projectDir := t.TempDir()
	writeLoop(t, projectDir, "cmd-loop", `{
		"schema_version": 1,
		"name": "cmd-loop",
		"settings": {"max_iterations": 1},
		"nodes": [
			{"id":"entry","type":"entry_prompt","content":"Start"},
			{"id":"check","type":"cmd","content":"printf cmd-ok"},
			{"id":"exit","type":"exit_criteria","content":"done?"}
		]
	}`)

	client := &mockClient{scripts: [][]llm.StreamEvent{
		textEvents("entry ok"),
		textEvents("LOOP_EXIT: yes"),
	}}
	ag := agent.New(agent.Config{Model: llm.Model{ID: "base"}, Client: client}, "system")
	runner := Runner{Agent: ag, Config: testConfig(), StoreOptions: StoreOptions{CWD: projectDir, HomeDir: t.TempDir()}}

	eventCh, err := runner.Run(context.Background(), "cmd-loop", "")
	require.NoError(t, err)
	events := drain(eventCh)

	require.Len(t, client.requests, 2)
	assert.Contains(t, client.requests[1].Messages[len(client.requests[1].Messages)-2].Content, "cmd-ok")

	results := toolResults(events, "cmd")
	require.Len(t, results, 1)
	assert.Equal(t, "cmd-ok", results[0].Result)
	assert.False(t, results[0].IsError)
	assert.Equal(t, int64(0), metadataInt64(results[0].Metadata, "exit_code"))

	var foundObservation bool
	for _, msg := range ag.Messages() {
		if strings.Contains(msg.Content, "Agent loop cmd node \"check\" result") &&
			strings.Contains(msg.Content, "cmd-ok") {
			foundObservation = true
		}
	}
	assert.True(t, foundObservation)
}

func TestRunnerCmdNodeDefaultAbortOnFailure(t *testing.T) {
	projectDir := t.TempDir()
	writeLoop(t, projectDir, "cmd-fail", `{
		"schema_version": 1,
		"name": "cmd-fail",
		"settings": {"max_iterations": 1},
		"nodes": [
			{"id":"entry","type":"entry_prompt","content":"Start"},
			{"id":"check","type":"cmd","content":"printf failed; exit 7"},
			{"id":"exit","type":"exit_criteria","content":"done?"}
		]
	}`)

	client := &mockClient{scripts: [][]llm.StreamEvent{textEvents("entry ok")}}
	ag := agent.New(agent.Config{Model: llm.Model{ID: "base"}, Client: client}, "system")
	runner := Runner{Agent: ag, Config: testConfig(), StoreOptions: StoreOptions{CWD: projectDir, HomeDir: t.TempDir()}}

	eventCh, err := runner.Run(context.Background(), "cmd-fail", "")
	require.NoError(t, err)
	events := drain(eventCh)

	require.Len(t, client.requests, 1)
	results := toolResults(events, "cmd")
	require.Len(t, results, 1)
	assert.True(t, results[0].IsError)
	assert.Contains(t, results[0].Result, "exit code 7")

	errs := eventErrors(events)
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0].Err.Error(), "cmd node failed")
}

func TestRunnerCmdNodeDisabledEmitsResultBeforeAbort(t *testing.T) {
	projectDir := t.TempDir()
	writeLoop(t, projectDir, "cmd-disabled", `{
		"schema_version": 1,
		"name": "cmd-disabled",
		"settings": {"max_iterations": 1},
		"nodes": [
			{"id":"entry","type":"entry_prompt","content":"Start"},
			{"id":"check","type":"cmd","content":"printf disabled"},
			{"id":"exit","type":"exit_criteria","content":"done?"}
		]
	}`)

	client := &mockClient{scripts: [][]llm.StreamEvent{textEvents("entry ok")}}
	ag := agent.New(agent.Config{Model: llm.Model{ID: "base"}, Client: client}, "system")
	cfg := testConfig()
	cfg.Tools.Disabled = []string{"bash"}
	runner := Runner{Agent: ag, Config: cfg, StoreOptions: StoreOptions{CWD: projectDir, HomeDir: t.TempDir()}}

	eventCh, err := runner.Run(context.Background(), "cmd-disabled", "")
	require.NoError(t, err)
	events := drain(eventCh)

	results := toolResults(events, "cmd")
	require.Len(t, results, 1)
	assert.True(t, results[0].IsError)
	assert.Contains(t, results[0].Result, "requires bash")

	errs := eventErrors(events)
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0].Err.Error(), "cmd node failed")

	var foundObservation bool
	for _, msg := range ag.Messages() {
		if strings.Contains(msg.Content, "cmd node requires bash") {
			foundObservation = true
		}
	}
	assert.True(t, foundObservation)
}

func TestRunnerCmdNodeCanContinueOnFailure(t *testing.T) {
	projectDir := t.TempDir()
	writeLoop(t, projectDir, "cmd-continue", `{
		"schema_version": 1,
		"name": "cmd-continue",
		"settings": {"max_iterations": 1},
		"nodes": [
			{"id":"entry","type":"entry_prompt","content":"Start"},
			{"id":"check","type":"cmd","settings":{"on_error":"continue"},"content":"printf failed; exit 7"},
			{"id":"exit","type":"exit_criteria","content":"done?"}
		]
	}`)

	client := &mockClient{scripts: [][]llm.StreamEvent{
		textEvents("entry ok"),
		textEvents("LOOP_EXIT: yes"),
	}}
	ag := agent.New(agent.Config{Model: llm.Model{ID: "base"}, Client: client}, "system")
	runner := Runner{Agent: ag, Config: testConfig(), StoreOptions: StoreOptions{CWD: projectDir, HomeDir: t.TempDir()}}

	eventCh, err := runner.Run(context.Background(), "cmd-continue", "")
	require.NoError(t, err)
	events := drain(eventCh)

	require.Len(t, client.requests, 2)
	assert.Empty(t, eventErrors(events))
	assert.Contains(t, client.requests[1].Messages[len(client.requests[1].Messages)-2].Content, "exit code 7")
}

func TestRunnerCmdNodeDryRunDoesNotExecute(t *testing.T) {
	projectDir := t.TempDir()
	target := filepath.Join(projectDir, "should-not-exist")
	writeLoop(t, projectDir, "cmd-dry-run", `{
		"schema_version": 1,
		"name": "cmd-dry-run",
		"settings": {"max_iterations": 1},
		"nodes": [
			{"id":"entry","type":"entry_prompt","content":"Start"},
			{"id":"check","type":"cmd","content":"touch `+target+`"},
			{"id":"exit","type":"exit_criteria","content":"done?"}
		]
	}`)

	client := &mockClient{scripts: [][]llm.StreamEvent{
		textEvents("entry ok"),
		textEvents("LOOP_EXIT: yes"),
	}}
	ag := agent.New(agent.Config{Model: llm.Model{ID: "base"}, Client: client, DryRun: true}, "system")
	runner := Runner{Agent: ag, Config: testConfig(), StoreOptions: StoreOptions{CWD: projectDir, HomeDir: t.TempDir()}}

	eventCh, err := runner.Run(context.Background(), "cmd-dry-run", "")
	require.NoError(t, err)
	events := drain(eventCh)

	require.NoFileExists(t, target)
	results := toolResults(events, "cmd")
	require.Len(t, results, 1)
	assert.Contains(t, results[0].Result, "[dry-run]")
	assert.Equal(t, true, results[0].Metadata["dry_run"])
}

func TestParseExitDecisionStripsMarker(t *testing.T) {
	shouldExit, markerOK, cleaned := parseExitDecision("All checks pass.\nLOOP_EXIT: yes\n")

	assert.True(t, shouldExit)
	assert.True(t, markerOK)
	assert.Equal(t, "All checks pass.", cleaned)
}
