package tui

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
	"github.com/muesli/termenv"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// firstUnbackedCell walks rendered ANSI text cell by cell, tracking SGR
// background state, and returns the cell index of the first visible cell
// that lacks a non-default background. Returns -1 if every cell is backed.
//
// This is the test-side mirror of the invariant ensureBackground enforces in
// fillBlock: every cell of a body, bar, or popup region must carry a bg.
func firstUnbackedCell(rendered string) (int, string) {
	cell := 0
	bgSet := false
	for len(rendered) > 0 {
		if loc := matchSGRLength(rendered); loc > 0 {
			bgSet = updateBgState(rendered[2:loc-1], bgSet)
			rendered = rendered[loc:]

			continue
		}

		r, size := utf8.DecodeRuneInString(rendered)
		rendered = rendered[size:]

		if r == '\n' || r == '\r' {
			cell = 0

			continue
		}

		w := runewidth.RuneWidth(r)
		if w == 0 {
			continue
		}

		if !bgSet {
			return cell, fmt.Sprintf("rune=%q at cell %d has no background", r, cell)
		}
		cell += w
	}

	return -1, ""
}

// assertContiguousBackground fails the test if any cell in any line of
// rendered lacks a background SGR. Each newline-separated line is checked
// independently — bg state does not carry across lines, mirroring how the
// terminal redraws each row.
func assertContiguousBackground(t *testing.T, label, rendered string) {
	t.Helper()
	for i, line := range strings.Split(rendered, "\n") {
		if line == "" {
			continue
		}
		cell, msg := firstUnbackedCell(line)
		assert.Equalf(t, -1, cell,
			"%s line %d: %s\nrendered: %q", label, i, msg, line)
	}
}

func setupTrueColor(t *testing.T) {
	t.Helper()
	lipgloss.SetColorProfile(termenv.TrueColor)
}

func TestStatusBarHasContinuousBackground(t *testing.T) {
	setupTrueColor(t)
	m := newTestModel(&mockClient{})
	m.width = 80
	m.height = 24
	m.recalcLayout()
	assertContiguousBackground(t, "statusBar", m.statusView())
}

func TestHeaderBarHasContinuousBackground(t *testing.T) {
	setupTrueColor(t)
	m := newTestModel(&mockClient{})
	m.width = 80
	m.height = 24
	m.recalcLayout()
	assertContiguousBackground(t, "headerBar", m.headerView())
}

func TestOverlayHelpHasContinuousBackground(t *testing.T) {
	setupTrueColor(t)
	m := newTestModel(&mockClient{})
	m.width = 80
	m.height = 24
	m.recalcLayout()
	m.openHelpOverlay()
	assertContiguousBackground(t, "helpOverlay", m.overlayView())
}

func TestUserMessageHasContinuousBackground(t *testing.T) {
	setupTrueColor(t)
	rendered := renderUserMsg(
		"hello there world this is a longer message that wraps eventually onto a second line",
		78,
	)
	require.Contains(t, rendered, "\n", "expected the test message to wrap to two lines")
	assertContiguousBackground(t, "userMsg", rendered)
}

func TestAssistantMessageHasContinuousBackground(t *testing.T) {
	setupTrueColor(t)
	rendered := renderAssistantMsg("ok here is a reply", 78)
	assertContiguousBackground(t, "assistantMsg", rendered)
}

func TestToolCallHasContinuousBackground(t *testing.T) {
	setupTrueColor(t)
	rendered := renderToolCall("read", `{"path":"main.go"}`, 78)
	assertContiguousBackground(t, "toolCall", rendered)
}

func TestToolResultHasContinuousBackground(t *testing.T) {
	setupTrueColor(t)
	rendered := renderToolResult("read", "package main", false, false, 78)
	assertContiguousBackground(t, "toolResult", rendered)
}

func TestLoopStatusHasContinuousBackground(t *testing.T) {
	setupTrueColor(t)
	rendered := renderLoopStatus("node implement (prompt)", 78)
	assertContiguousBackground(t, "loopStatus", rendered)
}

func TestFullViewBodyHasContinuousBackground(t *testing.T) {
	setupTrueColor(t)
	m := newTestModel(&mockClient{})
	m.width = 80
	m.height = 24
	m.recalcLayout()
	m.syncViewportContent(false)
	assertContiguousBackground(t, "fullView", m.View())
}
