package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
)

var errHTTPError = errors.New("HTTP error")

// OpenAIClient speaks the OpenAI Chat Completions API. It works against
// api.openai.com as well as any OpenAI-compatible server (Ollama, LM Studio,
// vLLM) via Model.BaseURL.
type OpenAIClient struct {
	HTTPClient *http.Client
}

func NewOpenAIClient() *OpenAIClient {
	return &OpenAIClient{HTTPClient: http.DefaultClient}
}

type chatRequest struct {
	Model         string         `json:"model"`
	Messages      []chatMessage  `json:"messages"`
	Tools         []chatTool     `json:"tools,omitempty"`
	ToolChoice    string         `json:"tool_choice,omitempty"`
	MaxTokens     int            `json:"max_tokens,omitempty"`
	Temperature   float64        `json:"temperature,omitempty"`
	Stream        bool           `json:"stream"`
	StreamOptions *streamOptions `json:"stream_options,omitempty"`
}

type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type chatMessage struct {
	Role       string         `json:"role"`
	Content    string         `json:"content"`
	ToolCalls  []chatToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
}

type chatTool struct {
	Type     string      `json:"type"`
	Function chatToolDef `json:"function"`
}

type chatToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

type chatToolCall struct {
	Index    int            `json:"index,omitempty"`
	ID       string         `json:"id,omitempty"`
	Type     string         `json:"type,omitempty"`
	Function chatToolCallFn `json:"function"`
}

type chatToolCallFn struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments"`
}

type chatChunk struct {
	Choices []chatChoice `json:"choices"`
	Usage   *chatUsage   `json:"usage,omitempty"`
}

type chatChoice struct {
	Index        int       `json:"index"`
	Delta        chatDelta `json:"delta"`
	FinishReason string    `json:"finish_reason,omitempty"`
}

type chatDelta struct {
	Role      string         `json:"role,omitempty"`
	Content   string         `json:"content,omitempty"`
	ToolCalls []chatToolCall `json:"tool_calls,omitempty"`
}

type chatUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

func (c *OpenAIClient) Stream(ctx context.Context, req Request) (<-chan StreamEvent, error) {
	body, err := buildRequestBody(req)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	baseURL := strings.TrimRight(req.Model.BaseURL, "/")
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}

	endpoint := baseURL + "/chat/completions"

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")

	if req.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+req.APIKey)
	}

	client := c.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}

	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		defer func() { _ = resp.Body.Close() }()

		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))

		return nil, fmt.Errorf("HTTP %d: %s: %w", resp.StatusCode, strings.TrimSpace(string(errBody)), errHTTPError)
	}

	events := make(chan StreamEvent, 32)

	go func() {
		defer close(events)
		defer func() { _ = resp.Body.Close() }()

		parseStream(ctx, resp.Body, events)
	}()

	return events, nil
}

func buildRequestBody(req Request) ([]byte, error) {
	payload := chatRequest{
		Model:         req.Model.ID,
		Messages:      convertMessages(req.Messages),
		Stream:        true,
		StreamOptions: &streamOptions{IncludeUsage: true},
	}
	if req.MaxTokens > 0 {
		payload.MaxTokens = req.MaxTokens
	}

	if req.Temperature > 0 {
		payload.Temperature = req.Temperature
	}

	if len(req.Tools) > 0 {
		payload.Tools = convertTools(req.Tools)
		payload.ToolChoice = "auto"
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal request body: %w", err)
	}

	return data, nil
}

func convertMessages(msgs []Message) []chatMessage {
	out := make([]chatMessage, 0, len(msgs))

	for _, msg := range msgs {
		outMsg := chatMessage{
			Role:    string(msg.Role),
			Content: msg.Content,
		}
		if msg.Role == RoleTool {
			outMsg.ToolCallID = msg.ToolCallID
		}

		if len(msg.ToolCalls) > 0 {
			outMsg.ToolCalls = make([]chatToolCall, len(msg.ToolCalls))

			for i, tc := range msg.ToolCalls {
				args := string(tc.Args)
				if args == "" {
					args = "{}"
				}

				outMsg.ToolCalls[i] = chatToolCall{
					ID:   tc.ID,
					Type: "function",
					Function: chatToolCallFn{
						Name:      tc.Name,
						Arguments: args,
					},
				}
			}
		}

		out = append(out, outMsg)
	}

	return out
}

func convertTools(tools []Tool) []chatTool {
	out := make([]chatTool, len(tools))

	for i, t := range tools {
		params := t.Parameters
		if params == "" {
			params = "{}"
		}

		out[i] = chatTool{
			Type: "function",
			Function: chatToolDef{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  json.RawMessage(params),
			},
		}
	}

	return out
}

type streamPartial struct {
	id, name string
	args     strings.Builder
	started  bool
}

func parseStream(ctx context.Context, body io.Reader, events chan<- StreamEvent) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	partials := make(map[int]*streamPartial)
	var stopReason string
	var usage Usage
	flushed := false

	emit := func(ev StreamEvent) bool {
		select {
		case <-ctx.Done():
			return false
		case events <- ev:
			return true
		}
	}

	flush := func() bool {
		if flushed {
			return true
		}

		flushed = true

		idx := make([]int, 0, len(partials))
		for i := range partials {
			idx = append(idx, i)
		}

		sort.Ints(idx)

		for _, i := range idx {
			part := partials[i]

			args := part.args.String()
			if args == "" {
				args = "{}"
			}

			if !emit(StreamEvent{ToolEnd: &ToolCall{ID: part.id, Name: part.name, Args: json.RawMessage(args)}}) {
				return false
			}
		}

		return true
	}

	for scanner.Scan() {
		if ctx.Err() != nil {
			return
		}

		if done := processStreamLine(scanner.Text(), emit, flush, partials, &usage, &stopReason); done {
			return
		}
	}

	err := scanner.Err()
	if err != nil {
		if ctx.Err() == nil {
			emit(StreamEvent{Err: fmt.Errorf("read stream: %w", err)})
		}

		return
	}

	if !flush() {
		return
	}

	emit(StreamEvent{Done: &Done{StopReason: stopReason, Usage: usage}})
}

func processStreamLine(
	line string,
	emit func(StreamEvent) bool,
	flush func() bool,
	partials map[int]*streamPartial,
	usage *Usage,
	stopReason *string,
) bool {
	if !strings.HasPrefix(line, "data: ") {
		return false
	}

	data := strings.TrimPrefix(line, "data: ")
	if data == "[DONE]" {
		if !flush() {
			return true
		}

		emit(StreamEvent{Done: &Done{StopReason: *stopReason, Usage: *usage}})

		return true
	}

	var chunk chatChunk
	err := json.Unmarshal([]byte(data), &chunk)
	if err != nil {
		return false
	}

	if chunk.Usage != nil {
		*usage = Usage{
			PromptTokens:     chunk.Usage.PromptTokens,
			CompletionTokens: chunk.Usage.CompletionTokens,
			TotalTokens:      chunk.Usage.TotalTokens,
		}
	}

	if len(chunk.Choices) == 0 {
		return false
	}

	choice := chunk.Choices[0]

	if choice.Delta.Content != "" {
		if !emit(StreamEvent{Text: choice.Delta.Content}) {
			return true
		}
	}

	for _, tc := range choice.Delta.ToolCalls {
		part, ok := partials[tc.Index]
		if !ok {
			part = &streamPartial{}
			partials[tc.Index] = part
		}

		if tc.ID != "" {
			part.id = tc.ID
		}

		if tc.Function.Name != "" {
			part.name = tc.Function.Name
		}

		if tc.Function.Arguments != "" {
			part.args.WriteString(tc.Function.Arguments)
		}

		if !part.started && part.id != "" && part.name != "" {
			part.started = true
			if !emit(StreamEvent{ToolStart: &ToolCall{ID: part.id, Name: part.name}}) {
				return true
			}
		}
	}

	if choice.FinishReason != "" {
		*stopReason = choice.FinishReason
		if !flush() {
			return true
		}
	}

	return false
}
