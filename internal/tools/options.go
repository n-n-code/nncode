package tools

import (
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
	in := options[0]
	if in.RootDir != "" {
		out.RootDir = in.RootDir
	}
	if in.MaxReadBytes > 0 {
		out.MaxReadBytes = in.MaxReadBytes
	}
	if in.MaxWriteBytes > 0 {
		out.MaxWriteBytes = in.MaxWriteBytes
	}
	if in.MaxBashOutputBytes > 0 {
		out.MaxBashOutputBytes = in.MaxBashOutputBytes
	}
	if in.BashTimeout > 0 {
		out.BashTimeout = in.BashTimeout
	}
	return out
}

func resolvePath(path string, opts Options) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("path is required")
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
		return "", fmt.Errorf("path %q is outside workspace root %q", path, root)
	}
	return candidate, nil
}

func truncateBytes(s string, max int, suffix string) (string, bool) {
	if max <= 0 || len(s) <= max {
		return s, false
	}
	return s[:max] + suffix, true
}
