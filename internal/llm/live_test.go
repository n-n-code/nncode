package llm

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestLiveStream(t *testing.T) {
	baseURL := os.Getenv("NNCODE_LIVE_BASE_URL")

	model := os.Getenv("NNCODE_LIVE_MODEL")
	if baseURL == "" || model == "" {
		t.Skip("set NNCODE_LIVE_BASE_URL and NNCODE_LIVE_MODEL to run live LLM smoke test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ch, err := NewOpenAIClient().Stream(ctx, Request{
		Model:  Model{ID: model, BaseURL: baseURL},
		APIKey: os.Getenv("NNCODE_LIVE_API_KEY"),
		Messages: []Message{
			{Role: RoleUser, Content: "Reply with ok only."},
		},
		MaxTokens: 256,
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	var (
		text strings.Builder
		done bool
	)

	for ev := range ch {
		if ev.Err != nil {
			t.Fatalf("stream event error: %v", ev.Err)
		}

		text.WriteString(ev.Text)

		if ev.Done != nil {
			done = true
		}
	}

	if !done {
		t.Fatal("stream ended without Done event")
	}

	if strings.TrimSpace(text.String()) == "" {
		t.Fatal("live model returned no text")
	}
}
