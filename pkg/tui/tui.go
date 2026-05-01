package tui

import (
	"context"
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"nncode/internal/agent"
	"nncode/internal/config"
	"nncode/internal/contextwindow"
	"nncode/internal/session"
	"nncode/internal/skills"
)

// Run starts the Bubble Tea TUI.
func Run(
	ag *agent.Agent,
	cfg *config.Config,
	sess *session.Session,
	reg *skills.Registry,
	activator *skills.Activator,
	window contextwindow.Window,
	contextResolver func(context.Context) contextwindow.Window,
) error {
	m := newModel(ag, cfg, sess, reg, activator, window, contextResolver)
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())

	finalModel, err := p.Run()
	if err != nil {
		return fmt.Errorf("tui run: %w", err)
	}

	if fm, ok := finalModel.(*model); ok {
		fm.saveSession()
	}

	return nil
}
