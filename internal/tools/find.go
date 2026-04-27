package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"nncode/internal/agent"
)

// Find returns the find file tool.
func Find(options ...Options) agent.Tool {
	opts := resolveOptions(options)

	return agent.Tool{
		Name: "find",
		Description: "Find files matching a glob pattern. " +
			"Use this to discover files by name or extension before reading them.",
		Parameters: `{
			"type": "object",
			"properties": {
				"pattern": {
					"type": "string",
					"description": "Glob pattern to match file names (default: *)"
				},
				"path": {
					"type": "string",
					"description": "Directory to search in (default: current directory)"
				}
			}
		}`,
		Execute: func(_ context.Context, args json.RawMessage) (agent.ToolResult, error) {
			params, err := parseFindParams(args)
			if err != nil {
				//nolint:nilerr // tool errors surface via ToolResult
				return agent.ToolResult{Content: err.Error(), IsError: true}, nil
			}

			searchPath, err := resolvePath(params.Path, opts)
			if err != nil {
				//nolint:nilerr // tool errors surface via ToolResult
				return agent.ToolResult{Content: err.Error(), IsError: true}, nil
			}

			files, truncated, err := collectMatchingFiles(searchPath, params.Pattern)
			if err != nil {
				//nolint:nilerr // tool errors surface via ToolResult
				return agent.ToolResult{Content: err.Error(), IsError: true}, nil
			}

			return agent.ToolResult{
				Content: formatFileList(files),
				Metadata: map[string]any{
					"count":     len(files),
					"truncated": truncated,
				},
			}, nil
		},
	}
}

type findParams struct {
	Pattern string `json:"pattern"`
	Path    string `json:"path"`
}

var errFindInvalidArgs = errors.New("invalid arguments")

func parseFindParams(args json.RawMessage) (findParams, error) {
	var params findParams

	err := json.Unmarshal(args, &params)
	if err != nil {
		return findParams{}, errFindInvalidArgs
	}

	if params.Pattern == "" {
		params.Pattern = "*"
	}

	if params.Path == "" {
		params.Path = "."
	}

	return params, nil
}

func collectMatchingFiles(searchPath, pattern string) ([]string, bool, error) {
	const maxFiles = 200

	info, err := os.Stat(searchPath)
	if err != nil {
		return nil, false, fmt.Errorf("failed to access path: %w", err)
	}

	var files []string

	if !info.IsDir() {
		matched, _ := filepath.Match(pattern, filepath.Base(searchPath))
		if matched {
			files = append(files, searchPath)
		}

		return files, false, nil
	}

	err = filepath.WalkDir(searchPath, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			//nolint:nilerr // skip unreadable entries
			return nil
		}

		if len(files) >= maxFiles {
			return filepath.SkipAll
		}

		matched, _ := filepath.Match(pattern, filepath.Base(path))
		if matched {
			files = append(files, path)
		}

		return nil
	})
	if err != nil {
		return nil, false, fmt.Errorf("search failed: %w", err)
	}

	return files, len(files) >= maxFiles, nil
}

func formatFileList(files []string) string {
	if len(files) == 0 {
		return "No files found"
	}

	return strings.Join(files, "\n")
}
