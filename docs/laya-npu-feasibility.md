# Laya on the Strix Halo NPU: feasibility report

Date: 2026-10-10

## Executive conclusion

Laya cannot run on this machine's NPU today without system and runtime work. The hardware is present and the upstream Linux kernel driver is installed, but the NPU is not operational: the machine boots with `amd_iommu=off`, `amdxdna` fails probe with `Running without IOMMU not supported`, no driver is bound to PCI device `1022:17f0`, and no `/dev/accel` node exists. XRT/VAIP and a Vitis AI-enabled ONNX Runtime are also absent.

The model is conceptually suitable for NPU evaluation because it is a bounded encoder/classification workload, and AMD publishes a BF16 DistilBERT NPU example. It is not a drop-in deployment. Laya's ModernBERT encoder and custom typed-decision head must be exported at fixed shapes, quantized or converted to BF16/INT8, compiled for the target, checked for unsupported operators and CPU fallbacks, and validated for probability drift. Current public ONNX Runtime Vitis AI documentation lists Ryzen AI NPU support on Windows, not Linux.

Recommendation: do not alter the Horizon Code runtime for NPU support yet. First establish a separately reproducible Linux NPU runtime and prove one fixed-shape Laya forward pass with provider logs showing actual NPU execution.

## What “prefill” means for Laya

Laya is not an autoregressive language model and has no separate prefill/decode phases. Each decision tokenizes the compact state and option descriptions, runs one bidirectional ModernBERT encoder forward pass plus the decision head, and returns probabilities. The encoder pass is analogous to LLM prefill and is the main candidate for NPU offload. A partitioned deployment might place the encoder on NPU and leave unsupported decision-head operations on CPU.

## Local machine evidence

- Hardware: AMD Strix/Krackan/Strix Halo NPU, PCI `c6:00.1`, device `1022:17f0` revision 11.
- Kernel: `7.2.5-3-omarchy` with `CONFIG_DRM_ACCEL=y`, `CONFIG_IOMMU_SUPPORT=y`, and `CONFIG_AMD_IOMMU=y`.
- Driver module: in-tree `amdxdna` is installed and loaded.
- Firmware for `amdnpu/17f0_10` and `amdnpu/17f0_11` is installed.
- Boot command line contains `amd_iommu=off`.
- Kernel log reports:

  ```text
  amdxdna 0000:c6:00.1: [drm] *ERROR* aie2_init: Running without IOMMU not supported
  amdxdna 0000:c6:00.1: [drm] *ERROR* amdxdna_probe: Hardware init failed, ret -22
  amdxdna 0000:c6:00.1: probe with driver amdxdna failed with error -22
  ```

- There is no `/dev/accel/accel0`, and `lspci -k` reports no driver in use for the NPU.
- The current user is not in the `render` group.
- No XRT, Ryzen AI, VAIP, or Vitis AI runtime package is installed.
- The ordinary `onnxruntime` used in the Laya smoke exposed `CPUExecutionProvider` and `AzureExecutionProvider`, not `VitisAIExecutionProvider`.

The first blocker is therefore below Laya and ONNX Runtime: the NPU cannot currently accept work.

## Software support boundary

The upstream `amd/xdna-driver` documentation supports the Linux `amdxdna` driver and XRT shim on Arch Linux, including Arch package recipes for XRT base/NPU, the driver, and the XDNA plugin. That establishes a Linux userspace route to the hardware, but it does not by itself provide a Laya-compatible ONNX Runtime execution provider.

Current ONNX Runtime Vitis AI documentation lists the supported Ryzen AI target OS as Windows. AMD's current Ryzen AI installation page is also Windows-focused. An older AMD “Ryzen AI Software on Linux” guide describes compiling on Linux and copying the result to a Windows Strix system for inference, rather than Linux NPU execution.

AMD's current DistilBERT BF16 example demonstrates that transformer classification can run through `VitisAIExecutionProvider` with a fixed 128-token shape and a VAIML configuration. It is useful architectural evidence, but its setup assumes the Ryzen AI software environment and does not prove that the required provider is available on this Arch Linux host.

## Laya-specific work required

1. Export a fixed-shape model rather than relying on the dynamic ONNX graph used in the CPU smoke. Candidate profiles should match real decision lengths, such as 256 and 512 tokens.
2. Convert or quantize the graph to a supported BF16 or INT8 format. The prior generic ONNX export expanded the checkpoint into roughly 1.6 GB of external FP32 data, which is not the desired NPU artifact.
3. Compile the graph for the exact Strix Halo NPU target and cache the compiled context. Vitis AI documentation warns that first session compilation can take minutes.
4. Inspect provider assignment logs. “Session initialized” is insufficient; logs must show the encoder subgraph actually running on NPU and enumerate CPU-fallback operators.
5. Check ModernBERT operations and Laya's marker gather/scoring and custom decision head. Unsupported dynamic indexing or shape operations may force partitioning or prevent useful offload.
6. Compare every option probability and argmax against the pinned CPU implementation over a held-out corpus. Classification probabilities are the product output, so small numerical drift can change routing near a threshold.
7. Measure end-to-end latency including tokenization, CPU↔NPU transfers, head execution, and cache warmup. The CPU/ONNX sidecar already measured roughly 125–165 ms warm on short states; an NPU path must beat that total, not merely report a faster encoder kernel.

## Safe enablement sequence

System changes are intentionally not performed as part of this report.

1. Enable AMD IOMMU by removing the explicit `amd_iommu=off` override and confirming firmware/BIOS IOMMU support, then reboot.
2. Confirm `amdxdna` binds and `/dev/accel/accel0` appears; fix device permissions through the appropriate distribution-managed group rather than broad device modes.
3. Install pinned, mutually compatible XRT NPU and XDNA plugin builds using the upstream Arch recipes; run their hardware tests first.
4. Identify or build a pinned Linux inference runtime capable of consuming the compiled Laya graph. Do not assume the standard PyPI `onnxruntime` package supports the NPU.
5. Validate a small AMD-published transformer example before attempting Laya.
6. Only then export, compile, validate, and benchmark Laya in an isolated sidecar.

## Sources

Accessed 2026-10-10:

- AMD XDNA Linux driver and Arch/XRT build guidance: <https://github.com/amd/xdna-driver>
- AMD Ryzen AI Software repository and transformer examples: <https://github.com/amd/RyzenAI-SW>
- AMD DistilBERT BF16 NPU example: <https://github.com/amd/RyzenAI-SW/tree/main/Transformer-examples/DistilBERT_text_classification_bf16>
- ONNX Runtime Vitis AI execution provider support table and runtime options: <https://onnxruntime.ai/docs/execution-providers/Vitis-AI-ExecutionProvider.html>
- AMD Ryzen AI installation documentation: <https://ryzenai.docs.amd.com/en/latest/inst.html>
