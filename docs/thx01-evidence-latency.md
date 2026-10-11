# THX-01 CPU deadline and evidence selection

## Measurement

Pinned THX-01 revision `bef2e2478be7acd79429324a658558a57552a2e8`, Python 3.12.14, torch 2.10.0+cpu, loopback sidecar. The real Go client issued six requests of each shape, using a 10-second measurement deadline and the production three-question contract. No Qwen inference was running. Inputs were representative synthetic evidence, not an exact replay of the previous benchmark journal.

| Evidence shape | Observed milliseconds, six requests | Reported aggregate input tokens |
|---|---|---:|
| Repeated numeric fixture data | 996.700, 977.360, 947.269, 944.387, 954.652, 949.819 | 3,072 |
| Compact Divisors task + verifier structure | 241.566, 262.171, 247.288, 251.578, 245.072, 241.472 | 794 |

All twelve requests returned valid A decisions. Aggregate input tokens cover all three questions; 3,072 does not mean a 3,072-token per-question window. The data-heavy shape was slower than the former 750 ms default, reproducing the deadline risk, but not proving the exact cause of the historical timeout under concurrent Qwen.

## Changes

- Default `--decision-timeout` is 2 seconds, providing headroom above the largest measured request while retaining a hard bound and normal-lane fallback.
- Evidence is source-labeled and stable-sorted so task/specification excerpts are first even if test code was read earlier.
- Read-file protocol headers and numbered-line prefixes are removed.
- Non-task lines over 240 bytes are omitted rather than allowing an expected-output literal to exhaust the input allowance.
- Task excerpts have a 1,200-byte limit; other excerpts have 256 bytes; combined evidence has 1,800 bytes. Collection is limited to eight excerpts during the existing two-step inspection window.
- Omissions and truncation still prohibit fast routing. The selector does not pretend that discarded code is complete evidence.

Tests cover fixture-first ordering, preserved task prose and verifier checks, removal of protocol overhead, bounded UTF-8-safe selection, and complete short task excerpts.

The new deadline is not a guarantee of success under resource contention. A subsequent paired benchmark is needed to validate task-informed post-inspection routing under concurrent Qwen. Active routing remains opt-in.
