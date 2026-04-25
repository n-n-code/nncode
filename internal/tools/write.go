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
		Name:        "write",
		Description: "Write content to a file at the given path. Creates parent directories if they don't exist. Use this to create new files or overwrite existing ones.",
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
		Execute: func(ctx context.Context, args json.RawMessage) (agent.ToolResult, error) {
			var params struct {
				Path    string `json:"path"`
				Content string `json:"content"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return agent.ToolResult{Content: "Invalid arguments", IsError: true}, nil
			}
			if len(params.Content) > opts.MaxWriteBytes {
				return agent.ToolResult{
					Content: fmt.Sprintf("Content is %d bytes, which exceeds the write limit of %d bytes", len(params.Content), opts.MaxWriteBytes),
					IsError: true,
				}, nil
			}

			path, err := resolvePath(params.Path, opts)
			if err != nil {
				return agent.ToolResult{Content: err.Error(), IsError: true}, nil
			}

			dir := filepath.Dir(path)
			if dir != "." && dir != "/" {
				if err := os.MkdirAll(dir, 0755); err != nil {
					return agent.ToolResult{Content: fmt.Sprintf("Failed to create directory: %s", err.Error()), IsError: true}, nil
				}
			}

			if err := os.WriteFile(path, []byte(params.Content), 0644); err != nil {
				return agent.ToolResult{Content: fmt.Sprintf("Failed to write file: %s", err.Error()), IsError: true}, nil
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
