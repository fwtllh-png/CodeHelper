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
7. [Local development and scripts](./development.md)

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
2. [Security model](./security.md)
3. [Local development and scripts](./development.md)
4. [Agent guide](./agent-guide.md)
5. [TUI and VS Code experience contract](./experience.md)
6. [CONTRIBUTING.md](../../CONTRIBUTING.md)
7. [Documentation governance](./documentation-governance.md)
8. [Roadmap](./roadmap.md)

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
