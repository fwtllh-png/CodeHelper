# CodeHelper Documentation

[简体中文](../zh-CN/README.md) | English

This directory is the maintained documentation set for the current codebase.
Historical implementation RFCs are intentionally not part of the product
documentation: architecture decisions that still matter are described as
current constraints in the relevant guide.

CodeHelper is also being developed as an executable Agent engineering book:
background concepts, system design, source walkthroughs, tests, failures, and
hands-on labs will form a progressive learning path. The information
architecture and delivery stages are defined in the
[Knowledge Documentation Plan](./knowledge-base-plan.md).

## Choose a Path

### I want to learn Agent engineering

1. [Product and system overview](./overview.md)
2. [Agent engineering book](../book/en/README.md)
3. [Book navigation and chapter status](../book/en/NAVIGATION.md)
4. [Knowledge documentation plan](./knowledge-base-plan.md)
5. [Architecture](./architecture.md)
6. [Security model](./security.md)
7. [Source code reading guide](./reading-guide.md)
8. [Local development and scripts](./development.md)

### I want to use CodeHelper

1. [Overview](./overview.md)
2. [Getting started](./getting-started.md)
3. [Configuration](./configuration.md)
4. [Usage and command workflows](./usage.md)
5. [One-click local DeepSeek](./deepseek-local.md)
6. [Security model](./security.md)
7. [Troubleshooting](./troubleshooting.md)

### I want to use the VS Code extension

1. [Getting started](./getting-started.md)
2. [One-click local DeepSeek](./deepseek-local.md)
3. [VS Code extension](./vscode.md)
4. [Configuration](./configuration.md)
5. [Troubleshooting](./troubleshooting.md)

### I want to contribute

1. [Architecture](./architecture.md)
2. [Runtime reliability hardening](./reliability-hardening.md)
3. [Production evaluation technical specification](./production-evaluation.md)
4. [Production evaluation implementation plan](./production-evaluation-implementation-plan.md)
5. [Production evaluation findings register](./production-evaluation-findings.md)
6. [17.4 Convergence Review reset](./production-evaluation-17.4-assessment.md)
7. [Security model](./security.md)
8. [Local development and scripts](./development.md)
9. [Source code reading guide](./reading-guide.md)
10. [Agent guide](./agent-guide.md)
11. [TUI and VS Code experience contract](./experience.md)
12. [CONTRIBUTING.md](../../CONTRIBUTING.md)
13. [Documentation governance](./documentation-governance.md)
14. [Roadmap](./roadmap.md)

## Document Ownership

| Document | Source of truth |
| --- | --- |
| CLI names and flags | `internal/host/cli` and `codehelper <command> --help` |
| TOML and environment fields | `internal/config/config.go` |
| Runtime protocol schema | `docs/protocol/runtime-protocol.schema.json` |
| Architecture boundaries | import graph and architecture tests |
| Build/test commands | `Makefile` and package scripts |
| VS Code compatibility | `extensions/vscode/compatibility.json` |
| TUI and VS Code experience semantics | `testdata/contracts/experience-contract.json` |
| Knowledge book structure and delivery | `knowledge-base-plan.md` |
| Book catalog and chapter status | `docs/book/catalog.json` |
| Book ownership, freshness, and release facts | `docs/book/governance.json` |
| Reliability program status | evidenced workstream status in `reliability-hardening.md` |
| Production evaluation candidate implementation | `evaluation/manifest.json`, `evaluation/schema`, and `evaluation/scenarios` |
| Production evaluation target contracts | normative requirements in `production-evaluation.md` |
| Production evaluation execution order | work units, gates, estimates, and stop conditions in `production-evaluation-implementation-plan.md` |
| Production evaluation findings | evidence-admitted formal entries in `production-evaluation-findings.md` |
| Production evaluation decisions | `evaluation/assessments`, the Findings Register, and the corresponding assessment documents |
| Roadmap | desired outcomes, never a claim of shipped behavior |

When implementation and documentation disagree, verify the implementation,
correct the documentation in the same change, and add a drift check when
practical.
