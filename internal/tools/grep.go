package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"nncode/internal/agent"
)

// Grep returns the grep search tool.
func Grep(options ...Options) agent.Tool {
	opts := resolveOptions(options)

	return agent.Tool{
		Name: "grep",
		Description: "Search file contents for a regular expression pattern. " +
			"Use this to discover where symbols are defined, find usages, or locate " +
			"code patterns across the codebase.",
		Parameters: `{
			"type": "object",
			"properties": {
				"pattern": {
					"type": "string",
					"description": "Regular expression pattern to search for"
				},
				"path": {
					"type": "string",
					"description": "Directory or file to search in (default: current directory)"
				},
				"output": {
					"type": "string",
					"enum": ["content", "count", "files"],
					"description": "Output format: content, count, or files. Default: content"
				}
			},
			"required": ["pattern"]
		}`,
		Execute: func(_ context.Context, args json.RawMessage) (agent.ToolResult, error) {
			params, err := parseGrepParams(args)
			if err != nil {
				//nolint:nilerr // tool errors surface via ToolResult
				return agent.ToolResult{Content: err.Error(), IsError: true}, nil
			}

			searchPath, err := resolvePath(params.Path, opts)
			if err != nil {
				//nolint:nilerr // tool errors surface via ToolResult
				return agent.ToolResult{Content: err.Error(), IsError: true}, nil
			}

			regex, err := regexp.Compile(params.Pattern)
			if err != nil {
				//nolint:nilerr // tool errors surface via ToolResult
				return agent.ToolResult{Content: "Invalid pattern: " + err.Error(), IsError: true}, nil
			}

			info, err := os.Stat(searchPath)
			if err != nil {
				//nolint:nilerr // tool errors surface via ToolResult
				return agent.ToolResult{Content: "Failed to access path: " + err.Error(), IsError: true}, nil
			}

			result, truncated := runGrep(searchPath, info.IsDir(), regex, params.Output)

			return agent.ToolResult{
				Content: result,
				Metadata: map[string]any{
					"truncated": truncated,
				},
			}, nil
		},
	}
}

type grepParams struct {
	Pattern string `json:"pattern"`
	Path    string `json:"path"`
	Output  string `json:"output"`
}

var (
	errGrepInvalidArgs = errors.New("invalid arguments")
	errGrepNoPattern   = errors.New("pattern is required")
)

func parseGrepParams(args json.RawMessage) (grepParams, error) {
	var params grepParams

	err := json.Unmarshal(args, &params)
	if err != nil {
		return grepParams{}, errGrepInvalidArgs
	}

	if strings.TrimSpace(params.Pattern) == "" {
		return grepParams{}, errGrepNoPattern
	}

	if params.Path == "" {
		params.Path = "."
	}

	if params.Output == "" {
		params.Output = "content"
	}

	return params, nil
}

type grepMatch struct {
	path string
	line int
	text string
}

func runGrep(searchPath string, isDir bool, regex *regexp.Regexp, output string) (string, bool) {
	const (
		maxMatches     = 100
		maxTotalOutput = 50000
	)

	var (
		matches      []grepMatch
		totalCount   int
		matchedFiles []string
		seenFiles    = make(map[string]struct{})
	)

	searchOne := func(path string) error {
		f, err := os.Open(path)
		if err != nil {
			return nil // skip unreadable files
		}
		defer func() { _ = f.Close() }()

		scanner := bufio.NewScanner(f)
		lineNum := 0
		fileMatched := false

		for scanner.Scan() {
			lineNum++
			if totalCount >= maxMatches {
				break
			}

			line := scanner.Text()
			if !regex.MatchString(line) {
				continue
			}

			totalCount++
			fileMatched = true

			if output == "content" {
				matches = append(matches, grepMatch{path: path, line: lineNum, text: line})
			}
		}

		if fileMatched {
			if _, ok := seenFiles[path]; !ok {
				seenFiles[path] = struct{}{}
				matchedFiles = append(matchedFiles, path)
			}
		}

		return nil
	}

	if !isDir {
		_ = searchOne(searchPath)
	} else {
		_ = filepath.WalkDir(searchPath, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				//nolint:nilerr // skip unreadable entries
				return nil
			}

			if totalCount >= maxMatches {
				return filepath.SkipAll
			}

			return searchOne(path)
		})
	}

	return formatGrepResult(matches, totalCount, matchedFiles, output, maxTotalOutput)
}

func formatGrepResult(matches []grepMatch, totalCount int, matchedFiles []string,
	output string, maxTotalOutput int,
) (string, bool) {
	var builder strings.Builder

	switch output {
	case "count":
		fmt.Fprintf(&builder, "%d matches", totalCount)
	case "files":
		if len(matchedFiles) == 0 {
			builder.WriteString("No matches found")
		} else {
			for _, f := range matchedFiles {
				builder.WriteString(f)
				builder.WriteString("\n")
			}
		}
	default: // content
		if len(matches) == 0 {
			builder.WriteString("No matches found")
		} else {
			for _, m := range matches {
				fmt.Fprintf(&builder, "%s:%d:%s\n", m.path, m.line, m.text)
			}
		}
	}

	result := builder.String()
	truncated := len(result) > maxTotalOutput

	if truncated {
		result = result[:maxTotalOutput] + "\n... (truncated, too many results)"
	}

	return result, truncated
}
