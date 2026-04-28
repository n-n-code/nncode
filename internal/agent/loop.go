package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"nncode/internal/llm"
)

func (a *Agent) runLoop(ctx context.Context, out chan<- Event, opts RunOptions) {
	model, maxTokens := a.requestOverrides(opts)
	tools := a.buildTools()

	for turn := 1; turn <= a.cfg.MaxTurns; turn++ {
		err := ctx.Err()
		if err != nil {
			emit(ctx, out, Event{Type: EventError, Err: err})

			return
		}

		if !emit(ctx, out, Event{Type: EventTurnStart, Turn: turn}) {
			return
		}

		req := llm.Request{
			Model:       model,
			Messages:    a.buildMessages(opts),
			Tools:       tools,
			APIKey:      a.cfg.APIKey,
			MaxTokens:   maxTokens,
			Temperature: a.cfg.Temperature,
		}

		streamCh, err := a.cfg.Client.Stream(ctx, req)
		if err != nil {
			emit(ctx, out, Event{Type: EventError, Err: fmt.Errorf("stream: %w", err)})

			return
		}

		text, toolCalls, usage, streamErr := collectStream(ctx, streamCh, out)
		if streamErr != nil {
			emit(ctx, out, Event{Type: EventError, Err: streamErr})

			return
		}

		a.messages = append(a.messages, llm.Message{
			Role:      llm.RoleAssistant,
			Content:   text,
			ToolCalls: toolCalls,
		})

		if !emit(ctx, out, Event{Type: EventTurnEnd, Turn: turn, Usage: usage}) {
			return
		}

		if len(toolCalls) == 0 {
			emit(ctx, out, Event{Type: EventDone, Usage: usage})

			return
		}

		results := make([]ToolResult, len(toolCalls))

		// Execute non-effectful tools in parallel.
		var waitGroup sync.WaitGroup

		for i, tc := range toolCalls {
			if isEffectfulTool(tc.Name) {
				continue
			}

			waitGroup.Add(1)

			go func(idx int, call llm.ToolCall) {
				defer waitGroup.Done()
				results[idx] = a.executeTool(ctx, call)
			}(i, tc)
		}

		waitGroup.Wait()

		if err := ctx.Err(); err != nil {
			emit(ctx, out, Event{Type: EventError, Err: err})

			return
		}

		// Execute effectful tools sequentially in original order.
		for i, tc := range toolCalls {
			if !isEffectfulTool(tc.Name) {
				continue
			}

			results[i] = a.executeTool(ctx, tc)
		}

		// Emit results and append messages in original order.
		for i, tc := range toolCalls {
			a.messages = append(a.messages, llm.Message{
				Role:       llm.RoleTool,
				Content:    results[i].Content,
				ToolCallID: tc.ID,
				ToolName:   tc.Name,
			})

			if !emit(ctx, out, Event{
				Type:     EventToolResult,
				ToolID:   tc.ID,
				ToolName: tc.Name,
				Result:   results[i].Content,
				IsError:  results[i].IsError,
				Metadata: results[i].Metadata,
			}) {
				return
			}
		}
	}

	emit(ctx, out, Event{Type: EventError, Err: fmt.Errorf("%w: %d", errMaxTurnsExceeded, a.cfg.MaxTurns)})
}

func (a *Agent) requestOverrides(opts RunOptions) (llm.Model, int) {
	model := a.cfg.Model
	if opts.Model.ID != "" {
		model = opts.Model
	}

	maxTokens := a.cfg.MaxTokens
	if opts.MaxTokens != 0 {
		maxTokens = opts.MaxTokens
	}

	return model, maxTokens
}

func (a *Agent) buildMessages(opts RunOptions) []llm.Message {
	msgs := make([]llm.Message, 0, len(a.messages)+1+len(opts.ScopedSystemMessages))
	if a.systemPrompt != "" {
		msgs = append(msgs, llm.Message{Role: llm.RoleSystem, Content: a.systemPrompt})
	}

	for _, content := range opts.ScopedSystemMessages {
		msgs = append(msgs, llm.Message{Role: llm.RoleSystem, Content: content})
	}

	return append(msgs, a.messages...)
}

func (a *Agent) buildTools() []llm.Tool {
	out := make([]llm.Tool, len(a.cfg.Tools))
	for i, t := range a.cfg.Tools {
		out[i] = llm.Tool{Name: t.Name, Description: t.Description, Parameters: t.Parameters}
	}

	return out
}

func (a *Agent) findTool(name string) *Tool {
	for i := range a.cfg.Tools {
		if a.cfg.Tools[i].Name == name {
			return &a.cfg.Tools[i]
		}
	}

	return nil
}

func (a *Agent) executeTool(ctx context.Context, tc llm.ToolCall) ToolResult {
	tool := a.findTool(tc.Name)
	if tool == nil {
		return ToolResult{Content: fmt.Sprintf("Unknown tool: %q", tc.Name), IsError: true}
	}

	if a.cfg.DryRun && isEffectfulTool(tc.Name) {
		return ToolResult{
			Content: fmt.Sprintf("[dry-run] Would execute %s with args: %s", tc.Name, string(tc.Args)),
			Metadata: map[string]any{
				"dry_run": true,
				"tool":    tc.Name,
			},
		}
	}

	result, err := tool.Execute(ctx, tc.Args)
	if err != nil {
		return ToolResult{Content: fmt.Sprintf("Error: %v", err), IsError: true}
	}

	return result
}

func isEffectfulTool(name string) bool {
	switch name {
	case "write", "edit", "patch", "bash":
		return true
	default:
		return false
	}
}

func collectStream(
	ctx context.Context, streamCh <-chan llm.StreamEvent, out chan<- Event,
) (string, []llm.ToolCall, llm.Usage, error) {
	var (
		text      strings.Builder
		toolCalls []llm.ToolCall
		usage     llm.Usage
	)

	for ev := range streamCh {
		if ev.Err != nil {
			return text.String(), toolCalls, usage, ev.Err
		}

		if ev.Text != "" {
			text.WriteString(ev.Text)

			if !emit(ctx, out, Event{Type: EventText, Text: ev.Text}) {
				return text.String(), toolCalls, usage, ctx.Err()
			}
		}

		if ev.ToolStart != nil {
			if !emit(ctx, out, Event{Type: EventToolCallStart, ToolID: ev.ToolStart.ID, ToolName: ev.ToolStart.Name}) {
				return text.String(), toolCalls, usage, ctx.Err()
			}
		}

		if ev.ToolEnd != nil {
			toolCalls = append(toolCalls, *ev.ToolEnd)
			if !emit(ctx, out, Event{
				Type:     EventToolCallEnd,
				ToolID:   ev.ToolEnd.ID,
				ToolName: ev.ToolEnd.Name,
				ToolArgs: string(ev.ToolEnd.Args),
			}) {
				return text.String(), toolCalls, usage, ctx.Err()
			}
		}

		if ev.Done != nil {
			usage = ev.Done.Usage
		}
	}

	return text.String(), toolCalls, usage, nil
}

func emit(ctx context.Context, out chan<- Event, ev Event) bool {
	select {
	case <-ctx.Done():
		return false
	case out <- ev:
		return true
	}
}
