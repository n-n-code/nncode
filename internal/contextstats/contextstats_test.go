package contextstats

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"nncode/internal/contextwindow"
	"nncode/internal/llm"
)

func TestComputeAssistantUsage(t *testing.T) {
	stats := Compute([]llm.Message{
		{Role: llm.RoleUser, Content: "hello"},
		{Role: llm.RoleAssistant, Content: "hi", Usage: llm.Usage{PromptTokens: 5, TotalTokens: 10}, Model: "m1"},
		{Role: llm.RoleAssistant, Content: "ok", Usage: llm.Usage{PromptTokens: 12, TotalTokens: 25}, Model: "m2"},
		{Role: llm.RoleTool, Content: "result"},
	})

	assert.Equal(t, 25, stats.LastTurnTokens)
	assert.Equal(t, 12, stats.LastPromptTokens)
	assert.Equal(t, 35, stats.SessionTokens)
	assert.Equal(t, map[string]int{"m1": 10, "m2": 25}, stats.ByModel)
	assert.Equal(t, "m2", stats.DominantModel())
	assert.Equal(t, "12/116", stats.ContextUsage(contextwindow.Window{
		Tokens: 128,
		Source: contextwindow.SourceConfig,
	}))
}

func TestCompute_FallsBackToHeuristicWhenUsageMissing(t *testing.T) {
	stats := Compute([]llm.Message{
		{Role: llm.RoleUser, Content: "hello world"},
		{Role: llm.RoleAssistant, Content: "hi there", Usage: llm.Usage{}},
	})

	// (11 + 8) chars / 4 = 4 tokens estimated.
	assert.Equal(t, 4, stats.LastPromptTokens)
	assert.Equal(t, "4/124", stats.ContextUsage(contextwindow.Window{
		Tokens: 128,
		Source: contextwindow.SourceConfig,
	}))
}
