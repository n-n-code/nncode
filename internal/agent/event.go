package agent

import "nncode/internal/llm"

type EventType int

const (
	EventText EventType = iota
	EventToolCallStart
	EventToolCallEnd
	EventToolResult
	EventTurnStart
	EventTurnEnd
	EventError
	EventDone
)

func (t EventType) String() string {
	switch t {
	case EventText:
		return "text"
	case EventToolCallStart:
		return "tool_call_start"
	case EventToolCallEnd:
		return "tool_call_end"
	case EventToolResult:
		return "tool_result"
	case EventTurnStart:
		return "turn_start"
	case EventTurnEnd:
		return "turn_end"
	case EventError:
		return "error"
	case EventDone:
		return "done"
	default:
		return "unknown"
	}
}

// Event is emitted by Agent.Run. Only the fields relevant to Type are set.
type Event struct {
	Type     EventType
	Text     string // EventText
	ToolID   string // EventToolCall*, EventToolResult
	ToolName string // EventToolCall*, EventToolResult
	ToolArgs string // EventToolCallEnd — the fully-assembled args JSON
	Result   string // EventToolResult
	IsError  bool   // EventToolResult
	Metadata map[string]any
	Err      error // EventError
	Turn     int   // EventTurn*
	Usage    llm.Usage
}
