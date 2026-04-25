package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"nncode/internal/agent"
)

// Edit returns the edit file tool.
func Edit(options ...Options) agent.Tool {
	opts := resolveOptions(options)

	return agent.Tool{
		Name: "edit",
		Description: "Replace a specific string in a file with a new string. " +
			"The old_string must match exactly (including whitespace). " +
			"Use this for precise edits to existing files.",
		Parameters: `{
			"type": "object",
			"properties": {
				"path": {
					"type": "string",
					"description": "The path to the file to edit"
				},
				"old_string": {
					"type": "string",
					"description": "The exact string to find and replace"
				},
				"new_string": {
					"type": "string",
					"description": "The new string to replace with"
				}
			},
			"required": ["path", "old_string", "new_string"]
		}`,
		Execute: func(_ context.Context, args json.RawMessage) (agent.ToolResult, error) {
			var params struct {
				Path      string `json:"path"`
				OldString string `json:"old_string"`
				NewString string `json:"new_string"`
			}

			err := json.Unmarshal(args, &params)
			if err != nil {
				return agent.ToolResult{Content: "Invalid arguments", IsError: true}, nil
			}

			if params.OldString == "" {
				return agent.ToolResult{Content: "old_string must not be empty", IsError: true}, nil
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

			data, err := io.ReadAll(io.LimitReader(f, int64(opts.MaxWriteBytes)+1))
			if err != nil {
				return agent.ToolResult{Content: "Failed to read file: " + err.Error(), IsError: true}, nil
			}

			if len(data) > opts.MaxWriteBytes {
				return agent.ToolResult{
					Content: fmt.Sprintf("File exceeds the edit limit of %d bytes", opts.MaxWriteBytes),
					IsError: true,
				}, nil
			}

			content := string(data)
			if !strings.Contains(content, params.OldString) {
				return agent.ToolResult{
					Content: "String not found in file. The old_string must match exactly, including whitespace.",
					IsError: true,
				}, nil
			}

			newContent := strings.Replace(content, params.OldString, params.NewString, 1)
			if len(newContent) > opts.MaxWriteBytes {
				return agent.ToolResult{
					Content: fmt.Sprintf(
						"Edited content is %d bytes, which exceeds the write limit of %d bytes",
						len(newContent), opts.MaxWriteBytes,
					),
					IsError: true,
				}, nil
			}

			err = os.WriteFile(path, []byte(newContent), fileMode)
			if err != nil {
				return agent.ToolResult{Content: "Failed to write file: " + err.Error(), IsError: true}, nil
			}

			return agent.ToolResult{
				Content: "Successfully edited " + path,
				Metadata: map[string]any{
					"path":          path,
					"bytes_written": len(newContent),
				},
			}, nil
		},
	}
}
