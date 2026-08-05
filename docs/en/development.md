# Local Development, Testing, and Scripts

[简体中文](../zh-CN/development.md) | English

## Development Environment

Required:

- Go 1.26+
- Git
- Make

For the VS Code extension:

- Node.js compatible with the lockfile
- npm
- VS Code 1.96+ for Electron integration

Optional tools:

- `syft` for full CycloneDX SBOM generation;
- `tmux` for attachable lanes;
- `bubblewrap`/Landlock support on Linux or Seatbelt support on macOS;
- Docker/remote SSH dependencies for extension matrix tests.

## Bootstrap

```bash
git clone https://github.com/fwtllh-png/CodeHelper.git
cd CodeHelper
go mod download
make vscode-install
make build
```

`make vscode-install` uses `npm ci` and should be used instead of manually
changing the lockfile.

## Fast Inner Loop

Go-only change:

```bash
gofmt -w path/to/file.go
go test ./path/to/package
go test ./path/to/package -run TestSpecificBehavior -count=1
```

VS Code change:

```bash
cd extensions/vscode
npm run check
npm test -- state runtime
```

Documentation change:

```bash
make docs-check
```

Before handing off:

```bash
git diff --check
make brand-check
```

## Make Targets

### Core

| Target | Purpose |
| --- | --- |
| `make fmt` | run Go formatting |
| `make build` | build `bin/codehelper` with metadata |
| `make test` | run all Go tests |
| `make race` | serial race-enabled Go tests |
| `make smoke` | build and verify help/version |
| `make docs-check` | validate maintained Markdown links and bilingual structure |
| `make verify` | broad repository gate: docs, brand, VS Code, vet, tests, race |
| `make clean` | remove generated build/release directories |

### Security and contracts

| Target | Purpose |
| --- | --- |
| `make security-test` | security, guard, plugin, CLI, engine, app tests |
| `make sandbox-attack-test` | sandbox/file/shell attack corpus |
| `make secret-leak-test` | release-binary secret redaction checks |
| `make acp-interop` | real binary ACP stdio lifecycle |
| `make api-contract` | real binary HTTP contract |
| `make protocol-contract` | shared scenarios over ACP and HTTP |
| `make protocol-schema` | regenerate committed runtime protocol schema |

### VS Code

| Target | Purpose |
| --- | --- |
| `make vscode-install` | `npm ci` |
| `make vscode-check` | generated drift, TypeScript, ESLint |
| `make vscode-test` | extension unit tests |
| `make vscode-runtime-integration` | real Go runtime over stdio without Electron |
| `make vscode-integration` | official VS Code Electron flow |
| `make vscode-security` | extension security tests |
| `make vscode-performance` | projection and runtime readiness budgets |
| `make vscode-package` | build/install a local VSIX |
| `make vscode-distribution` | create multi-target dry-run release artifacts |
| `make vscode-rc` | complete release-candidate gate |

### Benchmarks and release

| Target | Purpose |
| --- | --- |
| `make bench` | hermetic coding benchmark |
| `make catalog-bench` | dynamic tool catalog scale benchmark |
| `make live-model-smoke` | explicit, non-hermetic provider smoke |
| `make package VERSION=x.y.z` | multi-platform CLI package and SBOM |

## Repository Scripts

| Script | Behavior |
| --- | --- |
| `scripts/check-docs.sh` | validates local Markdown links and bilingual mirrors |
| `scripts/check-brand.sh` | rejects stale product branding |
| `scripts/test-brand-check.sh` | self-tests the brand scanner |
| `scripts/test-secret-leak.sh` | runs redaction checks against a built binary |
| `scripts/live-model-smoke.sh` | calls one explicitly configured live model |
| `scripts/content-fixture-smoke.sh` | validates optional content dependency detection |
| `scripts/package-release.sh` | builds five targets, checksums, SBOM, manifest, smoke |
| `scripts/deepseek-local.sh` | builds/configures local DeepSeek and launches TUI or VS Code |
| `scripts/setup-vscode-local.sh` | macOS official-VS-Code local installation |

Extension scripts under `extensions/vscode/scripts` own TypeScript build,
protocol/compatibility generation, Electron/remote integration, VSIX packaging,
release manifests, provenance, matrix evidence, and RC reporting.

Every script must:

- run from any caller directory by resolving the repository root;
- use strict error handling;
- propagate non-zero failures;
- avoid personal absolute paths unless exposed as an override;
- never print or read secrets from tracked repository documentation;
- treat the explicitly ignored local DeepSeek runbook as credential input only;
- document generated output and cleanup behavior.

## Generated Files

Do not hand-edit:

- `docs/protocol/runtime-protocol.schema.json`;
- `extensions/vscode/src/protocol/generated.ts`;
- `extensions/vscode/src/compatibility/generated.ts`.

Use:

```bash
make protocol-schema
cd extensions/vscode
npm run generate:protocol
npm run generate:compatibility
```

Commit generated output with the source change.

## Test Strategy

Choose tests by risk:

| Change | Minimum validation |
| --- | --- |
| local pure function | package test |
| shared runtime behavior | package + dependent runtime tests |
| protocol | schema drift + ACP + HTTP contracts |
| persistence | repository + state package tests |
| guard/security | focused race tests and attack corpus |
| VS Code state/UI | typecheck, ESLint, relevant Node tests |
| release scripts | dry-run package/distribution gate |
| documentation | `make docs-check` |

Full `go test ./...` can include platform-sensitive integration and benchmark
tests. Report environmental failures precisely and rerun affected packages in
isolation before labeling a regression.

## Platform Notes

- macOS uses platform-specific sandbox behavior and Keychain integration.
- Linux strength depends on kernel and available sandbox helpers.
- Windows has different process and filesystem isolation guarantees.
- Race tests and real fixture lifecycle tests can be resource sensitive under
  high package parallelism.

Do not weaken a test just to make an unsupported platform appear supported.

## Release Development

CLI package:

```bash
VERSION=0.1.0 RELEASE_STAGE=experimental make package
```

VS Code dry-run:

```bash
make vscode-distribution
make vscode-matrix-report
```

Formal release credentials must be provided from outside the repository. See
[VS Code Extension](./vscode.md) for the release boundary and compatibility
contract.
