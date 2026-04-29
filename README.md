# nncode

<img width="2172" height="724" alt="nncode" src="https://github.com/user-attachments/assets/cf33cae9-2335-4555-90b9-7e895cd48682" />

A minimal coding agent built in Go. Completely under your control — from system prompts to models used.

Stripped down to essentials: an agent loop, a few tools, and a CLI. No sub-agents, no MCP, no permission popups. Just prompt → LLM → tools → repeat.

## Why nncode?

- **Deploy anywhere.** Single static binary, zero runtime dependencies. Drop it into a CI image, a container, or an air-gapped box and it just works.
- **Audit everything.** Every turn is a JSONL session on disk. Every tool call is recorded. You can diff sessions, grep them, or replay them.
- **Small surface, small risk.** The tool set is tiny and every effectful action is confirmable. The codebase is small enough to read in an afternoon.
- **Not chasing features.** No git integration, no web search, no MCP. We ship deployability and auditability instead.

## Features

- **LLM providers**: OpenAI Chat Completions + OpenAI-compatible local servers (Ollama, LM Studio, vLLM)
- **Agent loop**: Streaming response → tool call detection → tool execution → loop until done
- **Built-in tools**: `read`, `write`, `edit`, `patch`, `bash`, `grep`, `find`
- **Parallel reads**: Non-effectful tools run concurrently; effectful tools stay sequential
- **Auto-context**: Detects `go.mod`, `package.json`, `Cargo.toml`, and other project files on startup and injects a compact summary into the system prompt
- **Agent Skills**: progressive local skill discovery from `.agents/skills`
- **Agent Loops**: reusable prompt loops from `.nncode/loops` or `~/.nncode/loops`
- **Sessions**: JSONL persistence to `~/.nncode/sessions/`
- **Config**: Global and project-local settings via JSON
- **Events**: Real-time streaming of text, tool calls, and errors

## Building

Requires Go 1.26 or newer (see `go.mod`). No runtime dependencies beyond the standard library.

```bash
git clone <repo-url> nncode
cd nncode
go build -o nncode ./cmd/nncode/   # produces ./nncode in the repo root
./nncode
```

To install into `$GOBIN` (typically `~/go/bin`):

```bash
go install ./cmd/nncode/
```

### Development

```bash
go vet ./...                   # static analysis
golangci-lint fmt ./...        # format imports and code
golangci-lint run ./...        # strict lint (default: all linters)
go test ./...                  # run tests
go build -o nncode ./cmd/nncode/
```

Tests use `github.com/stretchr/testify` (test-only).

Live LLM smoke tests are opt-in:

```bash
NNCODE_LIVE_BASE_URL=http://127.0.0.1:8033/v1 NNCODE_LIVE_MODEL=<served-model-id> go test ./internal/llm -run Live
```

## Usage

### Interactive mode

```bash
nncode                    # use the configured default_model
nncode -model llama3      # override the model for this invocation
nncode -resume 123456789  # resume a saved session before chatting
nncode -dry-run           # preview effectful tools without executing them
> list the files in the current directory
> read main.go and summarize it
> /loop implement-review-fix add the requested feature
```

### Non-interactive (print and exit)

```bash
echo "summarize this codebase" | nncode
echo "…" | nncode -model gpt-4o-mini
echo "make the requested edit" | nncode -strict
echo "add the requested feature" | nncode -loop implement-review-fix -strict
nncode -loop-check implement-review-fix
nncode loop list
echo "add the requested feature" | nncode loop run implement-review-fix -strict
nncode loop check implement-review-fix
```

In piped mode, nncode warns if the agent exits without assistant text and without a successful effectful tool call (`write`, `edit`, `patch`, or `bash`). Add `-strict` to turn that incomplete-turn warning into a non-zero exit for automation.

### Diagnostics

```bash
nncode doctor             # validate config, tools, sessions, and credentials
nncode doctor -model llama3
nncode doctor -model llama3 -live
nncode doctor -timeout 30s
nncode -check             # shorthand for non-live diagnostics
```

`doctor -live` sends a tiny prompt to the selected model and also tries live context-window metadata. Without `-live`, diagnostics stay local and offline. Doctor also checks Agent Skills and Agent Loops discovery and reports those issues as warnings, not startup failures.

### Built-in commands

| Command | Description |
|---------|-------------|
| `/help` | Show available commands |
| `/quit` | Exit the agent |
| `/exit` | Alias for `/quit` |
| `/reset` | Reset the current session |
| `/context -print` | Print the stored context: startup system prompt plus saved conversation messages; loop-scoped system messages are not stored |
| `/context -reset` | Reset stored conversation context and activated skill state |
| `/session` | Show current session info |
| `/sessions` | List saved sessions |
| `/resume <id\|path>` | Load a saved session into the current conversation |
| `/tools` | List available tools |
| `/skills` | List discovered Agent Skills and diagnostics |
| `/skill:name [message]` | Activate a skill manually, optionally then run a message |
| `/loops` | Browse configured Agent Loops |
| `/loop <name\|path> [message]` | Run an Agent Loop, optionally with a message |
| `/loop-validate <name\|path>` | Validate an Agent Loop file |
| `/prompt` | Show the active system prompt |

## Configuration

The config file is optional — a small set of built-in defaults is used (see `internal/config/config.go`). To override, create:

- **Global**: `~/.nncode/config.json` — overlays the built-in defaults
- **Project-local**: `./.nncode/config.json` — overlays the global file

Overlay semantics: a non-empty `default_model` replaces the one below it, model entries are merged per key, and non-zero tool limits override the defaults. Partial configs work — `{"default_model": "llama3"}` is enough to switch to the built-in llama3 entry. Invalid configs fail fast at startup.

### Picking a model

Resolution order, highest wins:

1. `-model <name>` flag
2. `default_model` from project-local config
3. `default_model` from global config
4. Built-in default (`gpt-4o`)

The named model must have an entry in the `models` map (a local-server entry needs a `base_url`). Unknown model names exit with an error instead of silently falling back to a different model.

```json
{
  "default_model": "gpt-4o",
  "models": {
    "gpt-4o": { "api_type": "openai-completions", "provider": "openai" },
    "gpt-4o-mini": { "api_type": "openai-completions", "provider": "openai" },
    "o3": { "api_type": "openai-completions", "provider": "openai" },
    "llama3": { "api_type": "openai-completions", "provider": "ollama", "base_url": "http://127.0.0.1:8033/v1", "context_window": 128000 }
  },
  "tools": {
    "disabled": [],
    "workspace_root": "",
    "max_read_bytes": 50000,
    "max_write_bytes": 1000000,
    "max_bash_output_bytes": 10000,
    "bash_timeout_seconds": 120
  }
}
```

The config map key is the name you pass to `-model`. If the server uses a different model identifier, set `"id"`:

```json
{
  "default_model": "local",
  "models": {
    "local": {
      "id": "<served-model-id>",
      "api_type": "openai-completions",
      "provider": "llamacpp",
      "base_url": "http://127.0.0.1:8033/v1",
      "max_tokens": 4096,
      "context_window": 128000,
      "context_probe": "auto"
    }
  }
}
```

`context_window` is optional. It is used as a manual fallback for the `CTX used/free` display when live provider metadata does not expose a context size. `context_probe` controls live metadata probing: `"auto"` (default) probes loopback URLs and `"llamacpp"` / `"llama.cpp"` providers, `"llamacpp"` forces llama.cpp probing for that model, and `"off"` disables live probing. When probing is enabled, nncode first tries llama.cpp metadata from `<server-root>/props?autoload=false`, then llama.cpp's nonstandard `/v1/models` `meta.n_ctx_train`. The TUI starts with any configured `context_window` and refreshes live metadata in the background; non-interactive runs resolve it before printing the final `CTX` summary. Standard OpenAI model objects do not expose context size, so cloud models show unknown unless `context_window` is configured.

If you pass `-model <unknown-name>` and exactly one configured model has a non-empty `base_url`, nncode automatically clones that entry for the unknown name. This makes it convenient to use arbitrary model names with local endpoints without pre-declaring every name in config.

`tools.disabled` can contain any of `read`, `write`, `edit`, `patch`, `bash`, `grep`, or `find`. If `tools.workspace_root` is set, file tools are limited to that directory and `bash` runs from that directory. This is a simple guardrail, not a security sandbox.

### Environment variables

| Variable | Description |
|----------|-------------|
| `OPENAI_API_KEY` | API key for OpenAI cloud models |
| `NNCODE_LIVE_API_KEY` | Optional API key for live LLM smoke tests |

### System prompt

Write to `~/.nncode/system_prompt.md` for a global override, or `./.nncode/system_prompt.md` for a project-local one. Project-local wins. If neither file exists, a minimal default is used.

If model-visible Agent Skills are discovered, nncode appends a compact generated catalog to this base prompt. `/prompt` shows the full composed prompt. The generated catalog is capped at 64 skills or about 12 KB, whichever comes first; the same capped subset is exposed in the `activate_skill` tool enum. If it is truncated, `/skills` and `doctor` show a diagnostic.

### Agent Loops

Agent Loops are reusable prompt loops stored as strict JSON files in:

- **Project-local**: `./.nncode/loops/<name>.json`
- **Global**: `~/.nncode/loops/<name>.json`

Project-local loops win over global loops with the same filename. Use `/loops` to browse runnable refs in the TUI, `/loop <name> [message]` in interactive mode, `nncode -loop <name>` or `nncode loop run <name>` with piped stdin, and `nncode -loop-check <name>` or `nncode loop check <name>` to validate a loop without running a model request. `nncode loop list` prints the same configured loop list from scripts.

An example loop is checked in at `examples/loops/implement-review-fix.json`. To try it, copy it into `./.nncode/loops/` or pass the file path directly to `/loop`, `-loop`, or `nncode loop run`.

V1 loops are linear and versioned with `schema_version: 1`: nncode runs one `entry_prompt`, repeats all body nodes (`prompt` or `cmd`) followed by one `exit_criteria`, then runs an optional `exit_prompt` once when the criteria passes. Exit criteria prompts must produce a `LOOP_EXIT: yes` or `LOOP_EXIT: no` line; the marker is stripped from terminal output. See the full authoring reference in [`docs/agent-loops-v1.md`](docs/agent-loops-v1.md) and the machine-readable schema in [`docs/agent-loops-v1.schema.json`](docs/agent-loops-v1.schema.json).

Example:

```json
{
  "schema_version": 1,
  "name": "implement-review-fix",
  "description": "Implement, review, and refine until done.",
  "settings": {
    "model": "gpt-4o",
    "max_iterations": 10
  },
  "nodes": [
    {
      "id": "entry",
      "type": "entry_prompt",
      "locked": true,
      "content": "Start this task:\n\n{{input}}"
    },
    {
      "id": "implement",
      "type": "prompt",
      "locked": false,
      "settings": { "model": "gpt-4o-mini" },
      "content": "Implement the next useful slice."
    },
    {
      "id": "test",
      "type": "cmd",
      "settings": { "on_error": "continue" },
      "content": "go test ./..."
    },
    {
      "id": "done",
      "type": "exit_criteria",
      "locked": true,
      "content": "Exit when the requested behavior is implemented and relevant validation passes."
    },
    {
      "id": "final",
      "type": "exit_prompt",
      "content": "Summarize what changed and what was verified."
    }
  ]
}
```

Node types are `entry_prompt`, `prompt`, `cmd`, `exit_criteria`, and `exit_prompt`. Node-level `settings.model` overrides loop-level `settings.model`, which overrides the active model. `cmd` nodes execute `content` through `bash -c`, use the same workspace root, timeout, and output limits as the `bash` tool, and default to aborting the loop on failure unless `settings.on_error` is `"continue"`. Loop JSON is strict: unknown fields are rejected instead of ignored. Unlocked loop files are ordinary files the agent may edit during a run; locked nodes are restored after each loop step if their content or settings change. Loop-specific system instructions are scoped to the loop run and are not saved into session history.

### Agent Skills

nncode supports local Agent Skills using progressive disclosure:

1. At startup it scans `.agents/skills` directories from the current working directory up to the git root. Closer directories win on name collisions.
2. It then scans `~/.agents/skills` at lower precedence.
3. It reads only each skill's `SKILL.md` frontmatter for `name`, `description`, and optional `disable-model-invocation`.
4. The system prompt gets only the visible skill names and descriptions.
5. The model can call `activate_skill` to load the full `SKILL.md` body and a capped resource listing.

Each skill lives in its own directory:

```text
.agents/skills/<directory>/SKILL.md
```

Example:

```markdown
---
name: coding-guidance-go
description: Go implementation and review guidance.
disable-model-invocation: false
---

# Go Coding Guidance

Use this when writing or reviewing Go code.
```

`name` and `description` are required. Invalid or incomplete skills are skipped and shown under `/skills` diagnostics. Unknown frontmatter fields and unsupported nested values are warned about and ignored when possible. `disable-model-invocation: true` hides a skill from the generated prompt and `activate_skill` enum, but `/skill:name` can still activate it explicitly.

Skill resource files are not loaded during discovery. When a skill is activated, nncode persists a structured activation marker, the body-only `SKILL.md` content inside `<skill_content>`, the skill directory, and up to 20 resource file paths. The marker is hidden from terminal previews but kept in session history so resumed sessions avoid reloading the same skill.

### Sessions

Sessions are saved as JSONL files in `~/.nncode/sessions/`. Use `/sessions` to list saved sessions, `/resume <id|path>` to continue one interactively, or `nncode -resume <id|path>` at startup.

### Local models

For a local OpenAI-compatible server:

1. Start the server on `http://127.0.0.1:8033/v1`, or set your own `base_url` in config
2. Make sure the configured model name is served by that endpoint
3. Run `nncode doctor -model <configured-name> -live`

For Ollama's default OpenAI-compatible endpoint, set `base_url` to `http://127.0.0.1:11434/v1`.

## Project Structure

```
nncode/
├── cmd/nncode/main.go        # Entry point
├── internal/
│   ├── agent/                # Agent loop & state
│   │   ├── agent.go          # Agent struct, prompt(), continue()
│   │   ├── loop.go           # Core agentLoop
│   │   ├── event.go          # Event types
│   │   └── tool.go           # Tool interface
│   ├── agentloop/            # User-defined Agent Loop loading and orchestration
│   ├── llm/                  # LLM abstraction
│   │   ├── client.go         # Client interface, StreamEvent
│   │   └── openai.go         # OpenAI provider
│   ├── doctor/               # Setup diagnostics
│   ├── projectctx/           # Project auto-context detection
│   ├── skills/               # Agent Skills discovery and activation
│   ├── tools/                # Built-in tools
│   │   ├── read.go
│   │   ├── write.go
│   │   ├── edit.go
│   │   ├── patch.go
│   │   ├── bash.go
│   │   ├── grep.go
│   │   ├── find.go
│   │   ├── activate_skill.go
│   │   └── options.go
│   ├── session/              # JSONL persistence
│   └── config/               # Settings loading
├── pkg/cli/                  # CLI layer
└── pkg/tui/                  # Bubble Tea TUI
```

## License

MIT
