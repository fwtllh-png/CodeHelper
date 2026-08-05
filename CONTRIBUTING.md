# Contributing to CodeHelper

[简体中文](./CONTRIBUTING.zh-CN.md) | English

## Principles

- Preserve one runtime and one guarded execution path.
- Prefer current code facts over historical design narrative.
- Keep changes scoped to the owning package.
- Add compatibility only when a published contract requires it.
- Treat documentation, tests, generated files, and operational behavior as part
  of the implementation.

## Before You Change Code

1. Read [Architecture](./docs/en/architecture.md).
2. Check `git status` and preserve unrelated work.
3. Locate package tests and ownership boundaries.
4. Decide whether the change affects protocol, persistence, security,
   configuration, release artifacts, or both documentation languages.

## Development Workflow

```bash
make build
go test ./path/to/package
make docs-check
git diff --check
```

Broaden tests according to blast radius. See
[Local Development](./docs/en/development.md).

## Change Requirements

### Runtime and protocol

- Define operation, event, cancellation, error, and replay semantics.
- Update schema/golden data and both ACP/HTTP contracts.
- Regenerate committed artifacts through repository commands.

### Persistence

- Keep initialization at the latest shape.
- Public schema changes require transactional migrations and migration tests.
- Test reopen, rollback, constraints, and corruption behavior.

### Security

- Describe attacker-controlled inputs.
- Preserve fail-closed behavior and the guard path.
- Test denial, malformed input, cleanup, and redaction.
- Do not broaden platform support claims without evidence.

### Documentation

- Update English and Chinese versions together.
- Use commands verified against `--help`.
- Remove superseded documents instead of leaving conflicting copies.
- Run `make docs-check`.

## Commit Quality

Use a subject that describes behavior, for example:

```text
fix: preserve task lease across worker restart
docs: add bilingual provider configuration guide
```

Avoid phase-only messages and unrelated refactors. Generated output should be
committed with the source that produced it.

## Review Checklist

- [ ] Behavior and failure semantics are clear.
- [ ] Ownership boundaries are preserved.
- [ ] Security checks cannot be bypassed.
- [ ] Persisted/protocol compatibility is intentional.
- [ ] Tests match risk and pass in the supported environment.
- [ ] English and Chinese docs are synchronized.
- [ ] No credential, machine path, or unrelated generated file is included.

## Reporting Environment-Limited Tests

Do not hide a failing full suite. State the exact failing package/test and
environment reason, then rerun the focused test in isolation. Platform
limitations such as missing strong sandbox capability are not application
success and not necessarily an application regression.
