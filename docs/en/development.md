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
| `make eval-contract-check` | run the candidate evaluation contract and Runner diagnostics; not release evidence |
| `make eval-foundation-check` | validate F1-F3 contracts, nine Oracles, seven Mutations, and production Provider/Runtime/Host Replay; not Qualification evidence |
| `make eval-replay` | run candidate structural Replay and Corpus diagnostics; not Runtime/Host Replay evidence |
| `make eval-oracle` | run the F3 candidate Core Pack, Oracle, Impact, and structural flake diagnostics; not Qualification evidence |
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
| `make observation-traits` | regenerate Observation traits for Go, TypeScript, and JSON Schema |
| `make observation-traits-check` | fail when generated Observation artifacts drift |

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
| `make vscode-package` | build/install and handshake the current Host target VSIX |
| `make vscode-package-universal` | build/install-audit the static universal VSIX |
| `make vscode-distribution` | create multi-target dry-run release artifacts |
| `make vscode-rc` | complete release-candidate gate |

### Benchmarks and release

| Target | Purpose |
| --- | --- |
| `make bench` | fixture coding benchmark on a strong host sandbox |
| `make catalog-bench` | dynamic tool catalog scale benchmark |
| `make multi-agent-performance` | bounded Agent Event projection budget |
| `make live-model-smoke` | explicit, non-hermetic provider smoke |
| `make live-multi-agent-smoke` | credentialed real-provider Agent spawn/wait/completion smoke |
| `make deepseek-live-smoke` | local Keychain-backed DeepSeek single-turn smoke |
| `make deepseek-multi-agent-smoke` | local Keychain-backed DeepSeek Multi-Agent smoke |
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
| `scripts/commanddocs` | generates or checks bilingual command inventories |
| `scripts/observationtraitgen` | generates Observation traits and public schema from one manifest |
| `scripts/live-model-smoke.sh` | calls one explicitly configured live model |
| `scripts/package-release.sh` | builds five targets, checksums, SBOM, manifest, smoke |
| `scripts/deepseek-local.sh` | builds/configures local DeepSeek and launches TUI or VS Code |
| `scripts/setup-vscode-local.sh` | macOS official-VS-Code local installation |

Benchmark and evaluation reports are transient `.tmp` or CI artifacts. Every
rate carries its numerator and denominator; an empty denominator produces
`null`, not a misleading zero. `make bench` remains the strict release gate
and does not accept unavailable tasks as passed.

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

DeepSeek V4 uses peak/off-peak pricing. Live smoke evidence selects the
effective UTC window and supplies cache-hit, cache-miss, and output prices as
one private model-metadata partition. The bundled route reports dynamic
pricing as unknown instead of persisting a stale static amount.

## Generated Files

Do not hand-edit:

- `docs/protocol/runtime-protocol.schema.json`;
- `docs/protocol/observation.schema.json`;
- `internal/observability/observation/traits.gen.go`;
- `extensions/vscode/src/protocol/observation.generated.ts`;
- `extensions/vscode/src/protocol/generated.ts`;
- `extensions/vscode/src/compatibility/generated.ts`.

Use:

```bash
make protocol-schema
make observation-traits
cd extensions/vscode
npm run generate:protocol
npm run generate:compatibility
```

Commit generated output with the source change.

`internal/observability/schema/observation_traits.json` is the only
hand-maintained Observation trait manifest. A new kind must declare owner,
durability, payload policy, retention class, required correlations, OTLP
mapping, and priority before generation succeeds.

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

Architecture regression uses two complementary contracts:

- `testdata/contracts/hotspot-baseline.json` binds responsibilities to package
  symbols and owner files. Missing or misplaced responsibilities, unreviewed
  internal dependencies, hotspot growth, and removed test assets fail
  `make hotspot-baseline`.
- `testdata/contracts/architecture-metrics-baseline.json` limits direct
  internal package fanout, production lines, Options and Mutex fields, hotspot
  file/function size, and duplicated Protocol Event switch sites. `make
  architecture-ratchet` measures the repository and compares limits with
  `ARCHITECTURE_BASE_REF` when that ref contains a baseline.

Architecture limits are monotonic. Increasing a limit requires a non-empty
per-metric `relaxations` reason; removing a target or metric requires an
explicit `retirements` reason. Stale exceptions fail the Ratchet so a temporary
allowance cannot silently become permanent. Discrete state metrics have no
headroom; package lines, file lines, and function lines allow at most 100, 20,
and 5 lines respectively. A larger gap between the measured value and its limit
also fails, forcing the baseline downward after a split. The measured report is
written to `.tmp/architecture/metrics.json`.

## Foundation Kits

Shared persistence mechanics live in `internal/persist/sqlkit`. Repositories
may reuse transaction lifecycle, row collection, canonical JSON, nullable
values, UTC timestamps, and exact affected-row checks. SQL statements,
optimistic concurrency, state transitions, not-found errors, and domain scans
remain in the owning repository. `sqlkit.WithTx` does not retry or nest
transactions; callback errors roll back, rollback errors are joined, and a
callback panic rolls back before being rethrown.

Simple typed tools use `internal/adapter/tool/typed` for strict JSON decoding
and output encoding, and `internal/adapter/tool/result` for structured success
or failure results. The typed adapter is not an execution path: descriptors
still register in the ordinary Registry, calls still require authorization,
and consequential tools still pass through Guard, approval, journal, and
sandbox policy. Resource resolution, availability, and repeat behavior remain
explicit in each tool descriptor.

The first migrated tool tier is `completion`, `lsp`, `memory`, `revert`,
`skill`, and `toolsearch`. Their migration gate rejects local
`json.Unmarshal` code so call-shape handling cannot silently diverge. Task
`List` shares its scanner with `Get`, selects all fields in one query, and has
a 1000-row benchmark plus a one-query contract test.

Focused validation:

```bash
go test -race ./internal/persist/... \
  ./internal/orchestration/task \
  ./internal/orchestration/automation
go test -race ./internal/adapter/tool/...
go test -run '^$' -bench BenchmarkList1000 -benchtime=1x \
  ./internal/orchestration/task
```

Choose tests by risk:

| Change | Minimum validation |
| --- | --- |
| local pure function | package test |
| shared runtime behavior | package + dependent runtime tests |
| protocol | schema drift + ACP + HTTP contracts |
| observation kind or exporter | trait generation + observation/router/OTLP tests |
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
