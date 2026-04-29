# AGENTS.md

## What this is

nncode is a minimal coding agent written in Go. It runs an agent loop: prompt -> LLM -> tool calls -> execute -> repeat. It has OpenAI-compatible model support, JSONL sessions, local Agent Skills, and a small synchronous tool set.

Keep the scope tight. It is a CLI that streams responses to stdout, with an optional Bubble Tea TUI for interactive terminal sessions.

## Product principles

- **Deployability first.** One Go binary, zero runtime dependencies. It runs in containers, CI images, and air-gapped networks without package managers or Node/Python stacks.
- **Auditability by default.** Every turn is a JSONL session. Every tool call is recorded. If you can't diff it, debug it, or replay it, the feature does not belong here.
- **Do not chase features.** Git integration, web search, and MCP are deliberately out of scope. nncode wins on being small, inspectable, and deterministic — not on feature parity with heavier alternatives.
- **Minimalism is a ceiling and a floor.** The tool set is intentionally small. Additions must prove they sharpen deployability or auditability; convenience alone is not enough.

> **Design reference:** For visual identity, color system, typography, and CRT styling rules, see [`DESIGN.md`](DESIGN.md). Agents working on TUI, branding, or terminal visuals should read it before making changes.

## Build & test

```bash
go vet ./...                         # Static analysis
golangci-lint fmt ./...              # Format imports and code
golangci-lint run ./...              # Strict lint (see .golangci.yml; default: all)
go test ./...                        # Run tests
go build -o nncode ./cmd/nncode/     # Build fresh repo-local binary
```

`golangci-lint` v2 is configured in `.golangci.yml` with `linters.default: all` (every linter enabled except `wsl` and `wsl_v5`) and `goimports` enabled as the project's formatter. Install locally with the [official script](https://golangci-lint.run/docs/welcome/install/local/). `scripts/run-release-checklist.sh` runs it as part of the full checklist (and skips it gracefully if it is not on PATH).

Tests use `github.com/stretchr/testify` (test-only dependency). Add or update tests for new behavior, including `cmd/nncode` and `pkg/cli` wiring when the user-facing CLI changes. No CI config yet.

If the default Go build cache is not writable in a sandbox, use `env GOCACHE=/tmp/nncode-go-build ...` with the same commands.

## Verify after every change

Never report a task as done without running these, in order:

1. `go vet ./...` — catches common mistakes the compiler misses
2. `golangci-lint fmt ./...` — ensures imports and formatting are consistent
3. `golangci-lint run ./...` — strict lint with `default: all`
4. `go test ./...` — all packages must stay green
5. `go build -o nncode ./cmd/nncode/` — produces a fresh binary (stale binaries are a common source of "it works on my machine" confusion)
6. Smoke-check the binary based on what you changed:
   - Flags or startup wiring → `./nncode -h` shows expected flags, and `./nncode -badflag` exits non-zero
   - CLI/slash commands → `printf '\n' | ./nncode` exits cleanly; for interactive changes, run `./nncode` and try the affected command
   - Config or model resolution → run with `-model <name>` and confirm the request goes to the expected `base_url` (a stale binary will use the old default — rebuild first)
   - Agent loop or tool execution → if possible, run a real prompt against a reachable model; otherwise explicitly flag "runtime path not exercised" when reporting
   - Non-interactive dogfooding → use `-strict` so a turn with no assistant text and no successful effectful tool call exits non-zero

Build/test passing is necessary but not sufficient — if you can't actually run the changed path, say so rather than claiming success.

`-strict` catches one important incomplete-turn class, but it does not prove the agent fulfilled the task. When dogfooding nncode for implementation work, inspect the files it touched and run the target project's own validation before trusting the result.

## Project structure

```
cmd/nncode/     # Entry point — flag parsing, wiring
internal/       # Private packages
  agent/        # Agent loop, state, tools, events (agent.go, loop.go, event.go, tool.go)
  agentloop/    # User-defined Agent Loop loading and orchestration
  doctor/       # Setup diagnostics used by `nncode doctor` and `nncode -check`
  llm/          # LLM abstraction — Client interface + OpenAI-compatible provider (client.go, openai.go)
  projectctx/   # Auto-detects project files and injects a compact summary into the system prompt
  skills/       # Agent Skills discovery, prompt catalog, activation, diagnostics
  tools/        # Built-in tools: read, write, edit, patch, bash, grep, find, activate_skill
  session/      # JSONL session persistence
  config/       # Settings loading (global + project-local, merged onto defaults)
pkg/cli/        # CLI layer — interactive + piped-stdin modes, slash commands
pkg/tui/        # Bubble Tea TUI — interactive terminal sessions with overlays
```

## Coding conventions

- No external runtime dependencies beyond the Go standard library (testify is test-only)
- `internal/` packages are private; only `pkg/` and `cmd/` are public-facing
- Error handling: wrap errors with `fmt.Errorf("...: %w", err)`, never swallow
- No panics in normal operation — return errors instead
- Keep functions small; the agent loop in `loop.go` is the largest file
- No logging — the CLI renders events; everything else returns errors

## User-facing surfaces

Agents editing user-facing behavior should know these exist:

- `-model <name>` flag overrides `default_model` for a single run
- `-resume <id|path>` loads a saved session before the first prompt
- `-loop <name|path>` runs a configured Agent Loop in piped mode
- `-loop-check <name|path>` validates a configured Agent Loop and exits without a model request
- `-confirm` requires explicit confirmation for effectful tools; in piped mode it skips them unless `-no-confirm` is also passed
- `-no-confirm` explicitly allows effectful tools without confirmation (pairs with `-confirm`)
- `-strict` only affects piped mode; it returns non-zero for incomplete turns with no assistant response and no successful effectful tool call
- `-check` runs local setup diagnostics; `nncode doctor [-model <name>] [-live] [-timeout <duration>]` is the fuller diagnostics command and warns on invalid configured Agent Loops
- `nncode loop list|check|run` exposes Agent Loop list, validation, and piped-run workflows for scripts
- `nncode session show|export|changes <id|path>` inspects saved sessions: show prints context, export renders Markdown, changes lists effectful tool calls
- `~/.nncode/config.json` (global) and `./.nncode/config.json` (project-local) overlay built-in defaults; partial configs work
- `~/.nncode/system_prompt.md` and `./.nncode/system_prompt.md` override the default system prompt; project-local wins
- `OPENAI_API_KEY` env var is the only credential source
- Tool names that can be disabled in config: `read`, `write`, `edit`, `patch`, `bash`, `grep`, `find`
- `-dry-run` previews effectful tool calls (`write`, `edit`, `patch`, `bash`) and Agent Loop `cmd` nodes without executing them; `read`, `grep`, `find`, and `activate_skill` still run normally
- Model config fields: `context_window` (manual context size fallback) and `context_probe` (`auto`, `off`, `llamacpp`) control live metadata probing
- Slash commands in interactive mode: `/help`, `/quit`, `/exit`, `/reset`, `/context -print|-reset`, `/compress`, `/session`, `/sessions`, `/resume`, `/tools`, `/skills`, `/skill:name`, `/loops`, `/loop`, `/loop-validate`, `/prompt`

## Agent Skills

Agent Skills use progressive disclosure. Keep this model intact unless the user explicitly asks for a larger design:

- Discover only local `.agents/skills` directories from cwd up to the git root, then `~/.agents/skills` at lower precedence.
- During discovery, read only `SKILL.md` frontmatter: required `name`, required `description`, optional `disable-model-invocation`.
- The system prompt gets a compact generated catalog of visible skill names and descriptions only. `/prompt` shows the composed prompt.
- The `activate_skill` tool is registered only when model-visible skills exist. Its enum matches the same capped catalog used in the prompt.
- Activation returns the body-only `SKILL.md` content inside `<skill_content>`, the skill directory, and a capped resource listing.
- `disable-model-invocation: true` hides a skill from the prompt and tool enum, but `/skill:name` can still activate it manually.
- Do not add `.nncode/skills`, `.claude`, package install, remote import, or runtime dependencies unless the user changes the scope.

## Dogfooding nncode

When using nncode itself as the patch author:

- Rebuild `./nncode` first. A stale binary is the easiest way to test the wrong behavior.
- Prefer a temporary HOME with an explicit config, especially for local models, so dogfood runs do not mutate the user's real sessions or config.
- In piped mode, prefer `printf '%s\n' '<prompt>' | env HOME=/tmp/nncode-live-home ./nncode -model <name> -strict`.
- Treat "read-only then exit" as a failed implementation attempt even if the process exits zero without `-strict`.
- Keep prompts small and concrete. If the model stalls or only analyzes, either rerun with a narrower prompt or take over manually.
- Always inspect actual file contents afterward; do not rely on session text or exit code alone.

## Adding a new tool

1. Create a new file in `internal/tools/` (e.g., `grep.go`)
2. Return an `agent.Tool` with `Name`, `Description`, `Parameters` (a JSON-schema literal string), and an `Execute(ctx, args json.RawMessage) (agent.ToolResult, error)` closure
3. Register it in `cmd/nncode/main.go` alongside the existing tools
4. Add tests in `internal/tools/tools_test.go` — call `tool.Execute(ctx, args)` directly
5. If the tool should count as an effectful action in piped `-strict` mode, update `pkg/cli` outcome tracking and tests

## Adding a new LLM provider

1. Implement the `llm.Client` interface in a new file under `internal/llm/`
2. The interface is small: a single `Stream(ctx context.Context, req Request) (<-chan StreamEvent, error)` method that yields `StreamEvent`s (text chunks, tool-call starts/ends, a final `Done`, or an `Err`)
3. Wire it up in `cmd/nncode/main.go` by constructing the client and passing it into `agent.Config.Client`

## Architecture notes

- The agent loop (`internal/agent/loop.go`) is the core: it calls the LLM, parses tool calls from the stream, executes tools, feeds results back as `llm.RoleTool` messages, and repeats until no tool calls remain or `MaxTurns` is hit
- The agent owns the conversation history; the CLI aliases `sess.Messages = agent.Messages()` after each turn and saves once on exit
- User-defined Agent Loops live in strict versioned JSON files under `./.nncode/loops` and `~/.nncode/loops`; project-local loops win by filename, `/loops` and `nncode loop list` show runnable refs, and the loop runner orchestrates repeated `Agent.RunWithOptions` calls with scoped loop system messages and lifecycle events
- Agent Loop v1 files require `schema_version: 1`; node types are `entry_prompt`, `prompt`, `cmd`, `exit_criteria`, and `exit_prompt`. The durable authoring spec is `docs/agent-loops-v1.md`, with a machine-readable schema at `docs/agent-loops-v1.schema.json`.
- Agent Loop `cmd` nodes execute `content` through bash, use the configured tool workspace root, timeout, and output limits, and default to aborting on failure unless `settings.on_error` is `"continue"`. Disabling the `bash` tool also disables `cmd` node execution.
- Non-effectful tools (`read`, `grep`, `find`, `activate_skill`) execute in parallel within a turn; effectful tools (`write`, `edit`, `patch`, `bash`) execute sequentially in their original order
- Tools are synchronous — they block the loop until they return
- Sessions are JSONL files in `~/.nncode/sessions/`; there's no undo or branching
- The CLI is synchronous and blocking — no async event queue
- Config resolution: built-in `defaultConfig()` → `~/.nncode/config.json` → `./.nncode/config.json` → `-model` flag, merged left-to-right (`Config.Merge` does the overlay)
- Skills are discovered at startup and composed into the system prompt before the agent is created; resumed sessions also mark previously activated skills from structured markers in message content
- Auto-context injection: on startup, `projectctx.Gather` detects common project files (`go.mod`, `package.json`, `Cargo.toml`, etc.) in the working directory and appends a compact `<project_context>` block to the system prompt so the model knows the project type before the first turn

## Boundaries

- Do not add runtime dependencies without asking
- The TUI lives in `pkg/tui/` and is the default for interactive terminal sessions; use `-no-tui` to force the plain CLI
- TUI mid-turn cancellation: when the agent is running, `ctrl+c` cancels the current turn instead of quitting; a second `ctrl+c` while idle exits the app
- Do not modify `internal/llm/openai.go` to support non-OpenAI-compatible APIs — use a new provider instead
