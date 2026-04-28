package agent

import (
	"fmt"

	"nncode/internal/llm"
)

type EventType int

const (
	EventText EventType = iota
	EventToolCallStart
	EventToolCallEnd
	EventToolResult
	EventTurnStart
	EventTurnEnd
	EventLoopStart
	EventLoopIterationStart
	EventLoopNodeStart
	EventLoopNodeEnd
	EventLoopExitDecision
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
	case EventLoopStart:
		return "loop_start"
	case EventLoopIterationStart:
		return "loop_iteration_start"
	case EventLoopNodeStart:
		return "loop_node_start"
	case EventLoopNodeEnd:
		return "loop_node_end"
	case EventLoopExitDecision:
		return "loop_exit_decision"
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

	LoopName            string // EventLoop*
	LoopPath            string // EventLoopStart
	LoopIteration       int    // EventLoopIterationStart, EventLoopExitDecision
	LoopNodeID          string // EventLoopNode*
	LoopNodeType        string // EventLoopNode*
	LoopExit            bool   // EventLoopExitDecision
	LoopExitMarkerFound bool   // EventLoopExitDecision
}

// LoopText renders the loop lifecycle event as a single status line, or "" if
// the event is not a loop event the user surfaces (CLI dim line / TUI status).
func (e Event) LoopText() string {
	//nolint:exhaustive // Only loop-lifecycle events render as status; others return "".
	switch e.Type {
	case EventLoopStart:
		return "loop " + e.LoopName
	case EventLoopIterationStart:
		return fmt.Sprintf("iteration %d", e.LoopIteration)
	case EventLoopNodeStart:
		return fmt.Sprintf("node %s (%s)", e.LoopNodeID, e.LoopNodeType)
	case EventLoopExitDecision:
		decision := "continue"
		if e.LoopExit {
			decision = "exit"
		}

		if !e.LoopExitMarkerFound {
			return "exit criteria: " + decision + " (marker missing)"
		}

		return "exit criteria: " + decision
	default:
		return ""
	}
}
