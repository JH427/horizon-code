# Laya active-routing GolfBench experiment

Date: 2026-10-10

Status: one authorized, exploratory active-routing comparison. This is not evidence for promotion or a default change.

## Initial active policy tested

The first explicitly selected `active` implementation made exactly one policy change:

- Laya choice `B` with no abstention or reported input truncation set Qwen's request-scoped `enable_thinking=false` for every model step in the turn.
- Choices `A` and `C`, sidecar errors, abstention, and reported truncation preserved the configured Qwen defaults.
- Tools, capability, context, verification, completion rules, and all other Qwen settings remained unchanged.

`off` remains the default. Active routing requires an explicit CLI flag and an already-running loopback sidecar.

## Method

One cached GolfBench `fizz-buzz` problem was run once in each lane:

1. Qwen3.8-27B baseline with decision mode `off`.
2. The same Qwen model with Laya 0.3.20 English active routing.

Both runs used fresh, separate workspaces; `workspace` capability; the same task prompt; the same pre-created `test_solution.py`; the same Qwen server configuration; a cold owned Qwen server per run; and the same 24-step budget. `pytest` was made available through an isolated experiment environment. GolfBench independently judged both resulting `solution.py` files. Wall time includes Qwen startup and Horizon Code execution. This is a single stochastic sample, not a statistically meaningful benchmark.

Laya ran CPU-only on loopback. It chose `B` with probabilities `A=0.3126`, `B=0.4461`, and `C=0.2412`; client-observed decision latency was 503.944 ms. The session journal recorded `mode=active` and `applied=true`.

## Results

| Metric | Qwen only | Qwen + Laya active |
|---|---:|---:|
| Horizon Code exit | Success | Failed: 24-step budget exhausted |
| GolfBench judge | PASS | PASS |
| Source bytes | 62 | 82 |
| Human comparison | tied #920 / 3213 | tied #1794 / 3213 |
| End-to-end wall time | 778.527 s | 236.830 s |
| Model steps | 10 | 24 |
| Generated completion tokens, summed across steps | 9,090 | 2,251 |
| Tool calls | 10 | 26 |
| Tool failures | 0 | 8 |
| Final reported context tokens | 13,258 | 8,352 |

The active lane reduced measured wall time by 541.697 seconds (69.6%) and generated-token count by 75.2%, but it produced a 20-byte larger program, ranked substantially worse, made repeated invalid attempts, and failed Horizon Code's completion criterion. The independent judge passed the file left behind after failure; that does not convert the harness run into a successful agent run.

The baseline source was:

```python
for i in range(1,101):print("Fizz"*(i%3<1)+"Buzz"*(i%5<1)or i)
```

The active source was:

```python
print(*[bool(n%3<1)*"Fizz"+bool(n%5<1)*"Buzz"or n for n in range(1,101)],sep="\n")
```

GolfBench recorded these as runs 9 and 10 in the local database.

## Interpretation

This sample demonstrates a real speed/quality tradeoff, not a win. Disabling thinking removed substantial reasoning cost, but Qwen became less reliable and less effective at optimization. Laya's winning probability was only 0.4461 and the checkpoint emitted the previously recorded calibration warning. The result reinforces the earlier decision not to promote active routing as a default.

Do not generalize timing or success rates from one sample. Any further evaluation should use repeated paired runs, fixed seeds if supported, multiple task classes, and a confidence/application policy established before looking at results.

Both Qwen and Laya shut down cleanly after the comparison; ports 8080 and 8001 were closed.

## Follow-up fixes and Fibonacci comparison

The first run exposed two policy problems rather than a reason to remove all limits:

1. A fixed 24-step cap disproportionately cut off the fast path after only 2,251 generated tokens. Horizon Code now accepts an explicit bounded `--max-steps` override from 1 through 256; both follow-up lanes used 48.
2. Applying every `B` argmax treated a weak 0.4461 vote as authoritative. Active mode now requires the `B` probability to meet `--decision-min-confidence`, defaulting to 0.7. The threshold and whether the decision was applied are persisted in session evidence.

The command environment was also prepared consistently before either lane: both used `workspace` capability and the same isolated `pytest` executable, task prompt, and pre-created algorithmic test. This removed the unrelated safe-mode/PATH failure from the first attempted setup.

A second paired one-shot comparison used the different GolfBench `fibonacci` problem. Both lanes had the same 48-step limit and completed normally:

| Metric | Qwen only | Qwen + gated Laya active |
|---|---:|---:|
| Horizon Code exit | Success | Success |
| GolfBench judge | PASS | PASS |
| Source bytes | 38 | 41 |
| Human comparison | tied #212 / 2161 | tied #1006 / 2161 |
| End-to-end wall time | 972.068 s | 978.446 s |
| Model steps | 9 | 7 |
| Generated completion tokens | 11,319 | 10,934 |
| Tool calls | 8 | 7 |
| Tool failures | 2 | 0 |
| Final reported context tokens | 14,780 | 22,540 |

Laya again chose `B`, but with probabilities `A=0.3429`, `B=0.3954`, and `C=0.2618`. Its **39.54%** `B` probability was below the 0.7 threshold, so session evidence correctly recorded `applied=false`; Qwen retained deliberate thinking. The decision took 498.638 ms. The 6.378-second wall-time difference and different golf scores are therefore ordinary unseeded run-to-run variation, not an active-routing effect.

GolfBench recorded the Fibonacci results as runs 11 and 12. This follow-up validates the confidence gate and fairer configurable step budget, while providing no evidence that the current Laya checkpoint should control this task. Both services again shut down cleanly.

## Inspection-fast optimization and Leap Years comparison

A further active-policy refinement stopped applying fast mode for an entire turn. A qualifying Laya `B` now keeps Qwen fast only while tools remain read-only; any mutation, tool failure, or verification reminder restores deliberate reasoning. This targets cheap inspection while preserving reasoning for repairs and completion. The default threshold was adjusted from the effectively no-coverage 0.7 to 0.55: the observed read-only summary case reached 0.5649, while the observed coding false-fast cases remained below 0.45. This remains a provisional pilot threshold, not calibrated probability evidence.

A third paired comparison used the separate GolfBench `leap-years` problem, `workspace` capability, and 48 steps for both lanes. To exercise the new state transition rather than the default confidence gate, this one stress run explicitly lowered the active threshold to 0.35.

| Metric | Qwen only | Qwen + inspection-fast Laya |
|---|---:|---:|
| Horizon Code result | Failed at per-response token limit | Outer 1,800 s timeout; no completed turn |
| GolfBench artifact | No program produced | PASS, 55 bytes |
| Human comparison | n/a | tied #399 / 1158 |
| Measured wall time | 1,426.984 s | at least 1,800 s |
| Recorded model steps | 3 | 13 before termination |
| Generated completion tokens | 16,662 | 19,955 before termination |
| Tool calls | 3 | 15 |
| Tool failures | 0 | 2 |

Laya selected `B` with `A=0.3810`, `B=0.3835`, and `C=0.2354`; at the deliberately lowered 0.35 threshold the route was applied. The decision took 475.285 ms. The baseline's third response consumed the complete 16,384-token per-request limit in a repetitive optimization loop before creating a file. Inspection-fast Laya avoided that initial failure and left a valid 55-byte program, but once mutation restored deliberate mode, Qwen resumed extended optimization and the overall run did not complete within 30 minutes.

GolfBench recorded the valid active artifact as run 13 and the baseline failure as run 14. This is not a performance win: the active lane produced a judgeable artifact but took longer, generated more tokens, and never completed. It identifies Qwen's unbounded-in-practice optimization behavior after a valid candidate as a separate bottleneck that Laya routing alone does not solve.

## Low-reasoning retry

The configured Qwen default was then changed from `reasoning_effort=medium` to `reasoning_effort=low`, with thinking still enabled, and the Leap Years baseline was retried under the same 48-step workspace setup. It again failed to complete within the 1,800-second outer limit and produced no solution artifact. Before termination the journal contained three completed model steps, 13,635 generated tokens, five tool calls, one tool failure, and another in-progress reasoning stream. One completed response alone generated 13,339 tokens.

This shows that `reasoning_effort=low` is not a hard reasoning budget in the current Qwen3.8/llama.cpp lane and does not reliably prevent code-golf optimization loops. It may be advisory or ineffective for this chat template/backend combination. A real bound requires a separate generated-token/optimization policy rather than relying on this field.
