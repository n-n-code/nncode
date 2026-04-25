// Package doctor runs setup diagnostics for nncode.
package doctor

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"nncode/internal/config"
	"nncode/internal/llm"
	"nncode/internal/session"
	"nncode/internal/skills"
)

type Status string

const (
	StatusOK   Status = "ok"
	StatusWarn Status = "warn"
	StatusFail Status = "fail"
)

// Check is one diagnostic result.
type Check struct {
	Name   string
	Status Status
	Detail string
}

// Options controls diagnostic execution.
type Options struct {
	Config    *config.Config
	ModelName string
	APIKey    string
	Live      bool
	Client    llm.Client
	Timeout   time.Duration
	Skills    *skills.Registry
}

// Run executes diagnostics. It is intentionally side-effect-light except for
// ensuring the session directory exists when checking writability.
func Run(ctx context.Context, opts Options) []Check {
	if opts.Timeout <= 0 {
		opts.Timeout = 10 * time.Second
	}

	if opts.Client == nil {
		opts.Client = llm.NewOpenAIClient()
	}

	var checks []Check

	cfg := opts.Config
	if cfg == nil {
		return append(checks, fail("config", "config was not loaded"))
	}

	err := cfg.Validate()
	if err != nil {
		checks = append(checks, fail("config", err.Error()))
	} else {
		checks = append(checks, ok("config", "merged config is valid"))
	}

	modelName := opts.ModelName
	if modelName == "" {
		modelName = cfg.DefaultModel
	}

	modelCfg, modelOK := cfg.ResolveModel(modelName)
	if !modelOK {
		checks = append(checks, fail("model", fmt.Sprintf("model %q is not configured", modelName)))
	} else {
		err := modelCfg.Validate(modelName)
		if err != nil {
			checks = append(checks, fail("model", err.Error()))
		} else {
			detail := fmt.Sprintf(
				"%s requests %s via provider %s",
				modelName, modelCfg.RequestID(modelName), modelCfg.Provider,
			)
			checks = append(checks, ok("model", detail))
		}
	}

	checks = append(checks, checkAPIKey(modelName, modelCfg, modelOK, opts.APIKey))
	checks = append(checks, checkTools(cfg.Tools)...)
	checks = append(checks, checkSkills(opts.Skills))
	checks = append(checks, checkSessionDir())

	if opts.Live {
		checks = append(checks, checkLive(ctx, opts, modelName, modelCfg, modelOK))
	} else {
		checks = append(checks, warn("live request", "skipped; pass -live to try a model request"))
	}

	return checks
}

func checkAPIKey(modelName string, modelCfg config.Model, modelOK bool, apiKey string) Check {
	if !modelOK {
		return warn("api key", "skipped because model could not be resolved")
	}

	if modelCfg.Provider == "openai" && modelCfg.BaseURL == "" {
		if strings.TrimSpace(apiKey) == "" {
			return fail("api key", "OPENAI_API_KEY is required for OpenAI cloud models")
		}

		return ok("api key", "OPENAI_API_KEY is set")
	}

	return ok("api key", "not required for "+modelName)
}

func checkTools(cfg config.ToolConfig) []Check {
	var checks []Check
	err := cfg.Validate()
	if err != nil {
		checks = append(checks, fail("tools", err.Error()))
	} else {
		checks = append(checks, ok("tools", "tool configuration is valid"))
	}

	if cfg.WorkspaceRoot == "" {
		return checks
	}

	root, err := filepath.Abs(cfg.WorkspaceRoot)
	if err != nil {
		return append(checks, fail("workspace root", err.Error()))
	}

	info, err := os.Stat(root)
	if err != nil {
		return append(checks, fail("workspace root", err.Error()))
	}

	if !info.IsDir() {
		return append(checks, fail("workspace root", root+" is not a directory"))
	}

	return append(checks, ok("workspace root", root))
}

func checkSkills(registry *skills.Registry) Check {
	if registry == nil {
		registry = skills.Discover(skills.DiscoverOptions{})
	}

	all := registry.Skills()
	visible := registry.ModelVisibleSkills()
	catalog := registry.ModelCatalog()

	diagnostics := registry.Diagnostics()
	if len(diagnostics) == 0 {
		if len(all) == 0 {
			return ok("skills", "no Agent Skills discovered")
		}

		detail := fmt.Sprintf(
			"%d discovered (%d model-visible, %d manual-only)",
			len(all), len(visible), len(all)-len(visible),
		)

		return ok("skills", detail)
	}

	warnings := 0
	errors := 0

	for _, diag := range diagnostics {
		switch diag.Level {
		case skills.DiagnosticError:
			errors++
		case skills.DiagnosticWarn:
			warnings++
		case skills.DiagnosticInfo:
			// Counted but not broken out separately.
		}
	}

	detail := fmt.Sprintf(
		"%d discovered; %d diagnostics (%d errors, %d warnings)",
		len(all), len(diagnostics), errors, warnings,
	)
	if catalog.Omitted > 0 {
		detail += fmt.Sprintf("; %d omitted from prompt/tool activation catalog", catalog.Omitted)
	}

	return warn("skills", detail+"; run /skills for details")
}

func checkSessionDir() Check {
	dir, err := session.DefaultDir()
	if err != nil {
		return fail("sessions", fmt.Sprintf("resolve session dir: %v", err))
	}

	err = os.MkdirAll(dir, 0755)
	if err != nil {
		return fail("sessions", fmt.Sprintf("create session dir: %v", err))
	}

	probe, err := os.CreateTemp(dir, ".doctor-*")
	if err != nil {
		return fail("sessions", fmt.Sprintf("session dir is not writable: %v", err))
	}

	name := probe.Name()
	err = probe.Close()
	if err != nil {
		_ = os.Remove(name)

		return fail("sessions", fmt.Sprintf("close session write probe: %v", err))
	}

	err = os.Remove(name)
	if err != nil {
		return fail("sessions", fmt.Sprintf("remove session write probe: %v", err))
	}

	return ok("sessions", dir)
}

func checkLive(ctx context.Context, opts Options, modelName string, modelCfg config.Model, modelOK bool) Check {
	if !modelOK {
		return fail("live request", "skipped because model could not be resolved")
	}

	liveCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()

	stream, err := opts.Client.Stream(liveCtx, llm.Request{
		Model: llm.Model{
			ID:      modelCfg.RequestID(modelName),
			BaseURL: modelCfg.BaseURL,
		},
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: "Reply with ok only."},
		},
		APIKey:    opts.APIKey,
		MaxTokens: 256,
	})
	if err != nil {
		return fail("live request", err.Error())
	}

	var text strings.Builder

	for ev := range stream {
		if ev.Err != nil {
			return fail("live request", ev.Err.Error())
		}

		text.WriteString(ev.Text)

		if ev.Done != nil {
			break
		}
	}

	reply := strings.TrimSpace(text.String())
	if reply == "" {
		return warn("live request", "model responded without text")
	}

	return ok("live request", fmt.Sprintf("model responded: %q", reply))
}

// Write renders checks in a stable human-readable format.
func Write(w io.Writer, checks []Check) {
	for _, check := range checks {
		fmt.Fprintf(w, "[%s] %-16s %s\n", check.Status, check.Name, check.Detail)
	}
}

// HasFailures reports whether any check failed.
func HasFailures(checks []Check) bool {
	for _, check := range checks {
		if check.Status == StatusFail {
			return true
		}
	}

	return false
}

func ok(name, detail string) Check {
	return Check{Name: name, Status: StatusOK, Detail: detail}
}

func warn(name, detail string) Check {
	return Check{Name: name, Status: StatusWarn, Detail: detail}
}

func fail(name, detail string) Check {
	return Check{Name: name, Status: StatusFail, Detail: detail}
}
