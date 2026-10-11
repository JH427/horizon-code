# THX-01 routing and bounded-Qwen experiment

Date: 2026-10-11

## Qwen thinking-control research

A Hermes source review checked Qwen3.8's chat template and llama.cpp. Qwen3.8's `reasoning_effort=low` only inserts an instruction asking the model to keep thinking brief; it is not a token budget. The deployed llama.cpp b9860 (`fdb1db877`) supports a hard reasoning sampler, but this revision reads the request field `thinking_budget_tokens`. Its internal sampler field is named `reasoning_budget_tokens`.

Horizon Code initially sent the internal name. llama.cpp silently ignored it, and a Prime Numbers response consumed the full 8,192-token completion cap. This was an implementation/protocol bug. After changing the wire field to `thinking_budget_tokens`, the same lane produced a 4,192-token response: approximately the configured 4,096-token reasoning budget plus the forced transition and tool call. The hard control is therefore active.

The paired experiment used a 1,024-token normal thinking budget. Because the smaller budget caused repeated repair turns and higher aggregate token use, the post-experiment normal default was set to a less aggressive 2,048-token thinking budget with a 4,096-token total cap. Active decision `A` selects an extended lane with `medium`, 4,096 thinking tokens, and an 8,192-token total cap. Active `B` remains the confidence-gated no-thinking lane; `C` and sidecar failures preserve normal defaults.

## THX-01 boundary

- Repository: `doofz/THX-01`
- Revision: `bef2e2478be7acd79429324a658558a57552a2e8`
- Package version: `thx01==1.0.0`, installed from the pinned repository snapshot
- Model weight SHA-256: `52ce834b2a4d265fbc351338655977023ddb61b0363f5f6a4fd096e6c70154b0`
- Parameters: 322M, based on mmBERT
- Runtime: CPython 3.12.14, `torch==2.10.0+cpu`
- Binding: `127.0.0.1:8001` only, unauthenticated loopback
- Resident RSS after evaluation: 1,862,860 KiB

THX-01's response adds `detected` and `latency_ms` fields to the TypeSafe-compatible contract. The strict Go client now accepts those documented fields, uses body latency when the inference header is absent, and still rejects unknown fields. A new `--decision-checkpoint` flag records the actual selected model rather than always claiming the Laya English checkpoint.

## Decision-only comparison

The same eight labelled turn-start states used for Laya were evaluated on CPU.

| Case | Expected | THX-01 | P(A) | P(B) | P(C) | Observed latency |
|---|---:|---:|---:|---:|---:|---:|
| Direct read/summarize | B | B | .0717 | .8401 | .0882 | 110.391 ms |
| Repository status | B | B | .0302 | .9121 | .0577 | 66.829 ms |
| Exact mechanical edit | B | A | .5460 | .1179 | .3361 | 74.545 ms |
| Multi-file implementation | A | A | .5024 | .1546 | .3430 | 71.207 ms |
| Failing-test diagnosis | A | A | .5105 | .2189 | .2706 | 75.186 ms |
| Tool failure/recovery | A | A | .4593 | .2448 | .2959 | 74.058 ms |
| Pending verification | A | A | .4238 | .2554 | .3208 | 76.134 ms |
| Ambiguous request | A | A | .4231 | .2045 | .3725 | 65.527 ms |

THX-01 matched 7/8 labels, compared with Laya's 4/8 and the deterministic heuristic's 7/8. Its only miss was conservative: it allocated deliberate reasoning to an exact edit. It made none of Laya's consequential false-fast choices. Median observed CPU latency was approximately 74 ms.

## Enhanced typed decision

THX-01 can answer multiple typed questions in one request, unlike the single-choice policy initially used. The client now also requests:

- an ordinal four-level task-complexity score;
- a `noul` probability that fulfilling the turn requires workspace mutation or potentially state-changing execution.

On the same eight cases, easy read/status requests scored 0.77–1.04 complexity and 0.07–0.10 mutation probability. Implementation, diagnosis, recovery, pending verification, and ambiguity scored 1.53–1.69 complexity and/or 0.58–0.82 mutation probability. The exact mechanical edit sat between them at 1.18 and 0.36. With deterministic pending-verification/failure guards and these auxiliary signals, all eight cases map to the intended effective lane: read/status use fast, while the other six use extended deliberate reasoning. Median CPU latency for all three questions was approximately 154 ms.

The active policy uses auxiliary outputs conservatively: fast `B` requires complexity at most 1.5 and mutation probability at most 0.4; an uncertain `C` escalates at complexity 1.5 or mutation probability 0.75. The raw values and selected `normal`, `fast`, or `deliberate` lane are persisted in decision evidence. THX-01 is the default checkpoint when routing is enabled, while decision mode itself remains off unless explicitly selected.

## Paired Prime Numbers GolfBench run

A new `prime-numbers/python` task was used. Both lanes had 48 model steps available and a 900-second outer limit. The fixed baseline used the normal 1,024-token lane. THX-01 active routing chose `A` (`A=.5948`, `B=.0925`, `C=.3126`) in 96.5 ms observed time and applied the extended lane.

| Lane | Completion | Completed steps | Generated tokens | Artifact | GolfBench |
|---|---:|---:|---:|---|---|
| Qwen only, normal budget | 900 s timeout | 15 | 9,662 | 75-byte syntax error | Run 15, fail |
| Qwen + THX-01 active `A` | 900 s timeout | 7 | 6,141 | 62-byte valid program | Run 16, pass; tied #580/1321 |

The benchmark harness had two defects. First, its local verifier incorrectly required byte-for-byte output without a trailing newline, while GolfBench intentionally normalizes trailing blank lines. Second, it instructed the agent to run `python verifier.py`, but Horizon Code's authoritative verification lane intentionally accepts `pytest` rather than arbitrary Python scripts. An inspection-purpose execution could show output, but could not clear pending verification after workspace mutation. Completion was therefore impossible even with a passing candidate. This caused Qwen to keep optimizing a 62-byte candidate that was already valid under the actual judge. The artifact was judged directly with GolfBench after timeout.

Consequently, the timeout and completion results cannot measure routing performance; only the artifact snapshots under equal wall time are informative. The active lane's artifact-quality win is encouraging but not attributable to classification alone: THX-01 selected a larger reasoning allocation, and this is one stochastic pair. It demonstrates the mechanism needed for the original hypothesis—selective escalation can improve the artifact under a fixed wall-clock limit—but does not yet establish a general quality improvement. A future paired run must use a real `pytest` verifier whose comparison matches GolfBench.

## Conclusion

THX-01 is materially better suited to this routing contract than the tested Laya checkpoint. Keep routing opt-in, retain deterministic safety conditions around fast mode, and use THX-01 for further shadow/active experiments.

A later corrected three-problem comparison supersedes the flawed completion comparison in this report; see `docs/thx01-three-problem-golfbench.md`. It found that the current broad active-`A` escalation was slower, used more tokens, and completed fewer tasks.

Post-experiment, Horizon Code added a conservative verified-artifact lock. Once a post-mutation verification succeeds, mutating and executable tools are blocked and Qwen receives at most two 1,024-token, no-thinking finalization steps. This preserves the first known-good workspace rather than permitting an unbounded optimization loop. It intentionally allows no post-verification mutation yet; safe snapshot/restore support would be required before bounded improvement attempts could be permitted.
