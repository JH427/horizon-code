# Laya decision-policy shadow sprint

Status: closed on 2026-10-10 with the **stop** outcome. See `docs/laya-decision-policy-smoke.md`.

Post-closeout addendum: the user later authorized one explicit active-routing GolfBench experiment. It did not reopen or satisfy the promotion gate: the active lane was faster but produced worse code and exhausted the agent step budget. See `docs/laya-active-golfbench-experiment.md`.

## Progress checkpoint — 2026-10-10

Completed:

- [x] Request-scoped Qwen inference options with default-preservation tests.
- [x] Typed Jev-compatible Laya HTTP client with bounded timeout/response size, strict decoding, validation, latency, truncation, and failure tests.
- [x] Agent-owned `off` and `shadow` policy modes; `off` remains the default and shadow evidence is never applied.
- [x] Typed decision events persisted in replayable session JSONL without duplicating the user prompt.
- [x] CLI flags and pre-start validation for decision mode, loopback URL, and timeout.
- [x] Ordinary verification: formatting, unit tests, race tests, vet, build, and diff checks pass without starting either model.

Completed after the initial checkpoint:

- [x] Populate previous-tool name/success in turn-start state when prior evidence is available.
- [x] Pin Laya `0.3.20` / repository tag `v0.3.20` and run the explicit loopback-only CPU/ONNX sidecar smoke against the staged English checkpoint.
- [x] Capture device, cold/warm latency, RAM, truncation/schema limitations, Qwen process state, and clean shutdown evidence.
- [x] Run the bounded representative-task decision-only shadow comparison.
- [x] Score the deterministic read-only heuristic baseline.
- [x] Close with exactly one outcome: **stop**.

The sidecar experiment was explicit and separate from ordinary tests. No Qwen inference was started. Laya scored 4/8 against the policy labels versus 7/8 for the deterministic heuristic and produced three consequential false-fast choices, so a matched active Qwen run was neither justified nor authorized.

## Goal

Add an optional, local Laya decision-policy seam and measure whether a typed decision model can reduce Qwen3.8-27B reasoning cost without reducing coding-task success or weakening Horizon Code's deterministic safety controls.

The sprint ends with evidence and a promotion decision. It does not assume Laya will earn an active role.

## Product hypothesis

Laya can classify a compact turn state as `fast`, `deliberate`, or `uncertain` cheaply enough to guide Qwen's per-request thinking budget. A useful policy must save more Qwen wall time or generated reasoning than the Laya call costs, while preserving task success and verification behavior.

Laya is a performance hint, not a second agent. Qwen remains the coding model. Go code remains the authority for capabilities, tool validation, verification, budgets, context integrity, process cleanup, and completion.

## Sprint scope

The sprint includes:

1. Per-request Qwen inference options in the model request boundary.
2. A narrow decision interface owned by `internal/agent` and an HTTP implementation under `internal/decision`.
3. `off` and `shadow` decision modes; `off` remains the default.
4. A single turn-start `choice` question with neutral option keys for `fast`, `deliberate`, and `uncertain`.
5. Typed session evidence for the decision, probability distribution, checkpoint identity, latency, truncation, errors, and whether the result was applied.
6. Deterministic fake-server and engine tests.
7. One explicit local sidecar smoke and a bounded replay/evaluation over the existing Horizon Code pilot-task shape.
8. A comparison against a simple deterministic heuristic.
9. A written promote, specialize, or stop decision.

## Explicit non-goals

This sprint does not:

- enable active routing by default;
- let Laya elevate capability or authorize shell, network, or destructive work;
- let Laya bypass verification or decide that a task is complete;
- let Laya generate tool arguments, paths, commands, or code;
- add MCP, plugins, a Python dependency, or an embedded inference runtime to the Go binary;
- add tool-profile filtering, model routing, context compaction policy, or a planner/reviewer layer;
- train or fine-tune Laya before shadow evidence demonstrates a useful decision boundary;
- install a persistent service or start heavy local training as part of ordinary tests.

## Available local model asset

The English root checkpoint is staged for the development agent at:

```text
/home/joshua/models/laya/english-7b928d828b7b0e022f929d9bd2e44165aa270148
```

It is pinned to Hugging Face revision `7b928d828b7b0e022f929d9bd2e44165aa270148`. `ACQUISITION.md` and `SHA256SUMS` in that directory record the selected files and structural validation. The `model.safetensors` weight is 842,609,210 bytes with SHA-256 `891102d372688fc2a094dac56a384bc537b87c63f21f9f3dac0be2b7cbc8d86c`, matching the Hub's published LFS hash. Acquisition does not establish runtime or functional readiness.

## Architecture

```text
local Laya sidecar (optional, loopback only)
        |
        | POST /v1/systemone
        v
internal/decision HTTP client
        |
        | Decision{mode, probabilities, latency, metadata}
        v
internal/agent policy seam
        |
        | shadow: record only
        | off: no call
        v
internal/model Request{Messages, Tools, InferenceOptions}
        |
        v
Qwen3.8-27B / llama.cpp
```

The Laya sidecar is external to Horizon Code for this sprint. Horizon Code must not own, install, or silently start it. A missing, slow, malformed, or unhealthy sidecar must never prevent the normal Qwen path.

## Decision contract

The first decision family is deliberately small:

```text
question: inference_mode
type: choice

A: Use normal deliberate reasoning for debugging, ambiguity, unfamiliar code,
   multi-file changes, failures, or consequential actions.
B: Use the fast path only for straightforward inspection, status lookup,
   summarization, or an exact mechanical request.
C: Uncertain; preserve normal deliberate reasoning.
```

Use neutral keys (`A`, `B`, `C`) and descriptions. Do not use Laya's `noul` primitive for the first pilot.

The decision state should be compact structured JSON, not the full transcript. Initial fields:

- user request, subject to a documented byte/token cap;
- capability mode;
- whether this is the first model step;
- pending-verification state;
- workspace dirty state;
- last tool name and success state when present;
- repair count;
- active-context pressure;
- schema version.

## Implementation plan

### 1. Add request-scoped Qwen options

Files:

- Modify `internal/model/client.go`.
- Modify `internal/model/client_test.go`.

Add an `InferenceOptions` value to `model.Request` with optional thinking, reasoning-effort, and maximum-output controls. Preserve current client configuration when a request does not override a field.

Tests must prove:

- an empty override produces today's request payload;
- a request can disable thinking without mutating the client globally;
- later requests return to the configured defaults;
- reasoning effort and token limit overrides serialize correctly.

### 2. Define the decision boundary

Files:

- Create `internal/decision/decision.go`.
- Create `internal/decision/client.go`.
- Create `internal/decision/client_test.go`.

Keep the interface narrow at its consumer. The HTTP client must use a bounded timeout, a fixed response-size limit, strict JSON decoding, and the local Jev-compatible `/v1/systemone` contract. It must surface model/checkpoint identity, answer probabilities, latency headers when available, and validation failures without inventing defaults.

Tests must cover success, abstention/uncertainty, non-2xx responses, malformed JSON, oversized responses, timeout/cancellation, missing answer fields, and unexpected option identifiers.

### 3. Add off and shadow policy modes

Files:

- Create `internal/agent/decision_policy.go`.
- Create `internal/agent/decision_policy_test.go`.
- Modify `internal/agent/engine.go`.
- Modify `internal/agent/engine_test.go`.

Rules:

- `off` performs no decision call.
- `shadow` calls once at turn start, records the result, and sends Qwen exactly the existing inference settings and complete allowed tool set.
- Decision failure, cancellation, or timeout does not fail the turn unless the parent turn itself is cancelled.
- The decider never receives capability it can expand and cannot alter verification state.
- No active mode is implemented in this sprint unless a separate review explicitly expands scope after shadow evidence.

### 4. Record typed decision evidence

Files:

- Modify `internal/agent/engine.go` event definitions.
- Modify `internal/session/store.go`.
- Modify `internal/session/store_test.go`.
- Modify TUI/headless handling only enough to keep the new event non-disruptive.

Record:

- decision schema version;
- mode (`shadow`);
- chosen option and full probability distribution;
- checkpoint/model identity;
- endpoint-reported and client-observed latency;
- input truncation;
- error/abstention state;
- `applied: false`.

The private session journal may contain user-request material already, but the decision event should avoid duplicating the full prompt when a stable turn reference and compact state metadata suffice.

### 5. Add CLI configuration

Files:

- Modify `cmd/horizon-code/main.go`.
- Modify `cmd/horizon-code/main_test.go`.
- Modify `README.md`.

Initial flags:

```text
--decision-mode off|shadow
--decision-url http://127.0.0.1:8001
--decision-timeout 750ms
```

Requirements:

- default mode is `off`;
- invalid modes and URLs fail before inference startup;
- decision configuration never changes the Qwen server ownership rules;
- `/status` may show decision mode and last shadow result, but the sprint does not require a new dashboard.

### 6. Verify the local sidecar boundary

Use a pinned Laya package/repository revision. Bind explicitly to `127.0.0.1`; never rely on Laya's `0.0.0.0` default. Start with one preloaded checkpoint and a bounded CPU/ONNX trial so the decision model does not silently compete with Qwen for GPU residency.

Record:

- exact package and checkpoint revision;
- model files and hashes when downloaded;
- actual compute device reported by `/health`;
- cold-start and warm decision latency;
- resident RAM and any observed Qwen interference;
- input truncation and response schema;
- shutdown and cleanup state.

This is an explicit local experiment, not part of normal `go test` or build commands.

### 7. Run the shadow comparison

Use representative Horizon Code tasks covering:

- direct read/summarize;
- repository status or lookup;
- exact mechanical edit;
- multi-file implementation;
- failing-test diagnosis;
- tool failure and recovery;
- pending-verification continuation;
- ambiguous request requiring inspection.

Compare Laya against:

1. the current always-deliberate baseline;
2. a simple deterministic heuristic such as “explicit read-only request → fast candidate; otherwise deliberate.”

Measure:

- decision cold/warm latency;
- Laya coverage, selective accuracy, and truncation;
- Qwen TTFT, prompt time, generated reasoning/output tokens, and wall time;
- tool calls, repairs, and verification behavior;
- final task success;
- false-fast classifications and whether they would have been recoverable.

Shadow classifications do not count as a performance win until matched active tests are separately authorized and run.

## Promotion gate

At sprint review, choose exactly one outcome:

### Promote to a later active-mode sprint

Only if Laya identifies a useful high-confidence subset, beats the simple heuristic, stays within an acceptable warm-latency tax, and shows a credible measured Qwen saving with no safety-boundary changes.

### Specialize and recalibrate

Choose this only if the decision boundary appears valuable but the open checkpoint is insufficient. Train on compact Horizon Code decision states, keep genuinely held-out calibration/evaluation data, and continue to treat confidence as task-local evidence.

### Stop

Stop if Laya does not beat the simple rule, frequently truncates decisive state, imposes more latency than it could save, interferes with Qwen residency/performance, or creates operational complexity without a measurable prize.

## Verification commands

Ordinary code changes must pass without starting either model:

```bash
go fmt ./...
go test -count=1 ./...
go test -race -count=1 ./...
go vet ./...
go build -trimpath -o bin/horizon-code ./cmd/horizon-code
git diff --check
```

The explicit model smoke must be a separate, manually invoked command with captured artifacts. Restore the normal Qwen lane and stop the Laya sidecar before finishing.

## Sprint acceptance criteria

- `off` is the default and preserves existing request behavior.
- `shadow` cannot change Qwen options, tools, capabilities, verification, or completion.
- All decision-client failure paths fall back to the existing Qwen path.
- Session logs contain enough typed evidence to replay and score the shadow decision.
- Ordinary tests require no model, network, Python, or GPU.
- A pinned local sidecar smoke records real cold/warm behavior and cleanup.
- The baseline, heuristic, and Laya shadow results are compared honestly.
- The sprint closes with a promote, specialize, or stop decision rather than silently enabling routing.

## References

- Smoke evidence and closeout: `docs/laya-decision-policy-smoke.md`
- Post-closeout active experiment: `docs/laya-active-golfbench-experiment.md`
- Visual explainer: `docs/laya-policy-explainer.html`
- Existing product contract: `docs/v0.1-design.md`
- Laya model family: https://huggingface.co/convaiinnovations/laya
- Laya repository: https://github.com/NandhaKishorM/laya
