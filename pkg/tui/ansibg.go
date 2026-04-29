package tui

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

// ensureBackground rewrites a single rendered line so that every visible cell
// carries an ANSI background. Cells already inside a bg-styled region pass
// through unchanged; bare cells (typically internal padding emitted by Bubble
// components or lipgloss.Place) get wrapped with padBG's SGR sequences.
//
// This makes fillBlock's "every cell has a background" guarantee robust
// against inner components that emit ANSI resets followed by literal spaces.
// Without this, terminal-default cells leak through the gaps and show as
// visible bars/strips against a darker theme.
func ensureBackground(line string, padBG lipgloss.TerminalColor) string {
	if line == "" {
		return line
	}

	bgOpen := backgroundOpenSequence(padBG)
	if bgOpen == "" {
		return line
	}

	const sgrOverhead = 16

	var out strings.Builder
	out.Grow(len(line) + sgrOverhead)

	bgInjected := false
	closeInjected := func() {
		if bgInjected {
			out.WriteString("\x1b[0m")
			bgInjected = false
		}
	}

	bgFromInner := false
	remaining := line
	for len(remaining) > 0 {
		if loc := matchSGRLength(remaining); loc > 0 {
			closeInjected()
			out.WriteString(remaining[:loc])
			bgFromInner = updateBgState(remaining[2:loc-1], false)
			remaining = remaining[loc:]

			continue
		}

		r, size := utf8.DecodeRuneInString(remaining)
		if r == '\n' {
			closeInjected()
			out.WriteByte('\n')
			remaining = remaining[size:]

			continue
		}

		if runewidth.RuneWidth(r) > 0 && !bgFromInner && !bgInjected {
			out.WriteString(bgOpen)
			bgInjected = true
		}

		out.WriteString(remaining[:size])
		remaining = remaining[size:]
	}

	closeInjected()

	return out.String()
}

// backgroundOpenSequence returns the bare ANSI prefix that opens a background
// of the given color, by sampling lipgloss's own renderer. Empty string if
// lipgloss does not emit a background SGR for the active profile (e.g. when
// running with the no-color profile in CI).
func backgroundOpenSequence(c lipgloss.TerminalColor) string {
	sample := lipgloss.NewStyle().Background(c).Render(" ")
	idx := strings.Index(sample, " ")
	if idx <= 0 {
		return ""
	}

	return sample[:idx]
}

// matchSGRLength returns the byte length of an SGR escape sequence at the
// start of buf, or 0 if none. SGR sequences look like \x1b[<digits or ;>m.
func matchSGRLength(buf string) int {
	if !strings.HasPrefix(buf, "\x1b[") {
		return 0
	}

	for i := 2; i < len(buf); i++ {
		c := buf[i]
		if c == 'm' {
			return i + 1
		}
		if (c < '0' || c > '9') && c != ';' {
			return 0
		}
	}

	return 0
}

// updateBgState updates background-set state from an SGR parameter list.
// Recognises full reset (0 / empty), explicit bg reset (49), 8-color bg
// (40-47), bright bg (100-107), 256-color bg (48;5;N) and truecolor bg
// (48;2;R;G;B).
func updateBgState(params string, current bool) bool {
	if params == "" {
		return false
	}

	parts := strings.Split(params, ";")
	for i := 0; i < len(parts); {
		switch param := parts[i]; param {
		case "0", "49":
			current = false
			i++
		case "48":
			// Extended-bg introducer consumes its sub-params atomically;
			// a malformed tail (missing operands) is skipped without state change.
			switch {
			case i+2 < len(parts) && parts[i+1] == "5":
				current = true
				i += 3
			case i+4 < len(parts) && parts[i+1] == "2":
				current = true
				i += 5
			default:
				i++
			}
		default:
			if n, err := strconv.Atoi(param); err == nil &&
				((n >= 40 && n <= 47) || (n >= 100 && n <= 107)) {
				current = true
			}
			i++
		}
	}

	return current
}
