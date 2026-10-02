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
- Typed tool registry with bounded read/search/edit tools, atomic multi-file patches, path operations, command execution, managed background processes, and optional Git support.
- Exact-match atomic edits and new-file creation confined to the selected workspace.
- `run_command` has no shell and accepts only selected build, test, static-check, and read-only Git commands.
- Workspace-changing tools force a successful verification command before the agent may finish. Every managed background job, including `inspect` jobs, must exit or be stopped **before** that verification starts; a job that exits during the check still requires a fresh check.
- Conversation requests, accepted answers, and tool evidence persist across turns in the same process. Interrupted tool rounds receive explicit unknown-result placeholders so follow-up requests never contain unpaired calls. This is not automatic session restoration from disk.
- Pending verification survives cancellation into the next turn; step, tool-call, repair, and tool-result budgets reset each turn, while the 48K active-context budget also covers retained conversation history.
- Borderless Bubble Tea “flight deck” with a compact activity/telemetry rail, dense semantic transcript, multiline composer, verification notices, cancellation, and local slash commands.
- Live measured Linux resource rail: host RAM used/available/total plus separate system-wide AMD VRAM used/total and GTT allocated/limit counters. GPU allocation is not mislabeled as per-process/model usage.
- Omarchy-native presentation: semantic ANSI palette slots and the terminal's default background, so active Omarchy terminal themes control Horizon Code without app-specific theme files.
- Visible streamed Qwen reasoning plus tool call IDs, JSON arguments, bounded inline result previews, and failures; complete results remain in private session logs.
- Append-only JSONL sessions and deterministic fake-model/HTTP integration tests. Recording failures cancel the active turn, suppress unrecorded events, and reject further turns until restart; errors are never silently discarded.
- Interactive turns inherit application cancellation. Shutdown cancels and joins the runner before closing its journal, tools, and owned inference server.
- Hierarchical `HORIZON.md` workspace instructions, discovered from filesystem root through the selected workspace, source-labeled parent first, capped at 64 KiB, and injected into the system message.
- Exact llama.cpp live/final telemetry: cumulative decode tok/s, prompt speed, generated tokens, active 48K context pressure, and the verified 262K served window. Pre-response client estimates are visibly marked with `~` and never presented as exact.
- Strict single-value tool JSON, validated streamed tool metadata, usage parsing, and fail-closed handling for truncated streams.
- Three explicit capability levels: confined `safe` mode by default, broad structured-exec `workspace` autonomy, and opt-in unrestricted `operator` shell autonomy.
- Deterministic context compaction replaces only older complete assistant/tool rounds, preserves the original request and recent evidence, and never breaks tool-call/result pairing.

The real Qwen3.8 path is validated against the local Q4_K_M + BF16 mmproj lane: streamed reasoning/text, native tool use, edit/diff/test loops, verification gating, and cancellation passed on 2026-08-25. On 2026-08-26, the exact operator release candidate also completed a bounded create/patch/move/shell/background/delete/`run_command` acceptance loop, passed its generated Go test, returned the requested exact token, and stopped its owned inference/process resources. Real inference remains explicit: ordinary Go tests and builds never launch the model.

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

### Slash commands and live resources

Slash commands are handled locally by the TUI and are never sent to the model or treated as tool calls:

```text
/help        command and key reference
/status      model, context, capability, VRAM/GTT, and RAM snapshot
/model       current model, native context, and workspace
/tools       tools registered for the active capability
/capability  active safety boundary and restart guidance
/clear       clear only the visible transcript
/quit        cancel active work and exit cleanly
```

The resource rail refreshes every two seconds. RAM is measured from `/proc/meminfo` as used (`MemTotal - MemAvailable`), available, and total. On AMD GPUs, Horizon Code accepts only exact DRM primary-card names backed by AMD's `amdgpu` driver and reports dedicated VRAM used/total separately from GTT allocated/limit. GTT is mapped host memory; its limit is not an independent physical GPU pool and is never added to VRAM as a fake GPU total. These GPU counters are system-wide allocations, not reliable per-process or model-only attribution. If more than one AMD GPU is eligible, GPU telemetry stays unavailable rather than guessing a device. Missing measurements display as unavailable, and a reported zero remains zero.

### Workspace instructions

Create `HORIZON.md` anywhere from a filesystem parent through the active workspace. Horizon Code loads matching files broadest-first; a more specific file can refine or override a parent. Workspace instructions cannot override the separately injected capability policy or change which tools are registered. Instruction content is bounded to 64 KiB total and is included only in model context and private session evidence—not printed into the transcript.

### Capability modes

Horizon Code never enables unrestricted execution implicitly:

```bash
# Existing confined file tools and verification-only commands.
horizon-code --capability safe

# Adds atomic patches, mkdir/move/delete, broad structured argv execution,
# and managed background processes.
horizon-code --capability workspace

# Adds bash -lc, package managers, network access, and Git writes.
horizon-code --capability operator
```

`safe` remains the default. `workspace` keeps command working directories inside the selected workspace and omits the dedicated shell tool, but it is not a security boundary or operating-system sandbox: invoked executables, interpreters, and wrappers can still access resources allowed to the current user. `operator` adds direct shell syntax and should be selected only when the model is authorized to act with the user's shell permissions. Arbitrary foreground, shell, and background execution conservatively marks verification pending—even when labeled as inspection. Shell and background jobs cannot self-certify: completion requires a later hardened `run_command` or an `exec_command` accepted as a build, test, or static check. Output, execution time, and process groups remain bounded; Horizon Code stops managed background jobs on exit. Long-running watchers/servers cannot remain active through a completed verified turn: poll or stop them, then run a fresh verification. Purpose labels do not make executable jobs read-only.

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
--capability safe|workspace|operator
```

## Toolchain

Go 1.25.8 or newer is required by the pinned Charm v2 dependencies. Go's toolchain selection downloads the required toolchain when necessary. The `search_code` tool also requires `ripgrep` (`rg`) on `PATH`.
