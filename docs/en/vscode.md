# VS Code Extension

[简体中文](../zh-CN/vscode.md) | English

## Design

The extension is a workspace-hosted TypeScript client for the Go runtime. It
owns editor context and presentation; it does not own model reasoning or direct
workspace mutation.

```text
VS Code UI and context bridge
        -> ACP over stdio
        -> CodeHelper Go runtime
        -> guard / journal / sandbox
```

This preserves the same approval, event, persistence, and security semantics as
the CLI and TUI.

## Capabilities

- multi-root and multi-chat workspace bindings;
- streaming chat with separate reasoning/output presentation;
- selection and diagnostic actions;
- edit-plan preview and approval;
- native navigation for Runtime-confirmed files, ranges, directories, symbols,
  diagnostics, and edit-plan diffs;
- background task, job, agent, usage, and change views;
- local, Remote SSH, and Dev Container workspace-host execution;
- external, managed, or bundled runtime selection;
- signed managed updates, rollback, and revocation checks.

## Workbench and Keyboard Journey

Chat is the primary surface. Changes, Threads, and Approvals are the primary
review navigation; Agents, Tasks, Jobs, and Usage are collapsed detail views by
default. Edit Plans use the native Diff editor, choices use Quick Pick, setup
uses Progress, and durable collections use Tree View.

Use `Ctrl+Enter` or `Cmd+Enter` to send and `Escape` to stop an active Turn.
The lifecycle strip names Setup, Empty, Loading, Streaming, Approval, Verify,
Failure, Recovery, and Completed states with a next action. Controls use VS
Code theme tokens, visible keyboard focus, forced-color borders, and reduced
motion. Reasoning and Tool details remain collapsed unless active or opened.

## Native Resource Navigation

Runtime-confirmed editor context, context selections, and Edit Plans are
projected into opaque resource IDs. File references in Chat open through native
VS Code APIs; ranges and diagnostics reveal the relevant selection, symbols use
the definition provider with an in-file fallback, directories reveal in
Explorer, and plans open in the Diff editor.

The Webview never submits a URI, path, or command. The Extension Host resolves
the opaque ID from the current Snapshot, validates the exact Workspace root and
relative path, then invokes a fixed VS Code action. Absolute paths, traversal,
arbitrary URI schemes, `command:` values, cross-root definitions, unknown IDs,
and stale Diff identities fail closed. This applies equally to local,
multi-root, Remote SSH, and Dev Container Workspaces.

## Built-in Setup and Repair

Use `CodeHelper: Setup Workspace` to configure an open, trusted workspace. The
guided flow selects a provider, model, and credential reference, writes the
`recommended` profile to `codehelper.toml`, updates the workspace Runtime
setting, and restarts the Runtime. The prompt accepts only an environment
variable name, protected file path, or keyring key; do not enter a secret
value.

Use `CodeHelper: Repair Runtime` when startup fails or readiness is degraded.
The command combines the VS Code Supervisor failure with `doctor --json`, then
shows each missing capability with its status, reason, impact, and repair
action. Binary-resolution failures offer Settings, managed update, and Output
actions. The Chat failure panel provides direct Setup and Repair buttons.

`CodeHelper: Run Quickstart` runs the bundled network-free first-turn journey
from VS Code without changing the selected workspace.

## Development Setup

```bash
make build
make vscode-install
make vscode-check
make vscode-test
make vscode-build
```

The compiled entry is `extensions/vscode/dist/extension.js`.

To run integration with the real runtime but without Electron:

```bash
make vscode-runtime-integration
```

To run the official VS Code Electron integration:

```bash
make vscode-integration
```

Electron is downloaded on first use and is intentionally excluded from default
verification.

## Build a VSIX

```bash
make vscode-package
```

For multi-target dry-run artifacts:

```bash
make vscode-distribution
```

Generated release output is under `extensions/vscode/dist/`.

## macOS One-Click Local Setup

The opinionated setup script:

1. requires official `/Applications/Visual Studio Code.app`;
2. builds/selects the current macOS target VSIX;
3. installs the repository example config;
4. reads `DEEPSEEK_API_KEY` from the environment or secure terminal prompt;
5. stores it in macOS Keychain as `deepseek/default`;
6. updates official VS Code user settings;
7. installs the extension and optionally opens the workspace.

For the repository owner's saved DeepSeek environment, use the complete
build/configure/install/launch path:

```bash
make deepseek-vscode
```

Credential lookup and Agent constraints are documented in
[One-Click Local DeepSeek](./deepseek-local.md).

Run the lower-level installer directly with an environment credential:

```bash
export DEEPSEEK_API_KEY='...'
make vscode-local-setup
unset DEEPSEEK_API_KEY
```

Options:

```bash
./scripts/setup-vscode-local.sh --skip-build
./scripts/setup-vscode-local.sh --no-open
```

Quit VS Code before running the script. Do not run it in a restricted embedded
terminal that blocks Keychain or extension-directory writes.

## Manual Runtime Configuration

VS Code settings:

```json
{
  "codehelper.binarySource": "auto",
  "codehelper.runtime.configPath": "/absolute/path/to/config.toml",
  "codehelper.runtime.autoStart": true,
  "codehelper.runtime.maxSteps": 64
}
```

Binary sources:

- `auto`: verified bundled, managed, then configured external source;
- `external`: user-selected runtime path;
- `managed`: signed update store;
- `bundled`: runtime packaged in the target VSIX.

Workspace trust matters. An untrusted workspace is forced into a read-only
posture and cannot select an arbitrary executable.

## Compatibility Contract

The source of truth is `extensions/vscode/compatibility.json`. It binds:

- supported extension and binary version ranges;
- ACP protocol range;
- operation schema version;
- required runtime features;
- supported target/platform identities.

Generated compatibility files must be refreshed through:

```bash
cd extensions/vscode
npm run generate:compatibility
```

## Release Gates

```bash
make vscode-rc
```

The RC flow covers static checks, protocol drift, runtime integration, security,
performance, Electron behavior, updates, distribution, matrix evidence, SBOM,
provenance, and report generation.

Formal release signing material must remain outside the repository:

```bash
export CODEHELPER_RELEASE_PRIVATE_KEY=/secure/release-private.pem
export CODEHELPER_RELEASE_TRUST_ROOTS=/secure/release-trust-roots.json
export CODEHELPER_RELEASE_KEY_ID=release-key-id
export CODEHELPER_RELEASE_VERSION=0.1.0
export CODEHELPER_RELEASE_SEQUENCE=1
```

Dry-run artifacts are not publishable release evidence.

## Troubleshooting

- `Runtime unavailable`: verify `binarySource`, config path, and workspace host.
- `Workspace identity mismatch`: do not copy state between unrelated editor URIs.
- `Writes denied`: trust the workspace, then inspect posture and approval.
- `Compatibility failure`: rebuild the runtime and extension from the same tree.
- `VSIX installed in the wrong editor`: use the absolute official VS Code CLI.
- `Managed update rejected`: inspect signature, sequence, revocation, digest, and
  target identity.

More cases are in [Troubleshooting](./troubleshooting.md).
