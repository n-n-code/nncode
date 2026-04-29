package session

import (
	"encoding/json"
	"fmt"
	"strings"

	"nncode/internal/llm"
)

// Change represents a single file-modifying tool call.
type Change struct {
	Tool   string
	File   string
	Detail string
}

// ExtractChanges scans messages for effectful tool calls that modify files.
// It cross-references tool results so failed calls are annotated accordingly.
func ExtractChanges(msgs []llm.Message) []Change {
	var changes []Change

	resultErrors := make(map[string]bool)
	for _, msg := range msgs {
		if msg.Role == llm.RoleTool && msg.ToolCallID != "" {
			resultErrors[msg.ToolCallID] = msg.IsError
		}
	}

	for _, msg := range msgs {
		if msg.Role != llm.RoleAssistant {
			continue
		}

		for _, tc := range msg.ToolCalls {
			change := parseToolCall(tc)
			if change == nil {
				continue
			}

			if resultErrors[tc.ID] {
				change.Detail += " (failed)"
			}

			changes = append(changes, *change)
		}
	}

	return changes
}

func parseToolCall(tc llm.ToolCall) *Change {
	var args map[string]any
	if err := json.Unmarshal(tc.Args, &args); err != nil {
		return nil
	}

	switch tc.Name {
	case "write":
		path, _ := args["path"].(string)
		if path == "" {
			return nil
		}

		return &Change{Tool: "write", File: path, Detail: "wrote file"}
	case "edit":
		path, _ := args["path"].(string)
		if path == "" {
			return nil
		}

		oldStr, _ := args["old_string"].(string)
		newStr, _ := args["new_string"].(string)

		return &Change{
			Tool:   "edit",
			File:   path,
			Detail: fmt.Sprintf("replaced %d chars with %d chars", len(oldStr), len(newStr)),
		}
	case "patch":
		path, _ := args["path"].(string)
		if path == "" {
			return nil
		}

		return &Change{Tool: "patch", File: path, Detail: "applied unified diff"}
	case "bash":
		command, _ := args["command"].(string)
		if command == "" {
			return nil
		}

		return &Change{Tool: "bash", File: "", Detail: command}
	default:
		return nil
	}
}

// FormatChanges renders changes as a human-readable report.
func FormatChanges(changes []Change) string {
	if len(changes) == 0 {
		return "No file changes recorded in this session."
	}

	var buf strings.Builder
	fmt.Fprintf(&buf, "Changes (%d):\n\n", len(changes))

	for _, c := range changes {
		if c.File != "" {
			fmt.Fprintf(&buf, "- **%s** `%s` — %s\n", c.Tool, c.File, c.Detail)
		} else {
			fmt.Fprintf(&buf, "- **%s** — %s\n", c.Tool, c.Detail)
		}
	}

	return buf.String()
}
