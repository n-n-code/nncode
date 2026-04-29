// Package contextstats computes token and context usage summaries.
package contextstats

import (
	"nncode/internal/contextwindow"
	"nncode/internal/llm"
)

// Stats summarizes assistant usage telemetry for stored messages.
type Stats struct {
	LastTurnTokens   int
	LastPromptTokens int
	SessionTokens    int
	ByModel          map[string]int
}

// Compute scans assistant messages for usage totals and the latest prompt size.
// When the model does not report usage, it falls back to a rough character-based
// heuristic so local-model users still get context-usage guidance.
func Compute(messages []llm.Message) Stats {
	stats := Stats{
		LastTurnTokens:   0,
		LastPromptTokens: 0,
		SessionTokens:    0,
		ByModel:          make(map[string]int),
	}

	for _, msg := range messages {
		if msg.Role != llm.RoleAssistant {
			continue
		}

		stats.LastTurnTokens = msg.Usage.TotalTokens
		stats.LastPromptTokens = msg.Usage.PromptTokens
		if msg.Usage.TotalTokens <= 0 {
			continue
		}

		stats.SessionTokens += msg.Usage.TotalTokens
		if msg.Model != "" {
			stats.ByModel[msg.Model] += msg.Usage.TotalTokens
		}
	}

	if stats.LastPromptTokens <= 0 && len(messages) > 0 {
		stats.LastPromptTokens = estimateTokens(messages)
	}

	return stats
}

const charsPerToken = 4

func estimateTokens(messages []llm.Message) int {
	total := 0
	for _, msg := range messages {
		total += len(msg.Content) / charsPerToken
		for _, tc := range msg.ToolCalls {
			total += len(tc.Args) / charsPerToken
		}
	}

	return total
}

// ContextUsage renders used/free context tokens for these stats.
func (s Stats) ContextUsage(window contextwindow.Window) string {
	return contextwindow.FormatUsage(s.LastPromptTokens, window)
}

// DominantModel returns the model that accounts for the most assistant tokens.
func (s Stats) DominantModel() string {
	var dominant string
	most := 0
	for model, count := range s.ByModel {
		if count > most {
			most = count
			dominant = model
		}
	}

	return dominant
}

// ContextRatio returns the fraction of the context window used by prompt tokens.
// Returns 0 when the window is unknown or zero.
func (s Stats) ContextRatio(window contextwindow.Window) float64 {
	if !window.Known() || window.Tokens == 0 {
		return 0
	}

	return float64(s.LastPromptTokens) / float64(window.Tokens)
}
