# One-Click Local DeepSeek

[Simplified Chinese](../zh-CN/deepseek-local.md) | English

This guide provides deterministic build, configuration, TUI, and official
VS Code entry points for humans and coding agents on macOS.

## Prerequisites

- Go 1.26 or later;
- Git and Make;
- Node.js and npm for the VS Code path;
- official Visual Studio Code at `/Applications/Visual Studio Code.app`;
- a valid DeepSeek API key;
- access to macOS Keychain.

## One-Click Commands

From the repository root:

```bash
make deepseek-init
make deepseek-tui
make deepseek-vscode
```

| Target | Result |
| --- | --- |
| `deepseek-init` | builds `bin/codehelper`, installs config, stores the credential in Keychain, and validates the route |
| `deepseek-tui` | performs initialization and launches a tool-enabled TUI |
| `deepseek-vscode` | builds the target VSIX, configures DeepSeek, installs the extension, and opens the workspace |

Quit all official VS Code windows before running `make deepseek-vscode`.

## Credential Sources

`scripts/deepseek-local.sh` looks up the credential in this order:

1. `DEEPSEEK_API_KEY`;
2. the ignored `docs/DEEPSEEK-LIVE.zh-CN.md` local runbook;
3. macOS Keychain service `codehelper`, account `deepseek/default`;
4. the pre-CodeHelper legacy Keychain service, account `deepseek/default`;
5. a terminal prompt without echo.

Initialization migrates the selected value into the current `codehelper`
Keychain service. The installed TOML contains only:

```toml
[credential]
kind = "keyring"
name = "deepseek/default"
```

## Local Runbook

Generate or refresh the machine-local runbook:

```bash
./scripts/deepseek-local.sh doc
```

The file `docs/DEEPSEEK-LIVE.zh-CN.md` contains the real local API key because
this repository owner explicitly uses it as a personal runbook. It is:

- explicitly ignored by `.gitignore`;
- created with mode `0600`;
- never required as the runtime credential store;
- forbidden from force-adding to Git or copying into output.

Maintained and tracked documentation must never contain a real credential.

## TUI Controls

The default workspace is the CodeHelper repository and the default local
posture is `bypass`:

```bash
make deepseek-tui
```

Use approval prompts or another workspace when needed:

```bash
CODEHELPER_LOCAL_POSTURE=suggest make deepseek-tui
CODEHELPER_LOCAL_WORKSPACE=/path/to/project make deepseek-tui
```

`bypass` is appropriate only for a trusted local workspace. It does not bypass
the tool guard, constitution, journal, or OS sandbox.

## Agent Instructions

An agent should invoke the Make targets and must not read, print, summarize, or
patch the local runbook:

```bash
make deepseek-init
CODEHELPER_LOCAL_POSTURE=suggest make deepseek-tui
make deepseek-vscode
```

Before running the VS Code command, the agent must tell the human to quit all
official VS Code windows. GUI launch is an intentional side effect.

If an IDE sandbox denies macOS Keychain writes, stop and ask the human to run
the same target in a normal macOS Terminal. Do not weaken the credential to a
tracked config value or expose it in command output.

## Verification

Non-interactive environment check:

```bash
./scripts/deepseek-local.sh check
./bin/codehelper config show --config ~/.config/codehelper/config.toml
```

The check verifies the binary, TOML, Keychain reference, and bundled
`openai_responses` route to `https://api.deepseek.com`; it does not send a
billable model request.

For a real provider request, start the TUI or use:

```bash
./bin/codehelper exec \
  --config ~/.config/codehelper/config.toml \
  --workspace . \
  --mode plan \
  --posture never \
  "Summarize this repository"
```

## Manual Recovery

Recreate only the local runbook:

```bash
DEEPSEEK_API_KEY='your-key' ./scripts/deepseek-local.sh doc
```

Recreate the full runtime configuration:

```bash
DEEPSEEK_API_KEY='your-key' make deepseek-init
```

Do not put the command in persistent shell history when using a literal value;
prefer the secure prompt or an existing Keychain entry.
