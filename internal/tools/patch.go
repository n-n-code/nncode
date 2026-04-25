package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"nncode/internal/agent"
)

const devNull = "/dev/null"

var hunkHeaderPattern = regexp.MustCompile(`^@@ -([0-9]+)(?:,([0-9]+))? \+([0-9]+)(?:,([0-9]+))? @@`)

type patchFile struct {
	oldPath string
	newPath string
	hunks   []patchHunk
}

type patchHunk struct {
	oldStart int
	lines    []patchLine
}

type patchLine struct {
	op   byte
	text string
}

// Patch returns the unified-diff patch tool.
func Patch(options ...Options) agent.Tool {
	opts := resolveOptions(options)

	return agent.Tool{
		Name: "patch",
		Description: "Apply a unified diff patch to one or more files. " +
			"Use this for multi-line code edits where exact string replacement would be brittle.",
		Parameters: `{
			"type": "object",
			"properties": {
				"patch": {
					"type": "string",
					"description": "A unified diff patch with ---/+++ file headers and @@ hunks"
				}
			},
			"required": ["patch"]
		}`,
		Execute: func(ctx context.Context, args json.RawMessage) (agent.ToolResult, error) {
			var params struct {
				Patch string `json:"patch"`
			}
			err := json.Unmarshal(args, &params)
			if err != nil {
				return agent.ToolResult{ //nolint:nilerr // tool errors surface via ToolResult, not Go error
					Content: "Invalid arguments", IsError: true,
				}, nil
			}

			files, err := parseUnifiedPatch(params.Patch)
			if err != nil {
				return agent.ToolResult{ //nolint:nilerr // tool errors surface via ToolResult, not Go error
					Content: err.Error(), IsError: true,
				}, nil
			}

			changed, bytesWritten, err := applyUnifiedPatch(files, opts)
			if err != nil {
				return agent.ToolResult{ //nolint:nilerr // tool errors surface via ToolResult, not Go error
					Content: err.Error(), IsError: true,
				}, nil
			}

			return agent.ToolResult{
				Content: fmt.Sprintf("Successfully patched %d file(s)", changed),
				Metadata: map[string]any{
					"files_changed":  changed,
					"bytes_written":  bytesWritten,
					"patch_sections": len(files),
				},
			}, nil
		},
	}
}

var (
	errPatchRequired      = errors.New("patch is required")
	errNoFileDiffs        = errors.New("patch contains no file diffs")
	errDeleteNotSupported = errors.New("deleting files with patch is not supported")
	errHunkOutsideFile    = errors.New("hunk starts outside file")
	errContextMismatch    = errors.New("context mismatch")
	errRemoveMismatch     = errors.New("remove mismatch")
)

func parseUnifiedPatch(raw string) ([]patchFile, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, errPatchRequired
	}

	lines := strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n")

	var files []patchFile

	i := 0
	for i < len(lines) {
		if !strings.HasPrefix(lines[i], "--- ") {
			i++

			continue
		}

		oldPath := parsePatchPath(lines[i][4:])

		i++
		if i >= len(lines) || !strings.HasPrefix(lines[i], "+++ ") {
			return nil, fmt.Errorf("patch missing +++ header after %s: %w", oldPath, errPatchRequired)
		}

		file := patchFile{oldPath: oldPath, newPath: parsePatchPath(lines[i][4:])}

		i++
		for i < len(lines) {
			line := lines[i]
			if strings.HasPrefix(line, "--- ") {
				break
			}

			if line == "" {
				i++

				continue
			}

			if !strings.HasPrefix(line, "@@ ") {
				return nil, fmt.Errorf("patch expected hunk header for %s, got %q: %w", file.targetPath(), line, errPatchRequired)
			}

			hunk, next, err := parsePatchHunk(lines, i)
			if err != nil {
				return nil, err
			}

			file.hunks = append(file.hunks, hunk)
			i = next
		}

		if len(file.hunks) == 0 {
			return nil, fmt.Errorf("patch for %s has no hunks: %w", file.targetPath(), errPatchRequired)
		}

		files = append(files, file)
	}

	if len(files) == 0 {
		return nil, errNoFileDiffs
	}

	return files, nil
}

func parsePatchHunk(lines []string, start int) (patchHunk, int, error) {
	matches := hunkHeaderPattern.FindStringSubmatch(lines[start])
	if matches == nil {
		return patchHunk{}, start, fmt.Errorf("invalid hunk header %q: %w", lines[start], errPatchRequired)
	}

	oldStart, err := strconv.Atoi(matches[1])
	if err != nil {
		return patchHunk{}, start, fmt.Errorf("invalid hunk old start: %w: %w", err, errPatchRequired)
	}

	hunk := patchHunk{oldStart: oldStart}

	i := start + 1
	for i < len(lines) {
		line := lines[i]
		if strings.HasPrefix(line, "@@ ") || strings.HasPrefix(line, "--- ") {
			break
		}

		if strings.HasPrefix(line, `\ No newline at end of file`) {
			i++

			continue
		}

		if line == "" && i == len(lines)-1 {
			break
		}

		if line == "" {
			return patchHunk{}, start, fmt.Errorf(
				"invalid empty patch line in hunk starting %q: %w",
				lines[start], errPatchRequired,
			)
		}

		op := line[0]
		if op != ' ' && op != '-' && op != '+' {
			return patchHunk{}, start, fmt.Errorf("invalid patch line prefix %q: %w", string(op), errPatchRequired)
		}

		hunk.lines = append(hunk.lines, patchLine{op: op, text: line[1:]})
		i++
	}

	if len(hunk.lines) == 0 {
		return patchHunk{}, start, fmt.Errorf("hunk %q has no body: %w", lines[start], errPatchRequired)
	}

	return hunk, i, nil
}

func parsePatchPath(raw string) string {
	fields := strings.Fields(raw)
	if len(fields) == 0 {
		return ""
	}

	path := fields[0]
	path = strings.TrimPrefix(path, "a/")
	path = strings.TrimPrefix(path, "b/")

	return path
}

func (f patchFile) targetPath() string {
	if f.newPath != "" && f.newPath != devNull {
		return f.newPath
	}

	return f.oldPath
}

func applyUnifiedPatch(files []patchFile, opts Options) (int, int, error) {
	changed := 0
	bytesWritten := 0

	for _, file := range files {
		if file.newPath == devNull {
			return changed, bytesWritten, errDeleteNotSupported
		}

		path, err := resolvePath(file.targetPath(), opts)
		if err != nil {
			return changed, bytesWritten, err
		}

		content := ""

		if file.oldPath != devNull {
			data, err := os.ReadFile(path)
			if err != nil {
				return changed, bytesWritten, fmt.Errorf("failed to read file %s: %w", path, err)
			}

			content = string(data)
		}

		newContent, err := applyPatchToContent(content, file.hunks)
		if err != nil {
			return changed, bytesWritten, fmt.Errorf("failed to apply patch to %s: %w", path, err)
		}

		if file.oldPath == devNull && newContent != "" && !strings.HasSuffix(newContent, "\n") {
			newContent += "\n"
		}

		if len(newContent) > opts.MaxWriteBytes {
			return changed, bytesWritten, fmt.Errorf(
				"patched content for %s is %d bytes, which exceeds the write limit of %d bytes: %w",
				path, len(newContent), opts.MaxWriteBytes, errPatchRequired,
			)
		}

		dir := filepath.Dir(path)
		if dir != "." && dir != "/" {
			err := os.MkdirAll(dir, 0755)
			if err != nil {
				return changed, bytesWritten, fmt.Errorf("failed to create directory %s: %w", dir, err)
			}
		}

		err = os.WriteFile(path, []byte(newContent), 0644) //nolint:gosec // path resolved via resolvePath earlier
		if err != nil {
			return changed, bytesWritten, fmt.Errorf("failed to write file %s: %w", path, err)
		}

		changed++
		bytesWritten += len(newContent)
	}

	return changed, bytesWritten, nil
}

func applyPatchToContent(content string, hunks []patchHunk) (string, error) {
	lines, finalNewline := splitContentLines(content)

	offset := 0
	for _, hunk := range hunks {
		pos := hunk.oldStart - 1 + offset
		if hunk.oldStart == 0 {
			pos = offset
		}

		if pos < 0 || pos > len(lines) {
			return "", errHunkOutsideFile
		}

		for _, line := range hunk.lines {
			switch line.op {
			case ' ':
				if pos >= len(lines) || lines[pos] != line.text {
					return "", fmt.Errorf("context mismatch at line %d: %w", pos+1, errContextMismatch)
				}

				pos++
			case '-':
				if pos >= len(lines) || lines[pos] != line.text {
					return "", fmt.Errorf("remove mismatch at line %d: %w", pos+1, errRemoveMismatch)
				}

				lines = append(lines[:pos], lines[pos+1:]...)
				offset--
			case '+':
				lines = append(lines, "")
				copy(lines[pos+1:], lines[pos:])
				lines[pos] = line.text
				pos++
				offset++
			}
		}
	}

	return joinContentLines(lines, finalNewline), nil
}

func splitContentLines(content string) ([]string, bool) {
	if content == "" {
		return nil, false
	}

	finalNewline := strings.HasSuffix(content, "\n")

	lines := strings.Split(content, "\n")
	if finalNewline {
		lines = lines[:len(lines)-1]
	}

	return lines, finalNewline
}

func joinContentLines(lines []string, finalNewline bool) string {
	if len(lines) == 0 {
		return ""
	}

	out := strings.Join(lines, "\n")
	if finalNewline {
		out += "\n"
	}

	return out
}
