package tui

import "github.com/charmbracelet/lipgloss"

// nⁿcode visual identity.
//
// Direction:
//   - Old MS-DOS / CRT terminal
//   - Black background
//   - Green phosphor text
//   - Minimal, sharp, professional
//   - No decorative copy unless explicitly needed.

//nolint:gochecknoglobals // Style variables are exported for reuse across the TUI package.
var (
	// ColorBlack is the main background black color.
	ColorBlack        = lipgloss.Color("#020403")
	ColorNearBlack    = lipgloss.Color("#050A06")
	ColorDeepGreen    = lipgloss.Color("#0A1A0D")
	ColorCRTGreen     = lipgloss.Color("#39FF45")
	ColorCRTGreenDim  = lipgloss.Color("#1FAE35")
	ColorCRTGreenSoft = lipgloss.Color("#88FF8F")
	ColorBrightGreen  = lipgloss.Color("#4FFF5C")
	ColorCRTShadow    = lipgloss.Color("#083D12")
	ColorMutedGreen   = lipgloss.Color("#6CA875")
	ColorWarningAmber = lipgloss.Color("#FFB000")
	ColorErrorRed     = lipgloss.Color("#FF4D4D")

	// App is the base terminal surface style.
	App = lipgloss.NewStyle().
		Foreground(ColorCRTGreen).
		Background(ColorBlack)

	// Screen is the main screen frame style.
	Screen = lipgloss.NewStyle().
		Foreground(ColorCRTGreen).
		Background(ColorBlack).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ColorCRTGreenDim).
		Padding(1, 2)

	// Panel is the panel / card container style.
	Panel = lipgloss.NewStyle().
		Foreground(ColorCRTGreen).
		Background(ColorNearBlack).
		Border(lipgloss.NormalBorder()).
		BorderForeground(ColorCRTShadow).
		Padding(1, 2)

	// PanelActive is the active panel or selected state style.
	PanelActive = lipgloss.NewStyle().
			Foreground(ColorCRTGreenSoft).
			Background(ColorNearBlack).
			Border(lipgloss.NormalBorder()).
			BorderForeground(ColorCRTGreen).
			Padding(1, 2).
			Bold(true)

	// Title is the primary heading style.
	Title = lipgloss.NewStyle().
		Foreground(ColorCRTGreenSoft).
		Background(ColorNearBlack).
		Bold(true)

	// Subtitle is the secondary heading style.
	Subtitle = lipgloss.NewStyle().
			Foreground(ColorCRTGreenDim).
			Background(ColorBlack).
			Bold(true)

	// Body is the normal body text style.
	Body = lipgloss.NewStyle().
		Foreground(ColorCRTGreen).
		Background(ColorBlack)

	// Muted is the dimmed helper text style.
	Muted = lipgloss.NewStyle().
		Foreground(ColorMutedGreen).
		Background(ColorBlack)

	// Prompt is the CLI prompt marker style.
	//
	// Use sparingly. Do not place this next to the logo.
	Prompt = lipgloss.NewStyle().
		Foreground(ColorCRTGreenSoft).
		Background(ColorBlack).
		Bold(true)

	// Input is the user input style.
	Input = lipgloss.NewStyle().
		Foreground(ColorCRTGreenSoft).
		Background(ColorNearBlack)

	// Success is the successful state style.
	Success = lipgloss.NewStyle().
		Foreground(ColorCRTGreenSoft).
		Background(ColorBlack).
		Bold(true)

	// Warning is the warning state style.
	Warning = lipgloss.NewStyle().
		Foreground(ColorWarningAmber).
		Background(ColorBlack).
		Bold(true)

	// Error is the error state style.
	Error = lipgloss.NewStyle().
		Foreground(ColorErrorRed).
		Background(ColorBlack).
		Bold(true).
		Padding(0, 1)

	// HeaderBar is the top header bar background.
	HeaderBar = lipgloss.NewStyle().
			Foreground(ColorCRTGreen).
			Background(ColorCRTShadow).
			Padding(0, 1)

	// HeaderLogo styles the wordmark inside the header bar.
	HeaderLogo = lipgloss.NewStyle().
			Foreground(ColorBrightGreen).
			Background(ColorCRTShadow).
			Bold(true)

	// HeaderInfo styles the right-aligned info text inside the header bar.
	HeaderInfo = lipgloss.NewStyle().
			Foreground(ColorMutedGreen).
			Background(ColorCRTShadow)

	// StatusBar is the status bar style.
	StatusBar = lipgloss.NewStyle().
			Foreground(ColorCRTGreen).
			Background(ColorCRTShadow).
			Padding(0, 1)

	// StatusKey is the status bar key / label style.
	StatusKey = lipgloss.NewStyle().
			Foreground(ColorCRTGreenSoft).
			Background(ColorCRTShadow).
			Bold(true)

	// StatusValue is the status bar value style.
	StatusValue = lipgloss.NewStyle().
			Foreground(ColorCRTGreen).
			Background(ColorCRTShadow)

	// TableHeader is the table header style.
	TableHeader = lipgloss.NewStyle().
			Foreground(ColorCRTGreenSoft).
			Background(ColorCRTShadow).
			Bold(true).
			Padding(0, 1)

	// TableCell is the table cell style.
	TableCell = lipgloss.NewStyle().
			Foreground(ColorCRTGreen).
			Background(ColorNearBlack).
			Padding(0, 1)

	// SelectedRow is the selected table/list row style.
	SelectedRow = lipgloss.NewStyle().
			Foreground(ColorCRTGreenSoft).
			Background(ColorDeepGreen).
			Bold(true).
			Padding(0, 1)

	// CodeBlock is the code block / command output style.
	CodeBlock = lipgloss.NewStyle().
			Foreground(ColorCRTGreen).
			Background(ColorNearBlack).
			Border(lipgloss.NormalBorder()).
			BorderForeground(ColorCRTShadow).
			Padding(1, 2)

	// InlineCode is the inline command / path / technical token style.
	InlineCode = lipgloss.NewStyle().
			Foreground(ColorCRTGreenSoft).
			Background(ColorCRTShadow).
			Padding(0, 1)

	// Divider is the divider line style.
	Divider = lipgloss.NewStyle().
		Foreground(ColorCRTShadow).
		Background(ColorBlack)

	// TUI-specific styles.

	// UserMsg is the user message bubble style.
	UserMsg = lipgloss.NewStyle().
		Foreground(ColorCRTGreenSoft).
		Background(ColorNearBlack).
		Padding(0, 1)

	// AssistantMsg is the assistant message text style.
	AssistantMsg = lipgloss.NewStyle().
			Foreground(ColorCRTGreen).
			Background(ColorBlack).
			Padding(0, 1)

	// ToolCall is the compact tool call indicator style.
	ToolCall = lipgloss.NewStyle().
			Foreground(ColorCRTGreenDim).
			Background(ColorNearBlack).
			Padding(0, 1)

	// ToolResult is the tool result preview (success) style.
	ToolResult = lipgloss.NewStyle().
			Foreground(ColorMutedGreen).
			Background(ColorBlack).
			Padding(0, 1)

	// OverlayBackdrop is the overlay background dimmer style.
	OverlayBackdrop = lipgloss.NewStyle().
			Background(ColorBlack)

	// OverlaySurface is the modal content background.
	OverlaySurface = lipgloss.NewStyle().
			Foreground(ColorCRTGreen).
			Background(ColorNearBlack)

	// Overlay is the overlay container style.
	Overlay = lipgloss.NewStyle().
		Foreground(ColorCRTGreen).
		Background(ColorNearBlack).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ColorCRTGreenDim).
		BorderBackground(ColorNearBlack).
		Padding(1, 2)

	// OverlayHint is the modal footer hint style.
	OverlayHint = lipgloss.NewStyle().
			Foreground(ColorMutedGreen).
			Background(ColorNearBlack)

	// Spinner is the spinner / waiting indicator style.
	Spinner = lipgloss.NewStyle().
		Foreground(ColorCRTGreenSoft).
		Background(ColorBlack)
)
