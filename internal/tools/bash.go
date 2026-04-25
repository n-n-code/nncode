package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"nncode/internal/agent"
)

// Bash returns the bash command tool.
func Bash(options ...Options) agent.Tool {
	opts := resolveOptions(options)

	return agent.Tool{
		Name: "bash",
		Description: "Execute a bash command in the shell. " +
			"Use this for running commands, installing packages, or any shell operation. " +
			"Output is captured and returned.",
		Parameters: `{
			"type": "object",
			"properties": {
				"command": {
					"type": "string",
					"description": "The bash command to execute"
				}
			},
			"required": ["command"]
		}`,
		Execute: func(ctx context.Context, args json.RawMessage) (agent.ToolResult, error) {
			var params struct {
				Command string `json:"command"`
			}

			err := json.Unmarshal(args, &params)
			if err != nil {
				return agent.ToolResult{Content: "Invalid arguments", IsError: true}, nil
			}

			if strings.TrimSpace(params.Command) == "" {
				return agent.ToolResult{Content: "command is required", IsError: true}, nil
			}

			execCtx := ctx

			var cancel context.CancelFunc
			if opts.BashTimeout > 0 {
				execCtx, cancel = context.WithTimeout(ctx, opts.BashTimeout)
				defer cancel()
			}

			cmd := exec.CommandContext(execCtx, "bash", "-c", params.Command)

			if opts.RootDir != "" {
				root, err := resolvePath(".", opts)
				if err != nil {
					return agent.ToolResult{Content: err.Error(), IsError: true}, nil
				}

				cmd.Dir = root
			}

			start := time.Now()
			output, err := cmd.CombinedOutput()
			duration := time.Since(start)
			result := string(output)
			result, truncated := truncateBytes(result, opts.MaxBashOutputBytes, "\n... (truncated)")
			metadata := map[string]any{
				"duration_ms": duration.Milliseconds(),
				"truncated":   truncated,
			}

			if errors.Is(execCtx.Err(), context.DeadlineExceeded) {
				return agent.ToolResult{
					Content:  fmt.Sprintf("Command timed out after %s:\n%s", formatDuration(opts.BashTimeout), result),
					IsError:  true,
					Metadata: metadata,
				}, nil
			}

			if err != nil {
				exitCode := "unknown"

				if cmd.ProcessState != nil {
					code := cmd.ProcessState.ExitCode()
					exitCode = strconv.Itoa(code)
					metadata["exit_code"] = code
				}

				return agent.ToolResult{
					Content:  fmt.Sprintf("Command failed with exit code %s:\n%s", exitCode, result),
					IsError:  true,
					Metadata: metadata,
				}, nil
			}

			metadata["exit_code"] = 0

			return agent.ToolResult{Content: strings.TrimSpace(result), Metadata: metadata}, nil
		},
	}
}

func formatDuration(d time.Duration) string {
	if d%time.Second == 0 {
		return fmt.Sprintf("%ds", int(d/time.Second))
	}

	return d.String()
}
