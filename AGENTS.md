# AGENTS.md

This file is the repository entry point for coding agents.

完整说明见 [docs/zh-CN/agent-guide.md](./docs/zh-CN/agent-guide.md)。

## Objective

Maintain CodeHelper as one local, guarded coding-agent runtime shared by CLI,
TUI, VS Code, ACP, workers, and orchestration.

## Read Before Editing

1. `README.md`
2. `docs/zh-CN/architecture.md`
3. the nearest package tests
4. `Makefile`
5. `git status --short`

## Hard Rules

- Hosts submit operations; they do not execute provider/tool/sandbox logic.
- Business loops stay in `internal/runtime/agent`.
- Construction stays in `internal/runtime/app/wire`.
- Consequential tools pass through `internal/adapter/tool/guard`.
- Do not bypass policy, approval, constitution, journal, or sandbox.
- Do not store raw secrets in tracked code, config, fixtures, logs, or docs.
- Never read, print, patch, or force-add `docs/DEEPSEEK-LIVE.zh-CN.md`; use the
  `deepseek-*` Make targets when operating the owner's local environment.
- Do not overwrite unrelated worktree changes.
- Do not add pre-release compatibility migrations without an explicit need.
- Product documentation and book chapters are maintained in Chinese only under
  `docs/zh-CN` and `docs/book/zh-CN`.
- Treat `docs/book/catalog.json` as the book structure and status source of
  truth. Do not create empty files for `planned` chapters.
- Treat `docs/book/governance.json` as the ownership, freshness, and release
  fact source of truth.
- Book chapters move to `draft` or `verified` only with a Chinese file and
  valid Front Matter; regenerate navigation through the repository command.
- Use repository commands for generated protocol and compatibility files.

## Ownership

```text
CLI/TUI/API hosts          internal/host
protocol/app/agent/wiring  internal/runtime
providers/tools/ecosystem internal/adapter
policy/sandbox            internal/security
tasks/workflows/subagents internal/orchestration
durable state             internal/persist
usage/traces/verification internal/observability
VS Code                    extensions/vscode
```

## Standard Validation

```bash
go test ./path/to/package
make docs-check
make book-check
git diff --check
```

For VS Code:

```bash
cd extensions/vscode
npm run check
npm test -- relevant-area
```

Broaden validation based on risk. Report environment-limited failures rather
than hiding them.
