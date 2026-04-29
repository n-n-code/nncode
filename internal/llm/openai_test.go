package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func newStreamClient(t *testing.T, statusCode int, body string, captured *http.Request) *OpenAIClient {
	t.Helper()

	return &OpenAIClient{
		HTTPClient: &http.Client{
			Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if captured != nil {
					body, err := io.ReadAll(r.Body)
					if err != nil {
						return nil, err
					}

					*captured = *r.Clone(r.Context())
					captured.Body = io.NopCloser(strings.NewReader(string(body)))
				}

				return &http.Response{
					StatusCode: statusCode,
					Status:     fmt.Sprintf("%d %s", statusCode, http.StatusText(statusCode)),
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader(body)),
					Request:    r,
				}, nil
			}),
		},
	}
}

func collectEvents(t *testing.T, ch <-chan StreamEvent) []StreamEvent {
	t.Helper()

	var out []StreamEvent

	timeout := time.After(2 * time.Second)

	for {
		select {
		case <-timeout:
			t.Fatalf("timed out collecting events; got %d so far", len(out))
		case ev, ok := <-ch:
			if !ok {
				return out
			}

			out = append(out, ev)
		}
	}
}

func TestStream_TextOnly(t *testing.T) {
	chunks := []string{
		"data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Hello\"}}]}\n",
		"data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\", world\"}}]}\n",
		"data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n",
		"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":2,\"total_tokens\":12}}\n",
		"data: [DONE]\n",
	}

	var req http.Request

	c := newStreamClient(t, http.StatusOK, strings.Join(chunks, ""), &req)

	ch, err := c.Stream(context.Background(), Request{
		Model:  Model{ID: "gpt-test", BaseURL: "http://example.test"},
		APIKey: "unit-test-api-key",
		Messages: []Message{
			{Role: RoleUser, Content: "hi"},
		},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	events := collectEvents(t, ch)

	var (
		text strings.Builder
		done *Done
	)

	for _, ev := range events {
		if ev.Text != "" {
			text.WriteString(ev.Text)
		}

		if ev.Done != nil {
			done = ev.Done
		}

		if ev.Err != nil {
			t.Fatalf("unexpected error event: %v", ev.Err)
		}
	}

	if text.String() != "Hello, world" {
		t.Fatalf("text = %q, want %q", text.String(), "Hello, world")
	}

	if done == nil {
		t.Fatalf("no Done event")
	}

	if done.StopReason != "stop" {
		t.Errorf("StopReason = %q, want %q", done.StopReason, "stop")
	}

	if done.Usage.TotalTokens != 12 {
		t.Errorf("Usage.TotalTokens = %d, want 12", done.Usage.TotalTokens)
	}

	if auth := req.Header.Get("Authorization"); auth != "Bearer unit-test-api-key" {
		t.Errorf("Authorization header = %q, want %q", auth, "Bearer unit-test-api-key")
	}

	if ct := req.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}

	if req.URL.Path != "/chat/completions" {
		t.Errorf("URL path = %q, want /chat/completions", req.URL.Path)
	}

	bodyBytes, _ := io.ReadAll(req.Body)

	var sent chatRequest
	if err := json.Unmarshal(bodyBytes, &sent); err != nil {
		t.Fatalf("unmarshal sent body: %v", err)
	}

	if sent.Model != "gpt-test" {
		t.Errorf("sent model = %q, want gpt-test", sent.Model)
	}

	if !sent.Stream {
		t.Error("sent stream=false, want true")
	}
}

func TestStream_ToolCall(t *testing.T) {
	chunks := []string{
		"data: {\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\"}}]}\n",
		"data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"read\",\"arguments\":\"\"}}]}}]}\n",
		"data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"{\\\"path\\\":\"}}]}}]}\n",
		"data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"\\\"main.go\\\"}\"}}]}}]}\n",
		"data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n",
		"data: [DONE]\n",
	}

	c := newStreamClient(t, http.StatusOK, strings.Join(chunks, ""), nil)

	ch, err := c.Stream(context.Background(), Request{
		Model: Model{ID: "gpt-test", BaseURL: "http://example.test"},
		Messages: []Message{
			{Role: RoleUser, Content: "read main.go"},
		},
		Tools: []Tool{
			{Name: "read", Description: "reads a file", Parameters: `{"type":"object"}`},
		},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	events := collectEvents(t, ch)

	var (
		start, end *ToolCall
		done       *Done
	)

	for _, ev := range events {
		if ev.ToolStart != nil {
			start = ev.ToolStart
		}

		if ev.ToolEnd != nil {
			end = ev.ToolEnd
		}

		if ev.Done != nil {
			done = ev.Done
		}

		if ev.Err != nil {
			t.Fatalf("unexpected error event: %v", ev.Err)
		}
	}

	if start == nil {
		t.Fatalf("no ToolStart event")
	}

	if start.Name != "read" || start.ID != "call_1" {
		t.Errorf("ToolStart = %+v", start)
	}

	if end == nil {
		t.Fatalf("no ToolEnd event")
	}

	if end.Name != "read" || string(end.Args) != `{"path":"main.go"}` {
		t.Errorf("ToolEnd = {ID:%s Name:%s Args:%s}", end.ID, end.Name, string(end.Args))
	}

	if done == nil || done.StopReason != "tool_calls" {
		t.Errorf("Done = %+v", done)
	}
}

func TestStream_HTTPError(t *testing.T) {
	c := newStreamClient(t, http.StatusUnauthorized, `{"error":{"message":"Invalid API key"}}`, nil)

	_, err := c.Stream(context.Background(), Request{
		Model:  Model{ID: "gpt-test", BaseURL: "http://example.test"},
		APIKey: "bad-unit-test-api-key",
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	if !strings.Contains(err.Error(), "HTTP 401") {
		t.Errorf("err = %v, want HTTP 401", err)
	}
}

func TestStream_ContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	c := &OpenAIClient{
		HTTPClient: &http.Client{
			Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Status:     "200 OK",
					Header:     make(http.Header),
					Body:       cancelBody{ctx: r.Context()},
					Request:    r,
				}, nil
			}),
		},
	}

	ch, err := c.Stream(ctx, Request{Model: Model{ID: "gpt-test", BaseURL: "http://example.test"}})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	timeout := time.After(2 * time.Second)

	for {
		select {
		case <-timeout:
			t.Fatal("channel did not close after context cancel")
		case _, ok := <-ch:
			if !ok {
				return
			}
		}
	}
}

type cancelBody struct {
	ctx context.Context
}

func (b cancelBody) Read(p []byte) (int, error) {
	select {
	case <-b.ctx.Done():
		return 0, b.ctx.Err()
	case <-time.After(50 * time.Millisecond):
		return copy(p, ": ping\n\n"), nil
	}
}

func (b cancelBody) Close() error {
	return nil
}

func TestStream_UsageOnEmptyChunkNoDone(t *testing.T) {
	// Some providers (e.g., Ollama with certain configs) send usage on an
	// empty-choices chunk and omit the [DONE] sentinel.
	chunks := []string{
		"data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Hi\"}}]}\n",
		"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":5,\"completion_tokens\":1,\"total_tokens\":6}}\n",
	}

	c := newStreamClient(t, http.StatusOK, strings.Join(chunks, ""), nil)

	ch, err := c.Stream(context.Background(), Request{
		Model: Model{ID: "gpt-test", BaseURL: "http://example.test"},
		Messages: []Message{
			{Role: RoleUser, Content: "hi"},
		},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	events := collectEvents(t, ch)

	var done *Done
	for _, ev := range events {
		if ev.Done != nil {
			done = ev.Done
		}
		if ev.Err != nil {
			t.Fatalf("unexpected error: %v", ev.Err)
		}
	}

	if done == nil {
		t.Fatal("expected Done event")
	}
	if done.Usage.TotalTokens != 6 {
		t.Errorf("Usage.TotalTokens = %d, want 6", done.Usage.TotalTokens)
	}
}

func TestStream_UsageBeforeFinishReason(t *testing.T) {
	// Provider sends usage on a chunk that still carries a finish_reason.
	chunks := []string{
		"data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Hi\"}}]}\n",
		"data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":5,\"completion_tokens\":1,\"total_tokens\":6}}\n",
		"data: [DONE]\n",
	}

	c := newStreamClient(t, http.StatusOK, strings.Join(chunks, ""), nil)

	ch, err := c.Stream(context.Background(), Request{
		Model: Model{ID: "gpt-test", BaseURL: "http://example.test"},
		Messages: []Message{
			{Role: RoleUser, Content: "hi"},
		},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	events := collectEvents(t, ch)

	var done *Done
	for _, ev := range events {
		if ev.Done != nil {
			done = ev.Done
		}
		if ev.Err != nil {
			t.Fatalf("unexpected error: %v", ev.Err)
		}
	}

	if done == nil {
		t.Fatal("expected Done event")
	}
	if done.Usage.TotalTokens != 6 {
		t.Errorf("Usage.TotalTokens = %d, want 6", done.Usage.TotalTokens)
	}
	if done.StopReason != "stop" {
		t.Errorf("StopReason = %q, want stop", done.StopReason)
	}
}

func TestStream_EmptyDeltaChunks(t *testing.T) {
	// Provider sends empty delta chunks before real content.
	chunks := []string{
		"data: {\"choices\":[{\"index\":0,\"delta\":{}}]}\n",
		"data: {\"choices\":[{\"index\":0,\"delta\":{}}]}\n",
		"data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Hello\"}}]}\n",
		"data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n",
		"data: [DONE]\n",
	}

	c := newStreamClient(t, http.StatusOK, strings.Join(chunks, ""), nil)

	ch, err := c.Stream(context.Background(), Request{
		Model: Model{ID: "gpt-test", BaseURL: "http://example.test"},
		Messages: []Message{
			{Role: RoleUser, Content: "hi"},
		},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	events := collectEvents(t, ch)

	var text strings.Builder
	for _, ev := range events {
		text.WriteString(ev.Text)
		if ev.Err != nil {
			t.Fatalf("unexpected error: %v", ev.Err)
		}
	}

	if text.String() != "Hello" {
		t.Errorf("text = %q, want Hello", text.String())
	}
}

func TestBuildRequestBody_ToolMessages(t *testing.T) {
	req := Request{
		Model: Model{ID: "gpt-test"},
		Messages: []Message{
			{Role: RoleSystem, Content: "you are helpful"},
			{Role: RoleUser, Content: "read main.go"},
			{
				Role:    RoleAssistant,
				Content: "",
				ToolCalls: []ToolCall{
					{ID: "c1", Name: "read", Args: json.RawMessage(`{"path":"main.go"}`)},
				},
			},
			{Role: RoleTool, Content: "package main", ToolCallID: "c1", ToolName: "read"},
		},
		Tools: []Tool{
			{Name: "read", Description: "reads", Parameters: `{"type":"object"}`},
		},
	}

	body, err := buildRequestBody(req)
	if err != nil {
		t.Fatalf("buildRequestBody: %v", err)
	}

	var decoded struct {
		Messages []struct {
			Role      string `json:"role"`
			Content   string `json:"content"`
			ToolCalls []struct {
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
			ToolCallID string `json:"tool_call_id"`
		} `json:"messages"`
		Tools []struct {
			Type     string `json:"type"`
			Function struct {
				Name       string          `json:"name"`
				Parameters json.RawMessage `json:"parameters"`
			} `json:"function"`
		} `json:"tools"`
		ToolChoice string `json:"tool_choice"`
	}

	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, string(body))
	}

	if len(decoded.Messages) != 4 {
		t.Fatalf("expected 4 messages, got %d", len(decoded.Messages))
	}

	if decoded.Messages[2].ToolCalls[0].Function.Arguments != `{"path":"main.go"}` {
		t.Errorf("tool call args round-trip broken: %q", decoded.Messages[2].ToolCalls[0].Function.Arguments)
	}

	if decoded.Messages[3].ToolCallID != "c1" {
		t.Errorf("tool result tool_call_id = %q, want c1", decoded.Messages[3].ToolCallID)
	}

	if decoded.ToolChoice != "auto" {
		t.Errorf("ToolChoice = %q, want auto", decoded.ToolChoice)
	}

	if string(decoded.Tools[0].Function.Parameters) != `{"type":"object"}` {
		t.Errorf("tool parameters = %s", string(decoded.Tools[0].Function.Parameters))
	}
}

func TestIsRetryableError_RetryableStatusCodes(t *testing.T) {
	retryable := []int{429, 502, 503, 504}
	for _, code := range retryable {
		t.Run(fmt.Sprintf("status_%d", code), func(t *testing.T) {
			assert.True(t, isRetryableError(nil, code))
		})
	}
}

func TestIsRetryableError_NonRetryableStatusCodes(t *testing.T) {
	nonRetryable := []int{200, 400, 401, 403, 404, 500}
	for _, code := range nonRetryable {
		t.Run(fmt.Sprintf("status_%d", code), func(t *testing.T) {
			assert.False(t, isRetryableError(nil, code))
		})
	}
}

func TestIsRetryableError_ContextErrors(t *testing.T) {
	assert.False(t, isRetryableError(context.Canceled, 0))
	assert.False(t, isRetryableError(context.DeadlineExceeded, 0))
}

func TestDoWithRetry_SucceedsOnFirstAttempt(t *testing.T) {
	client := &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(""))}, nil
		}),
	}

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://test", nil)
	resp, err := doWithRetry(context.Background(), client, req)

	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	require.NoError(t, resp.Body.Close())
}

func TestDoWithRetry_RetriesThenSucceeds(t *testing.T) {
	var attempts int
	client := &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			attempts++
			if attempts < 2 {
				return &http.Response{
					StatusCode: http.StatusTooManyRequests,
					Body:       io.NopCloser(strings.NewReader("rate limited")),
				}, nil
			}

			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(""))}, nil
		}),
	}

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://test", nil)
	resp, err := doWithRetry(context.Background(), client, req)

	require.NoError(t, err)
	assert.Equal(t, 2, attempts)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	require.NoError(t, resp.Body.Close())
}

func TestDoWithRetry_ExhaustsRetries(t *testing.T) {
	var attempts int
	client := &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			attempts++

			return &http.Response{
				StatusCode: http.StatusServiceUnavailable,
				Body:       io.NopCloser(strings.NewReader("unavailable")),
			}, nil
		}),
	}

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://test", nil)
	//nolint:bodyclose // doWithRetry closes non-OK response bodies internally.
	_, err := doWithRetry(context.Background(), client, req)

	require.Error(t, err)
	assert.Equal(t, maxRetries+1, attempts)
	assert.Contains(t, err.Error(), "503")
}

func TestDoWithRetry_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	client := &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return nil, context.Canceled
		}),
	}

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://test", nil)
	resp, err := doWithRetry(ctx, client, req)
	if resp != nil {
		_ = resp.Body.Close()
	}

	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestDoWithRetry_ResetsRequestBodyOnRetry(t *testing.T) {
	var bodies []string
	client := &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			body, _ := io.ReadAll(r.Body)
			bodies = append(bodies, string(body))
			if len(bodies) < 2 {
				return &http.Response{
					StatusCode: http.StatusTooManyRequests,
					Body:       io.NopCloser(strings.NewReader("rate limited")),
				}, nil
			}

			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(""))}, nil
		}),
	}

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, "http://test", strings.NewReader("payload"))
	resp, err := doWithRetry(context.Background(), client, req)

	require.NoError(t, err)
	assert.Len(t, bodies, 2)
	assert.Equal(t, "payload", bodies[0])
	assert.Equal(t, "payload", bodies[1])
	require.NoError(t, resp.Body.Close())
}
