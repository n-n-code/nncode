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

	"nncode/internal/llm"
)

const (
	defaultMaxTurns        = 20
	defaultMaxTokens       = 4096
	defaultEventBufferSize = 32
)

var errMaxTurnsExceeded = errors.New("max turns exceeded")

type Config struct {
	Model       llm.Model
	Client      llm.Client
	APIKey      string
	Tools       []Tool
	MaxTurns    int
	MaxTokens   int
	Temperature float64
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
func (a *Agent) AddSystemMessage(content string) {
	a.messages = append(a.messages, llm.Message{
		Role:       llm.RoleSystem,
		Content:    content,
		ToolCalls:  nil,
		ToolCallID: "",
		ToolName:   "",
		Timestamp:  0,
	})
}
func (a *Agent) SetMessages(m []llm.Message) {
	a.messages = append([]llm.Message(nil), m...)
}

// Reset clears conversation history. Config (model, tools, system prompt)
// is preserved.
func (a *Agent) Reset() { a.messages = nil }

// Run appends the user message to history and starts the loop in a goroutine.
// Events stream on the returned channel; the channel closes when the turn
// ends (either naturally, on error, or on context cancellation).
func (a *Agent) Run(ctx context.Context, userMsg string) <-chan Event {
	a.messages = append(a.messages, llm.Message{
		Role:       llm.RoleUser,
		Content:    userMsg,
		ToolCalls:  nil,
		ToolCallID: "",
		ToolName:   "",
		Timestamp:  0,
	})
	events := make(chan Event, defaultEventBufferSize)

	go func() {
		defer close(events)

		a.runLoop(ctx, events)
	}()

	return events
}
