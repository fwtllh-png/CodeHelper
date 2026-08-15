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
6. [Context engineering architecture upgrade](./context-engineering-architecture-upgrade.md)
7. [Multi-Agent architecture upgrade](./multi-agent-architecture-upgrade.md)
8. [Approval architecture upgrade](./approval-architecture-upgrade.md)
9. [Token efficiency architecture upgrade](./token-efficiency-architecture-upgrade.md)
10. [Provider architecture upgrade](./provider-architecture-upgrade.md)
11. [Tool and local execution architecture upgrade](./tool-execution-architecture-upgrade.md)
12. [Security model](./security.md)
13. [Local development and scripts](./development.md)

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
5. [VS Code Runtime monitoring](./runtime-monitoring.md)
6. [Troubleshooting](./troubleshooting.md)

### I want to contribute

1. [Architecture](./architecture.md)
2. [Context engineering architecture upgrade](./context-engineering-architecture-upgrade.md)
3. [Multi-Agent architecture upgrade](./multi-agent-architecture-upgrade.md)
4. [Approval architecture upgrade](./approval-architecture-upgrade.md)
5. [Token efficiency architecture upgrade](./token-efficiency-architecture-upgrade.md)
6. [Provider architecture upgrade](./provider-architecture-upgrade.md)
7. [Tool and local execution architecture upgrade](./tool-execution-architecture-upgrade.md)
8. [Security model](./security.md)
9. [Local development and scripts](./development.md)
10. [Agent guide](./agent-guide.md)
11. [TUI and VS Code experience contract](./experience.md)
12. [VS Code Runtime monitoring](./runtime-monitoring.md)
13. [CONTRIBUTING.md](../../CONTRIBUTING.md)
14. [Documentation governance](./documentation-governance.md)
15. [Roadmap](./roadmap.md)

## Document Ownership

| Document | Source of truth |
| --- | --- |
| CLI names and flags | `internal/host/cli` and `codehelper <command> --help` |
| TOML and environment fields | `internal/config/config.go` |
| Runtime protocol schema | `docs/protocol/runtime-protocol.schema.json` |
| Architecture boundaries | import graph and architecture tests |
| Build/test commands | `Makefile` and package scripts |
| VS Code compatibility | `extensions/vscode/compatibility.json` |
| TUI and VS Code experience semantics | `docs/experience-contract.json` |
| Knowledge book structure and delivery | `knowledge-base-plan.md` |
| Book catalog and chapter status | `docs/book/catalog.json` |
| Book ownership, freshness, and release facts | `docs/book/governance.json` |
| Roadmap | desired outcomes, never a claim of shipped behavior |

When implementation and documentation disagree, verify the implementation,
correct the documentation in the same change, and add a drift check when
practical.
