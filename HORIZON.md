# Horizon Code Workspace Instructions

- Inspect the workspace with tools before answering; do not guess file contents or command results.
- Use Qwen3.8 native tool calls with exact tool names and valid single-object JSON arguments. After a tool result or error, continue from the returned evidence and repair malformed calls rather than narrating them.
- Keep edits narrow, preserve existing work, and never escape the selected workspace.
- After any change, inspect `git_diff` when available and run the smallest relevant non-Git build, test, or static-check command. Repair failures and verify again.
- Do not claim completion until the latest change has successful verification evidence. Ordinary tests and builds must never start real inference.
- Keep the Go harness small and explicit; agent policy belongs in `internal/agent`, protocol handling in `internal/model`, and tool validation/execution in `internal/tools`.
