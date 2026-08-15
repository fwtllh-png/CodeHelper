# Repository Scripts

[简体中文](./README.zh-CN.md) | English

Scripts in this directory are stable command-line entry points for validation,
smoke tests, local setup, and release packaging. Run them from the repository
root unless a script explicitly states otherwise.

| Script | Network | Output / side effect |
| --- | --- | --- |
| `check-docs.sh` | no | validates Markdown links and bilingual mirrors |
| `check-book.sh` | no | validates book catalog, metadata, mirrors, paths, and navigation |
| `check-doc-governance.py` | external-link mode only | validates ownership, PR impact, freshness, release facts, images, and external links |
| `render-book-navigation.py` | no | regenerates bilingual navigation from the book catalog |
| `check-brand.sh` | no | scans tracked source for stale branding |
| `test-brand-check.sh` | no | self-tests brand scanner behavior |
| `test-secret-leak.sh` | no | validates binary redaction behavior |
| `run-test-lane.py` | command-dependent | writes passed, failed, or unavailable JSON lane evidence |
| `check-hotspot-baseline.go` | no | validates the Stage 0 hotspot freeze and post-split responsibility owners |
| `toolexecbaseline` | no | records and validates the EX0 local tool-execution surface, safety contracts, and known risks |
| `commanddocs` | no | generates or checks bilingual command lists from the Cobra tree |
| `upgradebaseline` | no | writes Stage 0 benchmark metrics and capability availability |
| `experiencecontract` | no | validates the shared experience baseline |
| `content-fixture-smoke.sh` | no | temporary content-dependency fixtures |
| `live-model-smoke.sh` | yes | single- or Multi-Agent real provider smoke; no persistent secret |
| `package-release.sh` | no | `dist/release`: binaries, checksums, SBOM, manifest |
| `deepseek-local.sh` | setup/package may use network | local DeepSeek build, Keychain config, TUI, and VS Code |
| `setup-vscode-local.sh` | package build may install dependencies | installs a target VSIX into official macOS VS Code |

## Conventions

Scripts must:

- resolve the repository root instead of assuming the caller's directory;
- use strict error handling and preserve failure exit codes;
- expose machine-specific paths through environment variables;
- avoid printing secrets or reading them from tracked repository documents;
- treat the explicitly ignored local DeepSeek runbook as secret input only;
- write generated artifacts only to documented build directories;
- clean temporary files with traps;
- provide `--help` when they accept options.

## Common Commands

```bash
make docs-check
make book-check
make book-navigation
make command-docs
make upgrade-baseline
make tool-execution-ex0
make test
make architecture-freeze
make host-journey-contract
make experience-baseline
make test-platform-capability
make test-integration
BASE_REF=origin/main make doc-impact
make release-fact-check
make doc-external-links
make brand-check
make secret-leak-test
make live-model-smoke
make live-multi-agent-smoke
make deepseek-multi-agent-smoke
VERSION=0.1.0 make package
make deepseek-init
make deepseek-tui
make deepseek-vscode
make vscode-local-setup
```

Full development and release context is documented in
[docs/en/development.md](../docs/en/development.md).
