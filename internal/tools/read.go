package tools

import (
	"context"
	"encoding/json"
	"io"
	"os"

	"nncode/internal/agent"
)

// Read returns the read file tool.
func Read(options ...Options) agent.Tool {
	opts := resolveOptions(options)

	return agent.Tool{
		Name: "read",
		Description: "Read the contents of a file at the given path. " +
			"Use this to inspect files, read code, or view any text-based file.",
		Parameters: `{
			"type": "object",
			"properties": {
				"path": {
					"type": "string",
					"description": "The path to the file to read"
				}
			},
			"required": ["path"]
		}`,
		Execute: func(_ context.Context, args json.RawMessage) (agent.ToolResult, error) {
			var params struct {
				Path string `json:"path"`
			}

			err := json.Unmarshal(args, &params)
			if err != nil {
				return agent.ToolResult{Content: "Invalid arguments", IsError: true}, nil
			}

			path, err := resolvePath(params.Path, opts)
			if err != nil {
				return agent.ToolResult{Content: err.Error(), IsError: true}, nil
			}

			f, err := os.Open(path)
			if err != nil {
				return agent.ToolResult{Content: "Failed to read file: " + err.Error(), IsError: true}, nil
			}

			defer func() { _ = f.Close() }()

			data, err := io.ReadAll(io.LimitReader(f, int64(opts.MaxReadBytes)+1))
			if err != nil {
				return agent.ToolResult{Content: "Failed to read file: " + err.Error(), IsError: true}, nil
			}

			content := string(data)
			if len(content) > opts.MaxReadBytes {
				content = content[:opts.MaxReadBytes] + "\n... (truncated, file too large)"
			}

			return agent.ToolResult{
				Content: content,
				Metadata: map[string]any{
					"path":       path,
					"bytes_read": len(data),
					"truncated":  len(data) > opts.MaxReadBytes,
				},
			}, nil
		},
	}
}
