package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"nncode/internal/agent"
	"nncode/internal/config"
	"nncode/internal/doctor"
	"nncode/internal/llm"
	"nncode/internal/session"
	"nncode/internal/skills"
	builtintools "nncode/internal/tools"
	"nncode/pkg/cli"
)

const defaultSystemPrompt = `You are a coding assistant running on the user's machine.

You have tools to read, write, and edit files, and to run bash commands. Use them
as needed to complete the user's request. Prefer reading before writing, and
verify your work by running commands when it makes sense.

Be concise. When the task is done, stop.`

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	return runWithArgs(os.Args[1:])
}

func runWithArgs(args []string) error {
	if len(args) > 0 && args[0] == "doctor" {
		return runDoctorCommand(args[1:])
	}

	flags := flag.NewFlagSet("nncode", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	modelFlag := flags.String("model", "", "model name to use (overrides default_model from config)")
	resumeFlag := flags.String("resume", "", "session ID or path to resume before running")
	checkFlag := flags.Bool("check", false, "run setup diagnostics and exit")
	strictFlag := flags.Bool("strict", false, "in piped mode, exit non-zero if the agent produces no response and no effectful tool call")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q", flags.Arg(0))
	}

	cfg, err := loadMergedConfig()
	if err != nil {
		return err
	}
	if *checkFlag {
		return runDoctorReport(cfg, *modelFlag, false, 10*time.Second)
	}
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("invalid config: %w", err)
	}

	return runAgent(cfg, *modelFlag, *resumeFlag, *strictFlag)
}

func loadMergedConfig() (*config.Config, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	proj, err := config.LoadProject()
	if err != nil {
		return nil, fmt.Errorf("load project config: %w", err)
	}
	cfg.Merge(proj)
	return cfg, nil
}

func runAgent(cfg *config.Config, modelFlag string, resumeRef string, strictPiped bool) error {
	modelName := modelFlag
	if modelName == "" {
		modelName = cfg.DefaultModel
	}
	modelCfg, ok := cfg.ResolveModel(modelName)
	if !ok {
		return fmt.Errorf("model %q is not configured", modelName)
	}
	if err := modelCfg.Validate(modelName); err != nil {
		return err
	}
	model := buildModel(modelName, modelCfg)

	skillRegistry := skills.Discover(skills.DiscoverOptions{})
	skillActivator := skills.NewActivator(skillRegistry)
	sysPrompt := skills.ComposeSystemPrompt(loadSystemPrompt(), skillRegistry)
	sess := session.New()
	if resumeRef != "" {
		path, err := session.Resolve(resumeRef)
		if err != nil {
			return fmt.Errorf("resolve resume session: %w", err)
		}
		loaded, err := session.Load(path)
		if err != nil {
			return fmt.Errorf("load resume session: %w", err)
		}
		sess = loaded
	}

	ag := agent.New(agent.Config{
		Model:     model,
		Client:    llm.NewOpenAIClient(),
		APIKey:    os.Getenv("OPENAI_API_KEY"),
		Tools:     buildTools(cfg.Tools, skillActivator),
		MaxTokens: modelCfg.MaxTokens,
	}, sysPrompt)
	if len(sess.Messages) > 0 {
		ag.SetMessages(sess.Messages)
		for _, msg := range sess.Messages {
			skillActivator.MarkActivatedFromText(msg.Content)
		}
	}

	return cli.New(ag, cfg, sess, cli.WithSkills(skillRegistry, skillActivator), cli.WithStrictPiped(strictPiped)).Run()
}

func runDoctorCommand(args []string) error {
	flags := flag.NewFlagSet("nncode doctor", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	modelFlag := flags.String("model", "", "model name to check (defaults to resolved default_model)")
	liveFlag := flags.Bool("live", false, "try a small live model request")
	timeoutFlag := flags.Duration("timeout", 10*time.Second, "timeout for -live request")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() > 0 {
		return fmt.Errorf("unexpected doctor argument %q", flags.Arg(0))
	}
	cfg, err := loadMergedConfig()
	if err != nil {
		return err
	}
	return runDoctorReport(cfg, *modelFlag, *liveFlag, *timeoutFlag)
}

func runDoctorReport(cfg *config.Config, modelName string, live bool, timeout time.Duration) error {
	checks := doctor.Run(context.Background(), doctor.Options{
		Config:    cfg,
		ModelName: modelName,
		APIKey:    os.Getenv("OPENAI_API_KEY"),
		Live:      live,
		Timeout:   timeout,
	})
	doctor.Write(os.Stdout, checks)
	if doctor.HasFailures(checks) {
		return fmt.Errorf("doctor found problems")
	}
	return nil
}

func buildTools(cfg config.ToolConfig, skillActivator *skills.Activator) []agent.Tool {
	opts := builtintools.Options{
		RootDir:            cfg.WorkspaceRoot,
		MaxReadBytes:       cfg.MaxReadBytes,
		MaxWriteBytes:      cfg.MaxWriteBytes,
		MaxBashOutputBytes: cfg.MaxBashOutputBytes,
		BashTimeout:        time.Duration(cfg.BashTimeoutSeconds) * time.Second,
	}
	var out []agent.Tool
	if !cfg.IsDisabled("read") {
		out = append(out, builtintools.Read(opts))
	}
	if !cfg.IsDisabled("write") {
		out = append(out, builtintools.Write(opts))
	}
	if !cfg.IsDisabled("edit") {
		out = append(out, builtintools.Edit(opts))
	}
	if !cfg.IsDisabled("patch") {
		out = append(out, builtintools.Patch(opts))
	}
	if !cfg.IsDisabled("bash") {
		out = append(out, builtintools.Bash(opts))
	}
	if skillActivator != nil && skillActivator.Registry() != nil && len(skillActivator.Registry().ModelVisibleSkills()) > 0 {
		out = append(out, builtintools.ActivateSkill(skillActivator))
	}
	return out
}

func buildModel(name string, cfg config.Model) llm.Model {
	return llm.Model{ID: cfg.RequestID(name), BaseURL: cfg.BaseURL}
}

// loadSystemPrompt returns the first prompt file found, preferring project-local
// over global. Falls back to defaultSystemPrompt.
func loadSystemPrompt() string {
	candidates := []string{filepath.Join(".nncode", "system_prompt.md")}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(home, ".nncode", "system_prompt.md"))
	}
	for _, path := range candidates {
		if data, err := os.ReadFile(path); err == nil {
			return string(data)
		}
	}
	return defaultSystemPrompt
}
