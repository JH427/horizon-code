# horizon-code Development Rules

## Product

horizon-code is a Go coding harness designed specifically for local Qwen3.8-27B inference on Horizon (AMD Strix Halo, llama.cpp/OpenAI-compatible endpoint).

## Principles

- Keep the harness small, legible, and model-focused.
- Optimize for reliable tool use and useful coding outcomes, not provider breadth.
- Borrow architectural lessons from Pi, but do not copy its TypeScript implementation.
- Prefer explicit code over framework-heavy abstractions.
- Preserve user work. Ask before destructive operations.
- Never claim a command, test, or model response succeeded without executing it.

## Go

- Run `go fmt ./...`, `go vet ./...`, and `go test ./...` after code changes.
- Avoid `interface{}`/`any` unless the protocol genuinely requires arbitrary JSON.
- Keep interfaces narrow and define them at the consumer.
- Return errors with useful operation context using `%w`.
- Use `context.Context` for model calls, tools, and subprocesses.
- Pin dependencies in `go.mod`/`go.sum`; justify new dependencies.

## Architecture

- `cmd/horizon-code`: CLI entrypoint only.
- `internal/agent`: agent loop, events, turn state, budgets.
- `internal/model`: Qwen/OpenAI-compatible streaming client and protocol handling.
- `internal/tools`: typed tools, validation, execution, and result limits.
- `internal/tui`: Bubble Tea application, views, styles, and keybindings.
- `internal/session`: durable local session/event storage.
- Do not put business logic in Bubble Tea views or CLI command handlers.
- Keep the model protocol independent from TUI rendering.

## TUI

Use Charm ecosystem libraries where they materially help:

- Bubble Tea for the Elm-style application loop.
- Lip Gloss for adaptive terminal layout and styling.
- Bubbles for robust reusable controls.
- Glamour for Markdown rendering.
- Huh only for structured forms and setup flows.
- Harmonica only when animation improves state comprehension.

The TUI should be polished but information-dense. Animation must not delay coding work.

## Git

- Do not commit unless explicitly asked.
- Stage only files changed for the current task.
- Never use destructive worktree commands (`git reset --hard`, `git clean -fd`, or blanket checkout).
