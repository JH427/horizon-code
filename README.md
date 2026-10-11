# horizon-code

A local-first coding harness written in Go and tuned for Qwen3.8-27B on Horizon.

[One-page technical explainer](docs/horizon-code-technical-explainer.html) — architecture, coding loop, THX-01 routing, safety boundaries, and benchmark evidence.

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
- Task-specific `--lock-after-verification` optionally freezes the first passing workspace and permits two short finalization steps. It is disabled by default so intermediate tests do not terminate ordinary multi-stage coding.

The real Qwen3.8 path is validated against the local Q4_K_M + BF16 mmproj lane: streamed reasoning/text, native tool use, edit/diff/test loops, verification gating, and cancellation passed on 2026-08-25. On 2026-08-26, the exact operator release candidate also completed a bounded create/patch/move/shell/background/delete/`run_command` acceptance loop, passed its generated Go test, returned the requested exact token, and stopped its owned inference/process resources. Real inference remains explicit: ordinary Go tests and builds never launch the model.

The detailed, source-backed v0.1 contract is in [docs/v0.1-design.md](docs/v0.1-design.md).

## Decision-policy pilots

The decision-policy sprint tests whether a local typed model can allocate Qwen's reasoning budget. `off` remains the default; shadow mode records decisions without changing Qwen, and sidecar timeout, malformed output, or unavailability falls back to normal inference.

The pinned Laya CPU/ONNX evaluation closed with **stop**: it matched 4/8 policy labels and made consequential false-fast choices. THX-01 subsequently matched 7/8 with no false-fast choices and earned further opt-in evaluation. THX-01 is now the default checkpoint whenever decision mode is enabled; routing itself remains off by default because the external sidecar is not installed or owned by Horizon Code. An initial Prime Numbers artifact snapshot favored THX-01 but used a flawed verifier. A corrected three-problem comparison did not support the current broad escalation policy: fixed Qwen completed 3/3, while THX-routed Qwen completed 2/3, took 39.5% longer, and used more tokens. Identical generic wrapper prompts also exposed that the one-shot pre-inspection router lacks enough task evidence. Active mode remains experimental and is never selected implicitly.

- Sprint contract: [docs/laya-decision-policy-sprint.md](docs/laya-decision-policy-sprint.md)
- Smoke evidence and closeout: [docs/laya-decision-policy-smoke.md](docs/laya-decision-policy-smoke.md)
- Authorized active GolfBench comparison: [docs/laya-active-golfbench-experiment.md](docs/laya-active-golfbench-experiment.md)
- THX-01 and bounded-Qwen experiment: [docs/thx01-routing-experiment.md](docs/thx01-routing-experiment.md)
- Corrected three-problem comparison: [docs/thx01-three-problem-golfbench.md](docs/thx01-three-problem-golfbench.md)
- Current hardening policy: [docs/decision-policy-hardening.md](docs/decision-policy-hardening.md)
- Strix Halo NPU feasibility report: [docs/laya-npu-feasibility.md](docs/laya-npu-feasibility.md)
- One-page visual explainer: [docs/laya-policy-explainer.html](docs/laya-policy-explainer.html)

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

Launching Horizon Code starts `~/.config/horizon-llm/run-server.sh` by default, waits for `/health`, verifies that `/v1/models` contains `qwen38-27b`, and then opens the TUI. The current normal Qwen lane keeps thinking enabled with `reasoning_effort=low`, a 2,048-token hard thinking budget, and a 4,096-token total completion cap. `reasoning_effort` itself is only advisory; the hard thinking limit is sent through llama.cpp's `thinking_budget_tokens` extension. Override the launcher with `--server-command` on another setup. The inference process group is owned by that Horizon Code session and is stopped when the program exits. If the configured health endpoint already has a healthy server, Horizon Code refuses to claim or stop it.

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

### Decision-policy shadow mode

Decision routing is disabled by default. Shadow mode records a turn-start decision and at most one post-inspection decision without applying either:

```bash
horizon-code --decision-mode shadow \
  --decision-url http://127.0.0.1:8001 \
  --decision-timeout 2s
```

The sidecar must already be running on a loopback HTTP address; Horizon Code never installs, starts, or owns it. Shadow results are written to the private session journal but cannot alter Qwen inference options, tools, capabilities, verification, or completion. An unavailable, slow, or malformed sidecar records an error and falls back to the existing Qwen path.

THX-01 answers three typed questions: inference lane, task complexity, and mutation likelihood. The router gathers file evidence during up to two initial inspection steps, then makes at most one additional decision before step three, provided no mutation occurred. Task/specification excerpts are prioritized over code/test excerpts. Evidence is capped at 1,200 bytes per task excerpt, 256 bytes per other excerpt, and 1,800 bytes total. Read-file formatting is stripped and oversized code/data lines are omitted with explicit incomplete-evidence marking. The default CPU sidecar deadline is 2 seconds; `--decision-timeout` remains configurable. The post-inspection route replaces the initial route.

Active routing requires `P(A)>=0.75` to extend reasoning. Uncertain `C` can escalate at complexity >=2.5 or mutation likelihood >=0.9; explicit endpoint abstention remains distinct and preserves defaults. Fast `B` requires both auxiliary outputs, complexity <=1.5, mutation likelihood <=0.4, sufficient B probability, and untruncated evidence. Tool failures and pending-verification recovery select deliberate reasoning independently of classifier availability. These thresholds are conservative experimental settings, not validated calibration. Shadow mode never changes reasoning; routing remains opt-in.

```bash
horizon-code --decision-mode active \
  --decision-url http://127.0.0.1:8001 \
  --decision-checkpoint doofz/THX-01 \
  --decision-timeout 2s \
  --decision-min-confidence 0.55
```

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
--decision-mode off|shadow|active
--decision-url http://127.0.0.1:8001
--decision-checkpoint ID
--decision-timeout DURATION
--decision-min-confidence (0.0,1.0]
--max-steps COUNT  # 1-256; omit or use 0 for the capability default
--lock-after-verification  # optional task-specific first-passing-artifact finalization
```

## Toolchain

Go 1.25.8 or newer is required by the pinned Charm v2 dependencies. Go's toolchain selection downloads the required toolchain when necessary. The `search_code` tool also requires `ripgrep` (`rg`) on `PATH`.
