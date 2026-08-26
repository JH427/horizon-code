# horizon-code

A local-first coding harness written in Go and tuned for Qwen3.8-27B on Horizon.

## Direction

- Purpose-built for llama.cpp's OpenAI-compatible API.
- Reliable typed tools and bounded model/tool loops.
- A polished terminal interface built with Charm libraries.
- Small enough to understand, profile, and tune end to end.
- Pi-inspired separation between model protocol, agent runtime, tools, sessions, and UI.

## Planned architecture

```text
cmd/horizon-code/   CLI entrypoint
internal/agent/     agent loop, events, budgets
internal/model/     Qwen streaming client and protocol
internal/tools/     file, search, patch, and shell tools
internal/tui/       Bubble Tea UI and Lip Gloss styles
internal/session/   local event/session persistence
```

## Implemented foundation

- Qwen reasoning, text, and fragmented tool-call streaming over the OpenAI-compatible API.
- Typed tool registry with bounded `list_files`, `read_file`, `search_code`, `edit_file`, `create_file`, `run_command`, and optional `git_diff` tools.
- Exact-match atomic edits and new-file creation confined to the selected workspace.
- `run_command` has no shell and accepts only selected build, test, static-check, and read-only Git commands.
- Workspace-changing tools force a successful non-Git verification command before the agent may finish.
- Pending verification survives cancellation into the next turn; explicit step, tool-call, repair, tool-result, and 48K active-context budgets bound each turn.
- Borderless Bubble Tea “flight deck” with a compact activity/telemetry rail, dense semantic transcript, multiline composer, verification notices, and cancellation.
- Omarchy-native presentation: semantic ANSI palette slots and the terminal's default background, so active Omarchy terminal themes control Horizon Code without app-specific theme files.
- Visible streamed Qwen reasoning plus tool call IDs, JSON arguments, bounded inline result previews, and failures; complete results remain in private session logs.
- Append-only JSONL sessions and deterministic fake-model/HTTP integration tests.
- Hierarchical `HORIZON.md` workspace instructions, discovered from filesystem root through the selected workspace, source-labeled parent first, capped at 64 KiB, and injected into the system message.
- Exact llama.cpp live/final telemetry: cumulative decode tok/s, prompt speed, generated tokens, active 48K context pressure, and the verified 262K served window. Pre-response client estimates are visibly marked with `~` and never presented as exact.
- Strict single-value tool JSON, validated streamed tool metadata, usage parsing, and fail-closed handling for truncated streams.

The real Qwen3.8 path is validated against the local Q4_K_M + BF16 mmproj lane: streamed reasoning/text, native `read_file` tool use, edit/diff/test loops, verification gating, and cancellation all passed on 2026-08-25. Real inference remains explicit: ordinary Go tests and builds never launch the model.

The detailed, source-backed v0.1 contract is in [docs/v0.1-design.md](docs/v0.1-design.md).

## Running

Build without starting inference:

```bash
go build -trimpath -o bin/horizon-code ./cmd/horizon-code
```

Install the command once:

```bash
install -Dm755 bin/horizon-code ~/.local/bin/horizon-code
```

Then launch it from any directory, Git repository or not:

```bash
cd /path/you/want/to/work/in
horizon-code
```

Launching Horizon Code starts `~/.config/horizon-llm/run-server.sh` by default, waits for `/health`, verifies that `/v1/models` contains `qwen38-27b`, and then opens the TUI. Override the launcher with `--server-command` on another setup. The inference process group is owned by that Horizon Code session and is stopped when the program exits. If the configured health endpoint already has a healthy server, Horizon Code refuses to claim or stop it.

Turns, reasoning, tool arguments, and tool results are written as replayable private JSONL files under `~/.local/state/horizon-code/sessions/` (or `$XDG_STATE_HOME/horizon-code/sessions/`). This keeps arbitrary working directories clean.

### Workspace instructions

Create `HORIZON.md` anywhere from a filesystem parent through the active workspace. Horizon Code loads matching files broadest-first; a more specific file can refine or override a parent. Instruction content is bounded to 64 KiB total and is included only in model context and private session evidence—not printed into the transcript.

A bounded headless prompt is available for explicit smoke testing:

```bash
bin/horizon-code --workspace /path/to/repo --prompt 'Read README.md and summarize it.'
```

Both interactive and headless modes start real inference. Do not run either as part of ordinary Go tests or builds.

The validated marker smoke is:

```bash
bin/horizon-code --workspace . \
  --prompt 'Use the read_file tool to read README.md, then summarize the project in one sentence.'
```

Useful overrides:

```text
--server-command PATH
--health-url URL
--base-url URL
--model ID
--workspace PATH
--session-dir PATH
--startup-timeout DURATION
--shutdown-timeout DURATION
```

## Toolchain

Go 1.25.8 or newer is required by the pinned Charm v2 dependencies. Go's toolchain selection downloads the required toolchain when necessary. The `search_code` tool also requires `ripgrep` (`rg`) on `PATH`.
