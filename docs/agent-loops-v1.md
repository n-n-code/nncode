# Agent Loops v1

Agent Loops are strict JSON files that define a linear workflow around the
normal nncode agent. Store them in `./.nncode/loops/<name>.json` for a project
or `~/.nncode/loops/<name>.json` globally. Project-local loops win when both
locations contain the same filename.

## File Shape

Every v1 loop must use `schema_version: 1`. The machine-readable field schema
is [`agent-loops-v1.schema.json`](agent-loops-v1.schema.json); nncode still
performs cross-node validation such as unique ids and required node counts.

```json
{
  "schema_version": 1,
  "name": "implement-review-fix",
  "description": "Implement, review, and refine until done.",
  "settings": {
    "model": "gpt-4o",
    "max_iterations": 5
  },
  "nodes": [
    {
      "id": "entry",
      "type": "entry_prompt",
      "locked": true,
      "content": "Start this task:\n\n{{input}}"
    },
    {
      "id": "test",
      "type": "cmd",
      "settings": { "on_error": "continue" },
      "content": "go test ./..."
    },
    {
      "id": "fix",
      "type": "prompt",
      "content": "Fix the next concrete issue revealed by the command output."
    },
    {
      "id": "done",
      "type": "exit_criteria",
      "locked": true,
      "content": "Exit when implementation and validation are complete."
    },
    {
      "id": "final",
      "type": "exit_prompt",
      "content": "Summarize changes, validation, and remaining gaps."
    }
  ]
}
```

Unknown fields are rejected.

## Execution Model

v1 loops are linear:

1. Run exactly one `entry_prompt` node once.
2. For each iteration, run all body nodes in JSON order. Body nodes are
   `prompt` and `cmd`.
3. Run exactly one `exit_criteria` node.
4. Continue until `exit_criteria` emits `LOOP_EXIT: yes`, or until
   `settings.max_iterations` is exceeded.
5. Run the optional `exit_prompt` node once after exit.

The `exit_criteria` response must include one line exactly `LOOP_EXIT: yes` or
`LOOP_EXIT: no`. nncode strips that marker from terminal output.

## Fields

Top-level fields:

| Field | Required | Description |
| --- | --- | --- |
| `schema_version` | yes | Must be `1`. |
| `name` | yes | Human-readable loop name. |
| `description` | no | Short summary for list and TUI views. |
| `settings.model` | no | Model name for all LLM nodes unless a node overrides it. |
| `settings.max_iterations` | no | Positive iteration cap. Defaults to `10`. |
| `nodes` | yes | Ordered node list. |

Node fields:

| Field | Required | Description |
| --- | --- | --- |
| `id` | yes | Unique node identifier. |
| `type` | yes | One of `entry_prompt`, `prompt`, `cmd`, `exit_criteria`, `exit_prompt`. |
| `locked` | no | If true, nncode restores the node after each loop step if the model edits it. |
| `settings.model` | no | Model override for LLM nodes. Invalid on `cmd` nodes. |
| `settings.on_error` | no | `cmd` only. Use `abort` or `continue`; defaults to `abort`. |
| `content` | type-dependent | Prompt text or command text. Required except an empty `entry_prompt` may use only user input. |

Validation rules:

- Exactly one `entry_prompt` is required.
- At least one body node is required.
- Exactly one `exit_criteria` is required.
- At most one `exit_prompt` is allowed.
- Node ids must be unique and non-empty.
- Node-level `settings.max_iterations` is invalid.
- Top-level `settings.on_error` is invalid.

## Node Types

`entry_prompt` starts the loop. If its content contains `{{input}}`, nncode
replaces every occurrence with the user input. If it does not contain the
placeholder, nncode appends the input under `User input:`.

`prompt` sends its content to the model. It can use `settings.model` to override
the loop or active model.

`cmd` executes its content through `bash -c`. It uses the same workspace root,
timeout, and output truncation settings as the `bash` tool. It is disabled when
the `bash` tool is disabled. Command output, exit code, duration, and truncation
status are recorded as a synthetic loop observation so later nodes can inspect
the result.

`exit_criteria` asks the model whether the loop is done. nncode requires the
`LOOP_EXIT` marker and continues when the marker is missing.

`exit_prompt` runs once after a successful exit and is usually used for final
reporting.

## Authoring Checklist

- Include `schema_version: 1`.
- Keep `id` values short, unique, and stable.
- Put repeatable validation in `cmd` nodes.
- Use `settings.on_error: "continue"` only when a later prompt or exit check
  should interpret a failing command.
- Lock policy or safety-critical nodes.
- Validate with `nncode loop check <name-or-path>` before sharing.
