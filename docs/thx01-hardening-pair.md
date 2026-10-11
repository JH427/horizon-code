# Hardened decision policy: paired Divisors benchmark

## Setup

One unseeded paired `divisors/python` GolfBench run, using Qwen3.8-27B Q4_K_M, workspace capability, 48 model steps, and a 900-second outer deadline. Both lanes explicitly enabled `--lock-after-verification` for this code-golf task. The normal lane used low effort with 2,048 thinking tokens and a 4,096 total completion cap. Active THX used the hardened policy and default 750 ms sidecar timeout.

THX-01 was pinned to `bef2e2478be7acd79429324a658558a57552a2e8`, running on CPU with torch 2.10.0+cpu. Both lanes had identical task/verifier files and a pinned pytest 9.1.1 runner explicitly on PATH. The verifier matched GolfBench output normalization and was preflighted with a known-correct program, removed before inference. Input hashes were unchanged after both runs.

## Results

| Metric | Qwen only | Qwen + THX active |
|---|---:|---:|
| Completed | yes | yes |
| GolfBench | run 23, pass | run 24, pass |
| Wall time including startup/shutdown | 254.056 s | 122.996 s |
| Completed model steps | 5 | 5 |
| Completion tokens | 2,397 | 971 |
| Tool calls / failures | 5 / 0 | 5 / 0 |
| Submission size | 63 bytes | 66 bytes |

Both artifacts independently passed pytest and the GolfBench sandbox judge. Both turns finalized normally after task-specific verification locking.

## Routing evidence

The THX lane initially selected A with P(A)=0.7678, complexity 1.808, mutation probability 0.658, and observed latency 206.8 ms. This exceeded the hardened 0.75 A threshold and selected deliberate reasoning.

At the post-inspection boundary, evidence was flagged as truncated. The sidecar request reached the 750 ms deadline. The journal recorded `phase=post_inspection`, `applied=false`, and `inference_lane=normal`; the engine replaced the initial extended allocation with normal defaults and completed successfully.

## Interpretation

The active-labelled run took 51.6% less wall time and used 59.5% fewer completion tokens, but left a three-byte larger solution. These are observations from one stochastic pair, not demonstrated classifier savings. In particular, post-inspection classification did not succeed: this run tests the timeout fallback, inspection truncation metadata, and successful completion after fallback, rather than validating task-informed routing quality.

Before another paired comparison, investigate CPU latency for evidence-bearing requests and select an empirically justified timeout. The evidence selection budget also truncated this small task's verifier; a useful post-inspection classifier needs compact task-focused excerpts rather than spending its input allowance on expected-output literals.
