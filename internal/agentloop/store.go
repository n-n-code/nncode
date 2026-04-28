package agentloop

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const loopFileExt = ".json"

var (
	errLoopRefRequired         = errors.New("loop name or path is required")
	errLoopNotFound            = errors.New("loop not found")
	errLoopMultipleJSONObjects = errors.New("loop file must contain one JSON object")
)

type Scope string

const (
	ScopeProject Scope = "project"
	ScopeGlobal  Scope = "global"
	ScopePath    Scope = "path"
)

type StoreOptions struct {
	CWD     string
	HomeDir string
}

type Summary struct {
	Ref           string
	Name          string
	Description   string
	Path          string
	Scope         Scope
	SchemaVersion int
	Nodes         []NodeSummary
	Err           error
}

type NodeSummary struct {
	ID     string
	Type   NodeType
	Locked bool
}

func Load(ref string, opts StoreOptions) (Definition, string, error) {
	path, _, err := Resolve(ref, opts)
	if err != nil {
		return Definition{}, "", err
	}

	def, err := LoadFile(path)
	if err != nil {
		return Definition{}, "", err
	}

	return def, path, nil
}

func LoadFile(path string) (Definition, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Definition{}, fmt.Errorf("read loop file: %w", err)
	}

	def, err := decodeDefinition(data)
	if err != nil {
		return Definition{}, fmt.Errorf("parse loop file %q: %w", path, err)
	}

	if err := def.Validate(); err != nil {
		return Definition{}, fmt.Errorf("validate loop file %q: %w", path, err)
	}

	return def, nil
}

func Validate(ref string, opts StoreOptions) (Summary, error) {
	path, scope, err := Resolve(ref, opts)
	if err != nil {
		return Summary{}, err
	}

	def, err := LoadFile(path)

	return summarize(path, scope, def, err), err
}

func Resolve(ref string, opts StoreOptions) (string, Scope, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", "", errLoopRefRequired
	}

	if isExplicitPath(ref) {
		return ref, ScopePath, nil
	}

	name := ref
	if filepath.Ext(name) == "" {
		name += loopFileExt
	}

	dirs, err := loopDirs(opts)
	if err != nil {
		return "", "", err
	}

	for _, dir := range dirs {
		path := filepath.Join(dir.Path, name)
		if _, err := os.Stat(path); err == nil {
			return path, dir.Scope, nil
		} else if !os.IsNotExist(err) {
			return "", "", fmt.Errorf("stat loop file %q: %w", path, err)
		}
	}

	return "", "", fmt.Errorf("%w: %q", errLoopNotFound, ref)
}

func summarize(path string, scope Scope, def Definition, err error) Summary {
	refName := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	summary := Summary{
		Ref:   refName,
		Name:  refName,
		Path:  path,
		Scope: scope,
		Err:   err,
	}
	if err == nil {
		summary.Name = def.Name
		summary.Description = def.Description
		summary.SchemaVersion = def.SchemaVersion
		summary.Nodes = summarizeNodes(def.Nodes)
	}

	return summary
}

func summarizeNodes(nodes []Node) []NodeSummary {
	out := make([]NodeSummary, 0, len(nodes))
	for _, node := range nodes {
		out = append(out, NodeSummary{
			ID:     node.ID,
			Type:   node.Type,
			Locked: node.Locked,
		})
	}

	return out
}

func List(opts StoreOptions) ([]Summary, error) {
	dirs, err := loopDirs(opts)
	if err != nil {
		return nil, err
	}

	byRef := make(map[string]Summary)

	for i := len(dirs) - 1; i >= 0; i-- {
		dir := dirs[i]
		entries, err := os.ReadDir(dir.Path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}

			return nil, fmt.Errorf("read loop directory %q: %w", dir.Path, err)
		}

		for _, entry := range entries {
			if entry.IsDir() || filepath.Ext(entry.Name()) != loopFileExt {
				continue
			}

			path := filepath.Join(dir.Path, entry.Name())
			def, loadErr := LoadFile(path)
			summary := summarize(path, dir.Scope, def, loadErr)
			byRef[summary.Ref] = summary
		}
	}

	out := make([]Summary, 0, len(byRef))
	for _, summary := range byRef {
		out = append(out, summary)
	}

	sort.Slice(out, func(i, j int) bool {
		return out[i].Ref < out[j].Ref
	})

	return out, nil
}

// WriteSummaries renders summaries as a list (one line per loop) for the
// plain CLI. descLimit > 0 truncates descriptions to that many bytes; <= 0
// disables truncation.
func WriteSummaries(out io.Writer, summaries []Summary, descLimit int) {
	if len(summaries) == 0 {
		fmt.Fprintln(out, "No Agent Loops configured.")

		return
	}

	fmt.Fprintf(out, "Agent Loops (%d):\n", len(summaries))

	for _, summary := range summaries {
		if summary.Err != nil {
			fmt.Fprintf(out, "  %-28s [%s] invalid: %v\n", summary.Ref, summary.Scope, summary.Err)

			continue
		}

		description := summary.Description
		if summary.Name != summary.Ref {
			description = "name: " + summary.Name + "  " + description
		}

		fmt.Fprintf(out, "  %-28s [%s] %s\n", summary.Ref, summary.Scope, truncateOneLine(description, descLimit))
	}
}

func truncateOneLine(s string, limit int) string {
	if limit > 0 && len(s) > limit {
		s = s[:limit] + "…"
	}

	return strings.ReplaceAll(s, "\n", " ")
}

func decodeDefinition(data []byte) (Definition, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()

	var def Definition
	if err := decoder.Decode(&def); err != nil {
		return Definition{}, fmt.Errorf("decode loop definition: %w", err)
	}

	if decoder.More() {
		return Definition{}, errLoopMultipleJSONObjects
	}

	return def, nil
}

func isExplicitPath(ref string) bool {
	return filepath.IsAbs(ref) || strings.ContainsAny(ref, `/\`)
}

type loopDir struct {
	Path  string
	Scope Scope
}

func loopDirs(opts StoreOptions) ([]loopDir, error) {
	cwd := opts.CWD
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("get working directory: %w", err)
		}
	}

	home := opts.HomeDir
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("get user home directory: %w", err)
		}
	}

	return []loopDir{
		{Path: filepath.Join(cwd, ".nncode", "loops"), Scope: ScopeProject},
		{Path: filepath.Join(home, ".nncode", "loops"), Scope: ScopeGlobal},
	}, nil
}
