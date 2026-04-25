package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"nncode/internal/agent"
)

// Write returns the write file tool.
func Write(options ...Options) agent.Tool {
	opts := resolveOptions(options)

	return agent.Tool{
		Name: "write",
		Description: "Write content to a file at the given path. " +
			"Creates parent directories if they don't exist. " +
			"Use this to create new files or overwrite existing ones.",
		Parameters: `{
			"type": "object",
			"properties": {
				"path": {
					"type": "string",
					"description": "The path to the file to write"
				},
				"content": {
					"type": "string",
					"description": "The content to write to the file"
				}
			},
			"required": ["path", "content"]
		}`,
		Execute: func(_ context.Context, args json.RawMessage) (agent.ToolResult, error) {
			var params struct {
				Path    string `json:"path"`
				Content string `json:"content"`
			}

			err := json.Unmarshal(args, &params)
			if err != nil {
				return agent.ToolResult{ //nolint:nilerr // tool errors surface via ToolResult
					Content: "Invalid arguments", IsError: true,
				}, nil
			}

			if len(params.Content) > opts.MaxWriteBytes {
				return agent.ToolResult{
					Content: fmt.Sprintf(
						"Content is %d bytes, which exceeds the write limit of %d bytes",
						len(params.Content), opts.MaxWriteBytes,
					),
					IsError: true,
				}, nil
			}

			path, err := resolvePath(params.Path, opts)
			if err != nil {
				return agent.ToolResult{ //nolint:nilerr // tool errors surface via ToolResult
					Content: err.Error(), IsError: true,
				}, nil
			}

			dir := filepath.Dir(path)
			if dir != "." && dir != "/" {
				err = os.MkdirAll(dir, dirMode)
				if err != nil {
					return agent.ToolResult{ //nolint:nilerr // tool errors surface via ToolResult
						Content: "Failed to create directory: " + err.Error(), IsError: true,
					}, nil
				}
			}

			err = os.WriteFile(path, []byte(params.Content), fileMode)
			if err != nil {
				return agent.ToolResult{ //nolint:nilerr // tool errors surface via ToolResult
					Content: "Failed to write file: " + err.Error(), IsError: true,
				}, nil
			}

			return agent.ToolResult{
				Content: fmt.Sprintf("Successfully wrote %d bytes to %s", len(params.Content), path),
				Metadata: map[string]any{
					"path":          path,
					"bytes_written": len(params.Content),
				},
			}, nil
		},
	}
}
