// Package projectctx detects project files in a directory and produces a
// compact markdown summary suitable for injection into the system prompt.
package projectctx

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	maxContextBytes     = 4096
	maxDescLen          = 60
	maxReadmeSnippetLen = 80
)

// Gather detects project files in dir and returns a compact markdown summary.
// It returns an empty string if no recognizable project files are found.
func Gather(dir string) string {
	var items []string

	if s := readGoMod(dir); s != "" {
		items = append(items, s)
	}
	if s := readPackageJSON(dir); s != "" {
		items = append(items, s)
	}
	if s := readCargoToml(dir); s != "" {
		items = append(items, s)
	}
	if s := readPyProject(dir); s != "" {
		items = append(items, s)
	}
	if fileExists(dir, "requirements.txt") {
		items = append(items, "- requirements.txt")
	}
	if fileExists(dir, "setup.py") {
		items = append(items, "- setup.py")
	}
	if s := readMakefile(dir); s != "" {
		items = append(items, s)
	}
	if fileExists(dir, "Dockerfile") {
		items = append(items, "- Dockerfile")
	}
	if fileExists(dir, "docker-compose.yml") {
		items = append(items, "- docker-compose.yml")
	}
	if s := readReadme(dir); s != "" {
		items = append(items, s)
	}
	if fileExists(dir, ".git") {
		items = append(items, "- .git repository")
	}

	if len(items) == 0 {
		return ""
	}

	var builder strings.Builder
	builder.WriteString("<project_context>\nWorking directory contains:\n")
	for _, item := range items {
		builder.WriteString(item)
		builder.WriteByte('\n')
	}
	builder.WriteString("</project_context>")

	if builder.Len() > maxContextBytes {
		return builder.String()[:maxContextBytes] + "\n...</project_context>"
	}

	return builder.String()
}

// AppendToPrompt appends gathered context to base if any was found.
func AppendToPrompt(base, dir string) string {
	ctx := Gather(dir)
	if ctx == "" {
		return base
	}

	if base != "" {
		base = strings.TrimRight(base, "\n") + "\n\n"
	}

	return base + ctx
}

func fileExists(dir, name string) bool {
	_, err := os.Stat(filepath.Join(dir, name))
	return err == nil
}

func readFirstLine(dir, name string) string {
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return ""
	}

	scanner := bufio.NewScanner(bytes.NewReader(data))
	if scanner.Scan() {
		return strings.TrimSpace(scanner.Text())
	}

	return ""
}

func readGoMod(dir string) string {
	data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return ""
	}

	var module, gover string
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if mod, ok := strings.CutPrefix(line, "module "); ok {
			module = strings.TrimSpace(mod)
		}
		if v, ok := strings.CutPrefix(line, "go "); ok {
			gover = strings.TrimSpace(v)
		}
		if module != "" && gover != "" {
			break
		}
	}

	if module == "" {
		return "- go.mod"
	}

	if gover != "" {
		return fmt.Sprintf("- go.mod (module: %s, go %s)", module, gover)
	}

	return fmt.Sprintf("- go.mod (module: %s)", module)
}

func readPackageJSON(dir string) string {
	data, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return ""
	}

	var pkg struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return "- package.json"
	}

	if pkg.Name == "" {
		return "- package.json"
	}

	if pkg.Description != "" {
		return fmt.Sprintf("- package.json (%s — %s)", pkg.Name, truncate(pkg.Description, maxDescLen))
	}

	return fmt.Sprintf("- package.json (%s)", pkg.Name)
}

func readCargoToml(dir string) string {
	data, err := os.ReadFile(filepath.Join(dir, "Cargo.toml"))
	if err != nil {
		return ""
	}

	scanner := bufio.NewScanner(bytes.NewReader(data))
	inPackage := false
	var name string

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "[package]" {
			inPackage = true

			continue
		}
		if inPackage && strings.HasPrefix(line, "[") {
			break
		}
		if inPackage && strings.HasPrefix(line, "name") {
			if _, v, ok := strings.Cut(line, "="); ok {
				name = strings.Trim(strings.TrimSpace(v), `"`)
			}

			break
		}
	}

	if name == "" {
		return "- Cargo.toml"
	}

	return fmt.Sprintf("- Cargo.toml (package: %s)", name)
}

func readPyProject(dir string) string {
	data, err := os.ReadFile(filepath.Join(dir, "pyproject.toml"))
	if err != nil {
		return ""
	}

	scanner := bufio.NewScanner(bytes.NewReader(data))
	inProject := false

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "[project]" {
			inProject = true

			continue
		}
		if inProject && strings.HasPrefix(line, "[") {
			break
		}
		if inProject && strings.HasPrefix(line, "name") {
			if _, v, ok := strings.Cut(line, "="); ok {
				name := strings.Trim(strings.TrimSpace(v), `"`)

				return fmt.Sprintf("- pyproject.toml (project: %s)", name)
			}
		}
	}

	return "- pyproject.toml"
}

func readMakefile(dir string) string {
	data, err := os.ReadFile(filepath.Join(dir, "Makefile"))
	if err != nil {
		return ""
	}

	scanner := bufio.NewScanner(bytes.NewReader(data))
	var targets []string

	for scanner.Scan() && len(targets) < 3 {
		line := strings.TrimSpace(scanner.Text())
		if !strings.Contains(line, ":") || strings.HasPrefix(line, ".") || strings.HasPrefix(line, "#") {
			continue
		}

		target, _, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}

		target = strings.TrimSpace(target)
		if target == "" || strings.Contains(target, " ") || strings.Contains(target, "%") {
			continue
		}

		targets = append(targets, target)
	}

	if len(targets) == 0 {
		return "- Makefile"
	}

	return fmt.Sprintf("- Makefile (targets: %s)", strings.Join(targets, ", "))
}

func readReadme(dir string) string {
	line := readFirstLine(dir, "README.md")
	if line == "" {
		return ""
	}

	// Skip shields/badges.
	if strings.HasPrefix(line, "[") || strings.HasPrefix(line, "<") || strings.HasPrefix(line, "!") {
		return "- README.md"
	}

	return fmt.Sprintf("- README.md (%s)", truncate(line, maxReadmeSnippetLen))
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}

	return s
}
