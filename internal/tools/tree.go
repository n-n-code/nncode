package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"nncode/internal/agent"
)

// Tree returns a Tool that lists directory contents in a tree-like format.
func Tree() agent.Tool {
	return agent.Tool{
		Name:        "tree",
		Description: "List directory contents in a compact tree-like format. Respects .gitignore if present.",
		Parameters: `{
			"type": "object",
			"properties": {
				"path": {"type": "string", "description": "Directory path to list (default: current directory)"},
				"depth": {"type": "integer", "description": "Maximum depth to traverse (default: 2)"}
			}
		}`,
		Execute: func(_ context.Context, args json.RawMessage) (agent.ToolResult, error) {
			var req struct {
				Path  string `json:"path"`
				Depth int    `json:"depth"`
			}
			if err := json.Unmarshal(args, &req); err != nil {
				return agent.ToolResult{Content: fmt.Sprintf("Invalid arguments: %v", err), IsError: true}, nil
			}

			if req.Path == "" {
				req.Path = "."
			}
			if req.Depth <= 0 {
				req.Depth = 2
			}

			root := filepath.Clean(req.Path)
			info, err := os.Stat(root)
			if err != nil {
				return agent.ToolResult{Content: fmt.Sprintf("Cannot access %s: %v", root, err), IsError: true}, nil
			}
			if !info.IsDir() {
				return agent.ToolResult{Content: root + " is not a directory", IsError: true}, nil
			}

			ignore := loadGitignore(root)
			var out strings.Builder
			out.WriteString(root + "\n")
			walk(root, "", 0, req.Depth, ignore, &out)

			return agent.ToolResult{Content: out.String()}, nil
		},
	}
}

// loadGitignore reads a simple .gitignore from dir and returns the patterns.
func loadGitignore(dir string) []string {
	data, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		return nil
	}

	var patterns []string
	for line := range strings.SplitSeq(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		patterns = append(patterns, line)
	}
	return patterns
}

// ignored reports whether name matches any of the gitignore patterns.
func ignored(name string, patterns []string) bool {
	for _, pattern := range patterns {
		if pattern == name {
			return true
		}
		if strings.HasSuffix(pattern, "/") && name == strings.TrimSuffix(pattern, "/") {
			return true
		}
		if matched, _ := filepath.Match(pattern, name); matched {
			return true
		}
	}
	return false
}

// walk recursively builds the tree output.
func walk(root, prefix string, depth, maxDepth int, ignore []string, out *strings.Builder) {
	if depth >= maxDepth {
		return
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}

	// Sort entries: dirs first, then files, alphabetically within each group.
	type item struct {
		name  string
		isDir bool
	}
	var items []item
	for _, e := range entries {
		name := e.Name()
		if name == ".git" || name == ".nncode" || ignored(name, ignore) {
			continue
		}
		items = append(items, item{name: name, isDir: e.IsDir()})
	}

	for i := range len(items) - 1 {
		for j := i + 1; j < len(items); j++ {
			if items[i].isDir != items[j].isDir {
				if !items[i].isDir {
					items[i], items[j] = items[j], items[i]
				}
			} else if items[i].name > items[j].name {
				items[i], items[j] = items[j], items[i]
			}
		}
	}

	for i, item := range items {
		isLast := i == len(items)-1
		branch := "├── "
		if isLast {
			branch = "└── "
		}
		out.WriteString(prefix + branch + item.name)
		if item.isDir {
			out.WriteString("/")
		}
		out.WriteString("\n")

		if item.isDir {
			nextPrefix := prefix + "│   "
			if isLast {
				nextPrefix = prefix + "    "
			}
			walk(filepath.Join(root, item.name), nextPrefix, depth+1, maxDepth, ignore, out)
		}
	}
}
