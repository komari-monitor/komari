# Codex Development Rules for komari

This repository is a fork of `komari-monitor/komari`. The current development goal is to add network test, mesh trace, and topology features without breaking the existing probe baseline functionality.

## Scope and Change Discipline

- Make changes in small, reviewable steps. Do not implement a complete large feature in a single task.
- Prefer the existing architecture, naming, package structure, and JSON-RPC style.
- Do not modify modules unrelated to the current task, including OAuth, basic CPU/memory reporting, heartbeat detection, login authentication, and existing WebSocket reconnect logic.
- Do not create commits automatically. After making changes, stop and wait for manual review.

## Data and API Conventions

- All new JSON fields must use `snake_case`.
- Keep JSON-RPC behavior and naming consistent with existing protocol conventions.

## Go Code Rules

- Run `gofmt` on all modified Go files.
- When executing system commands, do not use shell string concatenation.
- Do not use `sh -c`, `cmd /c`, or equivalent shell wrappers for user-controllable strings.
- Use `exec.CommandContext` with explicit argument arrays for command execution.

## Agent Executor Requirements

Any task involving Agent executors must include:

- Timeout handling.
- Output size limits.
- Target host validation.
- Concurrency limits.

## Master Scheduler Requirements

Any task involving the Master scheduler must include:

- A global concurrency limit.
- A per-Agent concurrency limit.
- Task status storage or an in-memory store abstraction.
- No long-running synchronous blocking of HTTP or RPC requests.

## Testing Baseline

- The local `go test ./...` baseline may fail because of an existing unrelated panic in `web/oauth/factory_test.go`.
- Do not fix that unrelated issue unless explicitly asked.
- For the current stage, only require these commands to pass:
  - `go test ./protocol/v2 ./pkg/rpc`

## Task Completion Report

At the end of every task, report:

- Modified file list.
- Test command.
- Test result.
- Whether any unfinished items remain.
