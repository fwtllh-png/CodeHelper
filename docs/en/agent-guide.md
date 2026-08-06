# Guide for AI Coding Agents

[简体中文](../zh-CN/agent-guide.md) | English

This document gives an AI coding agent the minimum reliable context needed to
work in this repository. Human contributors should also use it as a review
checklist.

## Mission

Preserve CodeHelper as one governed local coding-agent runtime with multiple
hosts. Prefer correctness, explicit evidence, security boundaries, and
maintainability over feature count.

## Start Here

1. Read the root `README.md`.
2. Read [Architecture](./architecture.md).
3. Read the nearest package tests before editing implementation.
4. Inspect `Makefile` for the standard validation command.
5. Check `git status`; do not overwrite unrelated user changes.

## Ownership Map

| Change concerns | Start in |
| --- | --- |
| CLI command or flags | `internal/host/cli` |
| TUI rendering | `internal/host/tui` |
| HTTP/ACP behavior | `internal/host/runtimeapi` |
| operation/event shape | `internal/runtime/protocol` |
| turn/session state | `internal/runtime/app` |
| model/tool loop | `internal/runtime/agent` |
| dependency construction | `internal/runtime/app/wire` |
| model/provider behavior | `internal/adapter/model`, `internal/adapter/provider` |
| tool behavior | `internal/adapter/tool` |
| approvals and sandbox | `internal/security`, `internal/adapter/tool/guard` |
| tasks/workflows | `internal/orchestration` |
| durable data | `internal/persist` |
| VS Code | `extensions/vscode` |

## Non-Negotiable Invariants

- Do not execute tools directly from a host.
- Do not put business loops in `wire`.
- Do not introduce a second runtime path for VS Code.
- Do not bypass guard, policy, constitution, journal, or sandbox checks.
- Do not store raw credentials in tracked config, logs, fixtures, or docs.
- Never read, print, summarize, patch, or force-add the ignored
  `docs/DEEPSEEK-LIVE.zh-CN.md` local runbook.
- Do not parse structured formats with brittle string manipulation when a
  parser exists.
- Do not silently accept unknown config fields or protocol variants.
- Do not claim verification that was not executed.
- Do not add compatibility migrations for unreleased development state without
  a clear requirement.

## Working Method

### Explore

Use `rg` and targeted reads. Identify:

- the owner package;
- current tests;
- public or persisted contracts;
- cross-platform files;
- generated artifacts;
- user-modified files.

### Plan

State the behavioral change, invariants, files, and verification. Keep the
change inside existing ownership boundaries.

### Implement

- follow local patterns;
- make the smallest coherent change;
- use semantic names;
- add comments only for non-obvious constraints;
- update English and Chinese docs together;
- regenerate artifacts through repository commands.

### Verify

Run the narrowest test first, then broaden by blast radius:

```bash
go test ./path/to/package
make docs-check
cd extensions/vscode && npm run check && npm test -- relevant-area
```

Use `git diff --check` before completion.

## Contract Changes

### Protocol

When changing `internal/runtime/protocol`:

1. update validation and tests;
2. regenerate the JSON schema;
3. regenerate VS Code protocol types;
4. run ACP and HTTP contract tests;
5. update both documentation languages.

### Persistence

When changing durable schema:

1. define compatibility expectations;
2. keep initialization equal to the latest schema;
3. after public release, add transactional migrations and migration tests;
4. verify foreign keys, indexes, rollback, and corruption handling;
5. update operational documentation.

### Security

For guard, policy, permissions, constitution, sandbox, egress, credentials, or
plugin trust:

1. enumerate the attacker-controlled input;
2. preserve fail-closed behavior;
3. test denial and cleanup, not only success;
4. run focused race/security tests;
5. avoid weakening platform claims.

## Testing Expectations

| Risk | Expected tests |
| --- | --- |
| local logic | unit tests |
| shared component | unit + integration consumers |
| protocol | golden/schema + transport contracts |
| persistence | create/read/update + failure/reopen |
| concurrency | deterministic synchronization + race |
| security | allow, deny, malformed input, cleanup |
| UI | state projection + message validation |
| script | happy path, invalid input, exit status |

Avoid private ad hoc test scripts when the repository already has a target or
fixture framework.

## Documentation Expectations

Documentation is part of the feature:

- describe current behavior, not development chronology;
- distinguish shipped capability from roadmap;
- use commands that exist in `--help`;
- never embed real credentials in tracked documentation;
- use `make deepseek-init`, `make deepseek-tui`, or `make deepseek-vscode`
  instead of inspecting the owner's ignored DeepSeek runbook;
- keep local links valid;
- update both `docs/en` and `docs/zh-CN`;
- remove obsolete material instead of leaving contradictory copies.

For the Agent engineering book:

- treat `docs/book/catalog.json` as the structure, title, path, milestone, and
  delivery-status source of truth;
- do not create Markdown placeholders for `planned` chapters;
- create English and Chinese files together when moving a chapter to `draft`;
- copy the language templates and keep Front Matter aligned with the catalog;
- use `docs/book/governance.json` for ownership, freshness, and release facts;
- declare affected chapter IDs in the PR documentation-impact block;
- run `make book-navigation` after catalog changes;
- run `make book-check` before reporting completion.

## Completion Report

State:

- what changed;
- which files or subsystems matter;
- what was tested;
- any environmental failures or skipped tests;
- compatibility or migration impact;
- remaining untracked files that were not part of the task.
