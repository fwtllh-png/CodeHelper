# Security Model and Operations

[简体中文](../zh-CN/security.md) | English

## Security Objective

CodeHelper runs model-selected tools against source code. The objective is not
to make arbitrary code safe; it is to keep authority explicit, effects bounded,
credentials out of model-visible state, and every consequential action
inspectable.

## Threat Model

Treat these inputs as untrusted:

- user prompts and pasted content;
- repository files, generated code, tests, and build scripts;
- model output and tool arguments;
- provider responses and native-search results;
- MCP servers, plugin packages, skill content, and hooks;
- HTTP/ACP client messages;
- persisted state copied from another workspace;
- archive paths, symlinks, environment variables, and process output.

The local operator and trusted release keys are authority roots, but operator
mistakes and compromised dependencies remain in scope.

## Layered Controls

| Layer | Purpose |
| --- | --- |
| mode | limits the kind of work requested |
| posture | determines deny/approval/automatic handling |
| workspace permissions | binds remembered authority to one workspace |
| constitution | hard constraints ordinary configuration cannot bypass |
| tool guard | central identity, risk, resource, approval, and evidence decision |
| edit journal | records before-images and interrupted work |
| verify gate | collects correctness evidence before commit |
| OS sandbox | enforces process/filesystem/network boundaries |
| egress controls | constrains remote endpoints and outbound clients |
| observability | records redacted events, receipts, usage, and traces |

No layer should be described as a replacement for another.

## Posture Guidance

- `never`: safest for initial repository inspection and untrusted workspaces.
- `suggest`: recommended for normal interactive development.
- `auto`: suitable for known policy rules and deterministic fixtures; denied
  operations may not prompt.
- `bypass`: only for an isolated trusted workspace with independent backups.

`bypass` is still subject to hard constraints and sandbox availability.

## Workspace and File Safety

- Resolve and validate paths relative to the configured workspace.
- Reject traversal, unsafe symlink behavior, and archive escape.
- Read before editing when the tool contract requires it.
- Journal mutating changes and preserve atomicity.
- Use worktrees for writing subagents when configured.
- Review generated plans with `apply --dry-run`.
- Keep valuable repositories under version control and maintain backups.

## Process Execution

- Commands run with a sanitized environment.
- Working directories and executable paths must be explicit.
- Timeouts, cancellation, and process-group cleanup are required.
- PTY and non-PTY execution must share policy boundaries.
- A missing required strong sandbox is a failure, not permission to run
  unsandboxed.

## Credentials

Allowed configuration contains references:

```toml
[credential]
kind = "env"
name = "OPENAI_API_KEY"
```

Operational rules:

- never commit secret values;
- do not pass secrets in prompts or command arguments;
- prefer OS keyring for desktop use;
- prefer secret-manager-injected environment variables in CI;
- protect secret files with restrictive permissions;
- rotate a credential after any suspected exposure;
- treat logs, receipts, crash dumps, and support bundles as sensitive even when
  redaction is enabled.

Run:

```bash
make secret-leak-test
```

## Network and Service Exposure

- Services default to `127.0.0.1`.
- Non-loopback deployment requires an authenticated and reviewed gateway.
- Provider base URLs and redirect behavior are security-sensitive.
- Native/web search results remain untrusted content.
- Do not expose trusted dynamic-tool registration to an untrusted client.
- Record endpoint inventory without recording credentials.

## MCP, Plugin, Skill, and Hook Supply Chain

### MCP

Review the executable, arguments, environment allowlist, OAuth configuration,
and endpoint. Use health isolation and bounded timeouts.

### Plugins

Require trusted publisher identity, signed registry data, digest verification,
immutable staging, receipts, and revocation. Rollback must select a previously
verified artifact.

### Skills

Lock the resolved source/version and review instructions/resources. A skill is
content interpreted by an agent and can carry prompt-injection risk.

### Hooks

Keep hooks minimal, bounded, and explicit. They must not become a hidden path
around tool policy.

## Logs and Diagnostics

Redaction reduces accidental leakage but does not make logs public. Store them
with restrictive access and bounded retention. Structured error output should
avoid raw remote/filesystem errors when they may contain secrets.

## Security Testing

```bash
make security-test
make sandbox-attack-test
make secret-leak-test
make vscode-security
```

Security changes should test:

- allowed behavior;
- denied behavior;
- malformed input;
- cancellation and cleanup;
- concurrent access;
- redaction;
- unsupported platform behavior.

## Incident Response

If a secret or signing key may be exposed:

1. stop affected runtime/update distribution;
2. revoke and rotate the credential/key;
3. invalidate affected plugin or binary artifacts;
4. preserve sanitized evidence;
5. inspect event/receipt/log scope;
6. publish a higher-sequence revocation manifest when applicable;
7. fix the control and add a regression test;
8. communicate exact affected versions and remediation.

If workspace integrity is uncertain, stop execution, preserve state and journal
data, inspect Git/diffs, and restore from a trusted revision or backup.

## Reporting

Do not include secrets or private source in a public report. Provide version,
platform, command shape, sanitized configuration provenance, expected/actual
security decision, and reproducible fixture where possible.
