# Task-focused evidence: paired Divisors benchmark

One unseeded paired `divisors/python` comparison after the source-prioritized evidence selector and 2-second sidecar deadline were implemented.

## Setup

Qwen3.8-27B Q4_K_M, workspace capability, 48 model steps, 900-second outer deadline, task-specific `--lock-after-verification`. Fixed Qwen used low effort with 2,048 thinking tokens and 4,096 total completion tokens. THX active used the hardened routing policy and default 2-second sidecar deadline.

THX checkpoint revision: `bef2e2478be7acd79429324a658558a57552a2e8`; CPU torch 2.10.0+cpu. Both workspaces contained identical task and verifier files, pinned pytest 9.1.1 on PATH, and GolfBench-compatible normalization. The verifier was preflighted with a known-correct solution, removed before inference. Task/verifier hashes were unchanged after both runs.

## Results

| Metric | Qwen only | Qwen + THX |
|---|---:|---:|
| Completion | normal | normal |
| GolfBench | run 25, pass | run 26, pass |
| Wall time including startup/shutdown | 176.967 s | 165.781 s |
| Model steps | 5 | 5 |
| Generated tokens | 1,627 | 1,482 |
| Tool calls / failures | 5 / 0 | 5 / 0 |
| Submission size | 63 bytes | 64 bytes |

Both submissions independently passed pytest and GolfBench. They implement the same expression; the THX artifact includes a trailing source newline, accounting for the one-byte difference.

## Decision evidence

- Turn start: A=.7678, B=.0533, C=.1789; complexity 1.808; mutation .658. Observed latency 203.0 ms. A exceeded the .75 threshold, selecting deliberate inspection reasoning.
- Post inspection: A=.5243, B=.0891, C=.3866; complexity 1.8397; mutation .4019. Observed latency 463.7 ms; reported aggregate input tokens 1,334 across the three questions. The request succeeded within the 2-second deadline under concurrent Qwen.
- The post-inspection A probability did not meet the escalation threshold, so the initial deliberate allocation was replaced by normal defaults. This is recorded as `applied=false`, `inference_lane=normal`, `phase=post_inspection`, meaning no request override—not that the initial deliberate allocation persisted.
- Intentional excerpt omissions were flagged `inspection_truncated=true`; incomplete evidence did not enable fast mode.

## Interpretation

Unlike the previous pair, task-informed post-inspection classification completed successfully rather than timing out. The experiment validates the selector/deadline remediation and replacement of the initial route with normal reasoning.

The THX run used 8.9% fewer tokens and took 6.3% less wall time, with a one-byte worse golf score. One unseeded pair cannot establish a reliable speed or coding-quality improvement; repeated controlled trials are needed. It is integration evidence, not a promotion result. Active routing remains opt-in.
