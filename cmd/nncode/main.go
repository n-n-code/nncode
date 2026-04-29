package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"nncode/internal/agent"
	"nncode/internal/agentloop"
	"nncode/internal/config"
	"nncode/internal/contextprint"
	"nncode/internal/contextwindow"
	"nncode/internal/doctor"
	"nncode/internal/llm"
	"nncode/internal/projectctx"
	"nncode/internal/session"
	"nncode/internal/skills"
	builtintools "nncode/internal/tools"
	"nncode/pkg/cli"
	"nncode/pkg/tui"
)

const defaultSystemPrompt = `You are a coding assistant running on the user's machine.

You have tools to read, write, and edit files, and to run bash commands. Use them
as needed to complete the user's request. Prefer reading before writing, and
verify your work by running commands when it makes sense.

Be concise. When the task is done, stop.`

const defaultDoctorTimeout = 10 * time.Second

var (
	errUnexpectedArg        = errors.New("unexpected argument")
	errUnexpectedDoctorArg  = errors.New("unexpected doctor argument")
	errUnexpectedLoopArg    = errors.New("unexpected loop argument")
	errUnexpectedSessionArg = errors.New("unexpected session argument")
	errSessionArgRequired   = errors.New("session argument required")
	errLoopArgRequired      = errors.New("loop argument required")
	errLoopCommandRequired  = errors.New("loop subcommand is required")
	errUnknownLoopCommand   = errors.New("unknown loop subcommand")
	errModelNotConfigured   = errors.New("model is not configured")
	errDoctorFoundProblems  = errors.New("doctor found problems")
	errLoopRequiresPiped    = errors.New("-loop requires piped stdin; use /loop in interactive mode")
)

func main() {
	err := run()
	if err != nil {
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

	if len(args) > 0 && args[0] == "loop" {
		return runLoopCommand(args[1:])
	}

	if len(args) > 0 && args[0] == "session" {
		return runSessionCommand(args[1:])
	}

	flags := flag.NewFlagSet("nncode", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	modelFlag := flags.String("model", "", "model name to use (overrides default_model from config)")
	loopFlag := flags.String("loop", "", "Agent Loop name or path to run with piped stdin")
	loopCheckFlag := flags.String("loop-check", "", "validate an Agent Loop name or path and exit")
	resumeFlag := flags.String("resume", "", "session ID or path to resume before running")
	checkFlag := flags.Bool("check", false, "run setup diagnostics and exit")

	strictFlag := flags.Bool("strict", false,
		"in piped mode, exit non-zero if the agent produces no response and no successful effectful tool call")

	confirmFlag := flags.Bool("confirm", false,
		"in piped mode, skip effectful tools and require -no-confirm to execute them")

	noConfirmFlag := flags.Bool("no-confirm", false,
		"in piped mode, explicitly allow effectful tools without confirmation")

	dryRunFlag := flags.Bool("dry-run", false,
		"preview effectful tool calls without executing them")

	noTUIFlag := flags.Bool("no-tui", false, "force plain CLI even in interactive terminal mode")

	err := flags.Parse(args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}

		return fmt.Errorf("parse flags: %w", err)
	}

	if flags.NArg() > 0 {
		return fmt.Errorf("%w: %q", errUnexpectedArg, flags.Arg(0))
	}

	if *loopCheckFlag != "" {
		return runLoopCheck(*loopCheckFlag)
	}

	cfg, err := prepareConfig(*modelFlag)
	if err != nil {
		return err
	}

	if *checkFlag {
		return runDoctorReport(cfg, *modelFlag, false, defaultDoctorTimeout)
	}

	err = cfg.Validate()
	if err != nil {
		return fmt.Errorf("invalid config: %w", err)
	}

	return runAgent(cfg, *modelFlag, *loopFlag, *resumeFlag, *strictFlag,
		*confirmFlag, *noConfirmFlag, *dryRunFlag, *noTUIFlag)
}

func prepareConfig(modelFlag string) (*config.Config, error) {
	cfg, err := loadMergedConfig()
	if err != nil {
		return nil, err
	}

	if modelFlag != "" {
		cfg.AutoVendModel(modelFlag)
	}

	return cfg, nil
}

func runLoopCheck(ref string) error {
	summary, err := agentloop.Validate(ref, agentloop.StoreOptions{})
	if err != nil {
		return fmt.Errorf("validate Agent Loop %q: %w", ref, err)
	}

	fmt.Fprintf(os.Stdout, "Agent Loop %q is valid (%s).\n", summary.Ref, summary.Path)

	return nil
}

func runLoopCommand(args []string) error {
	if len(args) == 0 {
		return errLoopCommandRequired
	}

	switch args[0] {
	case "list":
		return runLoopListCommand(args[1:])
	case "check":
		return runLoopCheckCommand(args[1:])
	case "run":
		return runLoopRunCommand(args[1:])
	default:
		return fmt.Errorf("%w: %q", errUnknownLoopCommand, args[0])
	}
}

func runSessionCommand(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%w: session subcommand required (show, export, changes)", errSessionArgRequired)
	}

	switch args[0] {
	case "show":
		return runSessionShowCommand(args[1:])
	case "export":
		return runSessionExportCommand(args[1:])
	case "changes":
		return runSessionChangesCommand(args[1:])
	default:
		return fmt.Errorf("%w: %q", errUnexpectedSessionArg, args[0])
	}
}

func loadSessionFromArg(arg string) (*session.Session, error) {
	path, err := session.Resolve(arg)
	if err != nil {
		return nil, fmt.Errorf("resolve session: %w", err)
	}

	sess, err := session.Load(path)
	if err != nil {
		return nil, fmt.Errorf("load session: %w", err)
	}

	return sess, nil
}

func runSessionShowCommand(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("%w: session show requires exactly one session ID or path", errSessionArgRequired)
	}

	sess, err := loadSessionFromArg(args[0])
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stdout, "Session: %s\n", sess.ID)
	fmt.Fprintf(os.Stdout, "Messages: %d\n\n", len(sess.Messages))
	fmt.Fprintln(os.Stdout, contextprint.Format("", sess.Messages))

	return nil
}

func runSessionExportCommand(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("%w: session export requires exactly one session ID or path", errSessionArgRequired)
	}

	sess, err := loadSessionFromArg(args[0])
	if err != nil {
		return err
	}

	fmt.Fprint(os.Stdout, session.ExportMarkdown(sess))

	return nil
}

func runSessionChangesCommand(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("%w: session changes requires exactly one session ID or path", errSessionArgRequired)
	}

	sess, err := loadSessionFromArg(args[0])
	if err != nil {
		return err
	}

	changes := session.ExtractChanges(sess.Messages)
	fmt.Fprint(os.Stdout, session.FormatChanges(changes))
	fmt.Fprintln(os.Stdout)

	return nil
}

func runLoopListCommand(args []string) error {
	flags := flag.NewFlagSet("nncode loop list", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)

	err := flags.Parse(args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}

		return fmt.Errorf("parse loop list flags: %w", err)
	}

	if flags.NArg() > 0 {
		return fmt.Errorf("%w: %q", errUnexpectedLoopArg, flags.Arg(0))
	}

	summaries, err := agentloop.List(agentloop.StoreOptions{})
	if err != nil {
		return fmt.Errorf("list Agent Loops: %w", err)
	}

	agentloop.WriteSummaries(os.Stdout, summaries, 0)

	return nil
}

func runLoopCheckCommand(args []string) error {
	flags := flag.NewFlagSet("nncode loop check", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)

	err := flags.Parse(args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}

		return fmt.Errorf("parse loop check flags: %w", err)
	}

	if flags.NArg() != 1 {
		return fmt.Errorf("%w: loop check requires exactly one name or path", errLoopArgRequired)
	}

	return runLoopCheck(flags.Arg(0))
}

func runLoopRunCommand(args []string) error {
	flags := flag.NewFlagSet("nncode loop run", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	modelFlag := flags.String("model", "", "model name to use (overrides default_model from config)")
	resumeFlag := flags.String("resume", "", "session ID or path to resume before running")
	strictFlag := flags.Bool("strict", false,
		"in piped mode, exit non-zero if the agent produces no response and no successful effectful tool call")
	dryRunFlag := flags.Bool("dry-run", false,
		"preview effectful tool calls without executing them")
	noTUIFlag := flags.Bool("no-tui", false, "force plain CLI even in interactive terminal mode")

	err := flags.Parse(args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}

		return fmt.Errorf("parse loop run flags: %w", err)
	}

	if flags.NArg() != 1 {
		return fmt.Errorf("%w: loop run requires exactly one name or path", errLoopArgRequired)
	}

	cfg, err := prepareConfig(*modelFlag)
	if err != nil {
		return err
	}

	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("invalid config: %w", err)
	}

	return runAgent(cfg, *modelFlag, flags.Arg(0), *resumeFlag, *strictFlag, false, false, *dryRunFlag, *noTUIFlag)
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

func runAgent(
	cfg *config.Config,
	modelFlag string,
	loopRef string,
	resumeRef string,
	strictPiped bool,
	confirm bool,
	noConfirm bool,
	dryRun bool,
	noTUI bool,
) error {
	modelName := modelFlag
	if modelName == "" {
		modelName = cfg.DefaultModel
	}

	modelCfg, ok := cfg.ResolveModel(modelName)
	if !ok {
		return fmt.Errorf("%w: %q", errModelNotConfigured, modelName)
	}

	err := modelCfg.Validate(modelName)
	if err != nil {
		return fmt.Errorf("validate model %q: %w", modelName, err)
	}

	model := buildModel(modelName, modelCfg)
	initialWindow := contextwindow.Configured(modelCfg)
	apiKey := os.Getenv("OPENAI_API_KEY")
	contextResolver := func(ctx context.Context) contextwindow.Window {
		resolveCtx, resolveCancel := context.WithTimeout(ctx, contextwindow.DefaultResolveTimeout)
		defer resolveCancel()

		return contextwindow.Resolver{APIKey: apiKey}.Resolve(resolveCtx, modelCfg, model.ID)
	}

	skillRegistry := skills.Discover(skills.DiscoverOptions{})
	skillActivator := skills.NewActivator(skillRegistry)
	basePrompt := composeSystemPrompt(loadSystemPrompt())
	basePrompt = projectctx.AppendToPrompt(basePrompt, "")
	sysPrompt := skills.ComposeSystemPrompt(basePrompt, skillRegistry)
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
		APIKey:    apiKey,
		Tools:     buildTools(cfg.Tools, skillActivator),
		MaxTokens: modelCfg.MaxTokens,
		DryRun:    dryRun,
	}, sysPrompt)
	if len(sess.Messages) > 0 {
		ag.SetMessages(sess.Messages)

		for _, msg := range sess.Messages {
			skillActivator.MarkActivatedFromText(msg.Content)
		}
	}

	isTerminal := stdinIsTerminal()
	if loopRef != "" && isTerminal {
		return errLoopRequiresPiped
	}

	useTUI := !noTUI && isTerminal

	if useTUI {
		err = tui.Run(ag, cfg, sess, skillRegistry, skillActivator, initialWindow, contextResolver)
		if err != nil {
			return fmt.Errorf("tui run: %w", err)
		}

		return nil
	}

	cliInst := cli.New(ag, cfg, sess,
		cli.WithSkills(skillRegistry, skillActivator),
		cli.WithContextWindow(initialWindow),
		cli.WithContextResolver(contextResolver),
		cli.WithLoopRef(loopRef),
		cli.WithStrictPiped(strictPiped),
		cli.WithConfirm(confirm, noConfirm))

	err = cliInst.Run()
	if err != nil {
		return fmt.Errorf("cli run: %w", err)
	}

	return nil
}

func stdinIsTerminal() bool {
	stat, err := os.Stdin.Stat()
	if err != nil {
		return false
	}

	return stat.Mode()&os.ModeCharDevice != 0
}

func runDoctorCommand(args []string) error {
	flags := flag.NewFlagSet("nncode doctor", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	modelFlag := flags.String("model", "", "model name to check (defaults to resolved default_model)")
	liveFlag := flags.Bool("live", false, "try a small live model request")

	timeoutFlag := flags.Duration("timeout", defaultDoctorTimeout, "timeout for -live request")

	err := flags.Parse(args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}

		return fmt.Errorf("parse doctor flags: %w", err)
	}

	if flags.NArg() > 0 {
		return fmt.Errorf("%w: %q", errUnexpectedDoctorArg, flags.Arg(0))
	}

	cfg, err := prepareConfig(*modelFlag)
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
		return errDoctorFoundProblems
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

	if !cfg.IsDisabled("grep") {
		out = append(out, builtintools.Grep(opts))
	}

	if !cfg.IsDisabled("find") {
		out = append(out, builtintools.Find(opts))
	}

	out = append(out, builtintools.Tree())

	if skillActivator != nil && len(skillActivator.Registry().ModelVisibleSkills()) > 0 {
		out = append(out, builtintools.ActivateSkill(skillActivator))
	}

	return out
}

func buildModel(name string, cfg config.Model) llm.Model {
	return llm.Model{ID: cfg.RequestID(name), BaseURL: cfg.BaseURL}
}

func composeSystemPrompt(base string) string {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}

	var builder strings.Builder
	builder.WriteString(strings.TrimRight(base, "\n"))
	builder.WriteString("\n\nThe current working directory is ")
	builder.WriteString(cwd)
	builder.WriteString(". Prefer relative paths when creating or modifying files.")

	return builder.String()
}

func loadSystemPrompt() string {
	candidates := []string{filepath.Join(".nncode", "system_prompt.md")}

	home, err := os.UserHomeDir()
	if err == nil {
		candidates = append(candidates, filepath.Join(home, ".nncode", "system_prompt.md"))
	}

	for _, path := range candidates {
		data, err := os.ReadFile(path)
		if err == nil {
			return string(data)
		}
	}

	return defaultSystemPrompt
}
