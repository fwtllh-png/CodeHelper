# Troubleshooting

[简体中文](../zh-CN/troubleshooting.md) | English

## First Response Checklist

Capture facts before changing configuration:

```bash
codehelper version --json
codehelper doctor --json
codehelper config check --config ./codehelper.toml
codehelper config show --config ./codehelper.toml
codehelper sandbox status --json
git status --short
```

For a reproducible report, include the command, sanitized config provenance,
platform, terminal event, and relevant redacted logs. Never include credentials.

## Configuration Does Not Take Effect

Symptoms:

- command uses an unexpected provider/model;
- workspace or timeout differs from TOML;
- a field appears ignored.

Actions:

1. Run `config show` and inspect provenance.
2. Check `CODEHELPER_*` environment variables.
3. Check command flags, which have highest precedence.
4. Verify the config path is the one used by the host.
5. Reject rather than work around unknown-field errors.

## Provider Authentication Fails

1. Run `codehelper auth status --config ...`.
2. Confirm the reference kind and name, not the secret value.
3. For `env`, export the variable in the same process environment.
4. For `file`, verify existence, ownership, and restrictive mode.
5. For `keyring`, run outside restricted terminals and check OS keyring access.
6. Resolve the provider/model through `model resolve`.

Do not switch to an inline secret to “test quickly.”

## Provider or Model Is Unknown

```bash
codehelper model list
codehelper model resolve --provider PROVIDER --model MODEL --json
```

The same model ID may exist under multiple provider IDs. Specify both. A custom
base URL also requires explicit model metadata/capabilities when they cannot be
derived safely.

## Tool Is Denied

Inspect:

- mode (`plan`, `act`, `operate`);
- posture (`never`, `suggest`, `auto`, `bypass`);
- workspace trust/permissions;
- approval response;
- repository rules;
- constitution hold;
- required sandbox strength.

`auto` may intentionally deny a risky tool without prompting. Use `suggest` for
interactive approval. Do not use `bypass` to hide a policy design problem.

## Sandbox Unavailable

```bash
codehelper sandbox status
codehelper sandbox probe
codehelper doctor
```

Platform capability is environmental. Install/configure the supported backend
or run a read-only workflow. Do not change tests or policy to claim strong
isolation when the backend is absent.

On macOS, restricted embedded terminals can interfere with Seatbelt, Keychain,
or application directories. Reproduce from Terminal/iTerm when appropriate.

## Verification Fails or Is Unavailable

1. Read the verify scope and command in `config show`.
2. Run the command manually from the same workspace.
3. Confirm required dependencies exist inside the sandbox environment.
4. Start with `soft` while establishing deterministic checks.
5. Use `hard` only when failure should fail/revert the turn.

A `sandbox_unavailable` benchmark failure is not evidence that application logic
failed; report it as an environment prerequisite failure.

## Persistent Database Cannot Open

- Verify the process can read/write the data directory.
- Confirm only supported binaries access the current pre-release schema.
- Check disk space and filesystem locks.
- Preserve the directory before destructive recovery.
- Run with a new data directory to distinguish data corruption from runtime
  configuration.

Corruption errors are deliberately explicit. Do not edit SQLite bytes manually.
Pre-release development databases may need to be recreated after schema resets.

## Session Does Not Resume

Check that:

- the same `--data-dir` is used;
- an active thread exists (`thread list`);
- explicit `--thread-id` is not overriding resume lookup;
- workspace identity matches;
- the prior terminal event was persisted.

Use `thread read` before modifying metadata.

## MCP Server Is Unhealthy

```bash
codehelper mcp validate --config ./mcp.json
codehelper mcp status --config ./mcp.json
codehelper mcp tools --config ./mcp.json
```

Inspect command path, environment allowlist, startup timeout, protocol version,
OAuth config, and circuit-breaker state. Test one server in isolation.

## Worker Task Is Stuck

```bash
codehelper worker list --data-dir ./.codehelper
codehelper automation list --data-dir ./.codehelper
codehelper fleet inspect --data-dir ./.codehelper --id RUN_ID
```

Check Run/Node/Attempt state, executor, Lease owner/epoch/expiry, heartbeat,
pending Effect, attempt count, `next_attempt_at`, authority digest, and Worker
budget. A live Lease belongs to its current epoch; do not recover it by editing
database rows. Fleet is a read projection and cannot resume or settle work.

## Extension State Does Not Converge

Use the matching `plugin` or `skill` list command, then inspect health and
receipts through a Runtime client or the VS Code Extensions view. Check source
identity, trust, generation, enabled state, capability state, and the last
operation receipt.

Retry a mutation only with the same operation identity when the client supports
it. Reusing an operation ID with different content is rejected. Do not repair
extension state by editing staged artifacts or durable receipt rows; disable,
revoke, verify, rollback, or reinstall through the control plane.

## Observations or OTLP Are Missing

Check the Runtime process environment:

```bash
env | rg '^(CODEHELPER_OBSERVATION_CAPTURE|CODEHELPER_OTEL_EXPORTER|OTEL_EXPORTER_OTLP_)='
```

- unset capture defaults to `metadata`;
- `off` intentionally records nothing;
- `metadata` intentionally omits raw payloads;
- `failure` captures eligible payloads only for failure-like observations;
- an invalid capture mode fails Runtime construction explicitly;
- an unavailable remote OTLP exporter falls back to the in-memory projector.

The durable Observation Journal is under the selected state directory at
`observability/journal-v1`; changing `--data-dir` changes that location.
Observation payload retention is time-based and separate from
`state.event_retention`. Missing OTLP export or a dropped observation affects
observation health, not the authoritative Turn outcome. Diagnose the collector
without rewriting Runtime Events, Receipts, or Terminal Envelopes.

## VS Code Runtime Is Unavailable

1. Run `CodeHelper: Show Status`.
2. Verify workspace trust.
3. Check `codehelper.binarySource`.
4. Check the absolute runtime config path.
5. Run the selected binary's `version --json`.
6. Compare it with `extensions/vscode/compatibility.json`.
7. Rebuild both runtime and extension from the same tree.

CodeHelper supports only local `file:` Workspaces. Remote SSH, Dev Containers,
and other `vscode-remote:` environments are rejected during activation.

## Capture a VS Code Runtime Failure

Run `CodeHelper: Start Runtime Capture` before reproducing the problem, then run
`CodeHelper: Stop Runtime Capture` when the test is complete. The completion
message reports the JSONL path under the extension's private Workspace storage.

The capture correlates all live protocol Events, replay markers, ACP request
lifecycle and IDs, Runtime stderr, process exit code or signal, automatic
restart state, and Session synchronization errors. It is opt-in and uses mode
`0600` because model output, Tool arguments/results, and diagnostics may contain
sensitive Workspace data. Review and redact the file before sharing it.

This VS Code Host capture is distinct from the Runtime Observation Journal.
Host capture follows ACP and process supervision; the Observation Journal
records Runtime evidence according to `CODEHELPER_OBSERVATION_CAPTURE`.

## Tests Fail Only in Full Parallel Run

Some fixture lifecycle and process tests are resource-sensitive. Rerun the
reported package and test in isolation:

```bash
go test ./path/to/package -run TestName -count=1
```

If isolated tests pass, report both results and investigate scheduling/resource
pressure. Do not silently omit the full-suite failure.

## Documentation Check Fails

`make docs-check` reports:

- broken local Markdown links;
- missing English/Chinese mirror files;
- forbidden links to removed historical docs.

Fix the source document or add the missing maintained counterpart. Do not create
empty placeholder files just to satisfy parity.
