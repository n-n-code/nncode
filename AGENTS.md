# AGENTS.md

## What this is

nncode is a minimal coding agent written in Go. It runs an agent loop: prompt -> LLM -> tool calls -> execute -> repeat. It has OpenAI-compatible model support, JSONL sessions, local Agent Skills, and a small synchronous tool set.

Keep the scope tight: no sub-agents, no MCP, no TUI, no permission UI. It is just a CLI that streams responses to stdout.

## Build & test

```bash
go vet ./...                         # Static analysis
go test ./...                        # Run tests
go build -o nncode ./cmd/nncode/     # Build fresh repo-local binary
```

Tests use `github.com/stretchr/testify` (test-only dependency). Add or update tests for new behavior, including `cmd/nncode` and `pkg/cli` wiring when the user-facing CLI changes. No CI config yet.

If the default Go build cache is not writable in a sandbox, use `env GOCACHE=/tmp/nncode-go-build ...` with the same commands.

## Verify after every change

Never report a task as done without running these, in order:

1. `go vet ./...` — catches common mistakes the compiler misses
2. `go test ./...` — all packages must stay green
3. `go build -o nncode ./cmd/nncode/` — produces a fresh binary (stale binaries are a common source of "it works on my machine" confusion)
4. Smoke-check the binary based on what you changed:
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
  doctor/       # Setup diagnostics used by `nncode doctor` and `nncode -check`
  llm/          # LLM abstraction — Client interface + OpenAI-compatible provider (client.go, openai.go)
  skills/       # Agent Skills discovery, prompt catalog, activation, diagnostics
  tools/        # Built-in tools: read, write, edit, patch, bash, activate_skill
  session/      # JSONL session persistence
  config/       # Settings loading (global + project-local, merged onto defaults)
pkg/cli/        # CLI layer — interactive + piped-stdin modes, slash commands
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
- `-strict` only affects piped mode; it returns non-zero for incomplete turns with no assistant response and no successful effectful tool call
- `-check` runs local setup diagnostics; `nncode doctor [-model <name>] [-live] [-timeout <duration>]` is the fuller diagnostics command
- `~/.nncode/config.json` (global) and `./.nncode/config.json` (project-local) overlay built-in defaults; partial configs work
- `~/.nncode/system_prompt.md` and `./.nncode/system_prompt.md` override the default system prompt; project-local wins
- `OPENAI_API_KEY` env var is the only credential source
- Tool names that can be disabled in config: `read`, `write`, `edit`, `patch`, `bash`
- Slash commands in interactive mode: `/help`, `/quit`, `/reset`, `/session`, `/sessions`, `/resume`, `/tools`, `/skills`, `/skill:name`, `/prompt`

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

- The agent loop (`internal/agent/loop.go`) is the core: it calls the LLM, parses tool calls from the stream, executes tools in order, feeds results back as `llm.RoleTool` messages, and repeats until no tool calls remain or `MaxTurns` is hit
- The agent owns the conversation history; the CLI aliases `sess.Messages = agent.Messages()` after each turn and saves once on exit
- Tools are synchronous — they block the loop until they return
- Sessions are JSONL files in `~/.nncode/sessions/`; there's no undo or branching
- The CLI is synchronous and blocking — no async event queue, no TUI
- Config resolution: built-in `defaultConfig()` → `~/.nncode/config.json` → `./.nncode/config.json` → `-model` flag, merged left-to-right (`Config.Merge` does the overlay)
- Skills are discovered at startup and composed into the system prompt before the agent is created; resumed sessions also mark previously activated skills from structured markers in message content

## Boundaries

- Do not add runtime dependencies without asking
- Do not add a TUI — the CLI is intentionally simple (stdin/stdout only)
- Do not add MCP, sub-agents, or plan mode — those are explicitly out of scope
- Do not modify `internal/llm/openai.go` to support non-OpenAI-compatible APIs — use a new provider instead
