# Getting Started

[简体中文](../zh-CN/getting-started.md) | English

## 1. Prerequisites

| Requirement | Notes |
| --- | --- |
| Go 1.26+ | required for the runtime |
| Git | required for repository workflows and worktree isolation |
| Make | recommended command entry point |
| Node.js + npm | required only for VS Code extension development |
| macOS/Linux | recommended; Windows support has different sandbox limits |

Check the environment:

```bash
go version
git --version
make --version
```

## 2. Build from Source

```bash
git clone https://github.com/fwtllh-png/CodeHelper.git
cd CodeHelper
make build
./bin/codehelper version
./bin/codehelper help
```

The binary is written to `bin/codehelper`. Build metadata can be overridden:

```bash
make build VERSION=0.1.0
```

## 3. Inspect Platform Readiness

```bash
./bin/codehelper doctor
./bin/codehelper sandbox status
./bin/codehelper sandbox probe
```

`doctor` reports capabilities; it does not grant permission. A missing strong
sandbox reports `blocked` because mutating execution and verification must fail
closed. Optional dependency gaps report `degraded`. The corresponding exit
codes are `2` and `1`; `ready` exits `0`.

## 4. Run Setup

From the repository you want CodeHelper to operate on:

```bash
/path/to/codehelper setup \
  --workspace . \
  --provider openai \
  --model gpt-4.1 \
  --credential-kind env \
  --credential-name OPENAI_API_KEY \
  --json
```

`setup` writes only the credential reference, probes the actual sandbox, and
runs a bundled network-free Runtime fixture. Add
`--probe-capabilities reasoning` only when the referenced live credential and
provider network are available. For a prompted flow, use:

```bash
/path/to/codehelper setup --workspace . --interactive
```

Automation can add `--require-ready` to make the exit code match the reported
`ready`, `degraded`, or `blocked` status. `init` remains the minimal file-only
alternative:

```bash
/path/to/codehelper init --workspace .
```

To inspect the resolved configuration and provenance:

```bash
/path/to/codehelper config check --config ./codehelper.toml
/path/to/codehelper config show --config ./codehelper.toml
```

See [Configuration](./configuration.md) for every supported section.

### Network-free first journey

Run the complete governed first-turn journey without a credential or network:

```bash
./bin/codehelper quickstart --json
```

The bundled fixture creates a temporary workspace and exercises a structured
plan, file read, edit preview, explicit approval, verification, execution
receipt, and terminal completion. Add `--keep` to retain the generated
workspace, or `--workspace EMPTY_DIR` to use a chosen empty directory.

## 5. Configure Credentials

The safest first setup uses an environment-variable reference:

```bash
export OPENAI_API_KEY='...'
./bin/codehelper auth login \
  --config ./codehelper.toml \
  --kind env \
  --name OPENAI_API_KEY
```

The TOML stores the name `OPENAI_API_KEY`, not its value. Other supported
credential references include protected files and OS keyrings:

```bash
./bin/codehelper auth set --help
./bin/codehelper auth status --config ./codehelper.toml
./bin/codehelper auth list
```

Do not put a raw API key in tracked documentation, source control, shell
history, issue text, or a configuration field. The repository owner's
explicitly ignored, mode-`0600` local DeepSeek runbook is the only documented
exception; agents must not read or print it.

### One-click local DeepSeek

On the repository owner's macOS development machine:

```bash
make deepseek-init
make deepseek-tui
make deepseek-vscode
```

These targets build the required artifact, resolve the local credential,
install the Keychain-backed configuration, and launch the selected host. See
[One-Click Local DeepSeek](./deepseek-local.md) before using them.

## 6. Validate with a Hermetic Fixture

Before using a real provider, prove the runtime path without network access:

```bash
./bin/codehelper exec \
  --provider-fixture ./testdata/providers/openai \
  --provider openai \
  --model gpt-fixture \
  --workspace . \
  --output-format stream-json \
  "say hello"
```

Fixture runs exercise the real runtime and event stream but use deterministic
recorded provider responses.

## 7. Run a Real Session

Read-only analysis:

```bash
./bin/codehelper exec \
  --config ./codehelper.toml \
  --mode plan \
  --posture never \
  "Explain the architecture and identify risky modules"
```

Interactive coding:

```bash
./bin/codehelper tui \
  --config ./codehelper.toml \
  --workspace . \
  --mode act \
  --posture suggest \
  --enable-tools
```

One-shot coding with persistent state:

```bash
./bin/codehelper exec \
  --config ./codehelper.toml \
  --data-dir ./.codehelper \
  --workspace . \
  --enable-tools \
  --mode act \
  --posture suggest \
  "Fix the failing unit test and verify the result"
```

Resume the active thread:

```bash
./bin/codehelper exec \
  --config ./codehelper.toml \
  --data-dir ./.codehelper \
  --resume \
  "Continue from the previous result"
```

## 8. Install the VS Code Extension Locally

Development workflow:

```bash
make vscode-install
make vscode-check
make vscode-test
make vscode-build
```

Build a local VSIX:

```bash
make vscode-package
```

On macOS, the repository also provides an opinionated local setup script:

```bash
export DEEPSEEK_API_KEY='...'
make vscode-local-setup
unset DEEPSEEK_API_KEY
```

Read [VS Code Extension](./vscode.md) before using this script; it installs into
the official VS Code application and stores the credential in macOS Keychain.

For a portable first run, use `CodeHelper: Setup Workspace` from the Command
Palette. If the Runtime does not start, the Chat failure panel and
`CodeHelper: Repair Runtime` expose structured readiness issues and repair
actions instead of requiring Output-channel inspection.

## 9. Recommended First Checks

```bash
make docs-check
make smoke
make test
```

`make verify` is the full repository gate. It may require platform-specific
sandbox capabilities and is intentionally heavier than the first checks.

## 10. Next Steps

- Learn permission behavior in [Usage](./usage.md).
- Configure budgets, verification, context, workers, and routes in
  [Configuration](./configuration.md).
- Read [Architecture](./architecture.md) before extending the runtime.
- Use [Troubleshooting](./troubleshooting.md) for sandbox, provider, state, and
  VS Code failures.
