# VS Code Extension

[简体中文](../zh-CN/vscode.md) | English

## Design

The extension is a locally hosted TypeScript client for the Go runtime. It
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
- local `file:` workspace execution in the UI Extension Host;
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
and stale Diff identities fail closed. This applies to local single-root and
multi-root Workspaces.

## Local Workspace Boundary

CodeHelper declares the VS Code extension as a UI extension and supports only
local `file:` Workspaces. Remote SSH, Dev Containers, Codespaces, WSL remote
Workspaces, and other `vscode-remote:` environments are outside the product
scope. The extension rejects Remote activation instead of launching a Runtime
against a mismatched filesystem or identity.

## Native Chat Contract Decisions

The native Chat contract fixes four product boundaries:

- durable Runtime modes are `plan`, `act`, and `operate`; the Composer labels
  them Plan, Implement, and Operate. Ask is prompt intent, not a fourth
  persisted mode;
- `execution_target` is currently `local` only. Sandbox is not advertised as a
  selectable target until Runtime construction and policy can apply it;
- one `WebviewView` may be moved by the user between the Sidebar and Panel. A
  separate Full Editor Chat surface is outside the current product scope;
- Checkpoint Restore is state-only. Busy, stale-Revision, unsupported, and
  wrong-Session failures use structured Problem details. File conflicts belong
  to explicit Revert or Merge, not Restore.

## Session Profile Contract

The Runtime owns each Session's mode, provider, model, reasoning effort,
enabled Tool IDs, approval posture, execution target, step limit, Revision,
and prompt-cache Revision. The Extension Host uses
`session/profile/get`/`session/profile/update`; Webview state and
`workspaceState` are not Profile stores.

Updates require the observed Revision and fail on stale writes or an active
Turn. Changes that alter model-visible request shape advance the prompt-cache
Revision. Capabilities expose only fields the current Runtime can apply. The
current Runtime route is immutable; Provider/Model switching is enabled only
through the restart-backed Setup flow. In-place switching is enabled only when
a catalog-backed Runtime advertises those fields as mutable.

The Composer projects this contract into native-style Mode, Provider, Model,
Thinking, Tools, Credential, and Approval controls. Mode, Thinking, Tools, and
Approval use Revision-checked Profile updates. Provider and Model use Setup
plus a local Runtime restart. Controls remain disabled when the Runtime does
not advertise the required capability.

The generated Model Capability contract includes display name, context and
output limits, Tool Calling, Image, Reasoning, Prompt Cache, Parallel Tool
support state, reasoning choices and default, Credential state, Availability,
unavailable reason, and selection mode. `provider/list` and `model/list` return
versioned catalogs. The current single route reports `restart_required`; it
does not claim hot switching.

The native Provider and Model Quick Picks consume these Runtime catalogs. They
support search, show availability and capability details, and label every
non-hot route as Restart Required. Selecting such a route enters the guarded
Setup flow and reconstructs the local Runtime; the Webview never submits a
Provider capability or Profile patch.

The Add Context control opens a native Host Quick Pick for saved workspace
files, the active selection, symbols, diagnostics, images, explicit clipboard
terminal output, and the current VS Code Git diff. The Webview sends only
add/remove intents and receives non-authoritative display chips. The Extension
Host captures paths, bytes, ranges, and digests. Runtime then re-resolves
workspace files, verifies canonical identity and SHA-256, bounds text and image
sizes, and emits a durable Receipt for every accepted item. PNG, JPEG, GIF, and
WebP are sent through provider-native image content blocks only when the
selected model advertises image input. Terminal text requires an explicit
clipboard confirmation; Git diff comes from the built-in VS Code Git API.

Execution Environment is projected from the Session Profile and is fixed to
Local. Keyboard routing ignores IME composition, gives Escape to the topmost
Session drawer before an active Turn, and maps Cmd/Ctrl+N to New Chat.

## Unified Tool Catalog

`session/tool/catalog` projects the Runtime registry for one Session. It
combines built-in, MCP, Plugin, Skill, and trusted Dynamic tools with catalog
identity, Generation, Digest, source, capability, access mode, sandbox
requirement, Availability, and Session-enabled state. Registry Authority and
input schemas do not cross this read boundary.

Each entry also exposes a static risk level and separate Policy and
Constitution states with reasons. Catalog projection uses `deferred` when a
decision requires validated call arguments and resources. This metadata is
explanatory only; the Tool Guard remains the final decision point.

Tool IDs are bound to their source family and, for MCP, the server identity, so
a revoked tool cannot transfer an existing Session grant to a same-name tool
from a different family or MCP server.

The Tools control opens a native searchable multi-select grouped by source.
Unavailable and deferred entries remain visible with their status. An empty
`enabled_tool_ids` list means the Runtime-compatible default of all tools; a
non-empty list is a strict Session Allowlist. The Engine applies that Allowlist
both when advertising model tool definitions and before execution. Selection
never grants permission: enabled calls still pass through Guard, policy,
approval, journal, and sandbox enforcement.

## Session Lifecycle

The Runtime is the durable authority for Session discovery and lifecycle state.
`session/list`, `session/status`, `session/lifecycle/update`, and
`session/delete` expose Revision-checked summaries and mutations. Search covers
the title, user request, Agent output, path, and symbol evidence; status combines durable Turn state
with live activity across every Thread in the Session, including pending
approval and input.

Durable Session summaries never carry transient search state. `session/list`
returns search matches separately, keyed by Session and Turn with a match kind
and optional snippet. A Host may cache the durable summary, but must discard or
replace matches when the query changes.

The Summary also projects the latest immutable Checkpoint changed-file count,
the local Execution Environment, parent Fork Thread, and latest Turn identity.
The Rail combines Workspace, Model, Mode, Status, pending/changed/fork activity,
and text filters. Selecting a Match switches Session and focuses the stable
Turn article identified by the Runtime.

Session actions use native VS Code UI for Duplicate, Open to Side, Markdown or
integrity-checked Structured Receipt export, and pending Approval reveal.
Duplicate copies the Runtime-owned Profile into a new Session; it never replays
historical Tool, command, network, or file effects. Export is built from the
Host projection and never reads Webview DOM.

Resource actions continue to submit only an opaque Resource ID. Open, Open to
Side, and Copy Relative Path all revalidate Root, Scheme, canonical path,
Range, and Diff membership in the Extension Host before using VS Code APIs.

The Session rail groups Pinned, Today, Yesterday, Previous 7 Days, Older, and
Archived entries. It supports status filtering and projects workspace,
provider/model, lifecycle status, usage, and pending interaction counts.
Rename uses a native InputBox. Pin, Archive, Restore, and Delete use a native
Quick Pick, and Delete requires a modal confirmation.

VS Code `workspaceState` stores only connection identity, replay cursor, and
the selected Session. Title, isolation, Profile, status, Pin, and Archive state
are queried from the Runtime after every restart. Archived Sessions remain
discoverable but are not connected until restored.

Archive and Delete fail while any Session Thread is running, awaiting approval,
or awaiting input. Delete also fails for the last Session in a Workspace and
for an isolated Session with unmerged Worktree changes. A successful isolated
delete removes its Worktree through the Runtime-owned lifecycle path. These
checks preserve Guard, journal, sandbox, and durable Revision CAS boundaries.

## Checkpoints, Forks, and Plans

Completed Turns and safely user-interrupted Turns create immutable Session
Checkpoints. `checkpoint/list`, `checkpoint/get`, `checkpoint/restore`, and
`checkpoint/fork` expose the Runtime-owned objects. Metadata records Session,
Thread, Turn, event cursor, Profile Revision, parent Checkpoint, changed-file
count, and conservative side-effect status. Model-visible replacement history
and the Profile snapshot are stored in CAS and verified before use.
When a Turn Receipt exists, Checkpoint metadata stores an immutable reference
to its Event ID, Turn ID, and cursor instead of copying mutable display text.

Restore is state-only. It replaces model-visible Runtime history and emits a
durable `checkpoint.restored` event, but never replays or reverses completed
file, Tool, command, or network effects. A stale Profile Revision, active Turn,
pending approval/input, corrupt CAS object, or cross-Session identity fails
closed. Restart reconstruction applies the same restore baseline.
ACP preserves the Runtime Problem object in JSON-RPC error data, including the
machine-readable reason and relevant status or Revision values.

Fork creates an independent Engine history from a Checkpoint and persists both
the parent Thread and parent Checkpoint lineage. The selected active Thread is
stored in Session lifecycle metadata, so restart does not fall back to the root
Thread. The Session menu opens a native Checkpoint Quick Pick for Restore and
Fork.

A completed streamed Plan is persisted as a structured Plan Artifact instead of
being inferred from rendered Markdown. The Plan card can open the body in a
native editor, start implementation, or request Autopilot. Both implementation
actions validate Artifact and Profile Revision, switch through the Runtime
Profile contract, and submit a new Turn. Autopilot requests `act` mode with
`auto` approval posture; Host permission ceilings, Guard, policy, journal, and
sandbox enforcement remain authoritative.

Retry and Continue are active Runtime workflows. They always create a new Turn
with an idempotency key and never replay historical Tool, command, network, or
file operations. Retry reuses the source Turn's durable model-visible request;
Continue uses terminal history plus optional guidance. Plan transitions
explicitly target the current Session, a Profile-preserving new Session, or a
state-only Checkpoint Fork. The Runtime validates the source Artifact and
builds the implementation prompt; the Webview never reconstructs it.

The Host/Webview incremental contract is active. Full Snapshot hydration
carries a monotonic projection Revision. Later Patches reference the exact
base Revision and contain only typed Turn, Runtime, Composer, and Resource
operations. The Webview Store applies a Patch atomically or requests a Full
Snapshot resynchronization. Turn, Call, Request, and Session identities preserve
expanded panels, focus, and scroll anchors across updates.

## Performance and Accessibility Contract

The Session rail virtualizes grouped search results: the Runtime-owned list and
search projection remain complete, while the Webview creates DOM only for the
visible rows and a bounded overscan window. The Transcript Store likewise keeps
the full projection while rendering only the viewport Turn window and bounded
overscan.
When the Chat view is hidden, the Extension Host continues updating the Runtime
projection but stops assembling and posting DOM Snapshots. The latest
projection is posted once when the view becomes visible again.

Release performance gates require:

- no more than 20 ms of added extension activation time;
- first interactive Chat within 300 ms, excluding Runtime startup;
- a 200-Turn Session Snapshot within 100 ms;
- 1000-Session search and virtual first paint within 150 ms;
- at most one streaming Patch per 16 ms frame.

Electron journeys dynamically switch Default Dark Modern, Default Light
Modern, and Default High Contrast themes, apply Zoom Level 4 (approximately
200%), and verify the Webview remains interactive. Keyboard evidence covers
visible focus, Session Home/End/Arrow navigation across virtual rows, Escape,
the narrow-rail focus trap, reduced motion, and forced-color borders.

## Built-in Setup and Repair

Use `CodeHelper: Setup Workspace` to configure an open, trusted workspace. The
guided flow selects a provider, model, and credential source, writes the
`recommended` profile to `codehelper.toml`, updates the workspace Runtime
setting, and restarts the Runtime. The recommended credential option uses a
password InputBox and VS Code SecretStorage. The Runtime configuration contains
only a generated environment reference; the Extension Host injects the secret
into the local Runtime process. The Chat Webview, Profile, logs, and settings
never receive the secret. External environment, protected-file, and OS-keyring
references remain available.

`CodeHelper: Configure Credential` replaces the current Provider credential in
SecretStorage without exposing it to the Webview. Untrusted Workspaces cannot
configure credentials or raise Approval posture. A Runtime started with
read-only posture also clamps restored Profiles to `never`, so a previously
persisted `bypass` value cannot cross the Host trust boundary.
After Setup or replacement, the Host explicitly calls the Provider model-list
endpoint through the Runtime credential and egress implementation. Only the
validation result, timestamp, and a bounded failure category are persisted;
the secret and raw Provider response never enter Webview state or logs.

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

The required local matrix has seven named jobs: macOS arm64 external
single/multi-root, macOS arm64 bundled, macOS x64 external under Rosetta,
update integration, distribution, security, and performance. Windows x64
package evidence is optional because this repository has no Windows Electron
runner. Remote SSH, Dev Containers, Codespaces, and WSL remote workspaces are
unsupported product environments, not missing release jobs.

On an Apple Silicon release host, the required Rosetta evidence can be run
independently with:

```bash
make vscode-rosetta-integration
```

This builds an amd64 Runtime, runs the pinned x64 VS Code Extension Host, and
asserts both host and binary architecture before recording the evidence.

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
