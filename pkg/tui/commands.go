package tui

import (
	tea "github.com/charmbracelet/bubbletea"

	"nncode/internal/agent"
)

// agentEventMsg wraps a single agent.Event for delivery into the Bubble Tea loop.
type agentEventMsg struct {
	Event agent.Event
}

// agentDoneMsg signals that the agent event channel has closed.
type agentDoneMsg struct{}

// nextEventCmd blocks on the next event from the channel.
func nextEventCmd(events <-chan agent.Event) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-events
		if !ok {
			return agentDoneMsg{}
		}

		return agentEventMsg{Event: ev}
	}
}
