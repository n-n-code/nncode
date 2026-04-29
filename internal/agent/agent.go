// Package agent runs the prompt → LLM → tool call → execute → repeat loop.
//
// The Agent owns conversation history. Each call to Run appends the user
// message, streams the LLM response, executes any tool calls the model
// emits, and keeps looping until the model returns without calling a tool
// (or MaxTurns is hit).
package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"nncode/internal/contextprint"
	"nncode/internal/llm"
)

const (
	defaultMaxTurns        = 200
	defaultMaxTokens       = 65536
	defaultEventBufferSize = 32
)

var errMaxTurnsExceeded = errors.New("max turns exceeded")

// ConfirmDecision is the user's response to an effectful-tool confirmation.
type ConfirmDecision int

const (
	// ConfirmAllow runs the tool as requested.
	ConfirmAllow ConfirmDecision = iota
	// ConfirmSkip cancels just this tool call so the agent can try another path.
	ConfirmSkip
	// ConfirmStop halts the run cleanly; remaining effectful calls in the same
	// turn are also skipped and no further LLM turns are issued.
	ConfirmStop
)

// ConfirmRequest is the payload passed to an EffectfulToolConfirm callback.
type ConfirmRequest struct {
	Name string
	Args string
	Turn int
}

type Config struct {
	Model                llm.Model
	Client               llm.Client
	APIKey               string
	Tools                []Tool
	MaxTurns             int
	MaxTokens            int
	Temperature          float64
	DryRun               bool
	EffectfulToolConfirm func(ctx context.Context, req ConfirmRequest) (ConfirmDecision, error)
}

// RunOptions applies to one RunWithOptions call without changing the agent's
// stored configuration.
type RunOptions struct {
	Model                llm.Model
	MaxTokens            int
	ScopedSystemMessages []string
	Metadata             map[string]any
}

type Agent struct {
	cfg          Config
	systemPrompt string
	messages     []llm.Message
}

func New(cfg Config, systemPrompt string) *Agent {
	if cfg.MaxTurns == 0 {
		cfg.MaxTurns = defaultMaxTurns
	}

	if cfg.MaxTokens == 0 {
		cfg.MaxTokens = defaultMaxTokens
	}

	return &Agent{cfg: cfg, systemPrompt: systemPrompt, messages: nil}
}

func (a *Agent) SystemPrompt() string     { return a.systemPrompt }
func (a *Agent) SetSystemPrompt(p string) { a.systemPrompt = p }
func (a *Agent) Messages() []llm.Message  { return append([]llm.Message(nil), a.messages...) }
func (a *Agent) Tools() []Tool            { return a.cfg.Tools }
func (a *Agent) SetTools(t []Tool)        { a.cfg.Tools = t }
func (a *Agent) Model() llm.Model         { return a.cfg.Model }
func (a *Agent) SetModel(m llm.Model)     { a.cfg.Model = m }
func (a *Agent) SetAPIKey(k string)       { a.cfg.APIKey = k }
func (a *Agent) DryRun() bool             { return a.cfg.DryRun }
func (a *Agent) SetEffectfulToolConfirm(fn func(ctx context.Context, req ConfirmRequest) (ConfirmDecision, error)) {
	a.cfg.EffectfulToolConfirm = fn
}
func (a *Agent) AddSystemMessage(content string) {
	a.addMessage(llm.RoleSystem, content)
}

// AddObservationMessage appends non-user runtime context to history. Chat APIs
// do not have a portable observation role, so observations are stored as
// user-compatible messages while keeping call sites semantically explicit.
func (a *Agent) AddObservationMessage(content string) {
	a.addMessage(llm.RoleUser, content)
}

func (a *Agent) SetMessages(m []llm.Message) {
	a.messages = append([]llm.Message(nil), m...)
}

// Reset clears conversation history. Config (model, tools, system prompt)
// is preserved.
func (a *Agent) Reset() { a.messages = nil }

const compressSystemPrompt = "You are a conversation compressor. Summarize the provided conversation into a " +
	"concise, self-contained paragraph that preserves all requirements, decisions, " +
	"and context needed to continue the work. " +
	"Include file paths, design choices, and any errors encountered. The summary will replace the conversation history."

// Compress sends the current conversation to the model and returns a summary
// that can be used as a replacement system context.
//
// Note: if the conversation already exceeds the model's context window, the
// compression request itself may fail. This is a known edge case; callers should
// handle the error gracefully.
func (a *Agent) Compress(ctx context.Context) (string, error) {
	text := contextprint.Format(a.systemPrompt, a.messages)
	req := llm.Request{
		Model:     a.cfg.Model,
		APIKey:    a.cfg.APIKey,
		MaxTokens: a.cfg.MaxTokens,
		Messages: []llm.Message{
			{Role: llm.RoleSystem, Content: compressSystemPrompt},
			{Role: llm.RoleUser, Content: text},
		},
	}

	events, err := a.cfg.Client.Stream(ctx, req)
	if err != nil {
		return "", fmt.Errorf("stream compression: %w", err)
	}

	var summary strings.Builder
	for ev := range events {
		if ev.Err != nil {
			return "", ev.Err
		}
		if ev.Text != "" {
			summary.WriteString(ev.Text)
		}
	}

	return strings.TrimSpace(summary.String()), nil
}

func (a *Agent) Run(ctx context.Context, userMsg string) <-chan Event {
	return a.RunWithOptions(ctx, userMsg, RunOptions{})
}

// RunWithOptions appends the user message to history and starts the loop in a
// goroutine, applying scoped request overrides for this single prompt turn.
func (a *Agent) RunWithOptions(ctx context.Context, userMsg string, opts RunOptions) <-chan Event {
	a.messages = append(a.messages, llm.Message{
		Role:       llm.RoleUser,
		Content:    userMsg,
		ToolCalls:  nil,
		ToolCallID: "",
		ToolName:   "",
		Timestamp:  0,
	})

	opts.ScopedSystemMessages = nonBlankStrings(opts.ScopedSystemMessages)
	events := make(chan Event, defaultEventBufferSize)

	go func() {
		defer close(events)

		a.runLoop(ctx, events, opts)
	}()

	return events
}

func (a *Agent) addMessage(role llm.Role, content string) {
	a.messages = append(a.messages, llm.Message{
		Role:       role,
		Content:    content,
		ToolCalls:  nil,
		ToolCallID: "",
		ToolName:   "",
		Timestamp:  0,
	})
}

func nonBlankStrings(in []string) []string {
	out := in[:0:0]
	for _, s := range in {
		if strings.TrimSpace(s) != "" {
			out = append(out, s)
		}
	}

	return out
}
