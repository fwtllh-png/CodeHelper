# Reading the CodeHelper Source Code

[简体中文](../zh-CN/reading-guide.md) | English

CodeHelper is a large Go codebase. This guide gives you a progressive reading
route so you can go from "where do I start" to "I understand one full turn of
work end to end" without trying to read 1800+ files in order.

Read [architecture.md](./architecture.md) first. This guide assumes you already
grasp the layered model and the hard dependency rules; it answers the practical
question of which file to open next.

> Tip: pair each package below with its `*_test.go`. CodeHelper's tests are
> excellent documentation: they pin down contracts that the docs only describe.

## The Layers at a Glance

```text
cmd/codehelper            process entry
internal/host             CLI, TUI, ACP presentation
internal/runtime          protocol, app state, agent loop, wiring
internal/adapter          providers, models, tools, MCP, skills, plugins
internal/security         policy, permission, constitution, sandbox
internal/orchestration    tasks, workers, workflows, lanes, fleet, subagents
internal/persist          SQLite, events, sessions, journals
internal/observability    usage, traces, verification, diagnostics
internal/platform         processes, PTY, OS integration
internal/config           defaults, TOML, env, validation
extensions/vscode         TypeScript editor extension
```

## Route 1 — Entry and Composition (smallest surface)

Start with how the process boots and how everything gets wired together.

1. `cmd/codehelper/main.go` — just signal handling and one call into the CLI.
2. `internal/host/cli/host.go`, `internal/host/cli/cobra.go` — the command
   tree. `exec.go` is the one-shot command that is the best end-to-end example.
3. `internal/runtime/app/wire/` — the composition root. Read
   `modules_core.go` then `modules_runtime.go`. The module sequence is
   documented in `architecture.md`; these files are the code behind it.
4. `internal/runtime/app/wire/runtime.go` and `background_executors.go` — how
   the facade and the background services (MCP refresh, terminal outbox,
   pending-turn recovery, worker scheduler) come to life.

This route answers: "how does a binary become a running runtime?"

## Route 2 — One Operation End to End (core loop)

This is the heart of the system. Follow one user request (an Operation)
through the runtime.

1. `internal/runtime/app/operation_dispatch.go` — maps typed Operations to
   handlers.
2. `internal/runtime/app/active_turn_registry.go` — atomically reserves Thread
   and Turn identity.
3. `internal/runtime/app/runtime.go` — the Runtime facade. Large but the
   central hub; skim the method names first.
4. `internal/runtime/agent/engine/engine.go` — the agent engine builds an
   immutable `TurnSpec`.
5. `internal/runtime/agent/engine/turn_kernel.go` — the reducer that advances
   a Turn; `turn_scope.go` holds Turn-local state.
6. `internal/runtime/agent/engine/turn_handler.go` and `model_handler.go` —
   the actual model call loop.
7. `internal/runtime/app/terminal_publisher.go` — atomic terminal commit and
   outbox publication.
8. `internal/runtime/app/eventhub/` — sequence assignment, append, replay,
   subscription fanout.

Follow the tests `application_e2e_test.go` and `engine_test.go` alongside to
see the loop in action.

## Route 3 — Guarded Tool Execution (security, the differentiator)

CodeHelper's key promise is that every consequential tool call passes through a
guard pipeline. Read this after Route 2.

1. `internal/adapter/tool/tool.go` and `catalog.go` — tool identity and
   registry/catalog.
2. `internal/adapter/tool/execution.go` — the execution path that funnels into
   the guard.
3. `internal/adapter/tool/guard/guard.go` — the pipeline:
   policy -> approval -> journal -> sandbox.
4. `internal/adapter/tool/guard/pipeline_attempt.go` and
   `pipeline_authorize.go` — step implementation.
5. `internal/security/policy/`, `internal/security/permissions/` — where rules
   and the permission store live.
6. `internal/security/constitution/` — hard, non-bypassable rules.
7. `internal/security/sandbox/` — OS isolation: `policy.go`, `backend.go`,
   and platform-specific `workspace_fs_unix.go` / `seccomp_linux.go`.

Then read how models and providers plug in:

- `internal/adapter/provider/` — the provider abstraction; read
  `types.go`, then one concrete provider such as `openai/`.
- `internal/adapter/model/` — model catalog and probing.
- `internal/adapter/mcp/`, `internal/adapter/skill/`, `internal/adapter/plugin/`
  — governed adapters that extend the runtime.

## Route 4 — State, Persistence, and Observability

Runtime state must survive and be inspectable.

1. `internal/persist/` — SQLite foundation (`sqlkit`), `contentstore`,
   `joblog`, `session`, `snapshot`, `workspacejournal`.
2. `internal/runtime/app/persistence/` — repositories and lifecycle recovery.
3. `internal/runtime/app/receipt.go` and `tool_execution_receipt.go` —
   structured evidence (context, tools, changes, approvals, verification).
4. `internal/observability/` — the observation plane:
   `observation/` (evidence schema), `router/` (durable routing),
   `usage/`, `trace/`, `verify/`, `diagnostics/`.
5. `internal/runtime/eventview/` — one typed interpretation of Event payloads
   consumed by the Go hosts.

## Route 5 — Orchestration and the VS Code Extension

For the longer-lived, multi-step side of the product.

1. `internal/orchestration/` — read `kernel/` (durable Run/Node/Attempt/Lease
   transitions) and `store/` first, then `worker/`, `workflow/`, `lane/`,
   `fleet/`, `subagent/`, `automation/`.
2. `extensions/vscode/src/chat/projector/` — `index.ts` owns sequence and Turn
   identity; `turn-projector.ts` exhaustively dispatches every Event Class.
3. `internal/host/tui/` and `internal/host/runtimeapi/` — other host surfaces.

## Suggested Reading Order

- Session 1 (1–2 hours): README -> architecture.md -> Route 1.
- Session 2: Route 2, following the e2e test.
- Session 3: Route 3 — the guard pipeline and sandbox.
- Session 4: Route 4 — persistence and observability.
- Session 5: Route 5 — orchestration, then VS Code.

## Tools That Make This Easier

- `go doc <package>` for signatures.
- `go test ./path/to/package` to run a package's contract tests.
- In an editor with the repo indexed, `search_definition` /
  `search_references` on a symbol (`TurnCoordinator`, `operationDispatcher`,
  `guard.Guard`, `TerminalPublisher`) to trace ownership.
- `go list -deps ./cmd/codehelper` to see the whole dependency graph.
