// Package cli drives the interactive prompt loop (and piped-stdin single-shot
// mode). It owns no LLM state itself — it delegates to the agent and streams
// events back to the terminal.
package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"nncode/internal/agent"
	"nncode/internal/config"
	"nncode/internal/session"
	"nncode/internal/skills"
)

type CLI struct {
	agent           *agent.Agent
	cfg             *config.Config
	sess            *session.Session
	in              io.Reader
	out             io.Writer
	errOut          io.Writer
	inputIsTerminal func() bool
	skillRegistry   *skills.Registry
	skillActivator  *skills.Activator
	strictPiped     bool
}

type Option func(*CLI)

// ErrIncompleteTurn reports that a non-interactive agent run ended without a
// meaningful response or effectful tool result.
var ErrIncompleteTurn = errors.New("agent turn incomplete")

func WithIO(in io.Reader, out io.Writer, errOut io.Writer, inputIsTerminal bool) Option {
	return func(c *CLI) {
		c.in = in
		c.out = out
		c.errOut = errOut
		c.inputIsTerminal = func() bool { return inputIsTerminal }
	}
}

func WithStrictPiped(strict bool) Option {
	return func(c *CLI) {
		c.strictPiped = strict
	}
}

func WithSkills(registry *skills.Registry, activator *skills.Activator) Option {
	return func(c *CLI) {
		c.skillRegistry = registry
		c.skillActivator = activator
	}
}

func New(ag *agent.Agent, cfg *config.Config, sess *session.Session, opts ...Option) *CLI {
	c := &CLI{
		agent:  ag,
		cfg:    cfg,
		sess:   sess,
		in:     os.Stdin,
		out:    os.Stdout,
		errOut: os.Stderr,
	}
	c.inputIsTerminal = defaultInputIsTerminal(c.in)
	for _, opt := range opts {
		opt(c)
	}
	if c.in == nil {
		c.in = os.Stdin
	}
	if c.out == nil {
		c.out = os.Stdout
	}
	if c.errOut == nil {
		c.errOut = os.Stderr
	}
	if c.inputIsTerminal == nil {
		c.inputIsTerminal = defaultInputIsTerminal(c.in)
	}
	return c
}

func defaultInputIsTerminal(in io.Reader) func() bool {
	return func() bool {
		f, ok := in.(*os.File)
		if !ok {
			return false
		}
		stat, err := f.Stat()
		return err == nil && stat.Mode()&os.ModeCharDevice != 0
	}
}

// Run decides between piped-stdin (single prompt, print and exit) and
// interactive mode based on whether stdin is a terminal.
func (c *CLI) Run() error {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	return c.RunContext(ctx)
}

func (c *CLI) RunContext(ctx context.Context) error {
	if !c.inputIsTerminal() {
		data, err := io.ReadAll(c.in)
		if err != nil {
			return fmt.Errorf("read stdin: %w", err)
		}
		prompt := strings.TrimSpace(string(data))
		if prompt == "" {
			return nil
		}
		outcome := c.runPrompt(ctx, prompt)
		c.saveSession()
		if outcome.Err != nil {
			return outcome.Err
		}
		if outcome.Incomplete() {
			fmt.Fprintln(c.errOut, "warning: agent completed without a response or effectful tool call; output may be incomplete")
			if c.strictPiped {
				return ErrIncompleteTurn
			}
		}
		return nil
	}

	return c.runInteractive(ctx)
}

func (c *CLI) runInteractive(ctx context.Context) error {
	fmt.Fprintf(c.out, "nncode - model: %s\n", c.agent.Model().ID)
	fmt.Fprintln(c.out, "Type your message and press Enter. /help for commands, /quit to exit.")
	fmt.Fprintln(c.out)

	scanner := bufio.NewScanner(c.in)
	scanner.Buffer(make([]byte, 0, 65536), 1024*1024)

	for {
		fmt.Fprint(c.out, "> ")
		if !scanner.Scan() {
			break
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "/") {
			if stop := c.handleCommand(ctx, line); stop {
				break
			}
			continue
		}
		_ = c.runPrompt(ctx, line)
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read input: %w", err)
	}
	c.saveSession()
	return nil
}

// handleCommand returns true if the loop should exit.
func (c *CLI) handleCommand(ctx context.Context, line string) bool {
	if strings.HasPrefix(line, "/skill:") {
		c.activateSkillCommand(ctx, line)
		return false
	}

	fields := strings.Fields(line)
	if len(fields) == 0 {
		return false
	}
	switch fields[0] {
	case "/quit", "/exit":
		return true
	case "/help":
		fmt.Fprintln(c.out, "Commands:")
		fmt.Fprintln(c.out, "  /help              Show this help")
		fmt.Fprintln(c.out, "  /quit              Exit the agent")
		fmt.Fprintln(c.out, "  /reset             Clear conversation history")
		fmt.Fprintln(c.out, "  /session           Show current session info")
		fmt.Fprintln(c.out, "  /sessions          List saved sessions")
		fmt.Fprintln(c.out, "  /resume <id|path>  Load a saved session")
		fmt.Fprintln(c.out, "  /tools             List available tools")
		fmt.Fprintln(c.out, "  /skills            List discovered Agent Skills")
		fmt.Fprintln(c.out, "  /skill:name [msg]  Activate an Agent Skill, optionally then run msg")
		fmt.Fprintln(c.out, "  /prompt            Show the current system prompt")
	case "/reset":
		c.agent.Reset()
		if c.skillActivator != nil {
			c.skillActivator.Reset()
		}
		c.sess = session.New()
		fmt.Fprintln(c.out, "Session reset.")
	case "/session":
		fmt.Fprintf(c.out, "ID:       %s\n", c.sess.ID)
		fmt.Fprintf(c.out, "Messages: %d\n", len(c.agent.Messages()))
		if c.sess.FilePath != "" {
			fmt.Fprintf(c.out, "File:     %s\n", c.sess.FilePath)
		}
	case "/sessions":
		c.listSessions()
	case "/resume":
		c.resumeSession(fields)
	case "/tools":
		for _, t := range c.agent.Tools() {
			fmt.Fprintf(c.out, "  %-8s %s\n", t.Name, t.Description)
		}
	case "/skills":
		c.listSkills()
	case "/prompt":
		fmt.Fprintln(c.out, c.agent.SystemPrompt())
	default:
		fmt.Fprintf(c.out, "Unknown command: %s (try /help)\n", line)
	}
	return false
}

func (c *CLI) listSkills() {
	if c.skillRegistry == nil || len(c.skillRegistry.Skills()) == 0 {
		fmt.Fprintln(c.out, "No Agent Skills discovered.")
	} else {
		fmt.Fprintf(c.out, "Agent Skills (%d):\n", len(c.skillRegistry.Skills()))
		for _, skill := range c.skillRegistry.Skills() {
			visibility := "model"
			if skill.DisableModelInvocation {
				visibility = "manual"
			}
			fmt.Fprintf(c.out, "  %-28s [%s] %s\n", skill.Name, visibility, truncate(skill.Description, 120))
		}
	}
	if c.skillRegistry == nil || len(c.skillRegistry.Diagnostics()) == 0 {
		return
	}
	fmt.Fprintln(c.out, "Diagnostics:")
	for _, diag := range c.skillRegistry.Diagnostics() {
		if diag.Path == "" {
			fmt.Fprintf(c.out, "  [%s] %s\n", diag.Level, diag.Message)
			continue
		}
		fmt.Fprintf(c.out, "  [%s] %s: %s\n", diag.Level, diag.Path, diag.Message)
	}
}

func (c *CLI) activateSkillCommand(ctx context.Context, line string) {
	if c.skillActivator == nil {
		fmt.Fprintln(c.out, "No Agent Skills are configured.")
		return
	}
	rest := strings.TrimSpace(strings.TrimPrefix(line, "/skill:"))
	if rest == "" {
		fmt.Fprintln(c.out, "Usage: /skill:name [message]")
		return
	}
	name, prompt, _ := strings.Cut(rest, " ")
	name = strings.TrimSpace(name)
	prompt = strings.TrimSpace(prompt)
	activation, err := c.skillActivator.Activate(name, true)
	if err != nil {
		fmt.Fprintf(c.out, "Could not activate skill: %v\n", err)
		return
	}
	if activation.Duplicate {
		fmt.Fprintf(c.out, "Skill %q is already active.\n", activation.Skill.Name)
	} else {
		c.agent.AddSystemMessage(skills.FormatActivation(activation))
		fmt.Fprintf(c.out, "Activated skill %q.\n", activation.Skill.Name)
	}
	c.sess.Messages = c.agent.Messages()
	if prompt != "" {
		_ = c.runPrompt(ctx, prompt)
	}
}

func (c *CLI) listSessions() {
	files, err := session.List()
	if err != nil {
		fmt.Fprintf(c.errOut, "warning: failed to list sessions: %v\n", err)
		return
	}
	if len(files) == 0 {
		fmt.Fprintln(c.out, "No saved sessions.")
		return
	}
	for _, file := range files {
		loaded, err := session.Load(file)
		if err != nil {
			fmt.Fprintf(c.out, "  %s (unreadable: %v)\n", file, err)
			continue
		}
		fmt.Fprintf(c.out, "  %-24s %4d messages  %s\n", loaded.ID, len(loaded.Messages), file)
	}
}

func (c *CLI) resumeSession(fields []string) {
	if len(fields) != 2 {
		fmt.Fprintln(c.out, "Usage: /resume <session-id|path>")
		return
	}
	path, err := session.Resolve(fields[1])
	if err != nil {
		fmt.Fprintf(c.out, "Could not resolve session: %v\n", err)
		return
	}
	loaded, err := session.Load(path)
	if err != nil {
		fmt.Fprintf(c.out, "Could not load session: %v\n", err)
		return
	}
	c.sess = loaded
	c.agent.SetMessages(loaded.Messages)
	if c.skillActivator != nil {
		c.skillActivator.Reset()
		for _, msg := range loaded.Messages {
			c.skillActivator.MarkActivatedFromText(msg.Content)
		}
	}
	fmt.Fprintf(c.out, "Resumed session %s (%d messages).\n", loaded.ID, len(loaded.Messages))
}

type promptOutcome struct {
	Err                   error
	assistantText         strings.Builder
	ToolCalls             int
	EffectfulToolResults  int
	SuccessfulToolResults int
	ErroredToolResults    int
}

func (o *promptOutcome) Incomplete() bool {
	if o == nil || o.Err != nil {
		return false
	}
	return strings.TrimSpace(o.assistantText.String()) == "" && o.EffectfulToolResults == 0
}

func (c *CLI) runPrompt(ctx context.Context, prompt string) promptOutcome {
	var outcome promptOutcome
	events := c.agent.Run(ctx, prompt)
	for ev := range events {
		switch ev.Type {
		case agent.EventText:
			outcome.assistantText.WriteString(ev.Text)
			fmt.Fprint(c.out, ev.Text)
		case agent.EventToolCallStart:
			fmt.Fprintf(c.out, "\n\033[2m▸ %s\033[0m", ev.ToolName)
		case agent.EventToolCallEnd:
			outcome.ToolCalls++
			if ev.ToolArgs != "" {
				fmt.Fprintf(c.out, "\033[2m %s\033[0m", truncate(ev.ToolArgs, 160))
			}
			fmt.Fprintln(c.out)
		case agent.EventToolResult:
			if ev.IsError {
				outcome.ErroredToolResults++
				fmt.Fprintf(c.out, "\033[31m✗ %s\033[0m\n", truncate(ev.Result, 500))
			} else {
				outcome.SuccessfulToolResults++
				if isEffectfulToolResult(ev.ToolName, ev.Metadata) {
					outcome.EffectfulToolResults++
				}
				preview := truncate(skills.StripActivationMarkers(ev.Result), 400)
				if preview != "" {
					fmt.Fprintf(c.out, "\033[2m%s\033[0m\n", preview)
				}
			}
		case agent.EventError:
			if outcome.Err == nil {
				outcome.Err = ev.Err
			}
			fmt.Fprintf(c.errOut, "\n[error] %v\n", ev.Err)
		case agent.EventDone:
			fmt.Fprintln(c.out)
		}
	}
	c.sess.Messages = c.agent.Messages()
	return outcome
}

func isEffectfulToolResult(name string, metadata map[string]any) bool {
	switch name {
	case "write", "edit":
		return true
	case "patch":
		return metadataInt(metadata, "files_changed") > 0
	case "bash":
		return metadataInt(metadata, "exit_code") == 0
	default:
		return false
	}
}

func metadataInt(metadata map[string]any, key string) int64 {
	if metadata == nil {
		return 0
	}
	switch value := metadata[key].(type) {
	case int:
		return int64(value)
	case int64:
		return value
	case int32:
		return int64(value)
	case float64:
		return int64(value)
	case float32:
		return int64(value)
	default:
		return 0
	}
}

func (c *CLI) saveSession() {
	if len(c.sess.Messages) == 0 {
		return
	}
	if err := c.sess.Save(""); err != nil {
		fmt.Fprintf(c.errOut, "warning: failed to save session: %v\n", err)
	}
}

func truncate(s string, n int) string {
	if len(s) > n {
		s = s[:n] + "…"
	}
	return strings.ReplaceAll(s, "\n", " ")
}
