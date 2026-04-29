package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"nncode/internal/llm"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockClient is a test double for llm.Client. Each entry in Scripts is the
// complete stream for one Stream() call.
type mockClient struct {
	Scripts   [][]llm.StreamEvent
	Fallback  []llm.StreamEvent
	StreamErr error

	Calls         int
	LastRequest   llm.Request
	RequestBlock  chan struct{} // if non-nil, wait on this before sending events
	OnStreamStart func()
}

var _ llm.Client = (*mockClient)(nil)

func (m *mockClient) Stream(ctx context.Context, req llm.Request) (<-chan llm.StreamEvent, error) {
	m.Calls++

	m.LastRequest = req
	if m.OnStreamStart != nil {
		m.OnStreamStart()
	}

	if m.StreamErr != nil {
		return nil, m.StreamErr
	}

	var events []llm.StreamEvent
	if len(m.Scripts) > 0 {
		events = m.Scripts[0]
		m.Scripts = m.Scripts[1:]
	} else {
		events = m.Fallback
	}

	ch := make(chan llm.StreamEvent, len(events)+1)

	go func() {
		defer close(ch)

		if m.RequestBlock != nil {
			select {
			case <-ctx.Done():
				return
			case <-m.RequestBlock:
			}
		}

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

// scriptText is a convenience that returns a single-text-and-done script.
func scriptText(text string) []llm.StreamEvent {
	return []llm.StreamEvent{
		{Text: text},
		{Done: &llm.Done{StopReason: "stop", Usage: llm.Usage{TotalTokens: 10}}},
	}
}

// scriptToolCall returns a script with a single tool call.
func scriptToolCall(id, name, args string) []llm.StreamEvent {
	call := llm.ToolCall{ID: id, Name: name, Args: json.RawMessage(args)}

	return []llm.StreamEvent{
		{ToolStart: &call},
		{ToolEnd: &call},
		{Done: &llm.Done{StopReason: "tool_calls"}},
	}
}

func drain(ch <-chan Event) []Event {
	var events []Event
	for e := range ch {
		events = append(events, e)
	}

	return events
}

func findType(events []Event, t EventType) []Event {
	var out []Event

	for _, e := range events {
		if e.Type == t {
			out = append(out, e)
		}
	}

	return out
}

func TestAgent_TextOnly(t *testing.T) {
	mock := &mockClient{Scripts: [][]llm.StreamEvent{scriptText("Hello!")}}
	agent := New(Config{
		Model:  llm.Model{ID: "test"},
		Client: mock,
		APIKey: "unit-test-api-key",
	}, "be helpful")

	events := drain(agent.Run(context.Background(), "hi"))

	assert.Equal(t, 1, mock.Calls)

	texts := findType(events, EventText)
	require.Len(t, texts, 1)
	assert.Equal(t, "Hello!", texts[0].Text)

	msgs := agent.Messages()
	require.Len(t, msgs, 2)
	assert.Equal(t, llm.RoleUser, msgs[0].Role)
	assert.Equal(t, llm.RoleAssistant, msgs[1].Role)
	assert.Equal(t, "Hello!", msgs[1].Content)

	// System prompt and APIKey were plumbed through.
	assert.Equal(t, "unit-test-api-key", mock.LastRequest.APIKey)
	require.GreaterOrEqual(t, len(mock.LastRequest.Messages), 1)
	assert.Equal(t, llm.RoleSystem, mock.LastRequest.Messages[0].Role)
	assert.Equal(t, "be helpful", mock.LastRequest.Messages[0].Content)

	// Verify ModelID is present on lifecycle events.
	turnStarts := findType(events, EventTurnStart)
	require.Len(t, turnStarts, 1)
	assert.Equal(t, "test", turnStarts[0].ModelID)

	turnEnds := findType(events, EventTurnEnd)
	require.Len(t, turnEnds, 1)
	assert.Equal(t, "test", turnEnds[0].ModelID)
	assert.Equal(t, 10, turnEnds[0].Usage.TotalTokens)

	dones := findType(events, EventDone)
	require.Len(t, dones, 1)
	assert.Equal(t, "test", dones[0].ModelID)

	// Verify usage and model are persisted on the assistant message.
	assert.Equal(t, 10, msgs[1].Usage.TotalTokens)
	assert.Equal(t, "test", msgs[1].Model)
	assert.Equal(t, 1, msgs[1].Metadata["turn"])
}

func TestAgent_RunWithOptionsMetadataPersisted(t *testing.T) {
	mock := &mockClient{Fallback: scriptText("ok")}
	ag := New(Config{Model: llm.Model{ID: "test"}, Client: mock}, "")

	drain(ag.RunWithOptions(context.Background(), "do", RunOptions{
		Metadata: map[string]any{"custom_key": "custom_value"},
	}))

	msgs := ag.Messages()
	require.Len(t, msgs, 2)
	assert.Equal(t, "custom_value", msgs[1].Metadata["custom_key"])
	assert.Equal(t, 1, msgs[1].Metadata["turn"])
}

func TestAgent_ToolCall_ThenFinalText(t *testing.T) {
	mock := &mockClient{
		Scripts: [][]llm.StreamEvent{
			scriptToolCall("c1", "echo", `{"msg":"hi"}`),
			scriptText("done"),
		},
	}
	invoked := 0
	tool := Tool{
		Name:        "echo",
		Description: "echo",
		Parameters:  `{"type":"object"}`,
		Execute: func(ctx context.Context, args json.RawMessage) (ToolResult, error) {
			invoked++

			return ToolResult{Content: "echoed: " + string(args)}, nil
		},
	}
	agent := New(Config{Model: llm.Model{ID: "test"}, Client: mock, Tools: []Tool{tool}}, "")

	events := drain(agent.Run(context.Background(), "please echo"))

	assert.Equal(t, 2, mock.Calls, "loop should run a second turn after tool call")
	assert.Equal(t, 1, invoked)

	starts := findType(events, EventToolCallStart)
	require.Len(t, starts, 1)
	assert.Equal(t, "echo", starts[0].ToolName)

	ends := findType(events, EventToolCallEnd)
	require.Len(t, ends, 1)
	assert.JSONEq(t, `{"msg":"hi"}`, ends[0].ToolArgs)

	results := findType(events, EventToolResult)
	require.Len(t, results, 1)
	assert.Equal(t, `echoed: {"msg":"hi"}`, results[0].Result)
	assert.False(t, results[0].IsError)

	msgs := agent.Messages()
	require.Len(t, msgs, 4) // user, assistant(tool call), tool, assistant(final)
	assert.Equal(t, llm.RoleTool, msgs[2].Role)
	assert.Equal(t, `echoed: {"msg":"hi"}`, msgs[2].Content)
	assert.Equal(t, "c1", msgs[2].ToolCallID)
}

func TestAgent_UnknownTool(t *testing.T) {
	mock := &mockClient{
		Scripts: [][]llm.StreamEvent{
			scriptToolCall("c1", "doesnotexist", `{}`),
			scriptText("sorry"),
		},
	}
	agent := New(Config{Model: llm.Model{ID: "test"}, Client: mock}, "")

	events := drain(agent.Run(context.Background(), "try it"))

	results := findType(events, EventToolResult)
	require.Len(t, results, 1)
	assert.True(t, results[0].IsError)
	assert.Contains(t, results[0].Result, "Unknown tool")
}

func TestAgent_ToolExecError(t *testing.T) {
	mock := &mockClient{
		Scripts: [][]llm.StreamEvent{
			scriptToolCall("c1", "fail", `{}`),
			scriptText("ok"),
		},
	}
	tool := Tool{
		Name: "fail", Parameters: "{}",
		Execute: func(ctx context.Context, args json.RawMessage) (ToolResult, error) {
			return ToolResult{}, errors.New("boom")
		},
	}
	agent := New(Config{Model: llm.Model{ID: "test"}, Client: mock, Tools: []Tool{tool}}, "")

	events := drain(agent.Run(context.Background(), "go"))
	results := findType(events, EventToolResult)
	require.Len(t, results, 1)
	assert.True(t, results[0].IsError)
	assert.Contains(t, results[0].Result, "boom")

	// The loop should continue to the second turn after a tool error.
	assert.Equal(t, 2, mock.Calls)
}

func TestAgent_StreamError(t *testing.T) {
	mock := &mockClient{StreamErr: errors.New("HTTP 401")}
	agent := New(Config{Model: llm.Model{ID: "test"}, Client: mock}, "")

	events := drain(agent.Run(context.Background(), "x"))
	errs := findType(events, EventError)
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0].Err.Error(), "HTTP 401")
}

func TestAgent_MaxTurns(t *testing.T) {
	// Always return a tool call → loop will keep going until MaxTurns.
	mock := &mockClient{Fallback: scriptToolCall("c1", "noop", `{}`)}
	tool := Tool{
		Name: "noop", Parameters: "{}",
		Execute: func(ctx context.Context, args json.RawMessage) (ToolResult, error) {
			return ToolResult{Content: "nothing"}, nil
		},
	}
	agent := New(Config{
		Model: llm.Model{ID: "test"}, Client: mock, Tools: []Tool{tool}, MaxTurns: 3,
	}, "")

	events := drain(agent.Run(context.Background(), "loop"))

	assert.Equal(t, 3, mock.Calls)

	errs := findType(events, EventError)
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0].Err.Error(), "max turns")
}

func TestAgent_ContextCancelStops(t *testing.T) {
	block := make(chan struct{})
	mock := &mockClient{
		RequestBlock: block,
		Fallback:     scriptText("never"),
	}
	agent := New(Config{Model: llm.Model{ID: "test"}, Client: mock}, "")

	ctx, cancel := context.WithCancel(context.Background())
	ch := agent.Run(ctx, "hello")

	// Cancel before the mock emits anything.
	time.AfterFunc(20*time.Millisecond, func() {
		cancel()
		close(block)
	})

	done := make(chan struct{})

	go func() {
		drain(ch)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not return after context cancel")
	}
}

func TestAgent_Reset(t *testing.T) {
	mock := &mockClient{Fallback: scriptText("hi")}
	agent := New(Config{Model: llm.Model{ID: "test"}, Client: mock}, "sp")
	drain(agent.Run(context.Background(), "msg"))
	require.Len(t, agent.Messages(), 2)
	agent.Reset()
	assert.Empty(t, agent.Messages())
	assert.Equal(t, "sp", agent.SystemPrompt(), "Reset should preserve system prompt")
}

func TestAgent_SystemPromptOmittedWhenEmpty(t *testing.T) {
	mock := &mockClient{Fallback: scriptText("x")}
	agent := New(Config{Model: llm.Model{ID: "test"}, Client: mock}, "")
	drain(agent.Run(context.Background(), "m"))

	for _, msg := range mock.LastRequest.Messages {
		if msg.Role == llm.RoleSystem {
			t.Fatalf("expected no system message, got %q", msg.Content)
		}
	}
}

func TestAgent_AddObservationMessageUsesUserCompatibleRole(t *testing.T) {
	agent := New(Config{Model: llm.Model{ID: "test"}, Client: &mockClient{}}, "")

	agent.AddObservationMessage("observed context")

	msgs := agent.Messages()
	require.Len(t, msgs, 1)
	assert.Equal(t, llm.RoleUser, msgs[0].Role)
	assert.Equal(t, "observed context", msgs[0].Content)
}

func TestAgent_BuildTools(t *testing.T) {
	mock := &mockClient{Fallback: scriptText("x")}
	tool := Tool{Name: "read", Description: "reads", Parameters: `{"type":"object"}`}
	agent := New(Config{Model: llm.Model{ID: "test"}, Client: mock, Tools: []Tool{tool}}, "")
	drain(agent.Run(context.Background(), "m"))
	require.Len(t, mock.LastRequest.Tools, 1)
	assert.Equal(t, "read", mock.LastRequest.Tools[0].Name)
	assert.JSONEq(t, `{"type":"object"}`, mock.LastRequest.Tools[0].Parameters)
}

func TestAgent_MultipleToolCallsInOneTurn(t *testing.T) {
	call1 := llm.ToolCall{ID: "c1", Name: "t", Args: json.RawMessage(`{}`)}
	call2 := llm.ToolCall{ID: "c2", Name: "t", Args: json.RawMessage(`{}`)}
	mock := &mockClient{
		Scripts: [][]llm.StreamEvent{
			{
				{ToolStart: &call1},
				{ToolEnd: &call1},
				{ToolStart: &call2},
				{ToolEnd: &call2},
				{Done: &llm.Done{StopReason: "tool_calls"}},
			},
			scriptText("done"),
		},
	}
	n := 0
	tool := Tool{
		Name: "t", Parameters: "{}",
		Execute: func(ctx context.Context, args json.RawMessage) (ToolResult, error) {
			n++

			return ToolResult{Content: fmt.Sprintf("ok %d", n)}, nil
		},
	}
	agent := New(Config{Model: llm.Model{ID: "test"}, Client: mock, Tools: []Tool{tool}}, "")
	drain(agent.Run(context.Background(), "do"))
	assert.Equal(t, 2, n)
}

func TestAgent_ParallelReadOnlyToolCalls(t *testing.T) {
	call1 := llm.ToolCall{ID: "c1", Name: "read", Args: json.RawMessage(`{})`)}
	call2 := llm.ToolCall{ID: "c2", Name: "read", Args: json.RawMessage(`{})`)}
	call3 := llm.ToolCall{ID: "c3", Name: "write", Args: json.RawMessage(`{})`)}

	mock := &mockClient{
		Scripts: [][]llm.StreamEvent{
			{
				{ToolStart: &call1},
				{ToolEnd: &call1},
				{ToolStart: &call2},
				{ToolEnd: &call2},
				{ToolStart: &call3},
				{ToolEnd: &call3},
				{Done: &llm.Done{StopReason: "tool_calls"}},
			},
			scriptText("done"),
		},
	}

	var order []string
	var mu sync.Mutex

	readTool := Tool{
		Name: "read", Parameters: "{}",
		Execute: func(ctx context.Context, args json.RawMessage) (ToolResult, error) {
			mu.Lock()
			order = append(order, "read")
			mu.Unlock()

			return ToolResult{Content: "read ok"}, nil
		},
	}
	writeTool := Tool{
		Name: "write", Parameters: "{}",
		Execute: func(ctx context.Context, args json.RawMessage) (ToolResult, error) {
			mu.Lock()
			order = append(order, "write")
			mu.Unlock()

			return ToolResult{Content: "write ok"}, nil
		},
	}

	agent := New(Config{Model: llm.Model{ID: "test"}, Client: mock, Tools: []Tool{readTool, writeTool}}, "")
	drain(agent.Run(context.Background(), "do"))

	assert.Equal(t, 2, mock.Calls)
	mu.Lock()
	assert.Len(t, order, 3, "all three tools should execute")
	mu.Unlock()

	msgs := agent.Messages()
	require.Len(t, msgs, 6) // user, assistant, tool1, tool2, tool3, assistant(final)
	assert.Equal(t, llm.RoleTool, msgs[2].Role)
	assert.Equal(t, "c1", msgs[2].ToolCallID)
	assert.Equal(t, "read ok", msgs[2].Content)
	assert.Equal(t, llm.RoleTool, msgs[3].Role)
	assert.Equal(t, "c2", msgs[3].ToolCallID)
	assert.Equal(t, "read ok", msgs[3].Content)
	assert.Equal(t, llm.RoleTool, msgs[4].Role)
	assert.Equal(t, "c3", msgs[4].ToolCallID)
	assert.Equal(t, "write ok", msgs[4].Content)
}

func TestAgent_ParallelPreservesEventOrder(t *testing.T) {
	call1 := llm.ToolCall{ID: "c1", Name: "read", Args: json.RawMessage(`{})`)}
	call2 := llm.ToolCall{ID: "c2", Name: "write", Args: json.RawMessage(`{})`)}

	mock := &mockClient{
		Scripts: [][]llm.StreamEvent{
			{
				{ToolStart: &call1},
				{ToolEnd: &call1},
				{ToolStart: &call2},
				{ToolEnd: &call2},
				{Done: &llm.Done{StopReason: "tool_calls"}},
			},
			scriptText("done"),
		},
	}

	readTool := Tool{
		Name: "read", Parameters: "{}",
		Execute: func(ctx context.Context, args json.RawMessage) (ToolResult, error) {
			return ToolResult{Content: "read-result"}, nil
		},
	}
	writeTool := Tool{
		Name: "write", Parameters: "{}",
		Execute: func(ctx context.Context, args json.RawMessage) (ToolResult, error) {
			return ToolResult{Content: "write-result"}, nil
		},
	}

	agent := New(Config{Model: llm.Model{ID: "test"}, Client: mock, Tools: []Tool{readTool, writeTool}}, "")
	events := drain(agent.Run(context.Background(), "do"))

	results := findType(events, EventToolResult)
	require.Len(t, results, 2)
	assert.Equal(t, "c1", results[0].ToolID)
	assert.Equal(t, "read-result", results[0].Result)
	assert.Equal(t, "c2", results[1].ToolID)
	assert.Equal(t, "write-result", results[1].Result)
}

func TestAgent_RunWithOptionsOverridesRequestOnly(t *testing.T) {
	mock := &mockClient{Fallback: scriptText("ok")}
	agent := New(Config{
		Model:     llm.Model{ID: "base", BaseURL: "http://base.example/v1"},
		Client:    mock,
		MaxTokens: 100,
	}, "")

	// Verify overridden model ID appears on events and the assistant message.
	events := drain(agent.RunWithOptions(context.Background(), "do", RunOptions{
		Model:                llm.Model{ID: "override", BaseURL: "http://override.example/v1"},
		MaxTokens:            42,
		ScopedSystemMessages: nil,
	}))

	assert.Equal(t, "override", mock.LastRequest.Model.ID)
	assert.Equal(t, "http://override.example/v1", mock.LastRequest.Model.BaseURL)
	assert.Equal(t, 42, mock.LastRequest.MaxTokens)
	assert.Equal(t, "base", agent.Model().ID)

	turnEnds := findType(events, EventTurnEnd)
	require.Len(t, turnEnds, 1)
	assert.Equal(t, "override", turnEnds[0].ModelID)
	msgs := agent.Messages()
	require.Len(t, msgs, 2)
	assert.Equal(t, "override", msgs[1].Model)
}

func TestAgent_ConfirmStopHaltsCleanly(t *testing.T) {
	bashCall := llm.ToolCall{ID: "c1", Name: "bash", Args: json.RawMessage(`{"command":"rm -rf /"}`)}
	writeCall := llm.ToolCall{ID: "c2", Name: "write", Args: json.RawMessage(`{"path":"x"}`)}

	mock := &mockClient{
		Scripts: [][]llm.StreamEvent{{
			{ToolStart: &bashCall},
			{ToolEnd: &bashCall},
			{ToolStart: &writeCall},
			{ToolEnd: &writeCall},
			{Done: &llm.Done{StopReason: "tool_calls"}},
		}},
	}

	bashTool := Tool{Name: "bash", Parameters: "{}",
		Execute: func(ctx context.Context, args json.RawMessage) (ToolResult, error) {
			t.Fatal("bash should not be executed when user picks stop")
			return ToolResult{}, nil
		}}
	writeTool := Tool{Name: "write", Parameters: "{}",
		Execute: func(ctx context.Context, args json.RawMessage) (ToolResult, error) {
			t.Fatal("write should not be executed once stop is selected")
			return ToolResult{}, nil
		}}

	confirmCalls := 0
	ag := New(Config{
		Model: llm.Model{ID: "test"}, Client: mock, Tools: []Tool{bashTool, writeTool},
		EffectfulToolConfirm: func(ctx context.Context, req ConfirmRequest) (ConfirmDecision, error) {
			confirmCalls++
			return ConfirmStop, nil
		},
	}, "")

	events := drain(ag.Run(context.Background(), "go"))

	assert.Equal(t, 1, confirmCalls, "stop should short-circuit; second tool must not re-prompt")
	assert.Equal(t, 1, mock.Calls, "no further LLM turns after user stop")

	results := findType(events, EventToolResult)
	require.Len(t, results, 2)
	for _, r := range results {
		assert.Contains(t, r.Result, "[stopped]")
		assert.Equal(t, true, r.Metadata["user_stopped"])
	}

	dones := findType(events, EventDone)
	require.Len(t, dones, 1)
	assert.Equal(t, true, dones[0].Metadata["stopped_by_user"])

	errs := findType(events, EventError)
	assert.Empty(t, errs, "user-stop is a clean halt, not an error")

	// Conversation state stays valid: every tool_use has a matching tool_result.
	var assistantToolCalls, toolResultMsgs int
	for _, msg := range ag.Messages() {
		if msg.Role == llm.RoleAssistant {
			assistantToolCalls += len(msg.ToolCalls)
		}
		if msg.Role == llm.RoleTool {
			toolResultMsgs++
		}
	}
	assert.Equal(t, assistantToolCalls, toolResultMsgs)
}

func TestAgent_ConfirmSkipContinues(t *testing.T) {
	bashCall := llm.ToolCall{ID: "c1", Name: "bash", Args: json.RawMessage(`{"command":"ls"}`)}

	mock := &mockClient{
		Scripts: [][]llm.StreamEvent{
			{
				{ToolStart: &bashCall},
				{ToolEnd: &bashCall},
				{Done: &llm.Done{StopReason: "tool_calls"}},
			},
			scriptText("understood"),
		},
	}

	executed := 0
	bashTool := Tool{Name: "bash", Parameters: "{}",
		Execute: func(ctx context.Context, args json.RawMessage) (ToolResult, error) {
			executed++
			return ToolResult{Content: "ok"}, nil
		}}

	ag := New(Config{
		Model: llm.Model{ID: "test"}, Client: mock, Tools: []Tool{bashTool},
		EffectfulToolConfirm: func(ctx context.Context, req ConfirmRequest) (ConfirmDecision, error) {
			return ConfirmSkip, nil
		},
	}, "")

	events := drain(ag.Run(context.Background(), "go"))

	assert.Equal(t, 0, executed, "skip must not invoke the tool")
	assert.Equal(t, 2, mock.Calls, "skip continues the loop to the next turn")

	results := findType(events, EventToolResult)
	require.Len(t, results, 1)
	assert.Contains(t, results[0].Result, "[skipped]")
}

func TestAgent_RunWithOptionsScopedSystemMessagesAreNotPersisted(t *testing.T) {
	mock := &mockClient{Fallback: scriptText("ok")}
	agent := New(Config{Model: llm.Model{ID: "test"}, Client: mock}, "base system")

	drain(agent.RunWithOptions(context.Background(), "do", RunOptions{
		Model:                llm.Model{ID: "", BaseURL: ""},
		MaxTokens:            0,
		ScopedSystemMessages: []string{"scoped only"},
	}))

	require.Len(t, mock.LastRequest.Messages, 3)
	assert.Equal(t, llm.RoleSystem, mock.LastRequest.Messages[0].Role)
	assert.Equal(t, "base system", mock.LastRequest.Messages[0].Content)
	assert.Equal(t, llm.RoleSystem, mock.LastRequest.Messages[1].Role)
	assert.Equal(t, "scoped only", mock.LastRequest.Messages[1].Content)

	for _, msg := range agent.Messages() {
		assert.NotEqual(t, llm.RoleSystem, msg.Role)
		assert.NotContains(t, msg.Content, "scoped only")
	}
}
