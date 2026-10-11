# Laya decision-policy smoke and closeout

Date: 2026-10-10

## Reproducibility boundary

- Laya package: PyPI `laya==0.3.20`
- PyPI wheel SHA-256: `6039e802fa5effb8dd492061cd7ad39a43087beadc4a4fa4a649614e77eb83d4`
- Corresponding repository tag: `v0.3.20` (annotated tag object `412531034634775c2c102fac8d160e84203cfae4`, commit `23a17522aa4942da6cce53a995a275760320b691`)
- Checkpoint: `convaiinnovations/laya` revision `7b928d828b7b0e022f929d9bd2e44165aa270148`
- Checkpoint weight SHA-256: `891102d372688fc2a094dac56a384bc537b87c63f21f9f3dac0be2b7cbc8d86c`
- Python: CPython 3.12.14 in an isolated ignored environment
- Runtime: `torch==2.14.1+cpu`, `onnxruntime==1.31.0`, `onnx==1.23.2`, `onnxscript==0.7.2`

The ONNX export used upstream's `scripts/export_onnx.py` from the pinned repository commit. Export took 22.794 seconds and peaked at 2,847,728 KiB RSS. The generated graph and external data hashes were:

- `laya.onnx`: `7fd0dc95d5ef52e6e7deab11aadecf6eb3087000bda39b8da91ab57d9acadf15`
- `laya.onnx.data`: `487746363a8da57bcadb4345352997d22a0fb90d70aa22c6856668d023242aba`

These generated files were experiment artifacts under ignored `.horizon-code/laya-smoke/`, not product dependencies. They were removed after their hashes and measurements were recorded, reclaiming approximately 2.7 GB.

## Sidecar boundary

A minimal wrapper injected one local `ONNXAgent` into Laya's pinned `create_app` server. It bound only to `127.0.0.1:8001`; Horizon Code did not install or start it automatically.

`GET /health` reported:

```json
{"status":"ok","loaded":["english"],"device":"cpu"}
```

ONNX Runtime reported only `CPUExecutionProvider` for the session. Sidecar model load was 2.670 seconds. Resident RSS was 1,864,864 KiB immediately after health and 1,946,728 KiB after requests (about 1.78–1.86 GiB).

One cold HTTP decision took 246.980 ms. Five warm decisions took 141.580, 125.063, 128.749, 130.256, and 164.403 ms (median 130.256 ms). The real Go decision client completed a subsequent request in 124.248 ms and strictly decoded the response. It selected `B` with probabilities `A=0.3005`, `B=0.3911`, and `C=0.3084`.

The endpoint did not emit `X-Inference-Time-Ms`, so endpoint latency remained unavailable while client-observed latency was recorded. It also omitted explicit truncation metadata. An overlength request returned `usage.input_tokens=512`, showing the checkpoint limit had been reached, but did not return `usage.truncated` or dropped-token counts. Consequently Horizon Code cannot infer sidecar truncation from this server response and records `input_truncated=false`; this is a protocol-evidence gap, not proof that input was preserved.

No Qwen/llama.cpp process was running before the smoke, so concurrent Qwen interference could not be measured. No Qwen process was started for this experiment. The sidecar accepted `SIGTERM`, drained its executor, exited normally, and left port 8001 closed.

The runtime warned that the checkpoint's `choice:11+` calibration temperature is below the supported range and was clamped from `0.10058280825614929` to `0.5`. Probabilities from the affected calibration bucket must not be treated as calibrated.

## Bounded shadow policy comparison

Eight task-shaped turn-start states were labelled from the sprint contract. `A` means preserve deliberate reasoning and `B` means fast candidate. The deterministic baseline used `B` only for explicit read-only summary/status requests and `A` otherwise.

| Case | Expected | Laya | Heuristic | Laya latency |
|---|---:|---:|---:|---:|
| Direct read/summarize | B | B | B | 148.287 ms |
| Repository status lookup | B | B | B | 136.338 ms |
| Exact mechanical edit | B | B | A | 126.367 ms |
| Multi-file implementation | A | B | A | 128.734 ms |
| Failing-test diagnosis | A | A | A | 143.803 ms |
| Tool failure and recovery | A | B | A | 140.350 ms |
| Pending-verification continuation | A | B | A | 132.907 ms |
| Ambiguous request | A | C | A | 123.040 ms |

Laya matched 4/8 labels (50.0%); the deterministic heuristic matched 7/8 (87.5%). More importantly, Laya produced false-fast choices for multi-file implementation, tool recovery, and pending verification. Its winning probabilities on those false-fast cases were only 0.4133, 0.3828, and 0.3757, but the open checkpoint's calibration warning and narrow margins leave no credible active threshold from this sample.

This was a decision-only shadow evaluation. It did not alter Qwen settings or tools, and it does not claim Qwen latency or token savings. A matched active Qwen run was not performed because active routing was outside sprint scope and the shadow model had already failed the prerequisite of beating the deterministic heuristic.

## Post-closeout Qwen regression smoke

After the Laya experiment was stopped, the rebuilt binary ran one explicit real-Qwen headless smoke with the default `decision-mode off`. Qwen called `read_file` for `README.md`, received the complete 168-line result, and returned the requested one-sentence project summary. Horizon Code then stopped its owned inference server cleanly; port 8080 was closed and no llama/Qwen process remained. The workspace was not modified by the smoke.

A second explicit end-to-end smoke ran the same task with the pinned Laya 0.3.20 English checkpoint in a loopback-only CPU sidecar and Horizon Code in `decision-mode shadow`. Laya selected `B` with probabilities `A=0.2975`, `B=0.5649`, and `C=0.1377` in 242.771 ms client-observed time. The private session journal recorded the typed event with `mode=shadow`, `applied=false`, checkpoint/model identity, probabilities, latency, and token count before Qwen called `read_file` and completed normally. Both owned processes shut down cleanly afterward: Qwen left port 8080 closed, and the manually managed Laya sidecar left port 8001 closed. The temporary 980 MB sidecar environment was then removed. This validates the shadow integration boundary but does not alter the Laya policy outcome.

## Closeout outcome: stop

The original sprint stopped at shadow mode. The open English checkpoint adds a material ~130 ms warm tax, does not expose reliable truncation evidence through the tested server contract, has a calibration warning, scores below the deterministic rule, and makes consequential false-fast classifications. Keep `off` as the default.

The user later superseded only the no-active-experiment boundary by explicitly authorizing one active GolfBench comparison. That run is documented in `docs/laya-active-golfbench-experiment.md`; its completion failure does not change this stop outcome or justify making active routing automatic.
