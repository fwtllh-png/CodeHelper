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
make book-check # when docs/book or its catalog changes
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
| `make test` | run the stable Hermetic lane |
| `make test-hermetic` | run serial, network-free Go tests |
| `make test-platform-capability` | run real host sandbox capability tests |
| `make test-integration` | run ACP and VS Code against a real binary |
| `make test-release` | run race, packaging, redaction, and release dry-run gates |
| `make hotspot-baseline` | validate the IMP-006 responsibility, dependency, size, and test-asset contract |
| `make architecture-freeze` | run the four hotspot characterization, golden, schema, and focused race gates |
| `make race` | serial race-enabled Go tests |
| `make smoke` | build and verify help/version |
| `make docs-check` | validate maintained Markdown links and bilingual structure |
| `make book-check` | validate knowledge-book catalog, metadata, mirrors, and paths |
| `make book-navigation` | regenerate bilingual book navigation from the catalog |
| `make verify` | broad repository gate: docs, brand, VS Code, vet, tests, race |
| `make clean` | remove generated build/release directories |

### Security and contracts

| Target | Purpose |
| --- | --- |
| `make security-test` | security, guard, plugin, CLI, engine, app tests |
| `make sandbox-attack-test` | sandbox/file/shell attack corpus |
| `make secret-leak-test` | release-binary secret redaction checks |
| `make acp-interop` | real binary ACP stdio lifecycle |
| `make protocol-contract` | shared runtime scenarios over ACP |
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
| `make bench` | fixture coding benchmark on a strong host sandbox |
| `make upgrade-baseline` | write the versioned coding metrics report |
| `make catalog-bench` | dynamic tool catalog scale benchmark |
| `make live-model-smoke` | explicit, non-hermetic provider smoke |
| `make package VERSION=x.y.z` | multi-platform CLI package and SBOM |

## Repository Scripts

| Script | Behavior |
| --- | --- |
| `scripts/check-docs.sh` | validates local Markdown links and bilingual mirrors |
| `scripts/check-book.sh` | validates the Agent engineering book contract |
| `scripts/render-book-navigation.py` | renders navigation from the book catalog |
| `scripts/check-brand.sh` | rejects stale product branding |
| `scripts/test-brand-check.sh` | self-tests the brand scanner |
| `scripts/test-secret-leak.sh` | runs redaction checks against a built binary |
| `scripts/run-test-lane.py` | runs a test lane and writes structured evidence |
| `scripts/upgradebaseline` | aggregates benchmark results into the Stage 0 baseline |
| `scripts/live-model-smoke.sh` | calls one explicitly configured live model |
| `scripts/content-fixture-smoke.sh` | validates optional content dependency detection |
| `scripts/package-release.sh` | builds five targets, checksums, SBOM, manifest, smoke |
| `scripts/deepseek-local.sh` | builds/configures local DeepSeek and launches TUI or VS Code |
| `scripts/setup-vscode-local.sh` | macOS official-VS-Code local installation |

The committed `docs/upgrade-baseline.json` records task success rate,
nearest-rank P50/P95 wall latency, retried-task rate, verification coverage,
unpriced-call rate, and recovery success rate. Every rate carries its numerator
and denominator; an empty denominator produces `null`, not a misleading zero.
Tasks blocked only by a missing strong host sandbox are recorded as
`unavailable` and excluded from metric denominators. `make bench` remains the
strict release gate and does not accept unavailable tasks as passed.

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

Tests are split into four explicit lanes:

| Lane | Command | Contract |
| --- | --- | --- |
| Hermetic | `make test` | default PR-safe tests; no network, credentials, GUI, or host sandbox requirement |
| Platform Capability | `make test-platform-capability` | real OS sandbox behavior; missing prerequisites produce `unavailable` |
| Integration | `make test-integration` | real CLI/ACP and VS Code Runtime lifecycle |
| Release | `make test-release` | expensive race, benchmark, cross-build, redaction, and packaging gates |

Each lane writes a JSON result under `.tmp/test-lanes/` with a
`passed`, `failed`, or `unavailable` status. CI requires the Linux platform
capability; local unsupported environments report `unavailable` without
pretending that the capability passed.

The Stage 0 hotspot freeze is defined in `docs/hotspot-baseline.json`.
Responsibilities begin as package-symbol contracts so a mechanical split can
move code. After a hotspot is split, `responsibility_files` binds each domain to
its owner file. Missing or misplaced responsibilities, new internal
dependencies, hotspot growth, and removed test assets fail the baseline.

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
| product documentation | `make docs-check` |
| knowledge book or catalog | `make docs-check && make book-check` |

Real platform capability tests use the `capability` Go build tag and therefore
cannot enter the Hermetic lane accidentally. Integration tests that require a
built binary are exercised by `make test-integration`, not inferred from a
green unit-test run.

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
