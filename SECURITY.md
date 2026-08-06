# Security Policy

[简体中文](./SECURITY.zh-CN.md) | English

## Supported Versions

CodeHelper is in initial development. Security fixes are applied to the latest
tagged release and the `main` branch. Older pre-release versions are not
maintained unless a release note states otherwise.

| Version | Supported |
| --- | --- |
| Latest tagged release | Yes |
| `main` | Yes, development branch |
| Older pre-release versions | No |

## Reporting a Vulnerability

Report vulnerabilities privately through
[GitHub Security Advisories](https://github.com/fwtllh-png/CodeHelper/security/advisories/new).
Do not open a public issue for a suspected vulnerability.

Include only the information needed to reproduce and assess the problem:

- affected version or commit;
- operating system and sandbox backend;
- affected host, provider, tool, or protocol;
- minimal reproduction steps;
- expected and observed security boundary;
- potential impact;
- a suggested fix, when available.

Do not include credentials, private source code, production data, or other
secrets. Use synthetic fixtures where possible.

The maintainers aim to acknowledge a report within three business days and
provide an initial assessment within seven business days. Complex issues may
require additional time. Coordinated disclosure timing will be agreed with the
reporter after the impact and remediation path are understood.

## Scope

Security-sensitive areas include:

- tool guard, policy, approval, permissions, and constitution enforcement;
- workspace file boundaries, edit journal, and recovery;
- process execution and OS sandbox isolation;
- credential references, redaction, and logs;
- provider, MCP, plugin, hook, and network boundaries;
- HTTP/SSE, ACP, web, CLI, TUI, and VS Code trust boundaries;
- persisted sessions, events, snapshots, and traces;
- release artifacts, checksums, updates, and supply-chain metadata.

General hardening suggestions without a demonstrated security impact may be
reported through a regular issue or Discussion.
