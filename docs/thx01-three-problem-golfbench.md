# THX-01 three-problem GolfBench comparison

Date: 2026-10-11

## Method

Three Python GolfBench tasks were run as paired isolated workspaces:

- Prime Numbers
- Divisors
- Cubes

Both lanes used Qwen3.8-27B Q4_K_M, workspace capability, temperature 1.0, a 48-step limit, and a 900-second outer timeout. The fixed lane used Horizon Code's normal `low`/2,048-token thinking budget and 4,096-token completion cap. The routed lane used THX-01 active mode; all three requests selected the extended `medium`/4,096-token thinking budget and 8,192-token completion cap.

Each workspace had an isolated pinned `pytest==9.1.1` environment and an executable `pytest` wrapper on that Horizon process's `PATH`. The verifier used GolfBench's output normalization. A successful verification activated Horizon Code's verified-artifact lock and two-step finalization phase. Final artifacts were independently judged by GolfBench.

## Results

| Problem | Lane | Result | Wall time | Steps | Generated tokens | Artifact |
|---|---|---:|---:|---:|---:|---:|
| Prime Numbers | Qwen normal | pass | 534.923 s | 9 | 5,558 | 64 bytes |
| Prime Numbers | Qwen + THX | pass | 744.487 s | 8 | 8,041 | 62 bytes |
| Divisors | Qwen normal | pass | 263.955 s | 6 | 2,488 | 63 bytes |
| Divisors | Qwen + THX | pass | 411.647 s | 5 | 4,161 | 64 bytes |
| Cubes | Qwen normal | pass | 675.195 s | 8 | 7,042 | 319 bytes |
| Cubes | Qwen + THX | timeout, no artifact | 900.284 s | 5 completed | 6,544 completed | none |

GolfBench run IDs are 17–22. Run 22 records the routed Cubes no-artifact timeout as an empty failed submission.

Across the three tasks:

- Fixed Qwen completed 3/3; THX routing completed 2/3.
- Fixed Qwen used 15,088 generated tokens; THX routing used 18,746 completed-response tokens, plus an unfinished response.
- Fixed Qwen took 1,474.073 seconds; THX routing took 2,056.418 seconds, 39.5% longer.
- THX improved Prime Numbers by two bytes, worsened Divisors by one byte, and failed Cubes where fixed Qwen passed.

The verified-artifact lock worked in all five completed runs: immediately after `pytest` passed, the workspace was locked and Qwen finalized without further mutation.

## Routing limitation exposed

All benchmark prompts used the same wrapper text: "Work on the code-golf task in TASK.md..." The actual problem statement was only available through a later file read. Because Horizon Code currently makes one routing decision before Qwen inspects the workspace, THX-01 received effectively identical state for all three tasks. It consequently returned exactly the same decision and probabilities each time:

- choice `A`; `P(A)=.8353`, `P(B)=.0481`, `P(C)=.1166`;
- complexity `1.6036`;
- mutation probability `.7545`;
- selected lane `deliberate`.

The sidecar took 162–203 ms, which is negligible compared with Qwen. The cost came from selecting the larger Qwen reasoning budget, not THX inference latency.

## Conclusion

This sample rejects the current broad active-`A` escalation policy for code-golf tasks: it consumed more time and tokens, did not improve aggregate quality, and reduced completion reliability. THX-01 itself classified the limited state consistently, but the one-shot pre-inspection integration deprived it of the information needed to distinguish the problems.

Keep THX-01 as the default decision checkpoint and keep active routing off by default. The next routing design should make a second decision after bounded read-only inspection, using the inspected task/repository evidence, rather than assigning an extended budget solely from a generic turn-start prompt. That design should be shadow-tested before another expensive active benchmark.

## Remediation implemented

The agent now reclassifies once after the first successful read-only file-read round, before issuing the next model request. It sends the original request plus up to 2 KiB of actual file-read evidence, flags truncated excerpts, and records the decision phase. The new route replaces the turn-start allocation; it does not compound it. No rerouting occurs after mutation or artifact locking. A sidecar failure restores normal defaults; incomplete evidence cannot enable the fast lane. Deterministic tests cover different task contents behind identical wrapper prompts, replacement of the initial route, truncation, shadow behavior, and the two-call bound. No new real-model benchmark has yet established a performance improvement.
