# Decision-policy hardening

This supersedes earlier policy descriptions; historical benchmark results remain unchanged.

- `C` means uncertain classification, not explicit endpoint abstention. HTTP-to-agent tests cover both paths.
- Mandatory repair/pending-verification allocation is independent of successful sidecar decoding, confidence, and abstention. Active turns restore deliberate repair compute after tool failure, until verification succeeds. Shadow/off modes do not apply routing changes.
- Fast routing requires both complexity and mutation outputs. Missing signals preserve normal defaults.
- Broad unconditional A escalation was removed: A now requires probability >=0.75. C escalation uses much higher auxiliary thresholds (complexity >=2.5 or mutation >=0.9). These are experimental guardrails, not calibrated thresholds.
- The initial inspection window lasts at most two model steps. The model is instructed to inspect the actual task and relevant code. Evidence is gathered across read rounds, prioritizing TASK.md/prompt.md/spec.md/requirements.md excerpts over other files. Protocol headers and line numbering are stripped. Limits are 1,200 bytes per task excerpt, 256 bytes per other excerpt, and 1,800 bytes total; oversized non-task lines are omitted and truncation/omission remains recorded. Rerouting occurs at most once before step three, only while the workspace remains unmodified. This is a bounded best-effort phase, not a guarantee that the relevant file was read; insufficient/truncated evidence cannot enable fast mode.
- First-passing-workspace locking is now task-specific, enabled by `--lock-after-verification`. It is disabled by default so intermediate passing tests do not freeze ordinary implementations. It does not claim to preserve the globally best artifact or support rollback.

Stabilization review additionally ensures shadow/off produce identical Qwen requests, closes the inspection window after step two even when no early file was read, preserves explicit zero request-scoped token limits on the wire, rejects inconsistent choice probabilities and NaN confidence settings, honors endpoint low-confidence/question-truncation flags, and measures observed latency through response-body completion. Deliberate configuration must use a supported Qwen effort and reserve completion space after thinking.

Validation includes real HTTP decoding into the agent policy, missing-signal rejection, sidecar failure/abstention recovery, inspection bounds, and ordinary-task continuation after verification. No real-model quality or latency improvement is claimed from these tests.

CPU latency remediation: the default deadline is now 2 seconds. Six representative fixture-heavy requests measured 944–997 ms, exceeding the old 750 ms deadline; six compact task-focused requests measured 241–262 ms. These measurements were taken without concurrent Qwen inference and do not establish worst-case loaded latency. See `docs/thx01-evidence-latency.md`.

Before active promotion, evaluate a held-out task set against a deterministic phase-aware baseline under equal budgets and repeated seeded runs. Threshold calibration remains empirical work, not something unit tests can establish.
