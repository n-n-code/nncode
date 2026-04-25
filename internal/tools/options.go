package tools

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

const (
	defaultMaxReadBytes       = 50000
	defaultMaxWriteBytes      = 1000000
	defaultMaxBashOutputBytes = 10000
	defaultBashTimeout        = 120 * time.Second
	fileMode                  = 0o644
	dirMode                   = 0o755
)

var (
	errPathRequired = errors.New("path is required")
	errPathOutside  = errors.New("path is outside workspace root")
)

// Options configures built-in tool limits. Zero values use the package defaults.
type Options struct {
	RootDir            string
	MaxReadBytes       int
	MaxWriteBytes      int
	MaxBashOutputBytes int
	BashTimeout        time.Duration
}

func resolveOptions(options []Options) Options {
	out := Options{
		MaxReadBytes:       defaultMaxReadBytes,
		MaxWriteBytes:      defaultMaxWriteBytes,
		MaxBashOutputBytes: defaultMaxBashOutputBytes,
		BashTimeout:        defaultBashTimeout,
	}
	if len(options) == 0 {
		return out
	}

	src := options[0]
	if src.RootDir != "" {
		out.RootDir = src.RootDir
	}

	if src.MaxReadBytes > 0 {
		out.MaxReadBytes = src.MaxReadBytes
	}

	if src.MaxWriteBytes > 0 {
		out.MaxWriteBytes = src.MaxWriteBytes
	}

	if src.MaxBashOutputBytes > 0 {
		out.MaxBashOutputBytes = src.MaxBashOutputBytes
	}

	if src.BashTimeout > 0 {
		out.BashTimeout = src.BashTimeout
	}

	return out
}

func resolvePath(path string, opts Options) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errPathRequired
	}

	if opts.RootDir == "" {
		return path, nil
	}

	root, err := filepath.Abs(opts.RootDir)
	if err != nil {
		return "", fmt.Errorf("resolve workspace root: %w", err)
	}

	candidate := path
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(root, candidate)
	}

	candidate, err = filepath.Abs(candidate)
	if err != nil {
		return "", fmt.Errorf("resolve path: %w", err)
	}

	rel, err := filepath.Rel(root, candidate)
	if err != nil {
		return "", fmt.Errorf("compare path to workspace root: %w", err)
	}

	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("%w: %q (root %q)", errPathOutside, path, root)
	}

	return candidate, nil
}

func truncateBytes(s string, limit int, suffix string) (string, bool) {
	if limit <= 0 || len(s) <= limit {
		return s, false
	}

	return s[:limit] + suffix, true
}
